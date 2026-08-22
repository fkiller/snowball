#include <inttypes.h>
#include <stdbool.h>
#include <stdio.h>
#include <string.h>

#include "board_audio.h"
#include "command_recognizer.h"
#include "diagnostics.h"
#include "device_identity.h"
#include "esp_log.h"
#include "esp_system.h"
#include "freertos/FreeRTOS.h"
#include "media_session.h"
#include "nvs_flash.h"
#include "provisioning.h"
#include "serial_output.h"
#include "speech.h"

static const char *TAG = "snowball";
static portMUX_TYPE app_lock = portMUX_INITIALIZER_UNLOCKED;
static uint32_t event_counter;
static uint32_t boot_nonce;
static uint32_t current_attempt;
static uint32_t failure_feedback_attempt;
static snowball_command_result_t current_command;

static uint32_t next_counter(void) {
    portENTER_CRITICAL(&app_lock);
    uint32_t value = ++event_counter;
    portEXIT_CRITICAL(&app_lock);
    return value;
}

static void set_failure_feedback_pending(uint32_t attempt, bool pending) {
    portENTER_CRITICAL(&app_lock);
    failure_feedback_attempt = pending ? attempt : 0;
    portEXIT_CRITICAL(&app_lock);
}

static bool take_failure_feedback_pending(uint32_t attempt) {
    portENTER_CRITICAL(&app_lock);
    bool pending = failure_feedback_attempt == attempt;
    if (pending) failure_feedback_attempt = 0;
    portEXIT_CRITICAL(&app_lock);
    return pending;
}

static bool attempt_is_current(uint32_t attempt) {
    portENTER_CRITICAL(&app_lock);
    bool current = attempt != 0 && attempt == current_attempt;
    portEXIT_CRITICAL(&app_lock);
    return current;
}

static void set_current_command(uint32_t attempt, const snowball_command_result_t *command) {
    portENTER_CRITICAL(&app_lock);
    if (attempt == current_attempt && command) current_command = *command;
    portEXIT_CRITICAL(&app_lock);
}

static bool get_current_command(uint32_t attempt, snowball_command_result_t *command) {
    if (!command) return false;
    portENTER_CRITICAL(&app_lock);
    bool current = attempt != 0 && attempt == current_attempt;
    if (current) *command = current_command;
    portEXIT_CRITICAL(&app_lock);
    return current;
}

static void trace_event(const char *stage, uint32_t attempt, const char *detail, float confidence) {
    diagnostics_record(stage, attempt, detail, confidence);
    diagnostics_memory_snapshot(stage, attempt);
    serial_output_lock();
    printf(
        "{\"version\":1,\"event\":\"debug\",\"stage\":\"%s\",\"attempt\":%" PRIu32,
        stage,
        attempt
    );
    if (detail && detail[0]) {
        printf(",\"detail\":\"");
        for (const unsigned char *cursor = (const unsigned char *)detail; *cursor; ++cursor) {
            if (*cursor == '\"' || *cursor == '\\') printf("\\%c", *cursor);
            else if (*cursor >= 0x20) putchar(*cursor);
        }
        printf("\"");
    }
    if (confidence >= 0.0f) printf(",\"confidence\":%.3f", confidence);
    printf("}\n");
    fflush(stdout);
    serial_output_unlock();
}

static uint32_t wake_event(int channel) {
    uint32_t attempt = next_counter();
    portENTER_CRITICAL(&app_lock);
    current_attempt = attempt;
    current_command = (snowball_command_result_t){0};
    failure_feedback_attempt = 0;
    portEXIT_CRITICAL(&app_lock);
    trace_event("wake_detected", attempt, "hi_esp", -1.0f);
    serial_output_printf(
        "{\"version\":1,\"event\":\"wake\",\"wake\":\"hi_esp\",\"channel\":%d,"
        "\"bootSequence\":%" PRIu32 ",\"attempt\":%" PRIu32 "}\n",
        channel,
        boot_nonce,
        attempt
    );
    return attempt;
}

