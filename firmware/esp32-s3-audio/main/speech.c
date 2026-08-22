#include "speech.h"

#include <assert.h>
#include <inttypes.h>
#include <stdbool.h>
#include <stdlib.h>

#include "board_audio.h"
#include "diagnostics.h"
#include "esp_afe_sr_iface.h"
#include "esp_afe_sr_models.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_task_wdt.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "model_path.h"
#include "serial_output.h"

static const char *TAG = "snowball/speech";
#define WAKE_REARM_FRESH_AUDIO_MS 800
#define WAKE_REARM_COOLDOWN_MS 3000
#define WAKE_REARM_QUIET_THRESHOLD 3500
#define WAKE_REARM_QUIET_FRAMES 8
#define AUDIO_LEVEL_LOG_INTERVAL_MS 10000
static const esp_afe_sr_iface_t *afe;
static esp_afe_sr_data_t *afe_data;
static srmodel_list_t *models;
static snowball_wake_callback_t on_wake;
static snowball_command_callback_t on_command;
static snowball_speech_audio_callback_t on_audio;
static portMUX_TYPE speech_lock = portMUX_INITIALIZER_UNLOCKED;
static bool running;
static bool session_mode;
static bool command_mode;
static uint32_t active_attempt;
/* AFE fetch can return one or more frames that were captured immediately
 * before Voice closed. Do not let one of those frames become a new wake word
 * as soon as the media task tears down. The deadline is set by
 * speech_end_session() and read under speech_lock because that callback runs
 * on the media task while detect_task runs on the AFE core. */
static int64_t wake_rearm_not_before_us;
static bool wakenet_enabled = true;
static bool aec_enabled;
static bool aec_available;

static bool session_is_active(void) {
    portENTER_CRITICAL(&speech_lock);
    bool value = session_mode;
    portEXIT_CRITICAL(&speech_lock);
    return value;
}

static void set_session_active(bool value) {
    portENTER_CRITICAL(&speech_lock);
    session_mode = value;
    portEXIT_CRITICAL(&speech_lock);
}

static bool command_is_active(void) {
    portENTER_CRITICAL(&speech_lock);
    bool value = command_mode;
    portEXIT_CRITICAL(&speech_lock);
    return value;
}

static void set_command_active(bool value, uint32_t attempt) {
    portENTER_CRITICAL(&speech_lock);
    command_mode = value;
    if (value) active_attempt = attempt;
    /* The command recognizer owns only the command-listening flag. The
       session generation must survive command resolution so a media failure
       can always perform terminal cleanup. */
    portEXIT_CRITICAL(&speech_lock);
}

static int64_t wake_rearm_deadline(void) {
    portENTER_CRITICAL(&speech_lock);
    int64_t deadline = wake_rearm_not_before_us;
    portEXIT_CRITICAL(&speech_lock);
    return deadline;
}

static bool frame_is_quiet(const int16_t *samples, size_t sample_count) {
    if (!samples || sample_count == 0) return false;
    for (size_t index = 0; index < sample_count; ++index) {
        int32_t value = samples[index];
        if (value < 0) value = -value;
        if (value > WAKE_REARM_QUIET_THRESHOLD) return false;
    }
    return true;
}

static void begin_session(uint32_t attempt) {
    portENTER_CRITICAL(&speech_lock);
    session_mode = true;
    if (attempt != 0) active_attempt = attempt;
    portEXIT_CRITICAL(&speech_lock);
}

#if CONFIG_SNOWBALL_COMMAND_TAIL
static bool resolve_command_frame(const int16_t *samples, uint32_t attempt) {
    snowball_command_result_t command = {0};
    snowball_command_state_t state = command_recognizer_feed(samples, &command);
    if (state != SNOWBALL_COMMAND_DETECTED && state != SNOWBALL_COMMAND_TIMEOUT) {
        return false;
    }
    board_audio_set_command_listening(false);
    set_command_active(false, attempt);
    if (state == SNOWBALL_COMMAND_TIMEOUT) {
        ESP_LOGI(TAG, "No command tail; resolving Hi ESP as new chat (attempt=%" PRIu32 ")", attempt);
    }
    begin_session(attempt);
    if (on_command) on_command(attempt, &command);
    return true;
}
#endif

