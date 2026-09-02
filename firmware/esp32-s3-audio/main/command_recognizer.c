#include "command_recognizer.h"

#include <stdbool.h>
#include <inttypes.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>

#include "cJSON.h"
#include "esp_check.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_mn_iface.h"
#include "esp_mn_models.h"
#include "esp_mn_speech_commands.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "nvs.h"

/* A short timeout makes a bare "Hi ESP" resolve to NEW_CHAT without requiring
 * a second phrase. MultiNet receives the very same AFE stream immediately
 * after WakeNet fires, so "Hi ESP Resume" can be spoken as one continuous
 * phrase; a deliberate pause is not required. */
#define COMMAND_TAIL_TIMEOUT_MS 1800
#define CANDIDATE_CATALOG_MAX 8192
#define CANDIDATE_NAME_MAX 64
#define CANDIDATE_COUNT_MAX 24
#define COMMAND_CAPACITY (1 + CANDIDATE_COUNT_MAX + (CANDIDATE_COUNT_MAX * 2))
#define CANDIDATE_NAMESPACE "snowball"
/* NVS keys are limited to 15 bytes, including no terminating NUL. */
#define CANDIDATE_CATALOG_KEY "cand_catalog"

enum { COMMAND_ID_RESUME = 1, COMMAND_ID_VOICE_BASE = 100, COMMAND_ID_PROJECT_BASE = 1000, COMMAND_ID_CODEX_PROJECT_BASE = 2000 };

typedef struct {
    int id;
    char phrase[128];
    snowball_command_kind_t kind;
    snowball_command_target_t target;
    char name[CANDIDATE_NAME_MAX];
} command_definition_t;

static char candidate_voices[CANDIDATE_COUNT_MAX][CANDIDATE_NAME_MAX];
static char candidate_projects[CANDIDATE_COUNT_MAX][CANDIDATE_NAME_MAX];
static size_t candidate_voice_count;
static size_t candidate_project_count;
static command_definition_t commands[COMMAND_CAPACITY];
static size_t command_count;

static const char *TAG = "snowball/command";
static esp_mn_iface_t *multinet;
static model_iface_data_t *model_data;
static bool listening;
static SemaphoreHandle_t command_mutex;
static bool grammar_allocated;
static uint32_t inference_frames;
static int64_t inference_started_us;

static void stop_listening_locked(const char *reason) {
    if (!listening) return;
    int64_t elapsed_ms = (esp_timer_get_time() - inference_started_us) / 1000;
    listening = false;
    ESP_LOGI(TAG, "[MULTINET] inference=stop reason=%s frames=%" PRIu32 " elapsed_ms=%" PRIi64,
             reason ? reason : "unspecified", inference_frames, elapsed_ms);
}

static bool safe_candidate_name(const char *value) {
    size_t length = value ? strlen(value) : 0;
    if (length < 2 || length >= CANDIDATE_NAME_MAX) return false;
    for (size_t index = 0; index < length; ++index) {
        unsigned char character = (unsigned char)value[index];
        if (character < 0x20 || character == 0x7f) return false;
    }
    return true;
}

static void reset_candidates(void) {
    candidate_voice_count = 0;
    candidate_project_count = 0;
    memset(candidate_voices, 0, sizeof(candidate_voices));
    memset(candidate_projects, 0, sizeof(candidate_projects));
}

static bool append_candidate(char target[CANDIDATE_COUNT_MAX][CANDIDATE_NAME_MAX], size_t *count, const char *value) {
    if (!safe_candidate_name(value) || *count >= CANDIDATE_COUNT_MAX) return false;
    for (size_t index = 0; index < *count; ++index) {
        if (strcasecmp(target[index], value) == 0) return true;
    }
    strlcpy(target[*count], value, CANDIDATE_NAME_MAX);
    ++*count;
    return true;
}

