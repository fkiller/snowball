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
#define MEDIA_AUDIO_QUEUE_DEPTH 32
#define MEDIA_PREVOICE_FRAME_CAPACITY 200 /* 8 seconds of 40 ms G.711A frames */
#define MEDIA_TLS_MIN_PSRAM_BLOCK (128U << 10)
#define MEDIA_TLS_MIN_INTERNAL_BLOCK (16U << 10)

static const char *TAG = "snowball/media";
static portMUX_TYPE media_lock = portMUX_INITIALIZER_UNLOCKED;
static QueueHandle_t audio_queue;
static size_t prevoice_count;
static size_t prevoice_read;
static bool uplink_enabled;
static int64_t prevoice_next_send_us;
static snowball_media_callback_t event_callback;
static esp_peer_handle_t active_peer;
static TaskHandle_t media_task_handle;
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
static uint32_t downlink_frames;
static uint32_t speaker_samples;
static bool live_audio_sent;

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
    if (!board_audio_stream_active()) return 0;
    ++downlink_frames;
    if (downlink_frames == 1) {
        ESP_LOGI(TAG, "first downlink audio frame received: %d bytes", frame->size);
    }
    int16_t pcm[MEDIA_AUDIO_SAMPLES_MAX];
    int offset = 0;
    while (offset < frame->size) {
        int count = frame->size - offset;
        if (count > MEDIA_AUDIO_SAMPLES_MAX) count = MEDIA_AUDIO_SAMPLES_MAX;
        for (int index = 0; index < count; ++index) {
            pcm[index] = alaw_to_linear(frame->data[offset + index]);
        }
        esp_err_t result = board_audio_stream_write_pcm8k(pcm, (size_t)count);
        if (result != ESP_OK) {
            memset(pcm, 0, sizeof(pcm));
            return ESP_PEER_ERR_FAIL;
        }
        speaker_samples += (uint32_t)count;
        offset += count;
    }
    memset(pcm, 0, sizeof(pcm));
    return 0;
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
            (void)xQueueSendToFront(audio_queue, &audio, 0);
            break;
        }
        if (result != ESP_PEER_ERR_NONE) {
            ESP_LOGW(TAG, "audio send failed: %d", result);
            break;
        }
        ++uplink_frames;
        if (!live_audio_sent) {
            live_audio_sent = true;
            ESP_LOGI(TAG, "first live microphone frame sent: %u bytes (total uplink_frames=%" PRIu32 ")",
                     (unsigned)audio.size, uplink_frames);
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
        if (uplink_enabled && prevoice_read < prevoice_count) {
            audio = prevoice_frames[prevoice_read++];
            available = true;
        }
        portEXIT_CRITICAL(&media_lock);
        if (!available) break;

        esp_peer_audio_frame_t frame = { .pts = audio.pts, .data = audio.data, .size = audio.size };
        int result = esp_peer_send_audio(peer, &frame);
        if (result == ESP_PEER_ERR_WOULD_BLOCK) {
            portENTER_CRITICAL(&media_lock);
            if (prevoice_read > 0) --prevoice_read;
            portEXIT_CRITICAL(&media_lock);
            break;
        } else if (result != ESP_PEER_ERR_NONE) {
            ESP_LOGW(TAG, "pre-Voice audio send failed: %d", result);
            break;
        }
        ++uplink_frames;
        if (uplink_frames == 1) {
            ESP_LOGI(TAG, "first preserved microphone frame sent: %u bytes", (unsigned)audio.size);
        }
    }
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
                published_connected = true;
                diagnostics_memory_snapshot("voice_connected", session_attempt);
                publish(SNOWBALL_MEDIA_CONNECTED, "pcma_connected");
            }
            send_prevoice_audio(active_peer);
            portENTER_CRITICAL(&media_lock);
            bool replaying_prevoice = uplink_enabled && prevoice_read < prevoice_count;
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
        vTaskDelay(pdMS_TO_TICKS(10));
    }

cleanup:
    diagnostics_memory_snapshot("media_cleanup", session_attempt);
    ESP_LOGI(
        TAG,
        "session audio totals: uplink=%" PRIu32 " downlink=%" PRIu32 " speaker_samples=%" PRIu32,
        uplink_frames,
        downlink_frames,
        speaker_samples
    );
    board_audio_stream_stop();
    xQueueReset(audio_queue);
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
    if (!prevoice_frames) prevoice_frames = heap_caps_calloc(
        MEDIA_PREVOICE_FRAME_CAPACITY, sizeof(*prevoice_frames), MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    if (!audio_queue || !prevoice_frames) return ESP_ERR_NO_MEM;
    event_callback = callback;
    int result = esp_peer_pre_generate_cert();
    if (result != ESP_PEER_ERR_NONE) {
        ESP_LOGW(TAG, "DTLS certificate pre-generation failed: %d", result);
    }
    return ESP_OK;
}

esp_err_t media_session_start(uint32_t boot_nonce, uint32_t offer_counter, uint32_t attempt) {
    if (!audio_queue || boot_nonce == 0 || offer_counter == 0 || attempt == 0) return ESP_ERR_INVALID_ARG;
    snowball_provisioning_status_t status = provisioning_status();
    if (!status.configured || status.enrollment_pending || !status.wifi_connected) return ESP_ERR_INVALID_STATE;
    portENTER_CRITICAL(&media_lock);
    if (active) {
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
    downlink_frames = 0;
    speaker_samples = 0;
    prevoice_count = 0;
    prevoice_read = 0;
    uplink_enabled = false;
    prevoice_next_send_us = 0;
    uplink_pts_ms = 0;
    live_audio_sent = false;
    portEXIT_CRITICAL(&media_lock);
    xQueueReset(audio_queue);
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
        bool hold_for_voice = !uplink_enabled;
        if (hold_for_voice) {
            if (prevoice_count < MEDIA_PREVOICE_FRAME_CAPACITY) {
                prevoice_frames[prevoice_count++] = audio;
            } else {
                memmove(&prevoice_frames[0], &prevoice_frames[1], (MEDIA_PREVOICE_FRAME_CAPACITY - 1) * sizeof(encoded_audio_t));
                prevoice_frames[MEDIA_PREVOICE_FRAME_CAPACITY - 1] = audio;
            }
            portEXIT_CRITICAL(&media_lock);
            continue;
        }
        portEXIT_CRITICAL(&media_lock);
        if (xQueueSend(audio_queue, &audio, 0) != pdTRUE) return ESP_ERR_TIMEOUT;
    }
    return ESP_OK;
}

void media_session_enable_uplink(uint32_t attempt) {
    portENTER_CRITICAL(&media_lock);
    bool current = active && connected && attempt != 0 && attempt == session_attempt;
    if (current) {
        uplink_enabled = true;
        if (prevoice_count > 15) {
            prevoice_read = prevoice_count - 15;
        } else {
            prevoice_read = 0;
        }
        prevoice_next_send_us = 0;
    }
    size_t frames = prevoice_count - prevoice_read;
    size_t skipped = prevoice_read;
    portEXIT_CRITICAL(&media_lock);
    if (current) {
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
