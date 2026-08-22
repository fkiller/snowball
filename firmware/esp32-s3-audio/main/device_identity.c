#include "device_identity.h"

#include <stdio.h>
#include <inttypes.h>
#include <string.h>

#include "esp_check.h"
#include "esp_mac.h"
#include "esp_random.h"
#include "mbedtls/ecp.h"
#include "mbedtls/base64.h"
#include "mbedtls/md.h"
#include "mbedtls/pk.h"
#include "mbedtls/sha256.h"
#include "nvs.h"

#define IDENTITY_NAMESPACE "identity"
#define PRIVATE_KEY_NAME "private_der"
#define PUBLIC_KEY_NAME "public_pem"
#define BOOT_SEQUENCE_NAME "boot_seq"
#define PRIVATE_DER_MAX 256

static int hardware_random(void *context, unsigned char *output, size_t length) {
    (void)context;
    esp_fill_random(output, length);
    return 0;
}

static esp_err_t generate_identity(nvs_handle_t nvs, char *public_pem, size_t public_pem_size) {
    esp_err_t result = ESP_FAIL;
    mbedtls_pk_context key;
    mbedtls_pk_init(&key);
    if (mbedtls_pk_setup(&key, mbedtls_pk_info_from_type(MBEDTLS_PK_ECKEY)) != 0 ||
        mbedtls_ecp_gen_key(MBEDTLS_ECP_DP_SECP256R1, mbedtls_pk_ec(key), hardware_random, NULL) != 0) {
        goto cleanup;
    }

    unsigned char private_der[PRIVATE_DER_MAX] = {0};
    int private_length = mbedtls_pk_write_key_der(&key, private_der, sizeof(private_der));
    if (private_length <= 0 || (size_t)private_length > sizeof(private_der)) {
        goto cleanup;
    }
    if (mbedtls_pk_write_pubkey_pem(&key, (unsigned char *)public_pem, public_pem_size) != 0) {
        goto cleanup;
    }
    const unsigned char *private_start = private_der + sizeof(private_der) - private_length;
    result = nvs_set_blob(nvs, PRIVATE_KEY_NAME, private_start, (size_t)private_length);
    if (result == ESP_OK) {
        result = nvs_set_str(nvs, PUBLIC_KEY_NAME, public_pem);
    }
    if (result == ESP_OK) {
        result = nvs_commit(nvs);
    }
    memset(private_der, 0, sizeof(private_der));

cleanup:
    mbedtls_pk_free(&key);
    return result;
}

esp_err_t device_identity_load_or_create(snowball_device_identity_t *identity) {
    if (!identity) {
        return ESP_ERR_INVALID_ARG;
    }
    memset(identity, 0, sizeof(*identity));
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(IDENTITY_NAMESPACE, NVS_READWRITE, &nvs);
    if (result != ESP_OK) {
        return result;
    }
    size_t public_length = sizeof(identity->public_key_pem);
    result = nvs_get_str(nvs, PUBLIC_KEY_NAME, identity->public_key_pem, &public_length);
    if (result == ESP_ERR_NVS_NOT_FOUND) {
        result = generate_identity(nvs, identity->public_key_pem, sizeof(identity->public_key_pem));
    }
    nvs_close(nvs);
    if (result != ESP_OK) {
        return result;
    }

    unsigned char digest[32];
    if (mbedtls_sha256((const unsigned char *)identity->public_key_pem, strlen(identity->public_key_pem), digest, 0) != 0) {
        return ESP_FAIL;
    }
    for (size_t index = 0; index < sizeof(digest); ++index) {
        snprintf(identity->fingerprint + (index * 2), 3, "%02x", digest[index]);
    }
    uint8_t mac[6];
    ESP_RETURN_ON_ERROR(esp_read_mac(mac, ESP_MAC_WIFI_STA), "snowball/identity", "read station MAC");
    snprintf(
        identity->hardware_id,
        sizeof(identity->hardware_id),
        "%02x:%02x:%02x:%02x:%02x:%02x",
        mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]
    );
    return ESP_OK;
}

esp_err_t device_identity_next_boot_sequence(uint32_t *sequence) {
    if (!sequence) return ESP_ERR_INVALID_ARG;
    nvs_handle_t nvs;
    esp_err_t result = nvs_open(IDENTITY_NAMESPACE, NVS_READWRITE, &nvs);
    if (result != ESP_OK) return result;
    uint32_t previous = 0;
    result = nvs_get_u32(nvs, BOOT_SEQUENCE_NAME, &previous);
    if (result == ESP_ERR_NVS_NOT_FOUND) result = ESP_OK;
    if (result == ESP_OK && previous == UINT32_MAX) result = ESP_ERR_INVALID_STATE;
    if (result == ESP_OK) result = nvs_set_u32(nvs, BOOT_SEQUENCE_NAME, previous + 1);
    if (result == ESP_OK) result = nvs_commit(nvs);
    nvs_close(nvs);
    if (result == ESP_OK) *sequence = previous + 1;
    return result;
}