static bool parse_candidate_catalog(const char *catalog_json) {
    if (!catalog_json || strlen(catalog_json) == 0 || strlen(catalog_json) >= CANDIDATE_CATALOG_MAX) return false;
    cJSON *catalog = cJSON_ParseWithLength(catalog_json, strlen(catalog_json));
    const cJSON *version = catalog ? cJSON_GetObjectItemCaseSensitive(catalog, "version") : NULL;
    const cJSON *source = catalog ? cJSON_GetObjectItemCaseSensitive(catalog, "source") : NULL;
    const cJSON *authenticated = catalog ? cJSON_GetObjectItemCaseSensitive(catalog, "authenticated") : NULL;
    const cJSON *voices = catalog ? cJSON_GetObjectItemCaseSensitive(catalog, "voices") : NULL;
    const cJSON *projects = catalog ? cJSON_GetObjectItemCaseSensitive(catalog, "projects") : NULL;
    bool valid = cJSON_IsNumber(version) && version->valueint == 1 && cJSON_IsString(source) &&
        strcmp(source->valuestring, "chatgpt-web") == 0 && cJSON_IsTrue(authenticated) &&
        cJSON_IsArray(voices) && cJSON_IsArray(projects);
    if (valid) {
        reset_candidates();
        size_t voice_count = 0;
        for (const cJSON *item = voices->child; item && voice_count <= CANDIDATE_COUNT_MAX; item = item->next) {
            if (!cJSON_IsString(item) || !append_candidate(candidate_voices, &voice_count, item->valuestring)) {
                valid = false;
                break;
            }
        }
        candidate_voice_count = voice_count;
        size_t project_count = 0;
        for (const cJSON *item = projects->child; valid && item && project_count <= CANDIDATE_COUNT_MAX; item = item->next) {
            if (!cJSON_IsString(item) || !append_candidate(candidate_projects, &project_count, item->valuestring)) {
                valid = false;
                break;
            }
        }
        candidate_project_count = project_count;
        if (voice_count > CANDIDATE_COUNT_MAX || project_count > CANDIDATE_COUNT_MAX) valid = false;
    }
    cJSON_Delete(catalog);
    if (!valid) reset_candidates();
    return valid;
}

static bool load_candidate_catalog(void) {
    char *catalog_json = calloc(1, CANDIDATE_CATALOG_MAX);
    if (!catalog_json) return false;
    nvs_handle_t nvs = 0;
    esp_err_t result = nvs_open(CANDIDATE_NAMESPACE, NVS_READONLY, &nvs);
    size_t length = CANDIDATE_CATALOG_MAX;
    if (result == ESP_OK) result = nvs_get_str(nvs, CANDIDATE_CATALOG_KEY, catalog_json, &length);
    if (nvs) nvs_close(nvs);
    bool loaded = result == ESP_OK && parse_candidate_catalog(catalog_json);
    memset(catalog_json, 0, CANDIDATE_CATALOG_MAX);
    free(catalog_json);
    return loaded;
}

static void append_command(int id, const char *phrase, snowball_command_kind_t kind, snowball_command_target_t target, const char *name) {
    if (command_count >= COMMAND_CAPACITY || !phrase || !name) return;
    command_definition_t *command = &commands[command_count++];
    command->id = id;
    strlcpy(command->phrase, phrase, sizeof(command->phrase));
    command->kind = kind;
    command->target = target;
    strlcpy(command->name, name, sizeof(command->name));
}

static void build_commands(void) {
    command_count = 0;
    append_command(COMMAND_ID_RESUME, "resume", SNOWBALL_COMMAND_RESUME, SNOWBALL_COMMAND_TARGET_CHATGPT, "");
    for (size_t index = 0; index < candidate_voice_count; ++index) {
        char phrase[128];
        snprintf(phrase, sizeof(phrase), "with %s", candidate_voices[index]);
        append_command(COMMAND_ID_VOICE_BASE + (int)index, phrase, SNOWBALL_COMMAND_VOICE, SNOWBALL_COMMAND_TARGET_CHATGPT, candidate_voices[index]);
    }
    for (size_t index = 0; index < candidate_project_count; ++index) {
        char phrase[128];
        snprintf(phrase, sizeof(phrase), "project %s", candidate_projects[index]);
        append_command(COMMAND_ID_PROJECT_BASE + (int)index, phrase, SNOWBALL_COMMAND_PROJECT, SNOWBALL_COMMAND_TARGET_CHATGPT, candidate_projects[index]);
        snprintf(phrase, sizeof(phrase), "codex project %s", candidate_projects[index]);
        append_command(COMMAND_ID_CODEX_PROJECT_BASE + (int)index, phrase, SNOWBALL_COMMAND_PROJECT, SNOWBALL_COMMAND_TARGET_CODEX, candidate_projects[index]);
    }
}