static void feed_task(void *argument) {
    (void)argument;
    int chunk = afe->get_feed_chunksize(afe_data);
    int channels = afe->get_feed_channel_num(afe_data);
    assert(channels == board_audio_feed_channels());
    size_t bytes = (size_t)chunk * sizeof(int16_t) * (size_t)channels;
    int16_t *buffer = heap_caps_malloc(bytes, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    assert(buffer);
    diagnostics_memory_snapshot("speech_feed_ready", 0);
    esp_task_wdt_add(NULL);
    diagnostics_memory_snapshot("speech_detect_ready", 0);
    int64_t next_level_log_us = esp_timer_get_time();
    while (running) {
        if (board_audio_read(buffer, bytes) == ESP_OK) {
            afe->feed(afe_data, buffer);
            int64_t now_us = esp_timer_get_time();
            if (now_us >= next_level_log_us) {
                snowball_audio_levels_t levels = {0};
                board_audio_peek_levels(&levels);
                serial_output_printf(
                    "{\"version\":1,\"event\":\"audio_level\",\"sampleFrames\":%" PRIu32
                    ",\"peak\":[%u,%u,%u,%u]}\n",
                    levels.sample_frames,
                    (unsigned)levels.peak[0],
                    (unsigned)levels.peak[1],
                    (unsigned)levels.peak[2],
                    (unsigned)levels.peak[3]
                );
                next_level_log_us = now_us + ((int64_t)AUDIO_LEVEL_LOG_INTERVAL_MS * 1000);
            }
        }
        esp_task_wdt_reset();
        /* The AFE feed call can return immediately when the DMA ring already
         * has data.  Yield for one tick so the CPU0 idle task is not starved
         * and the task watchdog cannot reset the board during an otherwise
         * healthy idle/listening period. */
        vTaskDelay(1);
    }
    free(buffer);
    vTaskDelete(NULL);
}

static void detect_task(void *argument) {
    (void)argument;
    const size_t audio_samples = (size_t)afe->get_fetch_chunksize(afe_data);
    assert(audio_samples > 0);
    const size_t fresh_frames_required =
        (((size_t)SNOWBALL_AUDIO_SAMPLE_RATE * WAKE_REARM_FRESH_AUDIO_MS) +
         (audio_samples * 1000U) - 1U) /
        (audio_samples * 1000U);
    size_t fresh_frames_remaining = 0;
    size_t quiet_frames_remaining = 0;
    bool reset_before_rearm = false;
    bool cooldown_logged = false;
    bool quiet_logged = false;
    uint32_t fetch_count = 0;
    esp_task_wdt_add(NULL);
    ESP_LOGI(
        TAG,
        "WakeNet detector ready; Hi ESP %s",
#if CONFIG_SNOWBALL_COMMAND_TAIL
        "accepts an optional English command tail"
#else
        "starts the media session directly"
#endif
    );

    while (running) {
        int64_t fetch_started = esp_timer_get_time();
        afe_fetch_result_t *result = afe->fetch(afe_data);
        int64_t fetch_elapsed = esp_timer_get_time() - fetch_started;
        ++fetch_count;
        if (fetch_count <= 3 || fetch_elapsed >= 1000000) {
            ESP_LOGI(TAG, "AFE fetch duration=%" PRId64 " us count=%" PRIu32, fetch_elapsed, fetch_count);
        }
        if (!result || result->ret_value == ESP_FAIL) {
            ESP_LOGE(TAG, "AFE fetch failed");
            break;
        }

        bool in_session = session_is_active();
        if (in_session) {
            if (aec_available && !aec_enabled) {
                /* Keep the wake-word path light.  AEC is initialized in the
                 * AFE so full-duplex Voice can enable it, but it need not
                 * consume CPU while the board is idle or listening for the
                 * command tail. */
                afe->enable_aec(afe_data);
                aec_enabled = true;
                ESP_LOGI(TAG, "AEC enabled for full-duplex media");
            }
            if (wakenet_enabled) {
                afe->disable_wakenet(afe_data);
                wakenet_enabled = false;
            }
            reset_before_rearm = true;
            fresh_frames_remaining = 0;
            quiet_frames_remaining = 0;
            quiet_logged = false;
            if (on_audio && result->data) on_audio(result->data, audio_samples);
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        /* Drop the post-session tail before re-arming WakeNet. Without this
         * quiet window, a wake word spoken while the previous Voice session
         * was still closing can remain in the AFE ring and fire one second
         * after the user said Bye, creating an apparently spontaneous second
         * session. */
        int64_t rearm_deadline = wake_rearm_deadline();
        int64_t now_us = esp_timer_get_time();
        if (now_us < rearm_deadline) {
            if (!cooldown_logged) {
                ESP_LOGI(
                    TAG,
                    "WakeNet suppressed after media end for %" PRId64 " ms",
                    (rearm_deadline - now_us) / 1000
                );
                cooldown_logged = true;
            }
            if (wakenet_enabled) {
                afe->disable_wakenet(afe_data);
                wakenet_enabled = false;
            }
            reset_before_rearm = true;
            fresh_frames_remaining = 0;
            quiet_frames_remaining = WAKE_REARM_QUIET_FRAMES;
            quiet_logged = false;
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }
        cooldown_logged = false;

        if (aec_available && aec_enabled) {
            afe->disable_aec(afe_data);
            aec_enabled = false;
            ESP_LOGI(TAG, "AEC disabled outside full-duplex media");
        }

        if (command_is_active()) {
#if CONFIG_SNOWBALL_COMMAND_TAIL
            portENTER_CRITICAL(&speech_lock);
            uint32_t attempt = active_attempt;
            portEXIT_CRITICAL(&speech_lock);
            (void)resolve_command_frame(result->data, attempt);
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
#else
            /* This branch is compiled only when a stale sdkconfig explicitly
             * enables command_mode without the recognizer. Recover instead of
             * spinning forever in an impossible state. */
            board_audio_set_command_listening(false);
            set_command_active(false, active_attempt);
#endif
        }

        bool feedback_busy = board_audio_feedback_busy() || board_audio_stream_active();
        if (feedback_busy) {
            if (wakenet_enabled) {
                afe->disable_wakenet(afe_data);
                wakenet_enabled = false;
            }
            reset_before_rearm = true;
            fresh_frames_remaining = 0;
            quiet_frames_remaining = WAKE_REARM_QUIET_FRAMES;
            quiet_logged = false;
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        if (!wakenet_enabled && reset_before_rearm) {
            int reset_result = afe->reset_buffer ? afe->reset_buffer(afe_data) : -1;
            if (reset_result != 1) {
                ESP_LOGW(TAG, "AFE buffer reset failed while re-arming WakeNet: %d", reset_result);
                vTaskDelay(pdMS_TO_TICKS(20));
                esp_task_wdt_reset();
                vTaskDelay(1);
                continue;
            }
            reset_before_rearm = false;
            fresh_frames_remaining = fresh_frames_required;
            quiet_frames_remaining = WAKE_REARM_QUIET_FRAMES;
            quiet_logged = false;
            ESP_LOGI(TAG, "AFE buffer reset; collecting fresh audio before WakeNet re-arm");
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        if (!wakenet_enabled && fresh_frames_remaining > 0) {
            --fresh_frames_remaining;
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        /* AFE reset removes the buffered tail, but the microphone can still
         * contain the last word of a user turn. Require a short quiet window
         * before arming WakeNet again; otherwise a late Bye/answer frame can
         * be promoted to a spontaneous second wake. This is intentionally a
         * small energy gate, not a speech recognizer, and only runs during the
         * post-session re-arm window. */
        if (!wakenet_enabled && quiet_frames_remaining > 0) {
            if (frame_is_quiet(result->data, audio_samples)) {
                --quiet_frames_remaining;
            } else {
                quiet_frames_remaining = WAKE_REARM_QUIET_FRAMES;
                if (!quiet_logged) {
                    ESP_LOGI(TAG, "WakeNet re-arm waiting for quiet microphone audio");
                    quiet_logged = true;
                }
            }
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        if (!wakenet_enabled) {
            afe->enable_wakenet(afe_data);
            wakenet_enabled = true;
            ESP_LOGI(TAG, "WakeNet re-armed");
            /* Never recognize a stale frame captured while playback was
             * active as a fresh wake word. */
            esp_task_wdt_reset();
            vTaskDelay(1);
            continue;
        }

        if (wakenet_enabled && result->wakeup_state != WAKENET_NO_DETECT) {
            ESP_LOGI(
                TAG,
                "WakeNet state=%d raw_channels=%d trigger_channel=%d",
                result->wakeup_state,
                result->raw_data_channels,
                result->trigger_channel_id
            );
        }
        /* Both positive WakeNet states are detections. In a multi-channel
         * model, DETECTED may precede channel verification; requiring only
         * CHANNEL_VERIFIED silently dropped legitimate Hi ESP events and
         * made the speaker appear unresponsive. The model is disabled
         * immediately below, so this remains a one-shot trigger. */
        bool detected = wakenet_enabled &&
            (result->wakeup_state == WAKENET_DETECTED ||
             result->wakeup_state == WAKENET_CHANNEL_VERIFIED);
        if (detected) {
            afe->disable_wakenet(afe_data);
            wakenet_enabled = false;
            reset_before_rearm = true;
            fresh_frames_remaining = 0;
            quiet_frames_remaining = 0;
            quiet_logged = false;
            /* A new wake supersedes any terminal/error tone left from the
               prior attempt. Stop that playback before reserving the command
               tail so the next interaction cannot inherit stale audio. */
            board_audio_begin_wake();
            uint32_t attempt = on_wake ? on_wake(result->trigger_channel_id) : 0;
            if (attempt != 0) {
#if CONFIG_SNOWBALL_COMMAND_TAIL
                command_recognizer_begin();
                if (board_audio_try_begin_command_listening()) {
                    set_command_active(true, attempt);
                    /* Wake feedback is queued only after the command tail has
                       reserved its audio state; this keeps the first chime
                       immediate without racing MultiNet setup. */
                    if (board_audio_queue_feedback_for_attempt(SNOWBALL_FEEDBACK_WAKE_READY, attempt) != ESP_OK) {
                        ESP_LOGW(TAG, "wake feedback queue failed (attempt=%" PRIu32 ")", attempt);
                    }
                    ESP_LOGI(TAG, "Hi ESP detected; listening for command tail (attempt=%" PRIu32 ")", attempt);
                    /* Do not discard the frame that triggered WakeNet. Feeding
                     * it immediately lets "Hi ESP Resume" work as one phrase
                     * while the next fetches continue the bounded tail. */
                    portENTER_CRITICAL(&speech_lock);
                    uint32_t command_attempt = active_attempt;
                    portEXIT_CRITICAL(&speech_lock);
                    (void)resolve_command_frame(result->data, command_attempt);
                } else {
                    ESP_LOGW(TAG, "Command listener unavailable after wake (attempt=%" PRIu32 ")", attempt);
                    (void)board_audio_queue_feedback_for_attempt(SNOWBALL_FEEDBACK_WAKE_READY, attempt);
                    set_command_active(false, attempt);
                    set_session_active(true);
                    snowball_command_result_t fallback = {
                        .kind = SNOWBALL_COMMAND_NEW_CHAT,
                        .target = SNOWBALL_COMMAND_TARGET_CHATGPT,
                        .confidence = 0.0f,
                    };
                    if (on_command) on_command(attempt, &fallback);
                }
#else
                begin_session(attempt);
                (void)board_audio_queue_feedback_for_attempt(SNOWBALL_FEEDBACK_WAKE_READY, attempt);
                snowball_command_result_t new_chat = {
                    .kind = SNOWBALL_COMMAND_NEW_CHAT,
                    .target = SNOWBALL_COMMAND_TARGET_CHATGPT,
                    .confidence = 1.0f,
                };
                if (on_command) on_command(attempt, &new_chat);
#endif
            } else {
                begin_session(attempt);
            }
        }
        esp_task_wdt_reset();
        /* Fetch can also complete without blocking once the AFE queue is
         * warm. Give the idle task a bounded scheduling point on CPU1. */
        vTaskDelay(1);
    }
    vTaskDelete(NULL);
}

esp_err_t speech_start(
    snowball_wake_callback_t wake_callback,
    snowball_command_callback_t command_callback,
    snowball_speech_audio_callback_t audio_callback
) {
    if (!wake_callback || !command_callback || !audio_callback) return ESP_ERR_INVALID_ARG;
    on_wake = wake_callback;
    on_command = command_callback;
    on_audio = audio_callback;
    models = esp_srmodel_init("model");
    if (!models) return ESP_ERR_NOT_FOUND;
    afe_config_t *config = afe_config_init(board_audio_input_format(), models, AFE_TYPE_SR, AFE_MODE_LOW_COST);
    if (!config) return ESP_ERR_NO_MEM;
    /* Match Waveshare's known-good esp_sr_02 configuration for this microphone array. */
    /*
     * Keep the wake/command path free of the AEC processor.  The esp-sr
     * runtime toggle only changes the AEC output flag; it does not remove the
     * AEC task from the AFE pipeline.  The same applies to the microphone-array
     * BSS stage (SE).  Either stage can hold the interrupt watchdog on this
     * board while no Voice session is active.  Full-duplex transport can still
     * be exercised with the raw microphone mix; AEC/SE are later, measured
     * capability gates rather than reasons to make wake recognition reboot.
     */
    config->aec_init = false;
    config->se_init = false;
    config->ns_init = false;
    config->vad_init = false;
    afe = esp_afe_handle_from_config(config);
    if (!afe) {
        afe_config_free(config);
        return ESP_FAIL;
    }
    afe_data = afe->create_from_config(config);
    afe_config_free(config);
    if (!afe_data) return ESP_ERR_NO_MEM;
#if CONFIG_SNOWBALL_COMMAND_TAIL
    esp_err_t command_result = command_recognizer_init(models, (size_t)afe->get_fetch_chunksize(afe_data));
    if (command_result != ESP_OK) {
        ESP_LOGE(TAG, "command recognizer initialization failed: %s", esp_err_to_name(command_result));
        return command_result;
    }
#endif
    running = true;
    wakenet_enabled = true;
    aec_available = false;
    aec_enabled = false;
    portENTER_CRITICAL(&speech_lock);
    wake_rearm_not_before_us = 0;
    portEXIT_CRITICAL(&speech_lock);
    ESP_LOGI(TAG, "AFE AEC initialized=%d", aec_available);
    /* AEC is deliberately absent from this stable wake/command profile. */
    if (aec_available) afe->disable_aec(afe_data);
    set_session_active(false);
    set_command_active(false, 0);
    if (xTaskCreatePinnedToCore(detect_task, "speech_detect", 8192, NULL, 5, NULL, 1) != pdPASS ||
        xTaskCreatePinnedToCore(feed_task, "speech_feed", 8192, NULL, 5, NULL, 0) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    return ESP_OK;
}

void speech_end_session(uint32_t attempt) {
    portENTER_CRITICAL(&speech_lock);
    if (attempt != 0 && active_attempt != 0 && active_attempt != attempt) {
        portEXIT_CRITICAL(&speech_lock);
        return;
    }
    session_mode = false;
    command_mode = false;
    active_attempt = 0;
    wake_rearm_not_before_us = esp_timer_get_time() + ((int64_t)WAKE_REARM_COOLDOWN_MS * 1000);
    portEXIT_CRITICAL(&speech_lock);
    board_audio_set_command_listening(false);
}
