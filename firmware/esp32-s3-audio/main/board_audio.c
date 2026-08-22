#include "board_audio.h"

#include "diagnostics.h"

#include <string.h>

#include "driver/i2c_master.h"
#include "driver/i2s_std.h"
#include "esp_codec_dev.h"
#include "esp_codec_dev_defaults.h"
#include "esp_check.h"
#include "esp_io_expander.h"
#include "esp_io_expander_tca95xx_16bit.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

/* Waveshare ESP32-S3-AUDIO-Board audio bus. */
#define AUDIO_I2C_PORT I2C_NUM_0
#define AUDIO_I2C_SCL GPIO_NUM_10
#define AUDIO_I2C_SDA GPIO_NUM_11
#define AUDIO_I2S_PORT I2S_NUM_1
#define AUDIO_I2S_MCLK GPIO_NUM_12
#define AUDIO_I2S_BCLK GPIO_NUM_13
#define AUDIO_I2S_LRCK GPIO_NUM_14
#define AUDIO_I2S_DIN GPIO_NUM_15
#define AUDIO_I2S_DOUT GPIO_NUM_16
#define AUDIO_PA_EXPANDER_ADDRESS ESP_IO_EXPANDER_I2C_TCA9555_ADDRESS_000
#define AUDIO_PA_EXPANDER_PIN IO_EXPANDER_PIN_NUM_8

static const char *TAG = "snowball/audio";
static i2c_master_bus_handle_t i2c_bus;
static esp_io_expander_handle_t io_expander;
static i2s_chan_handle_t tx_channel;
static i2s_chan_handle_t rx_channel;
static const audio_codec_data_if_t *input_data;
static const audio_codec_ctrl_if_t *input_control;
static const audio_codec_if_t *input_codec;
static esp_codec_dev_handle_t input_device;
static const audio_codec_data_if_t *output_data;
static const audio_codec_ctrl_if_t *output_control;
static const audio_codec_gpio_if_t *output_gpio;
static const audio_codec_if_t *output_codec;
static esp_codec_dev_handle_t output_device;
static esp_codec_dev_sample_info_t output_sample;
static bool output_open;
static int32_t acknowledgement_silence[512 * 2];
static QueueHandle_t feedback_queue;
static SemaphoreHandle_t output_mutex;
static portMUX_TYPE feedback_lock = portMUX_INITIALIZER_UNLOCKED;
static uint32_t pending_feedback;
static uint32_t playback_active;
static uint32_t feedback_generation = 1;
static snowball_feedback_callback_t feedback_callback;
static bool command_listening;
static bool stream_active;
static bool debug_feedback_enabled = true;
static portMUX_TYPE level_lock = portMUX_INITIALIZER_UNLOCKED;
static snowball_audio_levels_t accumulated_levels;

typedef struct {
    snowball_audio_feedback_t feedback;
    uint32_t attempt;
    uint32_t generation;
} queued_feedback_t;

typedef struct {
    uint16_t frequency;
    uint16_t duration_ms;
    uint16_t gap_ms;
} feedback_note_t;

static const feedback_note_t wake_ready_notes[] = {
    {440, 120, 25}, {660, 120, 0},
};
static const feedback_note_t command_new_chat_notes[] = {
    {880, 110, 0},
};
static const feedback_note_t command_resume_notes[] = {
    {880, 80, 35}, {880, 110, 0},
};
static const feedback_note_t command_chatgpt_project_notes[] = {
    {660, 70, 30}, {660, 70, 30}, {660, 110, 0},
};
static const feedback_note_t command_codex_project_notes[] = {
    {660, 65, 25}, {660, 65, 25}, {660, 65, 25}, {660, 100, 0},
};
static const feedback_note_t command_voice_notes[] = {
    {880, 80, 25}, {554, 80, 25}, {880, 120, 0},
};
static const feedback_note_t command_not_recognized_notes[] = {
    {330, 120, 35}, {220, 180, 0},
};
static const feedback_note_t action_executed_notes[] = {
    {523, 90, 25}, {659, 90, 25}, {784, 140, 0},
};
static const feedback_note_t action_not_ready_notes[] = {
    {392, 100, 30}, {262, 100, 30}, {392, 140, 0},
};
static const feedback_note_t action_failed_notes[] = {
    {660, 100, 25}, {440, 100, 25}, {220, 180, 0},
};

