#include "provisioning.h"

#include <ctype.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/time.h>
#include <time.h>

#include "cJSON.h"
#include "board_audio.h"
#include "command_recognizer.h"
#include "diagnostics.h"
#include "esp_check.h"
#include "esp_event.h"
#include "esp_http_client.h"
#include "esp_log.h"
#include "esp_netif.h"
#include "esp_system.h"
#include "esp_wifi.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "freertos/task.h"
#include "nvs.h"
#include "nvs_flash.h"
#include "mbedtls/x509_crt.h"
#include "mbedtls/sha256.h"
#include "serial_output.h"

#define PROTOCOL_VERSION 1
#if CONFIG_SNOWBALL_COMMAND_TAIL
#define FIRMWARE_VERSION "0.3.1-command-fsm"
#else
#define FIRMWARE_VERSION "0.3.1-bare-voice"
#endif
#define DEVICE_MODEL "Waveshare ESP32-S3-AUDIO-Board"
#define PROVISION_NAMESPACE "snowball"
#define SERIAL_LINE_MAX 2048
#define REQUEST_ID_MAX 64
#define ENROLLMENT_TOKEN_MAX 512
#define CA_PEM_MAX 4096
#define HTTP_RESPONSE_MAX 8192
#define MEDIA_RESPONSE_MAX (64U << 10)
#define HTTP_DATE_MAX 64
#define CLOCK_SANITY_EPOCH ((time_t)1704067200) /* 2024-01-01 UTC */
#define DEVICE_EVENT_PROCESSING_ATTEMPTS 120
#define DEVICE_EVENT_NETWORK_RETRIES 6

static const char *TAG = "snowball/provision";
static snowball_device_identity_t device_identity;
static snowball_provisioning_status_t current_status;
static SemaphoreHandle_t status_mutex;
/* esp_http_client and esp-tls allocate sizeable shared transport state.  Keep
 * CA fetch, event delivery, enrollment, diagnostics, and media signalling
 * serialized; the board has one Wi-Fi link and concurrent clients only add
 * heap fragmentation and races with the audio tasks. */
static SemaphoreHandle_t network_mutex;
static QueueHandle_t event_queue;
static snowball_delivery_callback_t delivery_callback;
static bool wifi_started;
static volatile bool enrollment_task_running;
static volatile bool candidate_sync_task_running;
static uint32_t candidate_sync_boot_nonce;
static uint32_t candidate_sync_counter;

static esp_err_t fetch_pinned_ca(
    const char *gateway,
    uint16_t http_port,
    const char *expected_sha256,
    char *certificate_pem,
    size_t certificate_size
);

typedef struct {
    char *buffer;
    size_t capacity;
    size_t length;
    bool overflow;
    char date_header[HTTP_DATE_MAX];
} http_capture_t;

static void secure_zero(void *value, size_t length) {
    volatile unsigned char *cursor = (volatile unsigned char *)value;
    while (length-- > 0) {
        *cursor++ = 0;
    }
}

static esp_err_t capture_http_data(esp_http_client_event_t *event) {
    http_capture_t *capture = (http_capture_t *)event->user_data;
    if (!capture) {
        return ESP_OK;
    }
    if (event->event_id == HTTP_EVENT_ON_HEADER) {
        if (event->header_key && event->header_value &&
            strcasecmp(event->header_key, "Date") == 0) {
            size_t length = strlen(event->header_value);
            if (length < sizeof(capture->date_header)) {
                memcpy(capture->date_header, event->header_value, length + 1);
            } else {
                capture->date_header[0] = '\0';
            }
        }
        return ESP_OK;
    }
    if (event->event_id != HTTP_EVENT_ON_DATA || event->data_len <= 0) {
        return ESP_OK;
    }
    if (capture->length + (size_t)event->data_len >= capture->capacity) {
        capture->overflow = true;
        return ESP_FAIL;
    }
    memcpy(capture->buffer + capture->length, event->data, (size_t)event->data_len);
    capture->length += (size_t)event->data_len;
    capture->buffer[capture->length] = '\0';
    return ESP_OK;
}

static int month_number(const char *month) {
    static const char *const names[] = {
        "Jan", "Feb", "Mar", "Apr", "May", "Jun",
        "Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
    };
    for (int index = 0; index < 12; ++index) {
        if (strcasecmp(month, names[index]) == 0) return index + 1;
    }
    return 0;
}

/* Proleptic Gregorian days since 1970-01-01; avoids timezone/libc state. */
static int64_t days_from_civil(int year, unsigned month, unsigned day) {
    year -= month <= 2;
    const int era = (year >= 0 ? year : year - 399) / 400;
    const unsigned year_of_era = (unsigned)(year - era * 400);
    const unsigned day_of_year =
        (153 * (month + (month > 2 ? (unsigned)-3 : 9)) + 2) / 5 + day - 1;
    const unsigned day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    return (int64_t)era * 146097 + (int64_t)day_of_era - 719468;
}

static bool parse_http_date(const char *value, time_t *result) {
    char weekday[4] = {0};
    char month[4] = {0};
    char zone[4] = {0};
    int day = 0;
    int year = 0;
    int hour = 0;
    int minute = 0;
    int second = 0;
    if (!value || !result ||
        sscanf(value, "%3[^,], %d %3s %d %d:%d:%d %3s",
               weekday, &day, month, &year, &hour, &minute, &second, zone) != 8 ||
        month_number(month) == 0 || strcmp(zone, "GMT") != 0 ||
        year < 2024 || year > 2100 || day < 1 || day > 31 ||
        hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 60) {
        return false;
    }
    int month_value = month_number(month);
    int64_t days = days_from_civil(year, (unsigned)month_value, (unsigned)day);
    if (days < 0) return false;
    *result = (time_t)(days * 86400 + hour * 3600 + minute * 60 + (second > 59 ? 59 : second));
    return true;
}

static bool clock_is_sane(void) {
    return time(NULL) >= CLOCK_SANITY_EPOCH;
}

static bool bootstrap_clock_from_http_date(const char *date_header) {
    if (clock_is_sane()) return true;
    time_t gateway_time = 0;
    if (!parse_http_date(date_header, &gateway_time)) {
        ESP_LOGW(TAG, "Gateway HTTP Date missing or invalid; TLS clock is not ready");
        return false;
    }
    struct timeval now = {.tv_sec = gateway_time, .tv_usec = 0};
    if (settimeofday(&now, NULL) != 0) {
        ESP_LOGW(TAG, "Gateway time bootstrap failed");
        return false;
    }
    ESP_LOGI(TAG, "System clock bootstrapped from pinned Gateway response");
    return true;
}

static bool valid_request_id(const char *value) {
    size_t length = value ? strlen(value) : 0;
    if (length == 0 || length > REQUEST_ID_MAX) {
        return false;
    }
    for (size_t index = 0; index < length; ++index) {
        unsigned char character = (unsigned char)value[index];
        if (!isalnum(character) && character != '-' && character != '_') {
            return false;
        }
    }
    return true;
}

static bool is_hex_digest(const char *value) {
    if (!value || strlen(value) != 64) {
        return false;
    }
    for (size_t index = 0; index < 64; ++index) {
        if (!isxdigit((unsigned char)value[index])) {
            return false;
        }
    }
    return true;
}

static bool is_base64url_token(const char *value) {
    size_t length = value ? strlen(value) : 0;
    if (length < 20 || length > ENROLLMENT_TOKEN_MAX) {
        return false;
    }
    for (size_t index = 0; index < length; ++index) {
        unsigned char character = (unsigned char)value[index];
        if (!isalnum(character) && character != '-' && character != '_') {
            return false;
        }
    }
    return true;
}

static bool is_private_ipv4(const char *value) {
    unsigned int a, b, c, d;
    char extra;
    if (!value || sscanf(value, "%u.%u.%u.%u%c", &a, &b, &c, &d, &extra) != 4 ||
        a > 255 || b > 255 || c > 255 || d > 255) {
        return false;
    }
    return a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168);
}

static bool object_has_only(const cJSON *object, const char *const *allowed, size_t allowed_count) {
    for (const cJSON *item = object ? object->child : NULL; item; item = item->next) {
        bool known = false;
        for (size_t index = 0; index < allowed_count; ++index) {
            if (strcmp(item->string, allowed[index]) == 0) {
                known = true;
                break;
            }
        }
        if (!known) {
            return false;
        }
        for (const cJSON *other = item->next; other; other = other->next) {
            if (strcmp(item->string, other->string) == 0) {
                return false;
            }
        }
    }
    return cJSON_IsObject(object);
}

static void send_json(cJSON *response) {
    char *line = cJSON_PrintUnformatted(response);
    if (line) {
        serial_output_line(line);
        cJSON_free(line);
    }
    cJSON_Delete(response);
}

static void send_error(const char *request_id, const char *code) {
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    if (valid_request_id(request_id)) {
        cJSON_AddStringToObject(response, "id", request_id);
    }
    cJSON_AddBoolToObject(response, "ok", false);
    cJSON_AddStringToObject(response, "error", code);
    send_json(response);
}

