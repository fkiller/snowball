#include "media_session.h"

#include <inttypes.h>
#include <stdlib.h>
#include <string.h>

#include "board_audio.h"
#include "diagnostics.h"
#include "esp_heap_caps.h"
#include "esp_log.h"
#include "esp_peer.h"
#include "esp_peer_default.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/task.h"
#include "provisioning.h"

#define MEDIA_SDP_MAX (64U << 10)
#define MEDIA_CONNECT_TIMEOUT_MS 30000
#define MEDIA_AUDIO_SAMPLES_MAX 320
#define MEDIA_AUDIO_QUEUE_DEPTH 8 /* 256 ms at the physical 32 ms uplink cadence */
#define MEDIA_DOWNLINK_QUEUE_DEPTH 8 /* 160 ms at the Gateway's 20 ms packetization */
#define MEDIA_PLAYBACK_TASK_STACK 8192
#define MEDIA_PLAYBACK_TASK_PRIORITY 5
#define MEDIA_METRICS_INTERVAL_MS 5000
#define MEDIA_PREVOICE_FRAME_CAPACITY 200 /* 6.4 seconds of physical 32 ms G.711A frames */
#define MEDIA_TLS_MIN_PSRAM_BLOCK (128U << 10)
#define MEDIA_TLS_MIN_INTERNAL_BLOCK (16U << 10)

static const char *TAG = "snowball/media";
static portMUX_TYPE media_lock = portMUX_INITIALIZER_UNLOCKED;
static QueueHandle_t audio_queue;
static QueueHandle_t downlink_queue;
static size_t prevoice_head;
static size_t prevoice_count;
static size_t prevoice_replay_index;
static size_t prevoice_replay_remaining;
static uint32_t prevoice_overwritten_frames;
static uint32_t prevoice_skipped_frames;
static bool uplink_enabled;
static snowball_media_callback_t event_callback;
static esp_peer_handle_t active_peer;
static TaskHandle_t media_task_handle;
static TaskHandle_t playback_task_handle;
static bool active;
static bool connected;
static bool stop_requested;
static bool dtls_closed;
static esp_peer_state_t peer_state = ESP_PEER_STATE_CLOSED;
static uint32_t session_attempt;
static uint32_t session_boot_nonce;
static uint32_t session_offer_counter;
static char *local_sdp;
static uint32_t uplink_frames;
static uint32_t uplink_generated_frames;
static uint32_t uplink_dropped_frames;
static uint32_t uplink_would_block_count;
static uint32_t uplink_queue_high_water;
static uint32_t downlink_frames;
static uint32_t downlink_played_frames;
static uint32_t downlink_dropped_frames;
static uint32_t downlink_queue_high_water;
static uint32_t speaker_samples;
static uint32_t playback_write_max_us;
static uint32_t playback_stack_low_water;
static bool live_audio_sent;
static bool playback_stop_requested;
static int64_t next_metrics_log_us;

