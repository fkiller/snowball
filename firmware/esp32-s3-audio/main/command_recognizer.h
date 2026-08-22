#pragma once

#include <stddef.h>
#include <stdint.h>

#include "esp_err.h"
#include "model_path.h"

typedef enum {
    SNOWBALL_COMMAND_NEW_CHAT = 0,
    SNOWBALL_COMMAND_RESUME,
    SNOWBALL_COMMAND_VOICE,
    SNOWBALL_COMMAND_PROJECT,
} snowball_command_kind_t;

typedef enum {
    SNOWBALL_COMMAND_TARGET_CHATGPT = 0,
    SNOWBALL_COMMAND_TARGET_CODEX,
} snowball_command_target_t;

typedef struct {
    snowball_command_kind_t kind;
    snowball_command_target_t target;
    float confidence;
    char name[64];
} snowball_command_result_t;

typedef enum {
    SNOWBALL_COMMAND_DETECTING = 0,
    SNOWBALL_COMMAND_DETECTED,
    SNOWBALL_COMMAND_TIMEOUT,
} snowball_command_state_t;

esp_err_t command_recognizer_init(srmodel_list_t *models, size_t afe_chunk_samples);
/* Store the authenticated Gateway candidate catalog for the next command
 * grammar build. Production voice/project names are never compiled into the
 * firmware; the catalog is validated before it is persisted in NVS. */
esp_err_t command_recognizer_store_catalog(const char *catalog_json);
/* Rebuild the bounded MultiNet grammar immediately after a fresh authenticated
 * catalog is stored. This avoids making the user power-cycle the board before
 * the first project/voice command becomes available. */
esp_err_t command_recognizer_rebuild(void);
void command_recognizer_begin(void);
snowball_command_state_t command_recognizer_feed(
    const int16_t *samples,
    snowball_command_result_t *result
);