esp_err_t device_identity_sign_enrollment(
    const snowball_device_identity_t *identity,
    const char *enrollment_token,
    const char *nonce,
    char *signature_base64url,
    size_t signature_size
) {
    if (!identity || !enrollment_token || !nonce || !signature_base64url || signature_size < 128) {
        return ESP_ERR_INVALID_ARG;
    }
    char message[768];
    int message_length = snprintf(
        message,
        sizeof(message),
        "snowball-enroll-v1\n%s\n%s\n%s\n%s",
        enrollment_token,
        identity->hardware_id,
        identity->fingerprint,
        nonce
    );
    if (message_length <= 0 || (size_t)message_length >= sizeof(message)) {
        return ESP_ERR_INVALID_SIZE;
    }

    nvs_handle_t nvs;
    esp_err_t result = nvs_open(IDENTITY_NAMESPACE, NVS_READONLY, &nvs);
    if (result != ESP_OK) return result;
    unsigned char private_der[PRIVATE_DER_MAX] = {0};
    size_t private_length = sizeof(private_der);
    result = nvs_get_blob(nvs, PRIVATE_KEY_NAME, private_der, &private_length);
    nvs_close(nvs);
    if (result != ESP_OK) return result;

    unsigned char digest[32];
    unsigned char signature[MBEDTLS_PK_SIGNATURE_MAX_SIZE] = {0};
    unsigned char encoded[160] = {0};
    size_t signature_length = 0;
    size_t encoded_length = 0;
    mbedtls_pk_context key;
    mbedtls_pk_init(&key);
    int crypto_result = mbedtls_sha256((const unsigned char *)message, (size_t)message_length, digest, 0);
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_parse_key(&key, private_der, private_length, NULL, 0, hardware_random, NULL);
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_sign(
            &key,
            MBEDTLS_MD_SHA256,
            digest,
            sizeof(digest),
            signature,
            sizeof(signature),
            &signature_length,
            hardware_random,
            NULL
        );
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_base64_encode(encoded, sizeof(encoded), &encoded_length, signature, signature_length);
    }
    if (crypto_result == 0 && encoded_length + 1 <= signature_size) {
        for (size_t index = 0; index < encoded_length; ++index) {
            if (encoded[index] == '+') encoded[index] = '-';
            if (encoded[index] == '/') encoded[index] = '_';
        }
        while (encoded_length > 0 && encoded[encoded_length - 1] == '=') --encoded_length;
        memcpy(signature_base64url, encoded, encoded_length);
        signature_base64url[encoded_length] = '\0';
        result = ESP_OK;
    } else {
        result = ESP_FAIL;
    }
    mbedtls_pk_free(&key);
    memset(private_der, 0, sizeof(private_der));
    memset(message, 0, sizeof(message));
    memset(digest, 0, sizeof(digest));
    memset(signature, 0, sizeof(signature));
    memset(encoded, 0, sizeof(encoded));
    return result;
}

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
) {
    if (!identity || !event || !wake || !command || !target || !name || !signature_base64url || signature_size < 128 ||
        boot_nonce == 0 || counter == 0 || confidence < 0.0f || confidence > 1.0f) {
        return ESP_ERR_INVALID_ARG;
    }
    char message[768];
    int message_length = snprintf(
        message,
        sizeof(message),
        "snowball-device-event-v1\n%s\n%s\n%" PRIu32 "\n%" PRIu32 "\n%s\n%s\n%s\n%s\n%s\n%.6f",
        identity->hardware_id,
        identity->fingerprint,
        boot_nonce,
        counter,
        event,
        wake,
        command,
        target,
        name,
        confidence
    );
    if (message_length <= 0 || (size_t)message_length >= sizeof(message)) {
        return ESP_ERR_INVALID_SIZE;
    }

    nvs_handle_t nvs;
    esp_err_t result = nvs_open(IDENTITY_NAMESPACE, NVS_READONLY, &nvs);
    if (result != ESP_OK) return result;
    unsigned char private_der[PRIVATE_DER_MAX] = {0};
    size_t private_length = sizeof(private_der);
    result = nvs_get_blob(nvs, PRIVATE_KEY_NAME, private_der, &private_length);
    nvs_close(nvs);

    unsigned char digest[32] = {0};
    unsigned char signature[MBEDTLS_PK_SIGNATURE_MAX_SIZE] = {0};
    unsigned char encoded[160] = {0};
    size_t signature_length = 0;
    size_t encoded_length = 0;
    mbedtls_pk_context key;
    mbedtls_pk_init(&key);
    int crypto_result = result == ESP_OK
        ? mbedtls_sha256((const unsigned char *)message, (size_t)message_length, digest, 0)
        : -1;
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_parse_key(&key, private_der, private_length, NULL, 0, hardware_random, NULL);
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_sign(
            &key, MBEDTLS_MD_SHA256, digest, sizeof(digest), signature, sizeof(signature),
            &signature_length, hardware_random, NULL
        );
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_base64_encode(encoded, sizeof(encoded), &encoded_length, signature, signature_length);
    }
    if (crypto_result == 0 && encoded_length + 1 <= signature_size) {
        for (size_t index = 0; index < encoded_length; ++index) {
            if (encoded[index] == '+') encoded[index] = '-';
            if (encoded[index] == '/') encoded[index] = '_';
        }
        while (encoded_length > 0 && encoded[encoded_length - 1] == '=') --encoded_length;
        memcpy(signature_base64url, encoded, encoded_length);
        signature_base64url[encoded_length] = '\0';
        result = ESP_OK;
    } else {
        result = ESP_FAIL;
    }
    mbedtls_pk_free(&key);
    memset(private_der, 0, sizeof(private_der));
    memset(message, 0, sizeof(message));
    memset(digest, 0, sizeof(digest));
    memset(signature, 0, sizeof(signature));
    memset(encoded, 0, sizeof(encoded));
    return result;
}