static esp_err_t init_i2c(void) {
    const i2c_master_bus_config_t config = {
        .i2c_port = AUDIO_I2C_PORT,
        .sda_io_num = AUDIO_I2C_SDA,
        .scl_io_num = AUDIO_I2C_SCL,
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .glitch_ignore_cnt = 7,
        .flags.enable_internal_pullup = true,
    };
    return i2c_new_master_bus(&config, &i2c_bus);
}

static esp_err_t enable_speaker_amplifier(void) {
    ESP_RETURN_ON_ERROR(
        esp_io_expander_new_i2c_tca95xx_16bit(
            i2c_bus,
            AUDIO_PA_EXPANDER_ADDRESS,
            &io_expander
        ),
        TAG,
        "create TCA9555 speaker control"
    );
    ESP_RETURN_ON_ERROR(
        esp_io_expander_set_dir(io_expander, AUDIO_PA_EXPANDER_PIN, IO_EXPANDER_OUTPUT),
        TAG,
        "configure speaker amplifier enable"
    );
    ESP_RETURN_ON_ERROR(
        esp_io_expander_set_level(io_expander, AUDIO_PA_EXPANDER_PIN, 1),
        TAG,
        "enable speaker amplifier"
    );
    vTaskDelay(pdMS_TO_TICKS(50));
    ESP_LOGI(TAG, "speaker amplifier enabled through TCA9555 EXIO8");
    return ESP_OK;
}

static esp_err_t init_i2s(void) {
    i2s_chan_config_t channel_config = I2S_CHANNEL_DEFAULT_CONFIG(AUDIO_I2S_PORT, I2S_ROLE_MASTER);
    /* The default I2S configuration leaves completed TX DMA descriptors
       untouched. When WebRTC audio pauses, the hardware then repeats the last
       non-zero descriptor indefinitely. Clear descriptors after transmission
       so every transport underrun is silence instead of a repeated tail. */
    channel_config.auto_clear_after_cb = true;
    ESP_RETURN_ON_ERROR(i2s_new_channel(&channel_config, &tx_channel, &rx_channel), TAG, "create I2S channels");

    i2s_std_config_t config = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(SNOWBALL_AUDIO_SAMPLE_RATE),
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_32BIT, I2S_SLOT_MODE_STEREO),
        .gpio_cfg = {
            .mclk = AUDIO_I2S_MCLK,
            .bclk = AUDIO_I2S_BCLK,
            .ws = AUDIO_I2S_LRCK,
            .dout = AUDIO_I2S_DOUT,
            .din = AUDIO_I2S_DIN,
            .invert_flags = {0},
        },
    };
    ESP_RETURN_ON_ERROR(i2s_channel_init_std_mode(tx_channel, &config), TAG, "configure I2S TX");
    ESP_RETURN_ON_ERROR(i2s_channel_init_std_mode(rx_channel, &config), TAG, "configure I2S RX");
    /* Keep the DAC transport disabled while idle. An enabled, unwritten TX
       DMA buffer produces audible noise on this board. */
    return i2s_channel_enable(rx_channel);
}

static esp_err_t init_input_codec(void) {
    audio_codec_i2s_cfg_t data_config = {
        .port = AUDIO_I2S_PORT,
        .rx_handle = rx_channel,
        .tx_handle = NULL,
    };
    input_data = audio_codec_new_i2s_data(&data_config);
    audio_codec_i2c_cfg_t control_config = {
        .addr = ES7210_CODEC_DEFAULT_ADDR,
        .bus_handle = i2c_bus,
    };
    input_control = audio_codec_new_i2c_ctrl(&control_config);
    es7210_codec_cfg_t codec_config = {
        .ctrl_if = input_control,
        .mic_selected = ES7210_SEL_MIC1 | ES7210_SEL_MIC2 | ES7210_SEL_MIC3 | ES7210_SEL_MIC4,
    };
    input_codec = es7210_codec_new(&codec_config);
    esp_codec_dev_cfg_t device_config = {
        .codec_if = input_codec,
        .data_if = input_data,
        .dev_type = ESP_CODEC_DEV_TYPE_IN,
    };
    input_device = esp_codec_dev_new(&device_config);
    if (!input_data || !input_control || !input_codec || !input_device) {
        return ESP_ERR_NO_MEM;
    }
    esp_codec_dev_sample_info_t sample = {
        .sample_rate = SNOWBALL_AUDIO_SAMPLE_RATE,
        .channel = 2,
        .bits_per_sample = 32,
    };
    ESP_RETURN_ON_ERROR(esp_codec_dev_open(input_device, &sample), TAG, "open ES7210");
    for (int channel = 0; channel < 4; ++channel) {
        ESP_RETURN_ON_ERROR(
            esp_codec_dev_set_in_channel_gain(input_device, ESP_CODEC_DEV_MAKE_CHANNEL_MASK(channel), 30.0),
            TAG,
            "set ES7210 gain"
        );
    }
    return ESP_OK;
}