static const char *command_wire_name(snowball_command_kind_t kind) {
    switch (kind) {
        case SNOWBALL_COMMAND_NEW_CHAT: return "new_chat";
        case SNOWBALL_COMMAND_RESUME: return "resume";
        case SNOWBALL_COMMAND_VOICE: return "voice";
        case SNOWBALL_COMMAND_PROJECT: return "project";
        default: return "";
    }
}

#if CONFIG_SNOWBALL_COMMAND_TAIL
static snowball_audio_feedback_t command_feedback(const snowball_command_result_t *command) {
    switch (command->kind) {
        case SNOWBALL_COMMAND_NEW_CHAT: return SNOWBALL_FEEDBACK_COMMAND_NEW_CHAT;
        case SNOWBALL_COMMAND_RESUME: return SNOWBALL_FEEDBACK_COMMAND_RESUME;
        case SNOWBALL_COMMAND_VOICE: return SNOWBALL_FEEDBACK_COMMAND_VOICE;
        case SNOWBALL_COMMAND_PROJECT:
            return command->target == SNOWBALL_COMMAND_TARGET_CODEX
                ? SNOWBALL_FEEDBACK_COMMAND_CODEX_PROJECT
                : SNOWBALL_FEEDBACK_COMMAND_CHATGPT_PROJECT;
        default: return SNOWBALL_FEEDBACK_COMMAND_NOT_RECOGNIZED;
    }
}
#endif

static esp_err_t queue_command(uint32_t attempt, const snowball_command_result_t *command) {
    snowball_device_event_t event = {
        .boot_nonce = boot_nonce,
        .counter = next_counter(),
        .diagnostic_attempt = attempt,
        .confidence = command->confidence,
    };
    strlcpy(event.event, "command", sizeof(event.event));
    strlcpy(event.wake, "hi_esp", sizeof(event.wake));
    strlcpy(event.command, command_wire_name(command->kind), sizeof(event.command));
    strlcpy(
        event.target,
        command->target == SNOWBALL_COMMAND_TARGET_CODEX ? "codex" : "chatgpt",
        sizeof(event.target)
    );
    strlcpy(event.name, command->name, sizeof(event.name));
    esp_err_t result = provisioning_queue_event(&event);
    trace_event(
        result == ESP_OK ? "command_queued" : "command_queue_failed",
        attempt,
        event.command,
        command->confidence
    );
    return result;
}

static void command_event(uint32_t attempt, const snowball_command_result_t *command) {
    if (!command || !attempt_is_current(attempt)) {
        trace_event("stale_command_ignored", attempt, "attempt_mismatch", -1.0f);
        return;
    }
    set_current_command(attempt, command);
    const char *wire_command = command_wire_name(command->kind);
    trace_event("command_resolved", attempt, wire_command, command->confidence);
    /* The speech transition queues the wake acknowledgement once. Add one
       semantic tone after resolution as well, including bare Hi ESP/new_chat,
       so a slow browser cannot look like a dead microphone. */
#if CONFIG_SNOWBALL_COMMAND_TAIL
    (void)board_audio_queue_feedback_for_attempt(command_feedback(command), attempt);
#endif

    esp_err_t result = media_session_start(boot_nonce, next_counter(), attempt);
    if (result != ESP_OK) {
        trace_event(
            result == ESP_ERR_INVALID_STATE ? "pairing_required" : "media_start_failed",
            attempt,
            esp_err_to_name(result),
            -1.0f
        );
        speech_end_session(attempt);
        (void)board_audio_queue_feedback_for_attempt(
            result == ESP_ERR_INVALID_STATE ? SNOWBALL_FEEDBACK_ACTION_NOT_READY : SNOWBALL_FEEDBACK_ACTION_FAILED,
            attempt
        );
    }
}

static void microphone_audio(const int16_t *samples, size_t sample_count) {
    if (media_session_connected()) {
        (void)media_session_push_pcm16k(samples, sample_count);
    }
}

