#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"

#define SNOWBALL_AUDIO_SAMPLE_RATE 16000
#define SNOWBALL_AUDIO_FEED_CHANNELS 4

typedef struct {
    uint16_t peak[SNOWBALL_AUDIO_FEED_CHANNELS];
    uint32_t sample_frames;
} snowball_audio_levels_t;

/* Audible development feedback. Each value has a deliberately distinct
 * rhythm so the board remains diagnosable without a display or serial
 * monitor. The queue API is non-blocking and serializes access to the codec. */
typedef enum {
    SNOWBALL_FEEDBACK_WAKE_READY = 0,
    SNOWBALL_FEEDBACK_COMMAND_NEW_CHAT,
    SNOWBALL_FEEDBACK_COMMAND_RESUME,
    SNOWBALL_FEEDBACK_COMMAND_CHATGPT_PROJECT,
    SNOWBALL_FEEDBACK_COMMAND_CODEX_PROJECT,
    SNOWBALL_FEEDBACK_COMMAND_VOICE,
    SNOWBALL_FEEDBACK_COMMAND_NOT_RECOGNIZED,
    SNOWBALL_FEEDBACK_ACTION_EXECUTED,
    SNOWBALL_FEEDBACK_ACTION_NOT_READY,
    SNOWBALL_FEEDBACK_ACTION_FAILED,
    SNOWBALL_FEEDBACK_MAX,
} snowball_audio_feedback_t;

esp_err_t board_audio_init(void);
esp_err_t board_audio_read(int16_t *buffer, size_t bytes);
void board_audio_reset_levels(void);
/* Read the current diagnostic window without consuming it. */
void board_audio_peek_levels(snowball_audio_levels_t *levels);
void board_audio_take_levels(snowball_audio_levels_t *levels);
esp_err_t board_audio_play_ack(void);
esp_err_t board_audio_queue_feedback(snowball_audio_feedback_t feedback);
esp_err_t board_audio_queue_feedback_for_attempt(snowball_audio_feedback_t feedback, uint32_t attempt);
/* Cancel queued/playing tones from an earlier wake before arming a new one. */
void board_audio_begin_wake(void);
bool board_audio_feedback_busy(void);
bool board_audio_playback_active(void);
bool board_audio_try_begin_command_listening(void);
void board_audio_set_command_listening(bool listening);
esp_err_t board_audio_stream_start(void);
esp_err_t board_audio_stream_write_pcm8k(const int16_t *samples, size_t sample_count);
void board_audio_stream_stop(void);
bool board_audio_stream_active(void);
typedef void (*snowball_feedback_callback_t)(snowball_audio_feedback_t feedback, uint32_t attempt, esp_err_t result);
void board_audio_set_feedback_callback(snowball_feedback_callback_t callback);
void board_audio_set_debug_feedback(bool enabled);
bool board_audio_debug_feedback_enabled(void);
const char *board_audio_input_format(void);
int board_audio_feed_channels(void);