static esp_err_t rebuild_commands_locked(void) {
    if (!multinet || !model_data) return ESP_ERR_INVALID_STATE;
    listening = false;
    if (grammar_allocated) {
        esp_mn_commands_free();
        grammar_allocated = false;
    }
    build_commands();
    esp_err_t result = esp_mn_commands_alloc(multinet, model_data);
    if (result != ESP_OK) return result;
    grammar_allocated = true;
    for (size_t index = 0; index < command_count; ++index) {
        result = esp_mn_commands_add(commands[index].id, commands[index].phrase);
        if (result != ESP_OK) return result;
    }
    esp_mn_error_t *errors = esp_mn_commands_update();
    if (errors && errors->num > 0) {
        ESP_LOGE(TAG, "%d command phrases could not be compiled", errors->num);
        return ESP_ERR_INVALID_ARG;
    }
    multinet->print_active_speech_commands(model_data);
    ESP_LOGI(TAG, "MultiNet command grammar rebuilt: %u phrases", (unsigned)command_count);
    return ESP_OK;
}

static const command_definition_t *find_command(int id) {
    for (size_t index = 0; index < command_count; ++index) {
        if (commands[index].id == id) return &commands[index];
    }
    return NULL;
}

esp_err_t command_recognizer_store_catalog(const char *catalog_json) {
    if (!command_mutex || xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) {
        return ESP_ERR_INVALID_STATE;
    }
    if (!parse_candidate_catalog(catalog_json)) {
        ESP_LOGW(TAG, "candidate catalog schema rejected");
        xSemaphoreGive(command_mutex);
        return ESP_ERR_INVALID_ARG;
    }
    nvs_handle_t nvs = 0;
    esp_err_t result = nvs_open(CANDIDATE_NAMESPACE, NVS_READWRITE, &nvs);
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "candidate catalog NVS open failed: %s", esp_err_to_name(result));
        (void)load_candidate_catalog();
        xSemaphoreGive(command_mutex);
        return result;
    }
    result = nvs_set_str(nvs, CANDIDATE_CATALOG_KEY, catalog_json);
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "candidate catalog NVS write failed: %s", esp_err_to_name(result));
    }
    if (result == ESP_OK) result = nvs_commit(nvs);
    if (result != ESP_OK) {
        ESP_LOGW(TAG, "candidate catalog NVS commit failed: %s", esp_err_to_name(result));
    }
    if (nvs) nvs_close(nvs);
    if (result == ESP_OK) {
        result = rebuild_commands_locked();
        if (result == ESP_OK) {
            ESP_LOGI(TAG, "candidate catalog stored; command grammar rebuilt without reboot");
        } else {
            ESP_LOGW(TAG, "candidate catalog stored but grammar rebuild failed: %s", esp_err_to_name(result));
        }
    } else {
        (void)load_candidate_catalog();
    }
    xSemaphoreGive(command_mutex);
    return result;
}

esp_err_t command_recognizer_rebuild(void) {
    if (!command_mutex || xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) {
        return ESP_ERR_INVALID_STATE;
    }
    esp_err_t result = rebuild_commands_locked();
    xSemaphoreGive(command_mutex);
    return result;
}