static esp_err_t init_output_codec(void) {
    audio_codec_i2s_cfg_t data_config = {
        .port = AUDIO_I2S_PORT,
        .rx_handle = NULL,
        .tx_handle = tx_channel,
    };
    output_data = audio_codec_new_i2s_data(&data_config);
    audio_codec_i2c_cfg_t control_config = {
        .addr = ES8311_CODEC_DEFAULT_ADDR,
        .bus_handle = i2c_bus,
    };
    output_control = audio_codec_new_i2c_ctrl(&control_config);
    output_gpio = audio_codec_new_gpio();
    es8311_codec_cfg_t codec_config = {
        .codec_mode = ESP_CODEC_DEV_WORK_MODE_DAC,
        .ctrl_if = output_control,
        .gpio_if = output_gpio,
        .pa_pin = GPIO_NUM_NC,
        .use_mclk = false,
    };
    output_codec = es8311_codec_new(&codec_config);
    esp_codec_dev_cfg_t device_config = {
        .codec_if = output_codec,
        .data_if = output_data,
        .dev_type = ESP_CODEC_DEV_TYPE_OUT,
    };
    output_device = esp_codec_dev_new(&device_config);
    if (!output_data || !output_control || !output_gpio || !output_codec || !output_device) {
        return ESP_ERR_NO_MEM;
    }
    output_sample = (esp_codec_dev_sample_info_t){
        .sample_rate = SNOWBALL_AUDIO_SAMPLE_RATE,
        .channel = 2,
        .bits_per_sample = 32,
    };
    ESP_RETURN_ON_ERROR(esp_codec_dev_open(output_device, &output_sample), TAG, "open ES8311");
    output_open = true;
    return esp_codec_dev_set_out_vol(output_device, 0);
}

static const feedback_note_t *feedback_notes(snowball_audio_feedback_t feedback, size_t *count) {
    switch (feedback) {
        case SNOWBALL_FEEDBACK_WAKE_READY:
            *count = sizeof(wake_ready_notes) / sizeof(wake_ready_notes[0]);
            return wake_ready_notes;
        case SNOWBALL_FEEDBACK_COMMAND_NEW_CHAT:
            *count = sizeof(command_new_chat_notes) / sizeof(command_new_chat_notes[0]);
            return command_new_chat_notes;
        case SNOWBALL_FEEDBACK_COMMAND_RESUME:
            *count = sizeof(command_resume_notes) / sizeof(command_resume_notes[0]);
            return command_resume_notes;
        case SNOWBALL_FEEDBACK_COMMAND_CHATGPT_PROJECT:
            *count = sizeof(command_chatgpt_project_notes) / sizeof(command_chatgpt_project_notes[0]);
            return command_chatgpt_project_notes;
        case SNOWBALL_FEEDBACK_COMMAND_CODEX_PROJECT:
            *count = sizeof(command_codex_project_notes) / sizeof(command_codex_project_notes[0]);
            return command_codex_project_notes;
        case SNOWBALL_FEEDBACK_COMMAND_VOICE:
            *count = sizeof(command_voice_notes) / sizeof(command_voice_notes[0]);
            return command_voice_notes;
        case SNOWBALL_FEEDBACK_COMMAND_NOT_RECOGNIZED:
            *count = sizeof(command_not_recognized_notes) / sizeof(command_not_recognized_notes[0]);
            return command_not_recognized_notes;
        case SNOWBALL_FEEDBACK_ACTION_EXECUTED:
            *count = sizeof(action_executed_notes) / sizeof(action_executed_notes[0]);
            return action_executed_notes;
        case SNOWBALL_FEEDBACK_ACTION_NOT_READY:
            *count = sizeof(action_not_ready_notes) / sizeof(action_not_ready_notes[0]);
            return action_not_ready_notes;
        case SNOWBALL_FEEDBACK_ACTION_FAILED:
            *count = sizeof(action_failed_notes) / sizeof(action_failed_notes[0]);
            return action_failed_notes;
        default:
            *count = 0;
            return NULL;
    }
}