esp_err_t device_identity_sign_media_offer(
    const snowball_device_identity_t *identity,
    uint32_t boot_nonce,
    uint32_t counter,
    const char *sdp,
    char *signature_base64url,
    size_t signature_size
) {
    if (!identity || !sdp || !signature_base64url || signature_size < 128 ||
        boot_nonce == 0 || counter == 0 || strlen(sdp) < 128 || strlen(sdp) > (64U << 10)) {
        return ESP_ERR_INVALID_ARG;
    }
    unsigned char sdp_digest[32] = {0};
    mbedtls_sha256_context digest_context;
    mbedtls_sha256_init(&digest_context);
    int crypto_result = mbedtls_sha256_starts(&digest_context, 0);
    if (crypto_result == 0) crypto_result = mbedtls_sha256_update(&digest_context, (const unsigned char *)"offer\n", 6);
    if (crypto_result == 0) crypto_result = mbedtls_sha256_update(&digest_context, (const unsigned char *)sdp, strlen(sdp));
    if (crypto_result == 0) crypto_result = mbedtls_sha256_finish(&digest_context, sdp_digest);
    mbedtls_sha256_free(&digest_context);
    if (crypto_result != 0) return ESP_FAIL;

    char sdp_digest_hex[65] = {0};
    for (size_t index = 0; index < sizeof(sdp_digest); ++index) {
        snprintf(sdp_digest_hex + (index * 2), 3, "%02x", sdp_digest[index]);
    }
    char message[256] = {0};
    int message_length = snprintf(
        message,
        sizeof(message),
        "snowball-device-media-v1\n%s\n%s\n%" PRIu32 "\n%" PRIu32 "\n%s",
        identity->hardware_id,
        identity->fingerprint,
        boot_nonce,
        counter,
        sdp_digest_hex
    );
    if (message_length <= 0 || (size_t)message_length >= sizeof(message)) {
        memset(sdp_digest, 0, sizeof(sdp_digest));
        memset(sdp_digest_hex, 0, sizeof(sdp_digest_hex));
        return ESP_ERR_INVALID_SIZE;
    }

    nvs_handle_t nvs;
    esp_err_t result = nvs_open(IDENTITY_NAMESPACE, NVS_READONLY, &nvs);
    if (result != ESP_OK) return result;
    unsigned char private_der[PRIVATE_DER_MAX] = {0};
    size_t private_length = sizeof(private_der);
    result = nvs_get_blob(nvs, PRIVATE_KEY_NAME, private_der, &private_length);
    nvs_close(nvs);

    unsigned char digest[32] = {0};
    unsigned char signature[MBEDTLS_PK_SIGNATURE_MAX_SIZE] = {0};
    unsigned char encoded[160] = {0};
    size_t signature_length = 0;
    size_t encoded_length = 0;
    mbedtls_pk_context key;
    mbedtls_pk_init(&key);
    crypto_result = result == ESP_OK
        ? mbedtls_sha256((const unsigned char *)message, (size_t)message_length, digest, 0)
        : -1;
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_parse_key(&key, private_der, private_length, NULL, 0, hardware_random, NULL);
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_pk_sign(
            &key, MBEDTLS_MD_SHA256, digest, sizeof(digest), signature, sizeof(signature),
            &signature_length, hardware_random, NULL
        );
    }
    if (crypto_result == 0) {
        crypto_result = mbedtls_base64_encode(encoded, sizeof(encoded), &encoded_length, signature, signature_length);
    }
    if (crypto_result == 0 && encoded_length + 1 <= signature_size) {
        for (size_t index = 0; index < encoded_length; ++index) {
            if (encoded[index] == '+') encoded[index] = '-';
            if (encoded[index] == '/') encoded[index] = '_';
        }
        while (encoded_length > 0 && encoded[encoded_length - 1] == '=') --encoded_length;
        memcpy(signature_base64url, encoded, encoded_length);
        signature_base64url[encoded_length] = '\0';
        result = ESP_OK;
    } else {
        result = ESP_FAIL;
    }
    mbedtls_pk_free(&key);
    memset(private_der, 0, sizeof(private_der));
    memset(message, 0, sizeof(message));
    memset(sdp_digest, 0, sizeof(sdp_digest));
    memset(sdp_digest_hex, 0, sizeof(sdp_digest_hex));
    memset(digest, 0, sizeof(digest));
    memset(signature, 0, sizeof(signature));
    memset(encoded, 0, sizeof(encoded));
    return result;
}