static bool tls_memory_gate(void) {
    size_t psram_largest = heap_caps_get_largest_free_block(MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    size_t internal_largest = heap_caps_get_largest_free_block(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT);
    if (psram_largest < MEDIA_TLS_MIN_PSRAM_BLOCK || internal_largest < MEDIA_TLS_MIN_INTERNAL_BLOCK) {
        ESP_LOGE(
            TAG,
            "TLS memory gate failed: psram_largest=%u (min=%u) internal_largest=%u (min=%u)",
            (unsigned)psram_largest,
            (unsigned)MEDIA_TLS_MIN_PSRAM_BLOCK,
            (unsigned)internal_largest,
            (unsigned)MEDIA_TLS_MIN_INTERNAL_BLOCK
        );
        return false;
    }
    ESP_LOGI(
        TAG,
        "TLS memory gate passed: psram_largest=%u internal_largest=%u",
        (unsigned)psram_largest,
        (unsigned)internal_largest
    );
    return true;
}

typedef struct {
    uint16_t size;
    uint32_t pts;
    uint8_t data[MEDIA_AUDIO_SAMPLES_MAX];
} encoded_audio_t;

typedef struct {
    uint16_t size;
    uint32_t attempt;
    uint8_t data[MEDIA_AUDIO_SAMPLES_MAX];
} downlink_audio_t;

static encoded_audio_t *prevoice_frames;
static uint32_t uplink_pts_ms;

static uint8_t linear_to_alaw(int16_t sample) {
    static const uint16_t segment_end[8] = {
        0x001f, 0x003f, 0x007f, 0x00ff, 0x01ff, 0x03ff, 0x07ff, 0x0fff,
    };
    int value = sample;
    uint8_t mask;
    if (value >= 0) {
        mask = 0xd5;
    } else {
        mask = 0x55;
        value = -value - 1;
    }
    if (value > 32767) value = 32767;
    value >>= 3;
    int segment = 0;
    while (segment < 8 && value > segment_end[segment]) ++segment;
    if (segment >= 8) return (uint8_t)(0x7f ^ mask);
    uint8_t encoded = (uint8_t)(segment << 4);
    encoded |= segment < 2
        ? (uint8_t)((value >> 1) & 0x0f)
        : (uint8_t)((value >> segment) & 0x0f);
    return encoded ^ mask;
}

static int16_t alaw_to_linear(uint8_t encoded) {
    encoded ^= 0x55;
    int value = (encoded & 0x0f) << 4;
    int segment = (encoded & 0x70) >> 4;
    switch (segment) {
        case 0:
            value += 8;
            break;
        case 1:
            value += 0x108;
            break;
        default:
            value += 0x108;
            value <<= segment - 1;
            break;
    }
    return (int16_t)((encoded & 0x80) ? value : -value);
}

static void publish(snowball_media_event_t event, const char *detail) {
    snowball_media_callback_t callback = event_callback;
    if (callback) callback(event, session_attempt, detail);
}

static int peer_state_callback(esp_peer_state_t state, void *context) {
    (void)context;
    portENTER_CRITICAL(&media_lock);
    peer_state = state;
    connected = state == ESP_PEER_STATE_CONNECTED;
    portEXIT_CRITICAL(&media_lock);
    ESP_LOGI(TAG, "peer state %d", state);
    return 0;
}

static int peer_message_callback(esp_peer_msg_t *message, void *context) {
    (void)context;
    if (!message || message->type != ESP_PEER_MSG_TYPE_SDP || !message->data || message->size < 4 || local_sdp) {
        return 0;
    }
    size_t length = strnlen((const char *)message->data, (size_t)message->size);
    if (length < 128 || length >= MEDIA_SDP_MAX || memcmp(message->data, "v=0", 3) != 0) {
        ESP_LOGE(TAG, "peer returned an invalid local SDP");
        return ESP_PEER_ERR_BAD_DATA;
    }
    local_sdp = heap_caps_malloc(length + 1, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!local_sdp) return ESP_PEER_ERR_NO_MEM;
    memcpy(local_sdp, message->data, length);
    local_sdp[length] = '\0';
    return 0;
}

static int peer_audio_info_callback(esp_peer_audio_stream_info_t *info, void *context) {
    (void)context;
    if (!info || info->codec != ESP_PEER_AUDIO_CODEC_G711A || info->sample_rate != 8000 || info->channel != 1) {
        ESP_LOGE(TAG, "Gateway negotiated unsupported audio");
        return ESP_PEER_ERR_NOT_SUPPORT;
    }
    return 0;
}

static int peer_audio_callback(esp_peer_audio_frame_t *frame, void *context) {
    (void)context;
    if (!frame || !frame->data || frame->size <= 0) return ESP_PEER_ERR_BAD_DATA;
    // The peer can deliver a packet between DTLS completion and I2S startup.
    // Dropping that leading packet keeps a harmless race from tearing down the
    // otherwise healthy session.
    if (!board_audio_stream_active() || !downlink_queue) return 0;
    portENTER_CRITICAL(&media_lock);
    ++downlink_frames;
    uint32_t received_frames = downlink_frames;
    uint32_t attempt = session_attempt;
    portEXIT_CRITICAL(&media_lock);
    if (received_frames == 1) {
        ESP_LOGI(TAG, "first downlink audio frame received: %d bytes", frame->size);
    }

    int offset = 0;
    while (offset < frame->size) {
        int count = frame->size - offset;
        if (count > MEDIA_AUDIO_SAMPLES_MAX) count = MEDIA_AUDIO_SAMPLES_MAX;
        downlink_audio_t audio = {
            .size = (uint16_t)count,
            .attempt = attempt,
        };
        memcpy(audio.data, frame->data + offset, (size_t)count);
        bool dropped = false;
        if (xQueueSend(downlink_queue, &audio, 0) != pdTRUE) {
            /* Full-duplex speech is real-time: keeping an old packet would
             * convert a transient playback stall into permanently growing
             * latency. Drop the oldest queued packet and retain the newest. */
            downlink_audio_t stale = {0};
            if (xQueueReceive(downlink_queue, &stale, 0) != pdTRUE ||
                xQueueSend(downlink_queue, &audio, 0) != pdTRUE) {
                dropped = true; /* The newest packet could not be retained. */
            } else {
                dropped = true; /* The displaced oldest packet was dropped. */
            }
            memset(&stale, 0, sizeof(stale));
        }
        UBaseType_t depth = uxQueueMessagesWaiting(downlink_queue);
        portENTER_CRITICAL(&media_lock);
        if (dropped) ++downlink_dropped_frames;
        if ((uint32_t)depth > downlink_queue_high_water) {
            downlink_queue_high_water = (uint32_t)depth;
        }
        portEXIT_CRITICAL(&media_lock);
        memset(&audio, 0, sizeof(audio));
        offset += count;
    }
    return 0;
}

static void playback_task(void *argument) {
    TaskHandle_t current_task = xTaskGetCurrentTaskHandle();
    uint32_t worker_attempt = (uint32_t)(uintptr_t)argument;
    uint32_t processed_frames = 0;
    downlink_audio_t audio;
    int16_t pcm[MEDIA_AUDIO_SAMPLES_MAX];
    while (xQueueReceive(downlink_queue, &audio, portMAX_DELAY) == pdTRUE) {
        portENTER_CRITICAL(&media_lock);
        bool stop = playback_stop_requested || worker_attempt != session_attempt ||
            audio.attempt != worker_attempt;
        portEXIT_CRITICAL(&media_lock);
        if (stop || audio.size == 0) break;

        for (size_t index = 0; index < audio.size; ++index) {
            pcm[index] = alaw_to_linear(audio.data[index]);
        }
        int64_t started_us = esp_timer_get_time();
        esp_err_t result = board_audio_stream_write_pcm8k(pcm, audio.size);
        uint32_t elapsed_us = (uint32_t)(esp_timer_get_time() - started_us);
        ++processed_frames;
        uint32_t stack_low_water = 0;
        if ((processed_frames & 0x3fU) == 1U) {
            stack_low_water = (uint32_t)uxTaskGetStackHighWaterMark(NULL);
        }
        portENTER_CRITICAL(&media_lock);
        bool stopping = playback_stop_requested;
        if (result == ESP_OK) {
            ++downlink_played_frames;
            speaker_samples += audio.size;
        } else if (!stopping) {
            ++downlink_dropped_frames;
        }
        if (elapsed_us > playback_write_max_us) playback_write_max_us = elapsed_us;
        if (stack_low_water > 0 &&
            (playback_stack_low_water == 0 || stack_low_water < playback_stack_low_water)) {
            playback_stack_low_water = stack_low_water;
        }
        portEXIT_CRITICAL(&media_lock);
        if (result != ESP_OK && !stopping) {
            ESP_LOGW(TAG, "speaker playback dropped: %s", esp_err_to_name(result));
        }
        memset(&audio, 0, sizeof(audio));
        memset(pcm, 0, sizeof(pcm));
    }
    memset(&audio, 0, sizeof(audio));
    memset(pcm, 0, sizeof(pcm));
    portENTER_CRITICAL(&media_lock);
    if (playback_task_handle == current_task) playback_task_handle = NULL;
    portEXIT_CRITICAL(&media_lock);
    vTaskDelete(NULL);
}

static esp_err_t start_playback_task(void) {
    portENTER_CRITICAL(&media_lock);
    if (playback_task_handle) {
        portEXIT_CRITICAL(&media_lock);
        ESP_LOGE(TAG, "refusing to overlap playback tasks");
        return ESP_ERR_INVALID_STATE;
    }
    playback_stop_requested = false;
    uint32_t attempt = session_attempt;
    portEXIT_CRITICAL(&media_lock);
    xQueueReset(downlink_queue);
    if (xTaskCreate(
            playback_task,
            "snowball_playback",
            MEDIA_PLAYBACK_TASK_STACK,
            (void *)(uintptr_t)attempt,
            MEDIA_PLAYBACK_TASK_PRIORITY,
            &playback_task_handle
        ) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    return ESP_OK;
}

static void stop_playback_task(void) {
    portENTER_CRITICAL(&media_lock);
    playback_stop_requested = true;
    TaskHandle_t task = playback_task_handle;
    portEXIT_CRITICAL(&media_lock);
    if (!task) {
        xQueueReset(downlink_queue);
        return;
    }

    /* Publish stream stop before waiting so an in-flight codec write finishes
     * and all subsequent writes fail closed. A zero-size item wakes a worker
     * that is blocked on the queue. */
    board_audio_stream_stop();
    xQueueReset(downlink_queue);
    downlink_audio_t sentinel = {0};
    (void)xQueueSend(downlink_queue, &sentinel, 0);
    for (int retry = 0; retry < 100; ++retry) {
        portENTER_CRITICAL(&media_lock);
        bool stopped = playback_task_handle == NULL;
        portEXIT_CRITICAL(&media_lock);
        if (stopped) return;
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    ESP_LOGE(TAG, "playback task did not stop within 1 second");
}

static bool should_stop(void) {
    portENTER_CRITICAL(&media_lock);
    bool requested = stop_requested;
    portEXIT_CRITICAL(&media_lock);
    return requested;
}

static esp_peer_state_t current_peer_state(void) {
    portENTER_CRITICAL(&media_lock);
    esp_peer_state_t state = peer_state;
    portEXIT_CRITICAL(&media_lock);
    return state;
}

static void send_queued_audio(esp_peer_handle_t peer) {
    encoded_audio_t audio;
    while (xQueueReceive(audio_queue, &audio, 0) == pdTRUE) {
        esp_peer_audio_frame_t frame = {
            .pts = audio.pts,
            .data = audio.data,
            .size = audio.size,
        };
        int result = esp_peer_send_audio(peer, &frame);
        if (result == ESP_PEER_ERR_WOULD_BLOCK) {
            bool retained = xQueueSendToFront(audio_queue, &audio, 0) == pdTRUE;
            portENTER_CRITICAL(&media_lock);
            ++uplink_would_block_count;
            if (!retained) ++uplink_dropped_frames;
            portEXIT_CRITICAL(&media_lock);
            break;
        }
        if (result != ESP_PEER_ERR_NONE) {
            portENTER_CRITICAL(&media_lock);
            ++uplink_dropped_frames;
            portEXIT_CRITICAL(&media_lock);
            ESP_LOGW(TAG, "audio send failed: %d", result);
            break;
        }
        portENTER_CRITICAL(&media_lock);
        ++uplink_frames;
        uint32_t sent_frames = uplink_frames;
        portEXIT_CRITICAL(&media_lock);
        if (!live_audio_sent) {
            live_audio_sent = true;
            ESP_LOGI(TAG, "first live microphone frame sent: %u bytes (total uplink_frames=%" PRIu32 ")",
                     (unsigned)audio.size, sent_frames);
        }
    }
    memset(&audio, 0, sizeof(audio));
}

/* Gateway's signed executed receipt means the browser has entered Voice.
 * Replay the bounded preserved speech frames immediately into the peer so live audio
 * can resume in real time without lag or queue dropouts. */
static void send_prevoice_audio(esp_peer_handle_t peer) {
    while (1) {
        encoded_audio_t audio = {0};
        bool available = false;
        portENTER_CRITICAL(&media_lock);
        if (uplink_enabled && prevoice_replay_remaining > 0) {
            audio = prevoice_frames[prevoice_replay_index];
            prevoice_replay_index = (prevoice_replay_index + 1U) % MEDIA_PREVOICE_FRAME_CAPACITY;
            --prevoice_replay_remaining;
            available = true;
        }
        portEXIT_CRITICAL(&media_lock);
        if (!available) break;

        esp_peer_audio_frame_t frame = { .pts = audio.pts, .data = audio.data, .size = audio.size };
        int result = esp_peer_send_audio(peer, &frame);
        if (result == ESP_PEER_ERR_WOULD_BLOCK) {
            portENTER_CRITICAL(&media_lock);
            prevoice_replay_index =
                (prevoice_replay_index + MEDIA_PREVOICE_FRAME_CAPACITY - 1U) %
                MEDIA_PREVOICE_FRAME_CAPACITY;
            ++prevoice_replay_remaining;
            ++uplink_would_block_count;
            portEXIT_CRITICAL(&media_lock);
            break;
        } else if (result != ESP_PEER_ERR_NONE) {
            portENTER_CRITICAL(&media_lock);
            ++uplink_dropped_frames;
            portEXIT_CRITICAL(&media_lock);
            ESP_LOGW(TAG, "pre-Voice audio send failed: %d", result);
            break;
        }
        portENTER_CRITICAL(&media_lock);
        ++uplink_frames;
        uint32_t sent_frames = uplink_frames;
        portEXIT_CRITICAL(&media_lock);
        if (sent_frames == 1) {
            ESP_LOGI(TAG, "first preserved microphone frame sent: %u bytes", (unsigned)audio.size);
        }
    }
}

static void log_media_metrics(void) {
    int64_t now_us = esp_timer_get_time();
    if (now_us < next_metrics_log_us) return;
    next_metrics_log_us = now_us + ((int64_t)MEDIA_METRICS_INTERVAL_MS * 1000);

    portENTER_CRITICAL(&media_lock);
    uint32_t generated = uplink_generated_frames;
    uint32_t sent = uplink_frames;
    uint32_t uplink_dropped = uplink_dropped_frames;
    uint32_t would_block = uplink_would_block_count;
    uint32_t uplink_high_water = uplink_queue_high_water;
    uint32_t prevoice_overwritten = prevoice_overwritten_frames;
    uint32_t prevoice_skipped = prevoice_skipped_frames;
    uint32_t received = downlink_frames;
    uint32_t played = downlink_played_frames;
    uint32_t downlink_dropped = downlink_dropped_frames;
    uint32_t downlink_high_water = downlink_queue_high_water;
    uint32_t max_write_us = playback_write_max_us;
    uint32_t stack_low_water = playback_stack_low_water;
    uint32_t attempt = session_attempt;
    portEXIT_CRITICAL(&media_lock);

    ESP_LOGI(
        TAG,
        "[MEDIA] attempt=%" PRIu32 " uplink generated=%" PRIu32 " sent=%" PRIu32 " dropped=%" PRIu32
        " queued=%u high_water=%" PRIu32 " would_block=%" PRIu32
        " prevoice_overwritten=%" PRIu32 " prevoice_skipped=%" PRIu32
        " downlink received=%" PRIu32 " played=%" PRIu32 " dropped=%" PRIu32
        " queued=%u high_water=%" PRIu32 " playback_max_us=%" PRIu32
        " playback_stack_low_water=%" PRIu32,
        attempt,
        generated,
        sent,
        uplink_dropped,
        (unsigned)uxQueueMessagesWaiting(audio_queue),
        uplink_high_water,
        would_block,
        prevoice_overwritten,
        prevoice_skipped,
        received,
        played,
        downlink_dropped,
        (unsigned)uxQueueMessagesWaiting(downlink_queue),
        downlink_high_water,
        max_write_us,
        stack_low_water
    );
}

static void media_task(void *argument) {
    (void)argument;
    diagnostics_memory_snapshot("media_task_start", session_attempt);
    const char *failure = NULL;
    bool published_connected = false;
    char *answer_sdp = NULL;
    esp_peer_default_cfg_t default_config = {
        .agent_recv_timeout = 100,
        .rtp_cfg = {
            .audio_recv_jitter = {
                .cache_timeout = 120,
                .resend_delay = 40,
                .cache_size = 8192,
            },
            .send_pool_size = 8192,
            .send_queue_num = 32,
            .max_resend_count = 2,
        },
    };
    esp_peer_cfg_t config = {
        .role = ESP_PEER_ROLE_CONTROLLING,
        .audio_info = {
            .codec = ESP_PEER_AUDIO_CODEC_G711A,
            .sample_rate = 8000,
            .channel = 1,
        },
        .audio_dir = ESP_PEER_MEDIA_DIR_SEND_RECV,
        .video_dir = ESP_PEER_MEDIA_DIR_NONE,
        .no_auto_reconnect = true,
        .enable_data_channel = false,
        .extra_cfg = &default_config,
        .extra_size = sizeof(default_config),
        .on_state = peer_state_callback,
        .on_msg = peer_message_callback,
        .on_audio_info = peer_audio_info_callback,
        .on_audio_data = peer_audio_callback,
    };

    publish(SNOWBALL_MEDIA_CONNECTING, "creating_peer");
    int peer_result = esp_peer_open(&config, esp_peer_get_default_impl(), &active_peer);
    diagnostics_memory_snapshot("peer_open", session_attempt);
    if (peer_result != ESP_PEER_ERR_NONE) {
        failure = "peer_open_failed";
        goto cleanup;
    }
    peer_result = esp_peer_new_connection(active_peer);
    if (peer_result != ESP_PEER_ERR_NONE) {
        failure = "offer_creation_failed";
        goto cleanup;
    }

    int64_t deadline = esp_timer_get_time() + ((int64_t)MEDIA_CONNECT_TIMEOUT_MS * 1000);
    while (!local_sdp && !should_stop() && esp_timer_get_time() < deadline) {
        int loop_result = esp_peer_main_loop(active_peer);
        if (loop_result != ESP_PEER_ERR_NONE) {
            ESP_LOGW(TAG, "peer main loop failed while creating offer: %d", loop_result);
            if (!should_stop()) failure = "peer_loop_failed";
            goto cleanup;
        }
        vTaskDelay(pdMS_TO_TICKS(10));
    }
    if (should_stop()) goto cleanup;
    if (!local_sdp) {
        failure = "local_sdp_timeout";
        goto cleanup;
    }

    answer_sdp = heap_caps_calloc(1, MEDIA_SDP_MAX, MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!answer_sdp) {
        failure = "answer_memory_failed";
        goto cleanup;
    }
    if (!tls_memory_gate()) {
        failure = "tls_memory_gate_failed";
        goto cleanup;
    }
    diagnostics_memory_snapshot("tls_before_offer", session_attempt);
    esp_err_t exchange = provisioning_exchange_media_offer(
        session_boot_nonce,
        session_offer_counter,
        local_sdp,
        answer_sdp,
        MEDIA_SDP_MAX
    );
    diagnostics_memory_snapshot("tls_after_offer", session_attempt);
    if (exchange != ESP_OK) {
        failure = exchange == ESP_ERR_INVALID_STATE ? "device_not_paired_or_offline" : "gateway_offer_failed";
        goto cleanup;
    }
    esp_peer_msg_t answer = {
        .type = ESP_PEER_MSG_TYPE_SDP,
        .data = (uint8_t *)answer_sdp,
        .size = (int)strlen(answer_sdp),
    };
    if (esp_peer_send_msg(active_peer, &answer) != ESP_PEER_ERR_NONE) {
        failure = "gateway_answer_rejected";
        goto cleanup;
    }

    while (!should_stop()) {
        int loop_result = esp_peer_main_loop(active_peer);
        if (loop_result != ESP_PEER_ERR_NONE) {
            ESP_LOGW(TAG, "peer main loop failed: %d", loop_result);
            if (!should_stop()) failure = "peer_loop_failed";
            break;
        }
        if (should_stop()) break;
        esp_peer_state_t state = current_peer_state();
        if (state == ESP_PEER_STATE_CONNECTED) {
            if (!published_connected) {
                if (board_audio_feedback_busy()) {
                    vTaskDelay(pdMS_TO_TICKS(10));
                    continue;
                }
                if (board_audio_stream_start() != ESP_OK) {
                    failure = "speaker_stream_failed";
                    break;
                }
                if (start_playback_task() != ESP_OK) {
                    board_audio_stream_stop();
                    failure = "playback_task_failed";
                    break;
                }
                published_connected = true;
                diagnostics_memory_snapshot("voice_connected", session_attempt);
                publish(SNOWBALL_MEDIA_CONNECTED, "pcma_connected");
            }
            send_prevoice_audio(active_peer);
            portENTER_CRITICAL(&media_lock);
            bool replaying_prevoice = uplink_enabled && prevoice_replay_remaining > 0;
            portEXIT_CRITICAL(&media_lock);
            if (!replaying_prevoice) send_queued_audio(active_peer);
        } else if (state == ESP_PEER_STATE_CONNECT_FAILED ||
                   (published_connected && state == ESP_PEER_STATE_DISCONNECTED)) {
            if (state == ESP_PEER_STATE_DISCONNECTED) {
                /* esp_peer 1.2.7 reports a remote DTLS close through its
                 * state callback.  Keep this transport transition local; the
                 * media task still owns codec and peer cleanup below. */
                portENTER_CRITICAL(&media_lock);
                dtls_closed = true;
                portEXIT_CRITICAL(&media_lock);
            } else {
                failure = "peer_connect_failed";
            }
            break;
        } else if (!published_connected && esp_timer_get_time() >= deadline) {
            failure = "peer_connect_timeout";
            break;
        }
        log_media_metrics();
        vTaskDelay(pdMS_TO_TICKS(10));
    }

cleanup:
    diagnostics_memory_snapshot("media_cleanup", session_attempt);
    stop_playback_task();
    portENTER_CRITICAL(&media_lock);
    uint32_t final_uplink_frames = uplink_frames;
    uint32_t final_uplink_generated = uplink_generated_frames;
    uint32_t final_uplink_dropped = uplink_dropped_frames;
    uint32_t final_downlink_frames = downlink_frames;
    uint32_t final_downlink_played = downlink_played_frames;
    uint32_t final_downlink_dropped = downlink_dropped_frames;
    uint32_t final_speaker_samples = speaker_samples;
    uint32_t final_uplink_would_block = uplink_would_block_count;
    uint32_t final_uplink_high_water = uplink_queue_high_water;
    uint32_t final_downlink_high_water = downlink_queue_high_water;
    uint32_t final_prevoice_overwritten = prevoice_overwritten_frames;
    uint32_t final_prevoice_skipped = prevoice_skipped_frames;
    uint32_t final_playback_max_us = playback_write_max_us;
    uint32_t final_playback_stack_low_water = playback_stack_low_water;
    portEXIT_CRITICAL(&media_lock);
    ESP_LOGI(
        TAG,
        "session audio totals: attempt=%" PRIu32
        " uplink_generated=%" PRIu32 " uplink_sent=%" PRIu32
        " uplink_dropped=%" PRIu32 " uplink_would_block=%" PRIu32
        " uplink_high_water=%" PRIu32 " prevoice_overwritten=%" PRIu32
        " prevoice_skipped=%" PRIu32 " downlink_received=%" PRIu32
        " downlink_played=%" PRIu32 " downlink_dropped=%" PRIu32
        " downlink_high_water=%" PRIu32 " speaker_samples=%" PRIu32
        " playback_max_us=%" PRIu32 " playback_stack_low_water=%" PRIu32,
        session_attempt,
        final_uplink_generated,
        final_uplink_frames,
        final_uplink_dropped,
        final_uplink_would_block,
        final_uplink_high_water,
        final_prevoice_overwritten,
        final_prevoice_skipped,
        final_downlink_frames,
        final_downlink_played,
        final_downlink_dropped,
        final_downlink_high_water,
        final_speaker_samples,
        final_playback_max_us,
        final_playback_stack_low_water
    );
    board_audio_stream_stop();
    xQueueReset(audio_queue);
    xQueueReset(downlink_queue);
    if (active_peer) {
        (void)esp_peer_close(active_peer);
        active_peer = NULL;
    }
    if (answer_sdp) {
        memset(answer_sdp, 0, MEDIA_SDP_MAX);
        free(answer_sdp);
    }
    if (local_sdp) {
        memset(local_sdp, 0, strlen(local_sdp));
        free(local_sdp);
        local_sdp = NULL;
    }
    portENTER_CRITICAL(&media_lock);
    bool was_connected = published_connected;
    bool transport_was_closed = dtls_closed;
    active = false;
    connected = false;
    stop_requested = false;
    dtls_closed = false;
    peer_state = ESP_PEER_STATE_CLOSED;
    media_task_handle = NULL;
    portEXIT_CRITICAL(&media_lock);
    publish(
        failure ? SNOWBALL_MEDIA_FAILED : SNOWBALL_MEDIA_ENDED,
        failure ? failure : (transport_was_closed ? "dtls_closed" : (was_connected ? "peer_closed" : "stopped"))
    );
    vTaskDelete(NULL);
}

esp_err_t media_session_init(snowball_media_callback_t callback) {
    if (!callback) return ESP_ERR_INVALID_ARG;
    if (!audio_queue) audio_queue = xQueueCreate(MEDIA_AUDIO_QUEUE_DEPTH, sizeof(encoded_audio_t));
    if (!downlink_queue) downlink_queue = xQueueCreate(MEDIA_DOWNLINK_QUEUE_DEPTH, sizeof(downlink_audio_t));
    if (!prevoice_frames) prevoice_frames = heap_caps_calloc(
        MEDIA_PREVOICE_FRAME_CAPACITY, sizeof(*prevoice_frames), MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!audio_queue || !downlink_queue || !prevoice_frames) return ESP_ERR_NO_MEM;
    event_callback = callback;
    int result = esp_peer_pre_generate_cert();
    if (result != ESP_PEER_ERR_NONE) {
        ESP_LOGW(TAG, "DTLS certificate pre-generation failed: %d", result);
    }
    return ESP_OK;
}

esp_err_t media_session_start(uint32_t boot_nonce, uint32_t offer_counter, uint32_t attempt) {
    if (!audio_queue || !downlink_queue || !prevoice_frames ||
        boot_nonce == 0 || offer_counter == 0 || attempt == 0) {
        return ESP_ERR_INVALID_ARG;
    }
    snowball_provisioning_status_t status = provisioning_status();
    if (!status.configured || status.enrollment_pending || !status.wifi_connected) return ESP_ERR_INVALID_STATE;
    portENTER_CRITICAL(&media_lock);
    if (active || playback_task_handle) {
        portEXIT_CRITICAL(&media_lock);
        return ESP_ERR_INVALID_STATE;
    }
    active = true;
    connected = false;
    stop_requested = false;
    dtls_closed = false;
    peer_state = ESP_PEER_STATE_CLOSED;
    session_attempt = attempt;
    session_boot_nonce = boot_nonce;
    session_offer_counter = offer_counter;
    uplink_frames = 0;
    uplink_generated_frames = 0;
    uplink_dropped_frames = 0;
    uplink_would_block_count = 0;
    uplink_queue_high_water = 0;
    downlink_frames = 0;
    downlink_played_frames = 0;
    downlink_dropped_frames = 0;
    downlink_queue_high_water = 0;
    speaker_samples = 0;
    playback_write_max_us = 0;
    playback_stack_low_water = 0;
    prevoice_head = 0;
    prevoice_count = 0;
    prevoice_replay_index = 0;
    prevoice_replay_remaining = 0;
    prevoice_overwritten_frames = 0;
    prevoice_skipped_frames = 0;
    uplink_enabled = false;
    uplink_pts_ms = 0;
    live_audio_sent = false;
    playback_stop_requested = false;
    next_metrics_log_us = esp_timer_get_time() + ((int64_t)MEDIA_METRICS_INTERVAL_MS * 1000);
    portEXIT_CRITICAL(&media_lock);
    xQueueReset(audio_queue);
    xQueueReset(downlink_queue);
    if (xTaskCreate(media_task, "snowball_media", 16384, NULL, 6, &media_task_handle) != pdPASS) {
        portENTER_CRITICAL(&media_lock);
        active = false;
        media_task_handle = NULL;
        portEXIT_CRITICAL(&media_lock);
        return ESP_ERR_NO_MEM;
    }
    return ESP_OK;
}

esp_err_t media_session_push_pcm16k(const int16_t *samples, size_t sample_count) {
    if (!samples || sample_count < 2) return ESP_ERR_INVALID_ARG;
    if (!media_session_active()) return ESP_ERR_INVALID_STATE;
    size_t source = 0;
    while (source + 1 < sample_count) {
        encoded_audio_t audio = {
            .pts = 0,
            .size = 0,
        };
        while (source + 1 < sample_count && audio.size < MEDIA_AUDIO_SAMPLES_MAX) {
            int32_t averaged = ((int32_t)samples[source] + (int32_t)samples[source + 1]) / 2;
            audio.data[audio.size++] = linear_to_alaw((int16_t)averaged);
            source += 2;
        }
        portENTER_CRITICAL(&media_lock);
        audio.pts = uplink_pts_ms;
        uplink_pts_ms += (uint32_t)(audio.size / 8);
        ++uplink_generated_frames;
        bool hold_for_voice = !uplink_enabled;
        if (hold_for_voice) {
            size_t write_index;
            if (prevoice_count < MEDIA_PREVOICE_FRAME_CAPACITY) {
                write_index = (prevoice_head + prevoice_count) % MEDIA_PREVOICE_FRAME_CAPACITY;
                ++prevoice_count;
            } else {
                write_index = prevoice_head;
                prevoice_head = (prevoice_head + 1U) % MEDIA_PREVOICE_FRAME_CAPACITY;
                ++prevoice_overwritten_frames;
            }
            prevoice_frames[write_index] = audio;
            portEXIT_CRITICAL(&media_lock);
            continue;
        }
        portEXIT_CRITICAL(&media_lock);
        if (xQueueSend(audio_queue, &audio, 0) != pdTRUE) {
            /* Do not preserve stale microphone audio. A bounded sliding
             * window makes overload audible as a brief gap instead of making
             * every later user turn arrive progressively later. */
            encoded_audio_t stale = {0};
            bool retained = xQueueReceive(audio_queue, &stale, 0) == pdTRUE &&
                xQueueSend(audio_queue, &audio, 0) == pdTRUE;
            portENTER_CRITICAL(&media_lock);
            ++uplink_dropped_frames;
            portEXIT_CRITICAL(&media_lock);
            memset(&stale, 0, sizeof(stale));
            if (!retained) return ESP_ERR_TIMEOUT;
        }
        UBaseType_t depth = uxQueueMessagesWaiting(audio_queue);
        portENTER_CRITICAL(&media_lock);
        if ((uint32_t)depth > uplink_queue_high_water) {
            uplink_queue_high_water = (uint32_t)depth;
        }
        portEXIT_CRITICAL(&media_lock);
    }
    return ESP_OK;
}

void media_session_enable_uplink(uint32_t attempt) {
    portENTER_CRITICAL(&media_lock);
    bool current = active && connected && attempt != 0 && attempt == session_attempt;
    bool newly_enabled = current && !uplink_enabled;
    if (newly_enabled) {
        uplink_enabled = true;
        prevoice_replay_remaining = prevoice_count > 15 ? 15 : prevoice_count;
        prevoice_skipped_frames = (uint32_t)(prevoice_count - prevoice_replay_remaining);
        prevoice_replay_index =
            (prevoice_head + prevoice_count - prevoice_replay_remaining) %
            MEDIA_PREVOICE_FRAME_CAPACITY;
    }
    size_t frames = prevoice_replay_remaining;
    size_t skipped = prevoice_count - prevoice_replay_remaining;
    portEXIT_CRITICAL(&media_lock);
    if (newly_enabled) {
        ESP_LOGI(TAG, "[STREAM] browser-ready uplink enabled; preserved_frames=%u (skipped=%u)",
                 (unsigned)frames, (unsigned)skipped);
    }
}

void media_session_stop(void) {
    portENTER_CRITICAL(&media_lock);
    stop_requested = active;
    if (active) connected = false;
    portEXIT_CRITICAL(&media_lock);
}

bool media_session_active(void) {
    portENTER_CRITICAL(&media_lock);
    bool value = active;
    portEXIT_CRITICAL(&media_lock);
    return value;
}

bool media_session_connected(void) {
    portENTER_CRITICAL(&media_lock);
    bool value = active && connected;
    portEXIT_CRITICAL(&media_lock);
    return value;
}