static snowball_provisioning_status_t status_copy(void) {
    snowball_provisioning_status_t copy;
    xSemaphoreTake(status_mutex, portMAX_DELAY);
    copy = current_status;
    xSemaphoreGive(status_mutex);
    return copy;
}

static void set_gateway_reachable(bool reachable) {
    if (!status_mutex) return;
    xSemaphoreTake(status_mutex, portMAX_DELAY);
    current_status.gateway_reachable = reachable;
    xSemaphoreGive(status_mutex);
}

static bool network_lock_take(void) {
    return network_mutex && xSemaphoreTake(network_mutex, portMAX_DELAY) == pdTRUE;
}

static void network_lock_give(void) {
    if (network_mutex) xSemaphoreGive(network_mutex);
}

snowball_provisioning_status_t provisioning_status(void) {
    return status_copy();
}

static void send_status(const char *request_id) {
    snowball_provisioning_status_t status = status_copy();
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON_AddBoolToObject(result, "configured", status.configured);
    cJSON_AddBoolToObject(result, "wifiConnected", status.wifi_connected);
    cJSON_AddBoolToObject(result, "gatewayReachable", status.gateway_reachable);
    cJSON_AddBoolToObject(result, "enrollmentPending", status.enrollment_pending);
    cJSON_AddBoolToObject(result, "debugFeedback", board_audio_debug_feedback_enabled());
    if (status.configured) {
        cJSON_AddStringToObject(result, "gateway", status.gateway);
        cJSON_AddNumberToObject(result, "gatewayHttpPort", status.gateway_http_port);
        cJSON_AddNumberToObject(result, "gatewayPort", status.gateway_port);
    }
#if CONFIG_SECURE_FLASH_ENC_ENABLED
    cJSON_AddStringToObject(result, "credentialStorage", "encrypted-nvs");
#else
    cJSON_AddStringToObject(result, "credentialStorage", "development-plaintext-nvs");
#endif
    send_json(response);
}

static void handle_debug_mode(const char *request_id, const cJSON *payload) {
    static const char *const fields[] = {"enabled"};
    const cJSON *enabled = cJSON_GetObjectItemCaseSensitive(payload, "enabled");
    if (!object_has_only(payload, fields, 1) || (!cJSON_IsTrue(enabled) && !cJSON_IsFalse(enabled))) {
        send_error(request_id, "invalid_debug_mode_payload");
        return;
    }
    board_audio_set_debug_feedback(cJSON_IsTrue(enabled));
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON_AddBoolToObject(result, "debugFeedback", board_audio_debug_feedback_enabled());
    send_json(response);
}

static void handle_trace(const char *request_id) {
    snowball_diagnostic_event_t *events = calloc(SNOWBALL_DIAGNOSTIC_CAPACITY, sizeof(*events));
    if (!events) {
        send_error(request_id, "out_of_memory");
        return;
    }
    size_t count = diagnostics_snapshot(events, SNOWBALL_DIAGNOSTIC_CAPACITY);
    cJSON *response = cJSON_CreateObject();
    if (!response) {
        free(events);
        send_error(request_id, "out_of_memory");
        return;
    }
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON *items = cJSON_AddArrayToObject(result, "events");
    for (size_t index = 0; index < count; ++index) {
        cJSON *item = cJSON_CreateObject();
        cJSON_AddNumberToObject(item, "sequence", (double)events[index].sequence);
        cJSON_AddNumberToObject(item, "uptimeMs", events[index].uptime_ms);
        cJSON_AddNumberToObject(item, "attempt", events[index].attempt);
        cJSON_AddStringToObject(item, "stage", events[index].stage);
        if (events[index].detail[0]) cJSON_AddStringToObject(item, "detail", events[index].detail);
        if (events[index].confidence >= 0.0f) {
            cJSON_AddNumberToObject(item, "confidence", events[index].confidence);
        }
        cJSON_AddItemToArray(items, item);
    }
    free(events);
    send_json(response);
}

static void handle_hello(const char *request_id) {
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON_AddStringToObject(result, "model", DEVICE_MODEL);
    cJSON_AddStringToObject(result, "firmwareVersion", FIRMWARE_VERSION);
    cJSON_AddNumberToObject(result, "protocolVersion", PROTOCOL_VERSION);
    cJSON_AddStringToObject(result, "hardwareId", device_identity.hardware_id);
    cJSON_AddStringToObject(result, "publicKey", device_identity.public_key_pem);
    cJSON_AddStringToObject(result, "publicKeyFingerprint", device_identity.fingerprint);
    cJSON_AddStringToObject(result, "developmentWake", "Hi ESP");
    send_json(response);
}

static void handle_audio_levels(const char *request_id) {
    board_audio_reset_levels();
    vTaskDelay(pdMS_TO_TICKS(1000));
    snowball_audio_levels_t levels = {0};
    board_audio_take_levels(&levels);
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON_AddNumberToObject(result, "sampleFrames", levels.sample_frames);
    cJSON *peaks = cJSON_AddArrayToObject(result, "peak");
    for (size_t channel = 0; channel < SNOWBALL_AUDIO_FEED_CHANNELS; ++channel) {
        cJSON_AddItemToArray(peaks, cJSON_CreateNumber(levels.peak[channel]));
    }
    send_json(response);
}

static void handle_audio_self_test(const char *request_id) {
    if (!board_audio_try_begin_command_listening()) {
        send_error(request_id, "audio_busy");
        return;
    }
    board_audio_reset_levels();
    vTaskDelay(pdMS_TO_TICKS(50));
    esp_err_t playback = board_audio_play_ack();
    board_audio_set_command_listening(false);
    vTaskDelay(pdMS_TO_TICKS(150));
    snowball_audio_levels_t levels = {0};
    board_audio_take_levels(&levels);
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", playback == ESP_OK);
    if (playback == ESP_OK) {
        cJSON *result = cJSON_AddObjectToObject(response, "result");
        cJSON_AddBoolToObject(result, "speakerWrite", true);
        cJSON_AddNumberToObject(result, "sampleFrames", levels.sample_frames);
        cJSON *peaks = cJSON_AddArrayToObject(result, "peak");
        for (size_t channel = 0; channel < SNOWBALL_AUDIO_FEED_CHANNELS; ++channel) {
            cJSON_AddItemToArray(peaks, cJSON_CreateNumber(levels.peak[channel]));
        }
    } else {
        cJSON_AddStringToObject(response, "error", "speaker_write_failed");
    }
    send_json(response);
}

/* Diagnostics-only HTTP callback.  The response body is deliberately
 * discarded: network-test must never retain or print arbitrary Internet
 * content. */
static esp_err_t discard_http_data(esp_http_client_event_t *event) {
    (void)event;
    return ESP_OK;
}