esp_err_t command_recognizer_init(srmodel_list_t *models, size_t afe_chunk_samples) {
    if (!models || afe_chunk_samples == 0) return ESP_ERR_INVALID_ARG;
    if (!command_mutex) {
        command_mutex = xSemaphoreCreateMutex();
        if (!command_mutex) return ESP_ERR_NO_MEM;
    }
    char *model_name = esp_srmodel_filter(models, ESP_MN_PREFIX, ESP_MN_ENGLISH);
    if (!model_name) {
        ESP_LOGE(TAG, "English MultiNet model is missing from the model partition");
        return ESP_ERR_NOT_FOUND;
    }
    multinet = esp_mn_handle_from_name(model_name);
    if (!multinet) return ESP_ERR_NOT_FOUND;
    model_data = multinet->create(model_name, COMMAND_TAIL_TIMEOUT_MS);
    if (!model_data) return ESP_ERR_NO_MEM;
    if ((size_t)multinet->get_samp_chunksize(model_data) != afe_chunk_samples) {
        ESP_LOGE(TAG, "MultiNet and AFE frame sizes do not match");
        return ESP_ERR_INVALID_SIZE;
    }
    reset_candidates();
    (void)load_candidate_catalog();
    build_commands();
    if (command_count == 1) {
        ESP_LOGW(TAG, "no Gateway candidate catalog is cached; only Resume is available until the next sync");
    }
    if (xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) return ESP_ERR_INVALID_STATE;
    esp_err_t grammar_result = rebuild_commands_locked();
    xSemaphoreGive(command_mutex);
    ESP_RETURN_ON_ERROR(grammar_result, TAG, "build command grammar");
    ESP_LOGI(TAG, "MultiNet command tail ready: %u phrases, %d ms bare-wake timeout",
             (unsigned)command_count, COMMAND_TAIL_TIMEOUT_MS);
    return ESP_OK;
}

void command_recognizer_begin(void) {
    if (!multinet || !model_data || !command_mutex || xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) return;
    multinet->clean(model_data);
    listening = true;
    inference_frames = 0;
    inference_started_us = esp_timer_get_time();
    ESP_LOGI(TAG, "[MULTINET] inference=start timeout_ms=%d", COMMAND_TAIL_TIMEOUT_MS);
    xSemaphoreGive(command_mutex);
}

void command_recognizer_stop(const char *reason) {
    if (!command_mutex || xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) return;
    stop_listening_locked(reason);
    xSemaphoreGive(command_mutex);
}

snowball_command_state_t command_recognizer_feed(
    const int16_t *samples,
    snowball_command_result_t *result
) {
    if (!samples || !result || !command_mutex || xSemaphoreTake(command_mutex, portMAX_DELAY) != pdTRUE) {
        return SNOWBALL_COMMAND_DETECTING;
    }
    if (!listening || !multinet || !model_data) {
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_DETECTING;
    }
    ++inference_frames;
    esp_mn_state_t state = multinet->detect(model_data, (int16_t *)samples);
    if (state == ESP_MN_STATE_DETECTING) {
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_DETECTING;
    }
    if (state == ESP_MN_STATE_TIMEOUT) {
        stop_listening_locked("timeout");
        *result = (snowball_command_result_t){
            .kind = SNOWBALL_COMMAND_NEW_CHAT,
            .target = SNOWBALL_COMMAND_TARGET_CHATGPT,
            .confidence = 1.0f,
        };
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_TIMEOUT;
    }
    if (state != ESP_MN_STATE_DETECTED) {
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_DETECTING;
    }

    esp_mn_results_t *recognized = multinet->get_results(model_data);
    if (!recognized || recognized->num < 1) {
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_DETECTING;
    }
    const command_definition_t *definition = find_command(recognized->command_id[0]);
    if (!definition) {
        ESP_LOGW(TAG, "MultiNet returned unknown command id %d", recognized->command_id[0]);
        xSemaphoreGive(command_mutex);
        return SNOWBALL_COMMAND_DETECTING;
    }
    float confidence = recognized->prob[0];
    if (confidence < 0.0f) confidence = 0.0f;
    if (confidence > 1.0f) confidence = 1.0f;
    *result = (snowball_command_result_t){
        .kind = definition->kind,
        .target = definition->target,
        .confidence = confidence,
    };
    strlcpy(result->name, definition->name, sizeof(result->name));
    stop_listening_locked("command_detected");
    ESP_LOGI(TAG, "command tail resolved: id=%d phrase=%s confidence=%.3f",
             definition->id, definition->phrase, confidence);
    xSemaphoreGive(command_mutex);
    return SNOWBALL_COMMAND_DETECTED;
}