static void media_event(snowball_media_event_t event, uint32_t attempt, const char *detail) {
    if (!attempt_is_current(attempt)) {
        trace_event("stale_media_event_ignored", attempt, detail, -1.0f);
        if (event == SNOWBALL_MEDIA_CONNECTED) media_session_stop();
        return;
    }
    switch (event) {
        case SNOWBALL_MEDIA_CONNECTING:
            trace_event("media_connecting", attempt, detail, -1.0f);
            break;
        case SNOWBALL_MEDIA_CONNECTED:
            trace_event("media_connected", attempt, detail, -1.0f);
            /* Keep a distinct user-facing lifecycle marker in the trace. The
               Gateway's authoritative Voice state is still the source of
               truth; this event only makes the device-side acceptance trail
               unambiguous when reviewing USB logs. */
            snowball_command_result_t command;
            if (!get_current_command(attempt, &command)) {
                trace_event("stale_media_command", attempt, detail, -1.0f);
                media_session_stop();
                break;
            }
            if (command.kind == SNOWBALL_COMMAND_PROJECT) {
                trace_event("project_media_connected", attempt, detail, -1.0f);
            } else {
                trace_event("voice_started", attempt, detail, -1.0f);
            }
            if (queue_command(attempt, &command) != ESP_OK) {
                set_failure_feedback_pending(attempt, true);
                media_session_stop();
            }
            break;
        case SNOWBALL_MEDIA_FAILED:
            trace_event("media_failed", attempt, detail, -1.0f);
            speech_end_session(attempt);
            set_failure_feedback_pending(attempt, false);
            (void)board_audio_queue_feedback_for_attempt(
                detail && strcmp(detail, "device_not_paired_or_offline") == 0
                    ? SNOWBALL_FEEDBACK_ACTION_NOT_READY
                    : SNOWBALL_FEEDBACK_ACTION_FAILED,
                attempt
            );
            break;
        case SNOWBALL_MEDIA_ENDED:
            trace_event("media_ended", attempt, detail, -1.0f);
            speech_end_session(attempt);
            if (take_failure_feedback_pending(attempt)) {
                (void)board_audio_queue_feedback_for_attempt(SNOWBALL_FEEDBACK_ACTION_FAILED, attempt);
            }
            break;
    }
}

static void delivery_event(
    const snowball_device_event_t *event,
    snowball_delivery_result_t result,
    const char *detail
) {
	if (event && strcmp(event->event, "sync") == 0) {
		trace_event(
			result == SNOWBALL_DELIVERY_SYNCED ? "candidate_sync_completed" : "candidate_sync_failed",
			event->diagnostic_attempt,
			detail,
			-1.0f
		);
		return;
	}
	if (!event || strcmp(event->event, "command") != 0 ||
        !attempt_is_current(event->diagnostic_attempt)) {
        if (event) trace_event("stale_delivery_ignored", event->diagnostic_attempt, detail, -1.0f);
        return;
    }
    bool project_media = strcmp(event->command, "project") == 0;
    switch (result) {
        case SNOWBALL_DELIVERY_EXECUTED:
            trace_event("command_executed", event->diagnostic_attempt, event->command, -1.0f);
            if (project_media) {
                speech_end_session(event->diagnostic_attempt);
                media_session_stop();
                (void)board_audio_queue_feedback_for_attempt(
                    SNOWBALL_FEEDBACK_ACTION_EXECUTED,
                    event->diagnostic_attempt
                );
            }
            break;
        case SNOWBALL_DELIVERY_ACCEPTED:
        case SNOWBALL_DELIVERY_NOT_READY:
            trace_event("command_not_ready", event->diagnostic_attempt, detail, -1.0f);
            set_failure_feedback_pending(event->diagnostic_attempt, false);
            media_session_stop();
            (void)board_audio_queue_feedback_for_attempt(
                SNOWBALL_FEEDBACK_ACTION_NOT_READY,
                event->diagnostic_attempt
            );
            break;
        case SNOWBALL_DELIVERY_PROCESSING:
            /* The Gateway is still finishing the browser transaction. The
               signed event is retried by the provisioning task, so keep the
               live media session intact and do not emit a failure tone. */
            trace_event("command_processing", event->diagnostic_attempt, detail, -1.0f);
            break;
        case SNOWBALL_DELIVERY_UNPAIRED:
            trace_event("device_unpaired_or_offline", event->diagnostic_attempt, detail, -1.0f);
            set_failure_feedback_pending(event->diagnostic_attempt, false);
            media_session_stop();
            (void)board_audio_queue_feedback_for_attempt(
                SNOWBALL_FEEDBACK_ACTION_NOT_READY,
                event->diagnostic_attempt
            );
            break;
        default:
            trace_event("command_failed", event->diagnostic_attempt, detail, -1.0f);
            set_failure_feedback_pending(event->diagnostic_attempt, true);
            media_session_stop();
            break;
    }
}