static esp_err_t probe_http_status(const char *url, int *http_status) {
    if (http_status) *http_status = -1;
    if (!url || !http_status) return ESP_ERR_INVALID_ARG;
    esp_http_client_config_t config = {
        .url = url,
        .method = HTTP_METHOD_GET,
        .event_handler = discard_http_data,
        .timeout_ms = 5000,
        .buffer_size = 512,
        .buffer_size_tx = 1024,
        .disable_auto_redirect = true,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (!client) return ESP_ERR_NO_MEM;
    esp_err_t result = esp_http_client_perform(client);
    *http_status = esp_http_client_get_status_code(client);
    esp_http_client_cleanup(client);
    return result;
}

static esp_err_t probe_https_status(
    const char *url,
    const char *certificate_pem,
    int *http_status
) {
    if (http_status) *http_status = -1;
    if (!url || !certificate_pem || !http_status) return ESP_ERR_INVALID_ARG;
    esp_http_client_config_t config = {
        .url = url,
        .method = HTTP_METHOD_GET,
        .cert_pem = certificate_pem,
        .event_handler = discard_http_data,
        .timeout_ms = 5000,
        .buffer_size = 512,
        .buffer_size_tx = 1024,
        .disable_auto_redirect = true,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (!client) return ESP_ERR_NO_MEM;
    esp_err_t result = esp_http_client_perform(client);
    *http_status = esp_http_client_get_status_code(client);
    esp_http_client_cleanup(client);
    return result;
}

static void handle_network_test(const char *request_id) {
    snowball_provisioning_status_t configured_status = status_copy();
    ESP_LOGI(TAG, "probe_configured_gateway: testing the configured private Gateway route");
    esp_netif_ip_info_t ip_info = {0};
    esp_netif_t *sta = esp_netif_get_handle_from_ifkey("WIFI_STA_DEF");
    bool ip_info_valid = sta && esp_netif_get_ip_info(sta, &ip_info) == ESP_OK && ip_info.ip.addr != 0;
    char sta_ip[16] = {0};
    char netmask[16] = {0};
    char dhcp_gateway[16] = {0};
    if (ip_info_valid) {
        esp_ip4addr_ntoa(&ip_info.ip, sta_ip, sizeof(sta_ip));
        esp_ip4addr_ntoa(&ip_info.netmask, netmask, sizeof(netmask));
        esp_ip4addr_ntoa(&ip_info.gw, dhcp_gateway, sizeof(dhcp_gateway));
    }

    /* routed private networks are valid; same_subnet is diagnostic-only and
     * must never be used as a reachability or pairing gate. */
    bool same_subnet = false;
    esp_ip4_addr_t configured_gateway = {0};
    if (ip_info_valid && esp_netif_str_to_ip4(configured_status.gateway, &configured_gateway) == ESP_OK) {
        same_subnet = (ip_info.ip.addr & ip_info.netmask.addr) ==
            (configured_gateway.addr & ip_info.netmask.addr);
    }

    int gateway_http_status = -1;
    int gateway_https_status = -1;
    int internet_http_status = -1;
    esp_err_t gateway_http_result = ESP_ERR_INVALID_STATE;
    esp_err_t gateway_ca_result = ESP_ERR_INVALID_STATE;
    esp_err_t gateway_https_result = ESP_ERR_INVALID_STATE;
    esp_err_t internet_result = ESP_ERR_INVALID_STATE;
    char gateway_url[96] = {0};
    char gateway_https_url[128] = {0};
    char ca_sha256[65] = {0};
    uint16_t http_port = 0;
    /* All esp_http_client instances share the ESP32-S3 networking stack and
     * its flash/PSRAM-backed buffers.  Keep this diagnostic transaction
     * serialized with event delivery and media negotiation. */
    bool network_locked = ip_info_valid && network_lock_take();

    if (configured_status.configured && ip_info_valid && network_locked) {
        nvs_handle_t nvs;
        esp_err_t nvs_result = nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs);
        if (nvs_result == ESP_OK) {
            size_t pin_length = sizeof(ca_sha256);
            nvs_result = nvs_get_u16(nvs, "http_port", &http_port);
            if (nvs_result == ESP_OK) {
                nvs_result = nvs_get_str(nvs, "ca_sha256", ca_sha256, &pin_length);
            }
            nvs_close(nvs);
        }
        if (nvs_result == ESP_OK) {
            int length = snprintf(gateway_url, sizeof(gateway_url),
                "http://%s:%u/ca.crt", configured_status.gateway, http_port);
            if (length <= 0 || (size_t)length >= sizeof(gateway_url)) {
                gateway_http_result = ESP_ERR_INVALID_SIZE;
            } else {
                /* This is an actual route test, even when the destination
                 * is outside the STA's local subnet. */
                gateway_http_result = probe_http_status(gateway_url, &gateway_http_status);
                if (gateway_http_result == ESP_OK && gateway_http_status == 200) {
                    char *certificate_pem = calloc(1, CA_PEM_MAX);
                    if (certificate_pem) {
                        gateway_ca_result = fetch_pinned_ca(
                            configured_status.gateway,
                            http_port,
                            ca_sha256,
                            certificate_pem,
                            CA_PEM_MAX
                        );
                        if (gateway_ca_result == ESP_OK) {
                            int https_length = snprintf(
                                gateway_https_url,
                                sizeof(gateway_https_url),
                                "https://%s:%u/api/health",
                                configured_status.gateway,
                                configured_status.gateway_port
                            );
                            if (https_length <= 0 || (size_t)https_length >= sizeof(gateway_https_url)) {
                                gateway_https_result = ESP_ERR_INVALID_SIZE;
                            } else {
                                gateway_https_result = probe_https_status(
                                    gateway_https_url,
                                    certificate_pem,
                                    &gateway_https_status
                                );
                            }
                        } else {
                            gateway_https_result = gateway_ca_result;
                        }
                        secure_zero(certificate_pem, CA_PEM_MAX);
                        free(certificate_pem);
                    } else {
                        gateway_ca_result = ESP_ERR_NO_MEM;
                    }
                } else {
                    gateway_ca_result = gateway_http_result != ESP_OK
                        ? gateway_http_result : ESP_ERR_INVALID_RESPONSE;
                }
            }
        } else {
            gateway_http_result = nvs_result;
            gateway_ca_result = nvs_result;
        }
    }

    if (ip_info_valid && network_locked) {
        /* Fixed HTTP is intentionally a diagnostic only.  It verifies DHCP,
         * default routing, DNS, TCP, and an Internet response without making
         * external connectivity a product dependency. */
        internet_result = probe_http_status("http://example.com/", &internet_http_status);
    }
    if (network_locked) network_lock_give();

    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", true);
    cJSON *result = cJSON_AddObjectToObject(response, "result");
    cJSON_AddBoolToObject(result, "configured", configured_status.configured);
    cJSON_AddBoolToObject(result, "wifiConnected", configured_status.wifi_connected);
    cJSON_AddBoolToObject(result, "ipInfoValid", ip_info_valid);
    cJSON_AddStringToObject(result, "staIp", sta_ip);
    cJSON_AddStringToObject(result, "netmask", netmask);
    cJSON_AddStringToObject(result, "dhcpGateway", dhcp_gateway);
    cJSON_AddStringToObject(result, "configuredGateway", configured_status.gateway);
    cJSON_AddBoolToObject(result, "sameSubnet", same_subnet);
    cJSON_AddNumberToObject(result, "gatewayHttpStatus", gateway_http_status);
    cJSON_AddStringToObject(result, "gatewayHttpError", esp_err_to_name(gateway_http_result));
    cJSON_AddNumberToObject(result, "gatewayCaHttpStatus", gateway_http_status);
    cJSON_AddBoolToObject(result, "gatewayCaPinned", gateway_ca_result == ESP_OK);
    cJSON_AddStringToObject(result, "gatewayCaError", esp_err_to_name(gateway_ca_result));
    cJSON_AddNumberToObject(result, "gatewayHttpsStatus", gateway_https_status);
    cJSON_AddBoolToObject(result, "gatewayHttpsReachable",
        gateway_https_result == ESP_OK && gateway_https_status >= 200 && gateway_https_status <= 599);
    cJSON_AddStringToObject(result, "gatewayHttpsError", esp_err_to_name(gateway_https_result));
    cJSON_AddNumberToObject(result, "internetHttpStatus", internet_http_status);
    cJSON_AddBoolToObject(result, "internetReachable",
        internet_result == ESP_OK && internet_http_status >= 200 && internet_http_status <= 599);
    cJSON_AddStringToObject(result, "internetError", esp_err_to_name(internet_result));
    send_json(response);
    secure_zero(ca_sha256, sizeof(ca_sha256));
}

static esp_err_t store_configuration(
    const char *ssid,
    const char *password,
    const char *gateway,
    uint16_t http_port,
    uint16_t port,
    const char *ca_sha256,
    const char *token
) {
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(PROVISION_NAMESPACE, NVS_READWRITE, &nvs);
    if (result != ESP_OK) return result;
    if ((result = nvs_set_str(nvs, "ssid", ssid)) == ESP_OK &&
        (result = nvs_set_str(nvs, "password", password)) == ESP_OK &&
        (result = nvs_set_str(nvs, "gateway", gateway)) == ESP_OK &&
        (result = nvs_set_u16(nvs, "http_port", http_port)) == ESP_OK &&
        (result = nvs_set_u16(nvs, "port", port)) == ESP_OK &&
        (result = nvs_set_str(nvs, "ca_sha256", ca_sha256)) == ESP_OK &&
        (result = nvs_set_str(nvs, "enroll_token", token)) == ESP_OK &&
        (result = nvs_set_u8(nvs, "pending", 1)) == ESP_OK) {
        result = nvs_commit(nvs);
    }
    nvs_close(nvs);
    return result;
}

static esp_err_t wifi_connect(const char *ssid, const char *password) {
    wifi_config_t config = {0};
    strlcpy((char *)config.sta.ssid, ssid, sizeof(config.sta.ssid));
    strlcpy((char *)config.sta.password, password, sizeof(config.sta.password));
    config.sta.threshold.authmode = password[0] ? WIFI_AUTH_WPA2_PSK : WIFI_AUTH_OPEN;
    config.sta.pmf_cfg.capable = true;
    config.sta.pmf_cfg.required = false;
    esp_err_t result = esp_wifi_set_mode(WIFI_MODE_STA);
    if (result == ESP_OK) {
        result = esp_wifi_set_config(WIFI_IF_STA, &config);
    }
    if (!wifi_started) {
        if (result == ESP_OK) {
            result = esp_wifi_start();
            wifi_started = result == ESP_OK;
        }
    }
    if (result == ESP_OK) {
        result = esp_wifi_connect();
    }
    secure_zero(&config, sizeof(config));
    return result;
}

static void scrub_provisioning_payload(const cJSON *payload) {
    const char *sensitive_fields[] = {"password", "enrollmentToken"};
    for (size_t index = 0; index < sizeof(sensitive_fields) / sizeof(sensitive_fields[0]); ++index) {
        cJSON *item = cJSON_GetObjectItemCaseSensitive(payload, sensitive_fields[index]);
        if (cJSON_IsString(item) && item->valuestring) {
            secure_zero(item->valuestring, strlen(item->valuestring));
        }
    }
}

static void handle_provision(const char *request_id, const cJSON *payload) {
    static const char *const fields[] = {
        "ssid", "password", "gateway", "gatewayHttpPort", "gatewayPort", "caSha256", "enrollmentToken", "expiresInSeconds"
    };
    if (!object_has_only(payload, fields, sizeof(fields) / sizeof(fields[0]))) {
        send_error(request_id, "invalid_payload_fields");
        return;
    }
    const cJSON *ssid = cJSON_GetObjectItemCaseSensitive(payload, "ssid");
    const cJSON *password = cJSON_GetObjectItemCaseSensitive(payload, "password");
    const cJSON *gateway = cJSON_GetObjectItemCaseSensitive(payload, "gateway");
    const cJSON *http_port = cJSON_GetObjectItemCaseSensitive(payload, "gatewayHttpPort");
    const cJSON *port = cJSON_GetObjectItemCaseSensitive(payload, "gatewayPort");
    const cJSON *ca_sha256 = cJSON_GetObjectItemCaseSensitive(payload, "caSha256");
    const cJSON *token = cJSON_GetObjectItemCaseSensitive(payload, "enrollmentToken");
    const cJSON *expires = cJSON_GetObjectItemCaseSensitive(payload, "expiresInSeconds");
    size_t ssid_length = cJSON_IsString(ssid) ? strlen(ssid->valuestring) : 0;
    size_t password_length = cJSON_IsString(password) ? strlen(password->valuestring) : SIZE_MAX;
    if (ssid_length == 0 || ssid_length > 32 || password_length > 63 ||
        (password_length > 0 && password_length < 8) ||
        !cJSON_IsString(gateway) || !is_private_ipv4(gateway->valuestring) ||
        !cJSON_IsNumber(http_port) || http_port->valuedouble != http_port->valueint || http_port->valueint < 1 || http_port->valueint > 65535 ||
        !cJSON_IsNumber(port) || port->valuedouble != port->valueint || port->valueint < 1 || port->valueint > 65535 ||
        !cJSON_IsString(ca_sha256) || !is_hex_digest(ca_sha256->valuestring) ||
        !cJSON_IsString(token) || !is_base64url_token(token->valuestring) ||
        !cJSON_IsNumber(expires) || expires->valuedouble != expires->valueint || expires->valueint < 30 || expires->valueint > 900) {
        send_error(request_id, "invalid_provisioning_values");
        return;
    }

    esp_err_t result = store_configuration(
        ssid->valuestring,
        password->valuestring,
        gateway->valuestring,
        (uint16_t)http_port->valueint,
        (uint16_t)port->valueint,
        ca_sha256->valuestring,
        token->valuestring
    );
    if (result != ESP_OK) {
        send_error(request_id, "configuration_store_failed");
        return;
    }
    xSemaphoreTake(status_mutex, portMAX_DELAY);
    current_status.configured = true;
    current_status.enrollment_pending = true;
    strlcpy(current_status.gateway, gateway->valuestring, sizeof(current_status.gateway));
    current_status.gateway_http_port = (uint16_t)http_port->valueint;
    current_status.gateway_port = (uint16_t)port->valueint;
    xSemaphoreGive(status_mutex);

    /* Do not echo any credential in the response or logs. */
    result = wifi_connect(ssid->valuestring, password->valuestring);
    cJSON *response = cJSON_CreateObject();
    cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
    cJSON_AddStringToObject(response, "id", request_id);
    cJSON_AddBoolToObject(response, "ok", result == ESP_OK);
    if (result == ESP_OK) {
        cJSON *response_result = cJSON_AddObjectToObject(response, "result");
        cJSON_AddStringToObject(response_result, "state", "joining_wifi");
    } else {
        cJSON_AddStringToObject(response, "error", "wifi_start_failed");
    }
    send_json(response);
}

static void handle_request(char *line) {
    cJSON *request = cJSON_ParseWithLength(line, strlen(line));
    static const char *const top_level[] = {"version", "id", "op", "payload"};
    if (!request || !object_has_only(request, top_level, sizeof(top_level) / sizeof(top_level[0]))) {
        cJSON_Delete(request);
        send_error(NULL, "invalid_json_or_fields");
        return;
    }
    const cJSON *version = cJSON_GetObjectItemCaseSensitive(request, "version");
    const cJSON *id = cJSON_GetObjectItemCaseSensitive(request, "id");
    const cJSON *operation = cJSON_GetObjectItemCaseSensitive(request, "op");
    const char *request_id = cJSON_IsString(id) ? id->valuestring : NULL;
    if (!cJSON_IsNumber(version) || version->valuedouble != PROTOCOL_VERSION ||
        !valid_request_id(request_id) || !cJSON_IsString(operation)) {
        send_error(request_id, "invalid_envelope");
        cJSON_Delete(request);
        return;
    }
    const cJSON *payload = cJSON_GetObjectItemCaseSensitive(request, "payload");
    if (strcmp(operation->valuestring, "hello") == 0 && !payload) {
        handle_hello(request_id);
    } else if (strcmp(operation->valuestring, "status") == 0 && !payload) {
        send_status(request_id);
    } else if (strcmp(operation->valuestring, "audio-levels") == 0 && !payload) {
        handle_audio_levels(request_id);
    } else if (strcmp(operation->valuestring, "audio-self-test") == 0 && !payload) {
        handle_audio_self_test(request_id);
    } else if (strcmp(operation->valuestring, "network-test") == 0 && !payload) {
        handle_network_test(request_id);
    } else if (strcmp(operation->valuestring, "debug-mode") == 0 && cJSON_IsObject(payload)) {
        handle_debug_mode(request_id, payload);
    } else if (strcmp(operation->valuestring, "trace") == 0 && !payload) {
        handle_trace(request_id);
    } else if (strcmp(operation->valuestring, "provision") == 0 && cJSON_IsObject(payload)) {
        handle_provision(request_id, payload);
        scrub_provisioning_payload(payload);
    } else if (strcmp(operation->valuestring, "factory-reset") == 0 && cJSON_IsObject(payload)) {
        static const char *const reset_fields[] = {"confirm"};
        const cJSON *confirmation = cJSON_GetObjectItemCaseSensitive(payload, "confirm");
        if (!object_has_only(payload, reset_fields, 1) || !cJSON_IsString(confirmation) ||
            strcmp(confirmation->valuestring, "ERASE SNOWBALL") != 0) {
            send_error(request_id, "factory_reset_confirmation_required");
            cJSON_Delete(request);
            return;
        }
        cJSON *response = cJSON_CreateObject();
        cJSON_AddNumberToObject(response, "version", PROTOCOL_VERSION);
        cJSON_AddStringToObject(response, "id", request_id);
        cJSON_AddBoolToObject(response, "ok", true);
        cJSON *result = cJSON_AddObjectToObject(response, "result");
        cJSON_AddStringToObject(result, "state", "factory_resetting");
        send_json(response);
        vTaskDelay(pdMS_TO_TICKS(100));
        ESP_ERROR_CHECK(nvs_flash_erase());
        esp_restart();
    } else {
        send_error(request_id, "unsupported_operation");
    }
    cJSON_Delete(request);
}

static void serial_task(void *argument) {
    (void)argument;
    char line[SERIAL_LINE_MAX + 2];
    size_t length = 0;
    bool discarding = false;
    while (true) {
        int character = fgetc(stdin);
        if (character == EOF) {
            clearerr(stdin);
            vTaskDelay(pdMS_TO_TICKS(10));
            continue;
        }
        if (character == '\r') {
            continue;
        }
        if (character == '\n') {
            if (discarding) {
                send_error(NULL, "line_too_long");
            } else if (length > 0) {
                line[length] = '\0';
                handle_request(line);
                secure_zero(line, length + 1);
            }
            length = 0;
            discarding = false;
            continue;
        }
        if (discarding) {
            continue;
        }
        if (length >= SERIAL_LINE_MAX) {
            discarding = true;
            secure_zero(line, sizeof(line));
            length = 0;
        } else {
            line[length++] = (char)character;
        }
    }
}

static bool certificate_matches_pin(const char *certificate_pem, const char *expected_hex) {
    mbedtls_x509_crt certificate;
    mbedtls_x509_crt_init(&certificate);
    int result = mbedtls_x509_crt_parse(
        &certificate,
        (const unsigned char *)certificate_pem,
        strlen(certificate_pem) + 1
    );
    unsigned char digest[32] = {0};
    char actual_hex[65] = {0};
    if (result == 0) {
        result = mbedtls_sha256(certificate.raw.p, certificate.raw.len, digest, 0);
    }
    if (result == 0) {
        for (size_t index = 0; index < sizeof(digest); ++index) {
            snprintf(actual_hex + (index * 2), 3, "%02x", digest[index]);
        }
    }
    bool matches = result == 0 && strcasecmp(actual_hex, expected_hex) == 0;
    secure_zero(digest, sizeof(digest));
    mbedtls_x509_crt_free(&certificate);
    return matches;
}

static esp_err_t fetch_pinned_ca(
    const char *gateway,
    uint16_t http_port,
    const char *expected_sha256,
    char *certificate_pem,
    size_t certificate_size
) {
    char url[96];
    int length = snprintf(url, sizeof(url), "http://%s:%u/ca.crt", gateway, http_port);
    if (length <= 0 || (size_t)length >= sizeof(url)) {
        set_gateway_reachable(false);
        return ESP_ERR_INVALID_SIZE;
    }
    http_capture_t capture = {.buffer = certificate_pem, .capacity = certificate_size};
    esp_http_client_config_t config = {
        .url = url,
        .method = HTTP_METHOD_GET,
        .event_handler = capture_http_data,
        .user_data = &capture,
        .timeout_ms = 5000,
        .buffer_size = 1024,
        .buffer_size_tx = 1024,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (!client) {
        set_gateway_reachable(false);
        return ESP_ERR_NO_MEM;
    }
    esp_err_t result = esp_http_client_perform(client);
    int status = esp_http_client_get_status_code(client);
    esp_http_client_cleanup(client);
    if (result != ESP_OK || status != 200 || capture.overflow || capture.length == 0 ||
        !certificate_matches_pin(certificate_pem, expected_sha256)) {
        secure_zero(certificate_pem, certificate_size);
        set_gateway_reachable(false);
        return ESP_ERR_INVALID_CRC;
    }
    /*
     * The board has no battery-backed RTC.  Bootstrap only an unset clock,
     * and only after the downloaded CA has matched the enrollment pin.  The
     * HTTP Date can therefore affect readiness (or cause denial), but it
     * cannot authenticate a TLS peer or replace the pinned CA.  All HTTPS
     * requests still perform normal certificate validity and chain checks.
     */
    if (!bootstrap_clock_from_http_date(capture.date_header) && !clock_is_sane()) {
        secure_zero(certificate_pem, certificate_size);
        set_gateway_reachable(false);
        return ESP_ERR_INVALID_STATE;
    }
    set_gateway_reachable(true);
    return ESP_OK;
}

static void random_hex(char *output, size_t random_bytes) {
    unsigned char raw[32] = {0};
    if (random_bytes > sizeof(raw)) random_bytes = sizeof(raw);
    esp_fill_random(raw, random_bytes);
    for (size_t index = 0; index < random_bytes; ++index) {
        snprintf(output + (index * 2), 3, "%02x", raw[index]);
    }
    secure_zero(raw, sizeof(raw));
}

static esp_err_t submit_enrollment(
    const char *gateway,
    uint16_t https_port,
    const char *certificate_pem,
    const char *token
) {
    char nonce[33] = {0};
    char signature[160] = {0};
    random_hex(nonce, 16);
    esp_err_t result = device_identity_sign_enrollment(&device_identity, token, nonce, signature, sizeof(signature));
    if (result != ESP_OK) return result;

    cJSON *request = cJSON_CreateObject();
    if (!request) return ESP_ERR_NO_MEM;
    cJSON_AddStringToObject(request, "enrollmentToken", token);
    cJSON_AddStringToObject(request, "hardwareId", device_identity.hardware_id);
    cJSON_AddStringToObject(request, "publicKeyFingerprint", device_identity.fingerprint);
    cJSON_AddStringToObject(request, "nonce", nonce);
    cJSON_AddStringToObject(request, "signature", signature);
    char *request_body = cJSON_PrintUnformatted(request);
    cJSON_Delete(request);
    secure_zero(signature, sizeof(signature));
    secure_zero(nonce, sizeof(nonce));
    if (!request_body) return ESP_ERR_NO_MEM;

    char response[HTTP_RESPONSE_MAX] = {0};
    http_capture_t capture = {.buffer = response, .capacity = sizeof(response)};
    char url[128];
    int url_length = snprintf(url, sizeof(url), "https://%s:%u/api/auth/device-enroll", gateway, https_port);
    if (url_length <= 0 || (size_t)url_length >= sizeof(url)) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        return ESP_ERR_INVALID_SIZE;
    }
    esp_http_client_config_t config = {
        .url = url,
        .cert_pem = certificate_pem,
        .event_handler = capture_http_data,
        .user_data = &capture,
        .timeout_ms = 8000,
        .buffer_size = 1024,
        .buffer_size_tx = 1024,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (!client) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        return ESP_ERR_NO_MEM;
    }
    esp_http_client_set_method(client, HTTP_METHOD_POST);
    esp_http_client_set_header(client, "Content-Type", "application/json");
    esp_http_client_set_post_field(client, request_body, (int)strlen(request_body));
    result = esp_http_client_perform(client);
    int status = esp_http_client_get_status_code(client);
    esp_http_client_cleanup(client);
    secure_zero(request_body, strlen(request_body));
    cJSON_free(request_body);
    if (result != ESP_OK || status != 200 || capture.overflow) {
        secure_zero(response, sizeof(response));
        return ESP_ERR_INVALID_RESPONSE;
    }
    cJSON *response_json = cJSON_Parse(response);
    const cJSON *enrolled = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "enrolled") : NULL;
    bool accepted = cJSON_IsTrue(enrolled);
    cJSON_Delete(response_json);
    secure_zero(response, sizeof(response));
    return accepted ? ESP_OK : ESP_ERR_INVALID_RESPONSE;
}