static bool feedback_cancelled(uint32_t generation) {
    portENTER_CRITICAL(&feedback_lock);
    bool cancelled = generation != feedback_generation;
    portEXIT_CRITICAL(&feedback_lock);
    return cancelled;
}

static esp_err_t write_tone(
    uint16_t frequency,
    uint16_t duration_ms,
    int32_t *frames,
    size_t frame_capacity,
    uint32_t generation
) {
    size_t total_frames = ((size_t)SNOWBALL_AUDIO_SAMPLE_RATE * duration_ms) / 1000U;
    for (size_t offset = 0; offset < total_frames; offset += frame_capacity) {
        if (feedback_cancelled(generation)) return ESP_ERR_INVALID_STATE;
        size_t block_frames = total_frames - offset < frame_capacity ? total_frames - offset : frame_capacity;
        for (size_t frame = 0; frame < block_frames; ++frame) {
            int32_t value = 0;
            if (frequency) {
                uint32_t phase = (uint32_t)(((uint64_t)(offset + frame) * frequency) % SNOWBALL_AUDIO_SAMPLE_RATE);
                value = phase < (SNOWBALL_AUDIO_SAMPLE_RATE / 2U) ? 0x18000000 : -0x18000000;
            }
            frames[frame * 2] = value;
            frames[frame * 2 + 1] = value;
        }
        esp_err_t result = esp_codec_dev_write(output_device, frames, (int)(block_frames * 2 * sizeof(*frames)));
        if (result != ESP_OK) return result;
    }
    return ESP_OK;
}

static bool reserve_feedback_playback(snowball_audio_feedback_t feedback, uint32_t generation) {
    portENTER_CRITICAL(&feedback_lock);
    bool allowed = generation == feedback_generation && !stream_active &&
        (feedback == SNOWBALL_FEEDBACK_WAKE_READY || !command_listening);
    if (allowed) ++playback_active;
    portEXIT_CRITICAL(&feedback_lock);
    return allowed;
}

static void release_feedback_playback(void) {
    portENTER_CRITICAL(&feedback_lock);
    if (playback_active > 0) --playback_active;
    portEXIT_CRITICAL(&feedback_lock);
}

static esp_err_t play_reserved_feedback(snowball_audio_feedback_t feedback, uint32_t generation) {
    if (!output_device) return ESP_ERR_INVALID_STATE;
    if (feedback_cancelled(generation)) return ESP_ERR_INVALID_STATE;
    size_t note_count = 0;
    const feedback_note_t *notes = feedback_notes(feedback, &note_count);
    if (!notes || note_count == 0) return ESP_ERR_INVALID_ARG;
    if (xSemaphoreTake(output_mutex, pdMS_TO_TICKS(2000)) != pdTRUE) return ESP_ERR_TIMEOUT;

    esp_err_t result = ESP_OK;
    int32_t frames[128 * 2] = {0};
    if (!output_open) {
        result = esp_codec_dev_open(output_device, &output_sample);
        if (result == ESP_OK) output_open = true;
    }
    if (result == ESP_OK) result = esp_codec_dev_set_out_vol(output_device, 0);
    if (result == ESP_OK && feedback != SNOWBALL_FEEDBACK_WAKE_READY) {
        result = esp_codec_dev_write(output_device, acknowledgement_silence, sizeof(acknowledgement_silence));
    }
    if (feedback != SNOWBALL_FEEDBACK_WAKE_READY) vTaskDelay(pdMS_TO_TICKS(30));
    if (result == ESP_OK) result = esp_codec_dev_set_out_vol(output_device, 80);
    for (size_t index = 0; result == ESP_OK && index < note_count; ++index) {
        result = write_tone(notes[index].frequency, notes[index].duration_ms, frames, 128, generation);
        if (result == ESP_OK && notes[index].gap_ms) {
            result = write_tone(0, notes[index].gap_ms, frames, 128, generation);
        }
    }
    memset(frames, 0, sizeof(frames));
    size_t silence_blocks = feedback == SNOWBALL_FEEDBACK_WAKE_READY ? 2 : 16;
    for (size_t block = 0; result == ESP_OK && block < silence_blocks; ++block) {
        if (feedback_cancelled(generation)) {
            result = ESP_ERR_INVALID_STATE;
            break;
        }
        result = esp_codec_dev_write(output_device, frames, sizeof(frames));
    }
    (void)esp_codec_dev_set_out_vol(output_device, 0);
    if (output_open) {
        (void)esp_codec_dev_close(output_device);
        output_open = false;
    }
    xSemaphoreGive(output_mutex);
    return result;
}

