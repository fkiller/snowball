#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"

typedef enum {
    SNOWBALL_MEDIA_CONNECTING = 0,
    SNOWBALL_MEDIA_CONNECTED,
    SNOWBALL_MEDIA_ENDED,
    SNOWBALL_MEDIA_FAILED,
} snowball_media_event_t;

typedef void (*snowball_media_callback_t)(
    snowball_media_event_t event,
    uint32_t attempt,
    const char *detail
);

esp_err_t media_session_init(snowball_media_callback_t callback);
esp_err_t media_session_start(uint32_t boot_nonce, uint32_t offer_counter, uint32_t attempt);
esp_err_t media_session_push_pcm16k(const int16_t *samples, size_t sample_count);
void media_session_stop(void);
bool media_session_active(void);
bool media_session_connected(void);