static esp_err_t store_candidate_catalog_from_response(
    const snowball_device_event_t *event,
    const cJSON *response_json,
    const char **detail
) {
    if (!event || !response_json ||
        (strcmp(event->event, "sync") != 0 && strcmp(event->event, "command") != 0)) return ESP_OK;
    const cJSON *outcome = cJSON_GetObjectItemCaseSensitive(response_json, "outcome");
    const cJSON *catalog = cJSON_GetObjectItemCaseSensitive(response_json, "catalog");
    if (!cJSON_IsString(outcome) || strcmp(outcome->valuestring, "executed") != 0 || !cJSON_IsObject(catalog)) {
        return ESP_OK;
    }
    char *catalog_json = cJSON_PrintUnformatted(catalog);
    if (!catalog_json) {
        if (detail) *detail = "candidate_store_failed";
        return ESP_ERR_NO_MEM;
    }
    esp_err_t result = command_recognizer_store_catalog(catalog_json);
    cJSON_free(catalog_json);
    if (result != ESP_OK && detail) *detail = "candidate_store_failed";
    return result;
}

static esp_err_t submit_device_event(
    const snowball_device_event_t *event,
    snowball_delivery_result_t *delivery_result,
    char *delivery_detail,
    size_t delivery_detail_size
) {
    if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_FAILED;
    if (delivery_detail && delivery_detail_size) delivery_detail[0] = '\0';
    if (!event || provisioning_status().enrollment_pending || !provisioning_status().wifi_connected) {
        if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_UNPAIRED;
        if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, "device_not_paired_or_offline", delivery_detail_size);
        return ESP_ERR_INVALID_STATE;
    }
    char gateway[16] = {0};
    char ca_sha256[65] = {0};
    uint16_t http_port = 0;
    uint16_t https_port = 0;
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs);
    if (result != ESP_OK) return result;
    size_t gateway_length = sizeof(gateway);
    size_t pin_length = sizeof(ca_sha256);
    if (nvs_get_str(nvs, "gateway", gateway, &gateway_length) != ESP_OK ||
        nvs_get_u16(nvs, "http_port", &http_port) != ESP_OK ||
        nvs_get_u16(nvs, "port", &https_port) != ESP_OK ||
        nvs_get_str(nvs, "ca_sha256", ca_sha256, &pin_length) != ESP_OK) {
        nvs_close(nvs);
        return ESP_ERR_NOT_FOUND;
    }
    nvs_close(nvs);

    char *certificate_pem = calloc(1, CA_PEM_MAX);
    char *response = NULL;
    if (!certificate_pem) return ESP_ERR_NO_MEM;
    if (!network_lock_take()) {
        secure_zero(certificate_pem, CA_PEM_MAX);
        free(certificate_pem);
        return ESP_ERR_INVALID_STATE;
    }
    result = fetch_pinned_ca(gateway, http_port, ca_sha256, certificate_pem, CA_PEM_MAX);
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "Gateway endpoint unavailable or CA pin rejected: %s", esp_err_to_name(result));
        secure_zero(certificate_pem, CA_PEM_MAX);
        free(certificate_pem);
        network_lock_give();
        return result;
    }

    char signature[160] = {0};
    result = device_identity_sign_event(
        &device_identity, event->boot_nonce, event->counter, event->event, event->wake,
        event->command, event->target, event->name, event->confidence, signature, sizeof(signature)
    );
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "device event signing failed: %s", esp_err_to_name(result));
        goto cleanup;
    }

    cJSON *request = cJSON_CreateObject();
    if (!request) {
        result = ESP_ERR_NO_MEM;
        goto cleanup;
    }
    cJSON_AddNumberToObject(request, "version", 1);
    cJSON_AddStringToObject(request, "hardwareId", device_identity.hardware_id);
    cJSON_AddStringToObject(request, "publicKeyFingerprint", device_identity.fingerprint);
    cJSON_AddNumberToObject(request, "bootNonce", event->boot_nonce);
    cJSON_AddNumberToObject(request, "counter", event->counter);
    cJSON_AddStringToObject(request, "event", event->event);
    if (event->wake[0]) cJSON_AddStringToObject(request, "wake", event->wake);
    if (event->command[0]) cJSON_AddStringToObject(request, "command", event->command);
    if (event->target[0]) cJSON_AddStringToObject(request, "target", event->target);
    if (event->name[0]) cJSON_AddStringToObject(request, "name", event->name);
    cJSON_AddNumberToObject(request, "confidence", event->confidence);
    cJSON_AddStringToObject(request, "signature", signature);
    char *request_body = cJSON_PrintUnformatted(request);
    cJSON_Delete(request);
    if (!request_body) {
        result = ESP_ERR_NO_MEM;
        goto cleanup;
    }

    response = calloc(1, HTTP_RESPONSE_MAX);
    if (!response) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        result = ESP_ERR_NO_MEM;
        goto cleanup;
    }
    http_capture_t capture = {.buffer = response, .capacity = HTTP_RESPONSE_MAX};
    char url[128];
    int url_length = snprintf(url, sizeof(url), "https://%s:%u/api/device/events", gateway, https_port);
    if (url_length <= 0 || (size_t)url_length >= sizeof(url)) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        result = ESP_ERR_INVALID_SIZE;
        goto cleanup;
    }
    esp_http_client_config_t config = {
        .url = url,
        .cert_pem = certificate_pem,
        .event_handler = capture_http_data,
        .user_data = &capture,
        /* A fresh ChatGPT page may need a navigation plus two authoritative
           Voice observations. Keep the device media session alive for that
           bounded startup window instead of treating a normal slow browser
           turn as a transport failure. */
        .timeout_ms = 30000,
        .buffer_size = 1024,
        .buffer_size_tx = 1024,
    };
    int status = 0;
    bool terminal_response = false;
    bool processing_seen = false;
    int network_failures = 0;
    /* A slow Chromium navigation is not a failed command. The Gateway returns
       202/processing while the first signed envelope is still in flight; retry
       that exact envelope long enough to receive the cached terminal result.
       The replay cursor prevents duplicate browser automation. */
    for (int attempt = 0; attempt < DEVICE_EVENT_PROCESSING_ATTEMPTS; ++attempt) {
        bool processing_response = false;
        capture.length = 0;
        capture.overflow = false;
        memset(response, 0, HTTP_RESPONSE_MAX);
        esp_http_client_handle_t client = esp_http_client_init(&config);
        if (!client) {
            result = ESP_ERR_NO_MEM;
            break;
        }
        esp_http_client_set_method(client, HTTP_METHOD_POST);
        esp_http_client_set_header(client, "Content-Type", "application/json");
        esp_http_client_set_post_field(client, request_body, (int)strlen(request_body));
        result = esp_http_client_perform(client);
        status = esp_http_client_get_status_code(client);
        esp_http_client_cleanup(client);
        if (result != ESP_OK) {
            ESP_LOGW(TAG, "device event HTTPS transport failed: %s status=%d attempt=%d",
                esp_err_to_name(result), status, attempt + 1);
        }
        if (result == ESP_OK && !capture.overflow && status >= 200 && status <= 599) {
            cJSON *response_json = cJSON_Parse(response);
            const cJSON *outcome = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "outcome") : NULL;
            const cJSON *action = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "action") : NULL;
			const cJSON *accepted = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "accepted") : NULL;
			const char *detail = cJSON_IsString(outcome) ? outcome->valuestring :
				(cJSON_IsString(action) ? action->valuestring : "accepted");
			esp_err_t catalog_result = store_candidate_catalog_from_response(event, response_json, &detail);
			if (!response_json) {
                if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, "invalid_json", delivery_detail_size);
                result = ESP_ERR_INVALID_RESPONSE;
			} else if (catalog_result != ESP_OK) {
				if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_FAILED;
				if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, detail, delivery_detail_size);
				terminal_response = true;
				cJSON_Delete(response_json);
				response_json = NULL;
				/* The Gateway result was received and cached; do not retry a
				 * valid signed sync forever when local NVS persistence failed. */
				result = ESP_OK;
				break;
			} else if (!cJSON_IsTrue(accepted)) {
                /* A signed event can be rejected terminally (for example
                 * revoked enrollment or a browser conflict). Retrying a
                 * cached 4xx/5xx response for two minutes only adds lag and
                 * can mask the real failure behind repeated tones. */
                if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_FAILED;
                if (delivery_detail && delivery_detail_size) {
                    snprintf(delivery_detail, delivery_detail_size, "http_%d", status);
                }
                terminal_response = true;
                cJSON_Delete(response_json);
                response_json = NULL;
                result = ESP_OK;
                break;
            } else if (status == 202 && strcmp(detail, "processing") == 0) {
                processing_response = true;
                processing_seen = true;
                network_failures = 0;
                if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_PROCESSING;
                if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, detail, delivery_detail_size);
                cJSON_Delete(response_json);
                response_json = NULL;
                result = ESP_ERR_TIMEOUT;
                if (attempt == 0 || attempt == 9 || attempt == 29 || attempt == 59 || attempt == 89) {
                    ESP_LOGI(TAG, "device event still processing after %d retries", attempt + 1);
                }
                if (attempt + 1 < DEVICE_EVENT_PROCESSING_ATTEMPTS) {
                    vTaskDelay(pdMS_TO_TICKS(200));
                    continue;
                }
                ESP_LOGW(TAG, "device event remained in processing state after %d retries", DEVICE_EVENT_PROCESSING_ATTEMPTS);
			} else {
				if (status < 200 || status >= 300) {
                    if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_FAILED;
				} else if (strcmp(detail, "executed") == 0) {
					if (delivery_result) *delivery_result = strcmp(event->event, "sync") == 0
						? SNOWBALL_DELIVERY_SYNCED
						: SNOWBALL_DELIVERY_EXECUTED;
                } else if (strcmp(detail, "not_ready") == 0 || strcmp(detail, "awaiting_device_media_adapter") == 0) {
                    if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_NOT_READY;
                } else {
                    if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_ACCEPTED;
                }
                if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, detail, delivery_detail_size);
                terminal_response = true;
                cJSON_Delete(response_json);
                response_json = NULL;
                result = ESP_OK;
                break;
            }
            cJSON_Delete(response_json);
        }
        if (result == ESP_OK && (status != 200 || capture.overflow)) {
            if (delivery_detail && delivery_detail_size) {
                if (capture.overflow) strlcpy(delivery_detail, "response_too_large", delivery_detail_size);
                else snprintf(delivery_detail, delivery_detail_size, "http_%d", status);
            }
            ESP_LOGW(TAG, "device event HTTPS rejected: status=%d overflow=%d", status, capture.overflow);
            result = ESP_ERR_INVALID_RESPONSE;
        } else if (result != ESP_OK && result != ESP_ERR_TIMEOUT) {
            if (delivery_detail && delivery_detail_size && !delivery_detail[0]) {
                strlcpy(delivery_detail, esp_err_to_name(result), delivery_detail_size);
            }
            ESP_LOGW(TAG, "device event HTTPS transport failed: %s", esp_err_to_name(result));
        }
        if (!processing_response && result != ESP_OK) {
            network_failures++;
            if (network_failures >= DEVICE_EVENT_NETWORK_RETRIES) {
                break;
            }
            vTaskDelay(pdMS_TO_TICKS(300U * network_failures));
        }
    }
    secure_zero(request_body, strlen(request_body));
    cJSON_free(request_body);
    if (!terminal_response && processing_seen && result == ESP_ERR_TIMEOUT) {
        if (delivery_result) *delivery_result = SNOWBALL_DELIVERY_FAILED;
        if (delivery_detail && delivery_detail_size) strlcpy(delivery_detail, "processing_timeout", delivery_detail_size);
    }
