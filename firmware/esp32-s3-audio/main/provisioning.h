#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "device_identity.h"
#include "esp_err.h"

typedef struct {
    bool configured;
    bool wifi_connected;
    /* True only after the pinned Gateway endpoint has answered successfully;
     * Wi-Fi association alone is not sufficient for a usable device link. */
    bool gateway_reachable;
    bool enrollment_pending;
    char gateway[16];
    uint16_t gateway_http_port;
    uint16_t gateway_port;
} snowball_provisioning_status_t;

typedef struct {
    uint32_t boot_nonce;
    uint32_t counter;
    uint32_t diagnostic_attempt;
    float confidence;
    char event[16];
    char wake[32];
    char command[32];
    char target[16];
    char name[128];
} snowball_device_event_t;

esp_err_t provisioning_start(const snowball_device_identity_t *identity);
snowball_provisioning_status_t provisioning_status(void);
esp_err_t provisioning_queue_event(const snowball_device_event_t *event);
/* Request one authenticated candidate snapshot after Wi-Fi/enrollment are
 * ready. The request is idempotent for the supplied boot/counter pair. */
void provisioning_request_candidate_sync(uint32_t boot_nonce, uint32_t counter);
esp_err_t provisioning_exchange_media_offer(
    uint32_t boot_nonce,
    uint32_t counter,
    const char *offer_sdp,
    char *answer_sdp,
    size_t answer_capacity
);
typedef enum {
    SNOWBALL_DELIVERY_EXECUTED = 0,
    SNOWBALL_DELIVERY_ACCEPTED,
    SNOWBALL_DELIVERY_PROCESSING,
    SNOWBALL_DELIVERY_NOT_READY,
    SNOWBALL_DELIVERY_UNPAIRED,
    SNOWBALL_DELIVERY_FAILED,
    SNOWBALL_DELIVERY_SYNCED,
} snowball_delivery_result_t;

typedef void (*snowball_delivery_callback_t)(
    const snowball_device_event_t *event,
    snowball_delivery_result_t result,
    const char *detail
);

void provisioning_set_delivery_callback(snowball_delivery_callback_t callback);
