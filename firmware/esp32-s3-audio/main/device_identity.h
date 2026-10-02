#pragma once

#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"

#define SNOWBALL_PUBLIC_KEY_PEM_MAX 256
#define SNOWBALL_FINGERPRINT_HEX_SIZE 65
#define SNOWBALL_HARDWARE_ID_SIZE 18

typedef struct {
    char public_key_pem[SNOWBALL_PUBLIC_KEY_PEM_MAX];
    char fingerprint[SNOWBALL_FINGERPRINT_HEX_SIZE];
    char hardware_id[SNOWBALL_HARDWARE_ID_SIZE];
} snowball_device_identity_t;

esp_err_t device_identity_load_or_create(snowball_device_identity_t *identity);
esp_err_t device_identity_next_boot_sequence(uint32_t *sequence);
esp_err_t device_identity_sign_enrollment(
    const snowball_device_identity_t *identity,
    const char *enrollment_token,
    const char *nonce,
    char *signature_base64url,
    size_t signature_size
);

esp_err_t device_identity_sign_event(
    const snowball_device_identity_t *identity,
    uint32_t boot_nonce,
    uint32_t counter,
    const char *event,
    const char *wake,
    const char *command,
    const char *target,
    const char *name,
    float confidence,
    char *signature_base64url,
    size_t signature_size
);

esp_err_t device_identity_sign_media_offer(
    const snowball_device_identity_t *identity,
    uint32_t boot_nonce,
    uint32_t counter,
    const char *sdp,
    char *signature_base64url,
    size_t signature_size
);