cleanup:
    secure_zero(signature, sizeof(signature));
    secure_zero(certificate_pem, CA_PEM_MAX);
    free(certificate_pem);
    if (response) {
        secure_zero(response, HTTP_RESPONSE_MAX);
        free(response);
    }
    network_lock_give();
    return result;
}

esp_err_t provisioning_exchange_media_offer(
    uint32_t boot_nonce,
    uint32_t counter,
    const char *offer_sdp,
    char *answer_sdp,
    size_t answer_capacity
) {
    if (!offer_sdp || !answer_sdp || answer_capacity < 1024 || boot_nonce == 0 || counter == 0 ||
        strlen(offer_sdp) < 128 || strlen(offer_sdp) > MEDIA_RESPONSE_MAX) {
        return ESP_ERR_INVALID_ARG;
    }
    answer_sdp[0] = '\0';
    snowball_provisioning_status_t status_copy = provisioning_status();
    if (!status_copy.configured || status_copy.enrollment_pending || !status_copy.wifi_connected) {
        return ESP_ERR_INVALID_STATE;
    }

    char gateway[16] = {0};
    char ca_sha256[65] = {0};
    uint16_t http_port = 0;
    uint16_t https_port = 0;
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs);
    if (result != ESP_OK) return result;
    size_t gateway_length = sizeof(gateway);
    size_t pin_length = sizeof(ca_sha256);
    if (nvs_get_str(nvs, "gateway", gateway, &gateway_length) != ESP_OK ||
        nvs_get_u16(nvs, "http_port", &http_port) != ESP_OK ||
        nvs_get_u16(nvs, "port", &https_port) != ESP_OK ||
        nvs_get_str(nvs, "ca_sha256", ca_sha256, &pin_length) != ESP_OK) {
        nvs_close(nvs);
        return ESP_ERR_NOT_FOUND;
    }
    nvs_close(nvs);

    char *certificate_pem = calloc(1, CA_PEM_MAX);
    char *response = calloc(1, MEDIA_RESPONSE_MAX);
    char signature[160] = {0};
    if (!certificate_pem || !response) {
        free(certificate_pem);
        free(response);
        return ESP_ERR_NO_MEM;
    }
    bool network_locked = network_lock_take();
    if (!network_locked) {
        result = ESP_ERR_INVALID_STATE;
        goto cleanup_media;
    }
    result = fetch_pinned_ca(gateway, http_port, ca_sha256, certificate_pem, CA_PEM_MAX);
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "Gateway media endpoint unavailable or CA pin rejected: %s", esp_err_to_name(result));
        goto cleanup_media;
    }

    result = device_identity_sign_media_offer(
        &device_identity, boot_nonce, counter, offer_sdp, signature, sizeof(signature)
    );
    if (result != ESP_OK) goto cleanup_media;

    cJSON *request = cJSON_CreateObject();
    if (!request) {
        result = ESP_ERR_NO_MEM;
        goto cleanup_media;
    }
    cJSON_AddNumberToObject(request, "version", 1);
    cJSON_AddStringToObject(request, "hardwareId", device_identity.hardware_id);
    cJSON_AddStringToObject(request, "publicKeyFingerprint", device_identity.fingerprint);
    cJSON_AddNumberToObject(request, "bootNonce", boot_nonce);
    cJSON_AddNumberToObject(request, "counter", counter);
    cJSON_AddStringToObject(request, "type", "offer");
    cJSON_AddStringToObject(request, "sdp", offer_sdp);
    cJSON_AddStringToObject(request, "signature", signature);
    char *request_body = cJSON_PrintUnformatted(request);
    cJSON_Delete(request);
    secure_zero(signature, sizeof(signature));
    if (!request_body) {
        result = ESP_ERR_NO_MEM;
        goto cleanup_media;
    }

    http_capture_t capture = {.buffer = response, .capacity = MEDIA_RESPONSE_MAX};
    char url[128];
    int url_length = snprintf(url, sizeof(url), "https://%s:%u/api/device/webrtc/offer", gateway, https_port);
    if (url_length <= 0 || (size_t)url_length >= sizeof(url)) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        result = ESP_ERR_INVALID_SIZE;
        goto cleanup_media;
    }
    esp_http_client_config_t config = {
        .url = url,
        .cert_pem = certificate_pem,
        .event_handler = capture_http_data,
        .user_data = &capture,
        .timeout_ms = 12000,
        .buffer_size = 2048,
        .buffer_size_tx = 2048,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (!client) {
        secure_zero(request_body, strlen(request_body));
        cJSON_free(request_body);
        result = ESP_ERR_NO_MEM;
        goto cleanup_media;
    }
    esp_http_client_set_method(client, HTTP_METHOD_POST);
    esp_http_client_set_header(client, "Content-Type", "application/json");
    esp_http_client_set_post_field(client, request_body, (int)strlen(request_body));
    result = esp_http_client_perform(client);
    int response_status = esp_http_client_get_status_code(client);
    esp_http_client_cleanup(client);
    secure_zero(request_body, strlen(request_body));
    cJSON_free(request_body);
    if (result != ESP_OK || response_status != 200 || capture.overflow) {
        result = ESP_ERR_INVALID_RESPONSE;
        goto cleanup_media;
    }

    cJSON *response_json = cJSON_Parse(response);
    const cJSON *version = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "version") : NULL;
    const cJSON *type = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "type") : NULL;
    const cJSON *sdp = response_json ? cJSON_GetObjectItemCaseSensitive(response_json, "sdp") : NULL;
    size_t answer_length = cJSON_IsString(sdp) ? strlen(sdp->valuestring) : 0;
    if (!cJSON_IsNumber(version) || version->valueint != 1 || !cJSON_IsString(type) ||
        strcmp(type->valuestring, "answer") != 0 || answer_length < 128 || answer_length >= answer_capacity) {
        result = ESP_ERR_INVALID_RESPONSE;
    } else {
        memcpy(answer_sdp, sdp->valuestring, answer_length + 1);
        result = ESP_OK;
    }
    cJSON_Delete(response_json);