static void feedback_event(snowball_audio_feedback_t feedback, uint32_t attempt, esp_err_t result) {
    const char *detail = result == ESP_OK ? "played" : esp_err_to_name(result);
    const char *stage = feedback == SNOWBALL_FEEDBACK_WAKE_READY
        ? "wake_feedback_completed"
        : "feedback_completed";
    trace_event(stage, attempt, detail, -1.0f);
}

void app_main(void) {
    ESP_ERROR_CHECK(serial_output_init());
    esp_reset_reason_t reset_reason = esp_reset_reason();
    ESP_LOGI(TAG, "boot reset reason=%d", (int)reset_reason);
    serial_output_printf(
        "{\"version\":1,\"event\":\"reset\",\"reason\":%d}\n",
        (int)reset_reason
    );
    diagnostics_memory_snapshot("boot", 0);
    esp_err_t nvs_result = nvs_flash_init();
    if (nvs_result == ESP_ERR_NVS_NO_FREE_PAGES || nvs_result == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        nvs_result = nvs_flash_init();
    }
    ESP_ERROR_CHECK(nvs_result);
    ESP_ERROR_CHECK(device_identity_next_boot_sequence(&boot_nonce));

    snowball_device_identity_t identity;
    ESP_ERROR_CHECK(device_identity_load_or_create(&identity));
    ESP_LOGI(TAG, "device %s; public key fingerprint %.12s...", identity.hardware_id, identity.fingerprint);
    ESP_ERROR_CHECK(provisioning_start(&identity));
    provisioning_set_delivery_callback(delivery_event);
    ESP_ERROR_CHECK(board_audio_init());
    diagnostics_memory_snapshot("audio_ready", 0);
    board_audio_set_feedback_callback(feedback_event);
    ESP_ERROR_CHECK(media_session_init(media_event));
    diagnostics_memory_snapshot("media_ready", 0);
    ESP_ERROR_CHECK(speech_start(wake_event, command_event, microphone_audio));
    diagnostics_memory_snapshot("speech_ready", 0);
    /* Candidate names are fetched over the authenticated device channel after
       Wi-Fi/enrollment settle. The grammar consumes the persisted catalog on
       the next boot; no production names are compiled into the image. */
    provisioning_request_candidate_sync(boot_nonce, next_counter());
#if CONFIG_SNOWBALL_COMMAND_TAIL
    ESP_LOGI(TAG, "Snowball speaker ready: Hi ESP accepts an English command tail");
#else
    ESP_LOGI(TAG, "Snowball speaker ready: Hi ESP starts full-duplex Voice");
#endif
#if CONFIG_SPIRAM_FETCH_INSTRUCTIONS && CONFIG_SPIRAM_RODATA
    ESP_LOGI(TAG, "PSRAM cache-safety enabled: flash operations keep code/rodata accessible");
#else
    ESP_LOGW(TAG, "PSRAM cache-safety disabled: long-idle flash/cache panic risk remains");
#endif
}