static esp_err_t play_feedback(snowball_audio_feedback_t feedback) {
    portENTER_CRITICAL(&feedback_lock);
    uint32_t generation = feedback_generation;
    portEXIT_CRITICAL(&feedback_lock);
    if (!reserve_feedback_playback(feedback, generation)) return ESP_ERR_INVALID_STATE;
    esp_err_t result = play_reserved_feedback(feedback, generation);
    release_feedback_playback();
    return result;
}

static void feedback_task(void *argument) {
    (void)argument;
    diagnostics_memory_snapshot("feedback_task_ready", 0);
    queued_feedback_t queued;
    while (true) {
        if (xQueueReceive(feedback_queue, &queued, portMAX_DELAY) == pdTRUE) {
            snowball_audio_feedback_t feedback = queued.feedback;
            if (!reserve_feedback_playback(feedback, queued.generation)) {
                /* A new wake invalidates old tones instead of replaying them
                   behind the new command. This is deliberately a drop, not a
                   retry: the old attempt is no longer user-actionable. */
                portENTER_CRITICAL(&feedback_lock);
                if (pending_feedback > 0) --pending_feedback;
                portEXIT_CRITICAL(&feedback_lock);
                if (feedback_callback) feedback_callback(feedback, queued.attempt, ESP_ERR_INVALID_STATE);
                continue;
            }
            esp_err_t result = play_reserved_feedback(feedback, queued.generation);
            release_feedback_playback();
            if (result != ESP_OK) {
                ESP_LOGW(TAG, "feedback %d failed: %s", feedback, esp_err_to_name(result));
            }
            portENTER_CRITICAL(&feedback_lock);
            if (pending_feedback > 0) --pending_feedback;
            portEXIT_CRITICAL(&feedback_lock);
            if (feedback_callback) feedback_callback(feedback, queued.attempt, result);
        }
    }
}