cleanup_media:
    secure_zero(signature, sizeof(signature));
    secure_zero(certificate_pem, CA_PEM_MAX);
    secure_zero(response, MEDIA_RESPONSE_MAX);
    free(certificate_pem);
    free(response);
    if (network_locked) network_lock_give();
    return result;
}

static void event_task(void *argument) {
    (void)argument;
    diagnostics_memory_snapshot("event_task_ready", 0);
    snowball_device_event_t event;
    while (true) {
        if (xQueueReceive(event_queue, &event, portMAX_DELAY) == pdTRUE) {
            snowball_delivery_result_t delivery_result = SNOWBALL_DELIVERY_FAILED;
            char delivery_detail[64] = {0};
            esp_err_t result = submit_device_event(&event, &delivery_result, delivery_detail, sizeof(delivery_detail));
            if (result != ESP_OK) {
                if (delivery_result != SNOWBALL_DELIVERY_UNPAIRED) {
                    delivery_result = SNOWBALL_DELIVERY_FAILED;
                    if (!delivery_detail[0]) {
                        strlcpy(delivery_detail, esp_err_to_name(result), sizeof(delivery_detail));
                    }
                }
                ESP_LOGW(
                    TAG,
                    "device event delivery failed: %s (%s)",
                    esp_err_to_name(result),
                    delivery_detail[0] ? delivery_detail : "no_detail"
                );
            }
            if (delivery_callback) delivery_callback(&event, delivery_result, delivery_detail);
            secure_zero(&event, sizeof(event));
        }
    }
}

