#pragma once

#include <stddef.h>
#include <stdint.h>

#include "command_recognizer.h"
#include "esp_err.h"

typedef uint32_t (*snowball_wake_callback_t)(int channel);
typedef void (*snowball_command_callback_t)(
    uint32_t attempt,
    const snowball_command_result_t *command
);
typedef void (*snowball_end_session_callback_t)(uint32_t attempt);
typedef void (*snowball_speech_audio_callback_t)(const int16_t *samples, size_t sample_count);

esp_err_t speech_start(
    snowball_wake_callback_t wake_callback,
    snowball_command_callback_t command_callback,
    snowball_end_session_callback_t end_session_callback,
    snowball_speech_audio_callback_t audio_callback
);
void speech_end_session(uint32_t attempt);
/* Called only after the Gateway's signed command receipt confirms that the
 * browser Voice action completed.  This is when Hi ESP becomes END_SESSION. */
void speech_session_activated(uint32_t attempt);