esp_err_t board_audio_init(void) {
    ESP_RETURN_ON_ERROR(init_i2c(), TAG, "initialize audio I2C");
    ESP_RETURN_ON_ERROR(enable_speaker_amplifier(), TAG, "enable speaker amplifier");
    ESP_RETURN_ON_ERROR(init_i2s(), TAG, "initialize audio I2S");
    ESP_RETURN_ON_ERROR(init_input_codec(), TAG, "initialize microphone codec");
    ESP_RETURN_ON_ERROR(init_output_codec(), TAG, "initialize speaker codec");
    output_mutex = xSemaphoreCreateMutex();
    feedback_queue = xQueueCreate(12, sizeof(queued_feedback_t));
    if (!output_mutex || !feedback_queue ||
        xTaskCreate(feedback_task, "audio_feedback", 4096, NULL, 4, NULL) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    ESP_LOGI(TAG, "ES7210 input and ES8311 output ready at 16 kHz");
    return ESP_OK;
}

esp_err_t board_audio_read(int16_t *buffer, size_t bytes) {
    if (!input_device || !buffer || bytes == 0) {
        return ESP_ERR_INVALID_STATE;
    }
    esp_err_t result = esp_codec_dev_read(input_device, buffer, (int)bytes);
    if (result != ESP_OK) {
        return result;
    }

    const size_t samples = bytes / sizeof(*buffer);
    uint16_t peaks[SNOWBALL_AUDIO_FEED_CHANNELS] = {0};
    for (size_t index = 0; index < samples; ++index) {
        int32_t value = buffer[index];
        uint16_t magnitude = (uint16_t)(value < 0 ? -value : value);
        size_t channel = index % SNOWBALL_AUDIO_FEED_CHANNELS;
        if (magnitude > peaks[channel]) {
            peaks[channel] = magnitude;
        }
    }
    portENTER_CRITICAL(&level_lock);
    for (size_t channel = 0; channel < SNOWBALL_AUDIO_FEED_CHANNELS; ++channel) {
        if (peaks[channel] > accumulated_levels.peak[channel]) {
            accumulated_levels.peak[channel] = peaks[channel];
        }
    }
    accumulated_levels.sample_frames += (uint32_t)(samples / SNOWBALL_AUDIO_FEED_CHANNELS);
    portEXIT_CRITICAL(&level_lock);
    return ESP_OK;
}

void board_audio_reset_levels(void) {
    portENTER_CRITICAL(&level_lock);
    accumulated_levels = (snowball_audio_levels_t){0};
    portEXIT_CRITICAL(&level_lock);
}

void board_audio_peek_levels(snowball_audio_levels_t *levels) {
    if (!levels) {
        return;
    }
    portENTER_CRITICAL(&level_lock);
    *levels = accumulated_levels;
    portEXIT_CRITICAL(&level_lock);
}

void board_audio_take_levels(snowball_audio_levels_t *levels) {
    if (!levels) {
        return;
    }
    portENTER_CRITICAL(&level_lock);
    *levels = accumulated_levels;
    accumulated_levels = (snowball_audio_levels_t){0};
    portEXIT_CRITICAL(&level_lock);
}

esp_err_t board_audio_play_ack(void) {
    return play_feedback(SNOWBALL_FEEDBACK_WAKE_READY);
}

esp_err_t board_audio_queue_feedback(snowball_audio_feedback_t feedback) {
    return board_audio_queue_feedback_for_attempt(feedback, 0);
}

esp_err_t board_audio_queue_feedback_for_attempt(snowball_audio_feedback_t feedback, uint32_t attempt) {
    if (!feedback_queue || feedback < 0 || feedback >= SNOWBALL_FEEDBACK_MAX) return ESP_ERR_INVALID_ARG;
    portENTER_CRITICAL(&feedback_lock);
    bool enabled = debug_feedback_enabled;
    portEXIT_CRITICAL(&feedback_lock);
    if (!enabled && feedback != SNOWBALL_FEEDBACK_WAKE_READY) return ESP_OK;
    portENTER_CRITICAL(&feedback_lock);
    uint32_t generation = feedback_generation;
    ++pending_feedback;
    portEXIT_CRITICAL(&feedback_lock);
    queued_feedback_t queued = {.feedback = feedback, .attempt = attempt, .generation = generation};
    if (xQueueSend(feedback_queue, &queued, 0) != pdTRUE) {
        portENTER_CRITICAL(&feedback_lock);
        --pending_feedback;
        portEXIT_CRITICAL(&feedback_lock);
        return ESP_ERR_TIMEOUT;
    }
    return ESP_OK;
}

void board_audio_begin_wake(void) {
    if (!feedback_queue) return;
    portENTER_CRITICAL(&feedback_lock);
    ++feedback_generation;
    if (feedback_generation == 0) feedback_generation = 1;
    command_listening = false;
    portEXIT_CRITICAL(&feedback_lock);

    queued_feedback_t discarded;
    while (xQueueReceive(feedback_queue, &discarded, 0) == pdTRUE) {
        portENTER_CRITICAL(&feedback_lock);
        if (pending_feedback > 0) --pending_feedback;
        portEXIT_CRITICAL(&feedback_lock);
    }
    /* Let an in-flight codec write observe the generation change. A tone block
       is bounded to roughly 8 ms, so this wait is short and bounded. */
    for (int attempt = 0; attempt < 50; ++attempt) {
        portENTER_CRITICAL(&feedback_lock);
        bool active = playback_active > 0;
        portEXIT_CRITICAL(&feedback_lock);
        if (!active) break;
        vTaskDelay(pdMS_TO_TICKS(5));
    }
}

void board_audio_set_debug_feedback(bool enabled) {
    portENTER_CRITICAL(&feedback_lock);
    debug_feedback_enabled = enabled;
    portEXIT_CRITICAL(&feedback_lock);
}

bool board_audio_debug_feedback_enabled(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool enabled = debug_feedback_enabled;
    portEXIT_CRITICAL(&feedback_lock);
    return enabled;
}

bool board_audio_feedback_busy(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool busy = pending_feedback > 0 || playback_active > 0;
    portEXIT_CRITICAL(&feedback_lock);
    return busy;
}

bool board_audio_playback_active(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool active = playback_active > 0;
    portEXIT_CRITICAL(&feedback_lock);
    return active;
}

bool board_audio_try_begin_command_listening(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool available = playback_active == 0 && !command_listening && !stream_active;
    if (available) command_listening = true;
    portEXIT_CRITICAL(&feedback_lock);
    return available;
}

void board_audio_set_command_listening(bool listening) {
    portENTER_CRITICAL(&feedback_lock);
    command_listening = listening;
    portEXIT_CRITICAL(&feedback_lock);
}

esp_err_t board_audio_stream_start(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool available = playback_active == 0 && !command_listening && !stream_active;
    if (available) stream_active = true;
    portEXIT_CRITICAL(&feedback_lock);
    if (!available) return ESP_ERR_INVALID_STATE;

    if (xSemaphoreTake(output_mutex, pdMS_TO_TICKS(2000)) != pdTRUE) {
        portENTER_CRITICAL(&feedback_lock);
        stream_active = false;
        portEXIT_CRITICAL(&feedback_lock);
        return ESP_ERR_TIMEOUT;
    }
    esp_err_t result = ESP_OK;
    if (!output_open) {
        result = esp_codec_dev_open(output_device, &output_sample);
        if (result == ESP_OK) output_open = true;
    }
    if (result == ESP_OK) result = esp_codec_dev_set_out_vol(output_device, 80);
    xSemaphoreGive(output_mutex);
    if (result != ESP_OK) {
        board_audio_stream_stop();
    }
    return result;
}

esp_err_t board_audio_stream_write_pcm8k(const int16_t *samples, size_t sample_count) {
    if (!samples || sample_count == 0 || sample_count > 320) return ESP_ERR_INVALID_ARG;
    portENTER_CRITICAL(&feedback_lock);
    bool active = stream_active;
    portEXIT_CRITICAL(&feedback_lock);
    if (!active) return ESP_ERR_INVALID_STATE;

    int32_t frames[320 * 2 * 2];
    for (size_t sample = 0; sample < sample_count; ++sample) {
        /* Multiplication is defined for negative PCM samples; left-shifting a
         * negative signed integer is undefined behavior in C. */
        int32_t value = (int32_t)samples[sample] * 65536;
        size_t frame = sample * 2;
        frames[frame * 2] = value;
        frames[frame * 2 + 1] = value;
        frames[(frame + 1) * 2] = value;
        frames[(frame + 1) * 2 + 1] = value;
    }
    if (xSemaphoreTake(output_mutex, pdMS_TO_TICKS(250)) != pdTRUE) return ESP_ERR_TIMEOUT;
    esp_err_t result = output_open
        ? esp_codec_dev_write(output_device, frames, (int)(sample_count * 4 * sizeof(*frames)))
        : ESP_ERR_INVALID_STATE;
    xSemaphoreGive(output_mutex);
    memset(frames, 0, sizeof(frames));
    return result;
}

void board_audio_stream_stop(void) {
    /* Publish the logical stop before any potentially blocking codec access.
       WakeNet and incoming packet callbacks must not remain pinned in stream
       mode merely because the output mutex or codec is slow to shut down. */
    portENTER_CRITICAL(&feedback_lock);
    stream_active = false;
    portEXIT_CRITICAL(&feedback_lock);
    if (!output_mutex) return;
    if (xSemaphoreTake(output_mutex, pdMS_TO_TICKS(2000)) != pdTRUE) {
        ESP_LOGE(TAG, "timed out acquiring output mutex while stopping stream");
        return;
    }
    if (output_device) (void)esp_codec_dev_set_out_vol(output_device, 0);
    if (output_open) {
        (void)esp_codec_dev_close(output_device);
        output_open = false;
    }
    xSemaphoreGive(output_mutex);
}

bool board_audio_stream_active(void) {
    portENTER_CRITICAL(&feedback_lock);
    bool active = stream_active;
    portEXIT_CRITICAL(&feedback_lock);
    return active;
}

void board_audio_set_feedback_callback(snowball_feedback_callback_t callback) {
    feedback_callback = callback;
}

const char *board_audio_input_format(void) {
    return "RMNM";
}

int board_audio_feed_channels(void) {
    return SNOWBALL_AUDIO_FEED_CHANNELS;
}