void provisioning_set_delivery_callback(snowball_delivery_callback_t callback) {
	delivery_callback = callback;
}

static void candidate_sync_task(void *argument) {
	(void)argument;
	/* Do not perform a throw-away CA request here.  The event worker is the
	 * single owner of authenticated Gateway HTTP and will fetch the CA once for
	 * the signed sync envelope.  The old probe-then-submit sequence created two
	 * clients back-to-back during audio startup and was the reproducible source
	 * of the ESP32-S3 HTTP-client null dereference. */
	vTaskDelay(pdMS_TO_TICKS(10000));
	bool queued = false;
	for (int attempt = 0; attempt < 24; ++attempt) {
		snowball_provisioning_status_t status = status_copy();
		esp_netif_t *sta = esp_netif_get_handle_from_ifkey("WIFI_STA_DEF");
		esp_netif_ip_info_t ip_info = {0};
		bool route_ready = status.configured && !status.enrollment_pending &&
			status.wifi_connected && sta &&
			esp_netif_get_ip_info(sta, &ip_info) == ESP_OK && ip_info.ip.addr != 0;
		if (route_ready) {
			snowball_device_event_t event = {
				.boot_nonce = candidate_sync_boot_nonce,
				.counter = candidate_sync_counter,
				.confidence = 0.0f,
			};
			strlcpy(event.event, "sync", sizeof(event.event));
			if (xQueueSend(event_queue, &event, 0) != pdTRUE) {
				ESP_LOGW(TAG, "candidate sync queue is full");
			} else {
				queued = true;
			}
			break;
		}
		/* Keep an offline board from entering the event worker until DHCP has
		 * supplied an IPv4 route.  Actual Gateway reachability is decided by the
		 * pinned request in submit_device_event, not by a subnet comparison. */
		vTaskDelay(pdMS_TO_TICKS(5000));
	}
	if (!queued) {
		ESP_LOGW(TAG, "candidate sync skipped while the Gateway was unreachable");
	}
	candidate_sync_task_running = false;
	vTaskDelete(NULL);
}

void provisioning_request_candidate_sync(uint32_t boot_nonce, uint32_t counter) {
	if (!event_queue || boot_nonce == 0 || counter == 0 || candidate_sync_task_running) return;
	candidate_sync_boot_nonce = boot_nonce;
	candidate_sync_counter = counter;
	candidate_sync_task_running = true;
	if (xTaskCreate(candidate_sync_task, "candidate_sync", 6144, NULL, 3, NULL) != pdPASS) {
		candidate_sync_task_running = false;
		ESP_LOGW(TAG, "candidate sync task could not start");
	}
}

esp_err_t provisioning_queue_event(const snowball_device_event_t *event) {
    if (!event_queue || !event || event->boot_nonce == 0 || event->counter == 0 ||
        strlen(event->event) >= sizeof(event->event) || strlen(event->wake) >= sizeof(event->wake) ||
        strlen(event->command) >= sizeof(event->command) || strlen(event->target) >= sizeof(event->target) ||
        strlen(event->name) >= sizeof(event->name) || event->confidence < 0.0f || event->confidence > 1.0f) {
        return ESP_ERR_INVALID_ARG;
    }
    return xQueueSend(event_queue, event, 0) == pdTRUE ? ESP_OK : ESP_ERR_TIMEOUT;
}

static esp_err_t mark_enrollment_complete(void) {
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(PROVISION_NAMESPACE, NVS_READWRITE, &nvs);
    if (result != ESP_OK) return result;
    result = nvs_erase_key(nvs, "enroll_token");
    if (result == ESP_OK || result == ESP_ERR_NVS_NOT_FOUND) {
        result = nvs_set_u8(nvs, "pending", 0);
    }
    if (result == ESP_OK) result = nvs_commit(nvs);
    nvs_close(nvs);
    if (result == ESP_OK) {
        xSemaphoreTake(status_mutex, portMAX_DELAY);
        current_status.enrollment_pending = false;
        xSemaphoreGive(status_mutex);
    }
    return result;
}

static void enrollment_task(void *argument) {
    (void)argument;
    diagnostics_memory_snapshot("enrollment_task_start", 0);
    char gateway[16] = {0};
    char ca_sha256[65] = {0};
    char token[ENROLLMENT_TOKEN_MAX + 1] = {0};
    uint16_t http_port = 0;
    uint16_t https_port = 0;
    size_t gateway_length = sizeof(gateway);
    size_t pin_length = sizeof(ca_sha256);
    size_t token_length = sizeof(token);
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs);
    if (result == ESP_OK) {
        if (nvs_get_str(nvs, "gateway", gateway, &gateway_length) != ESP_OK ||
            nvs_get_u16(nvs, "http_port", &http_port) != ESP_OK ||
            nvs_get_u16(nvs, "port", &https_port) != ESP_OK ||
            nvs_get_str(nvs, "ca_sha256", ca_sha256, &pin_length) != ESP_OK ||
            nvs_get_str(nvs, "enroll_token", token, &token_length) != ESP_OK) {
            result = ESP_ERR_NOT_FOUND;
        }
        nvs_close(nvs);
    }
    char *certificate_pem = calloc(1, CA_PEM_MAX);
    if (result == ESP_OK && !certificate_pem) result = ESP_ERR_NO_MEM;
    for (int attempt = 0; result == ESP_OK && attempt < 5; ++attempt) {
        bool network_locked = network_lock_take();
        if (!network_locked) {
            result = ESP_ERR_INVALID_STATE;
        } else {
            result = fetch_pinned_ca(gateway, http_port, ca_sha256, certificate_pem, CA_PEM_MAX);
            if (result == ESP_OK) result = submit_enrollment(gateway, https_port, certificate_pem, token);
            if (result == ESP_OK) result = mark_enrollment_complete();
            network_lock_give();
        }
        if (result != ESP_OK && attempt < 4) {
            result = ESP_OK;
            vTaskDelay(pdMS_TO_TICKS(3000));
        }
    }
    if (certificate_pem) {
        secure_zero(certificate_pem, CA_PEM_MAX);
        free(certificate_pem);
    }
    secure_zero(token, sizeof(token));
    if (!provisioning_status().enrollment_pending) {
        ESP_LOGI(TAG, "Gateway enrollment completed with pinned TLS and device-key proof");
    } else {
        ESP_LOGW(TAG, "Gateway enrollment remains pending");
    }
    enrollment_task_running = false;
    vTaskDelete(NULL);
}

static void wifi_event(void *argument, esp_event_base_t base, int32_t event_id, void *data) {
    (void)argument;
    (void)data;
    if (base == IP_EVENT && event_id == IP_EVENT_STA_GOT_IP) {
        xSemaphoreTake(status_mutex, portMAX_DELAY);
        current_status.wifi_connected = true;
        current_status.gateway_reachable = false;
        xSemaphoreGive(status_mutex);
        ESP_LOGI(TAG, "Wi-Fi connected");
        if (status_copy().enrollment_pending && !enrollment_task_running) {
            enrollment_task_running = true;
            if (xTaskCreate(enrollment_task, "device_enroll", 8192, NULL, 5, NULL) != pdPASS) {
                enrollment_task_running = false;
                ESP_LOGW(TAG, "Gateway enrollment task could not start");
            }
        }
    } else if (base == WIFI_EVENT && event_id == WIFI_EVENT_STA_DISCONNECTED) {
        xSemaphoreTake(status_mutex, portMAX_DELAY);
        current_status.wifi_connected = false;
        current_status.gateway_reachable = false;
        xSemaphoreGive(status_mutex);
        if (status_copy().configured) {
            esp_wifi_connect();
        }
    }
}

static void connect_stored_wifi(void) {
    char ssid[33] = {0};
    char password[64] = {0};
    size_t ssid_length = sizeof(ssid);
    size_t password_length = sizeof(password);
    nvs_handle_t nvs;
    if (nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs) != ESP_OK) return;
    esp_err_t ssid_result = nvs_get_str(nvs, "ssid", ssid, &ssid_length);
    esp_err_t password_result = nvs_get_str(nvs, "password", password, &password_length);
    nvs_close(nvs);
    if (ssid_result == ESP_OK && password_result == ESP_OK) {
        if (wifi_connect(ssid, password) != ESP_OK) {
            ESP_LOGW(TAG, "stored Wi-Fi connection could not be started");
        }
    }
    memset(password, 0, sizeof(password));
}

static void load_redacted_status(void) {
    nvs_handle_t nvs;
    if (nvs_open(PROVISION_NAMESPACE, NVS_READONLY, &nvs) != ESP_OK) return;
    size_t gateway_length = sizeof(current_status.gateway);
    uint8_t pending = 0;
    if (nvs_get_str(nvs, "gateway", current_status.gateway, &gateway_length) == ESP_OK &&
        nvs_get_u16(nvs, "http_port", &current_status.gateway_http_port) == ESP_OK &&
        nvs_get_u16(nvs, "port", &current_status.gateway_port) == ESP_OK) {
        current_status.configured = true;
    }
    if (nvs_get_u8(nvs, "pending", &pending) == ESP_OK) {
        current_status.enrollment_pending = pending == 1;
    }
    nvs_close(nvs);
}

esp_err_t provisioning_start(const snowball_device_identity_t *identity) {
    if (!identity) return ESP_ERR_INVALID_ARG;
    device_identity = *identity;
    status_mutex = xSemaphoreCreateMutex();
    if (!status_mutex) return ESP_ERR_NO_MEM;
    network_mutex = xSemaphoreCreateMutex();
    if (!network_mutex) return ESP_ERR_NO_MEM;
    event_queue = xQueueCreate(8, sizeof(snowball_device_event_t));
    if (!event_queue) return ESP_ERR_NO_MEM;
    load_redacted_status();
    ESP_RETURN_ON_ERROR(esp_netif_init(), TAG, "initialize network interfaces");
    esp_err_t event_result = esp_event_loop_create_default();
    if (event_result != ESP_OK && event_result != ESP_ERR_INVALID_STATE) return event_result;
    if (!esp_netif_get_handle_from_ifkey("WIFI_STA_DEF")) {
        esp_netif_create_default_wifi_sta();
    }
    wifi_init_config_t wifi_config = WIFI_INIT_CONFIG_DEFAULT();
    ESP_RETURN_ON_ERROR(esp_wifi_init(&wifi_config), TAG, "initialize Wi-Fi");
    ESP_RETURN_ON_ERROR(esp_wifi_set_storage(WIFI_STORAGE_RAM), TAG, "set volatile Wi-Fi driver storage");
    ESP_RETURN_ON_ERROR(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, wifi_event, NULL), TAG, "register Wi-Fi event");
    ESP_RETURN_ON_ERROR(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, wifi_event, NULL), TAG, "register IP event");
    if (current_status.configured) {
        connect_stored_wifi();
    }
    if (xTaskCreate(serial_task, "usb_serial", 6144, NULL, 6, NULL) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    /* The signed event path performs mbedTLS key parsing plus HTTPS client
     * setup. Keep that call chain away from the audio task stacks; the prior
     * 8K-word stack was overwritten before the first event could reach HTTPS. */
    if (xTaskCreate(event_task, "device_events", 16384, NULL, 4, NULL) != pdPASS) {
        return ESP_ERR_NO_MEM;
    }
    ESP_LOGI(TAG, "Web Serial provisioning protocol v%d ready", PROTOCOL_VERSION);
    return ESP_OK;
}
