#include "diagnostics.h"

#include <inttypes.h>
#include <string.h>

#include "esp_heap_caps.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "serial_output.h"

static snowball_diagnostic_event_t event_ring[SNOWBALL_DIAGNOSTIC_CAPACITY];
static size_t event_count;
static size_t event_cursor;
static uint64_t next_sequence = 1;
static portMUX_TYPE event_lock = portMUX_INITIALIZER_UNLOCKED;

void diagnostics_record(const char *stage, uint32_t attempt, const char *detail, float confidence) {
    if (!stage || !stage[0]) return;

    snowball_diagnostic_event_t event = {
        .uptime_ms = (uint32_t)(esp_timer_get_time() / 1000ULL),
        .attempt = attempt,
        .confidence = confidence,
    };
    strlcpy(event.stage, stage, sizeof(event.stage));
    if (detail) strlcpy(event.detail, detail, sizeof(event.detail));

    portENTER_CRITICAL(&event_lock);
    event.sequence = next_sequence++;
    event_ring[event_cursor] = event;
    event_cursor = (event_cursor + 1U) % SNOWBALL_DIAGNOSTIC_CAPACITY;
    if (event_count < SNOWBALL_DIAGNOSTIC_CAPACITY) ++event_count;
    portEXIT_CRITICAL(&event_lock);
}

size_t diagnostics_snapshot(snowball_diagnostic_event_t *events, size_t capacity) {
    if (!events || capacity == 0) return 0;

    portENTER_CRITICAL(&event_lock);
    size_t count = event_count < capacity ? event_count : capacity;
    size_t oldest = (event_cursor + SNOWBALL_DIAGNOSTIC_CAPACITY - event_count) %
        SNOWBALL_DIAGNOSTIC_CAPACITY;
    size_t skip = event_count - count;
    for (size_t index = 0; index < count; ++index) {
        events[index] = event_ring[(oldest + skip + index) % SNOWBALL_DIAGNOSTIC_CAPACITY];
    }
    portEXIT_CRITICAL(&event_lock);
    return count;
}

void diagnostics_memory_snapshot(const char *stage, uint32_t attempt) {
#if CONFIG_SNOWBALL_HEAP_DIAGNOSTICS
    if (!stage || !stage[0]) return;
    size_t internal_free = heap_caps_get_free_size(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT);
    size_t internal_largest = heap_caps_get_largest_free_block(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT);
    size_t internal_min = heap_caps_get_minimum_free_size(MALLOC_CAP_INTERNAL | MALLOC_CAP_8BIT);
    size_t dma_free = heap_caps_get_free_size(MALLOC_CAP_DMA);
    size_t dma_largest = heap_caps_get_largest_free_block(MALLOC_CAP_DMA);
    size_t psram_free = heap_caps_get_free_size(MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    size_t psram_largest = heap_caps_get_largest_free_block(MALLOC_CAP_SPIRAM | MALLOC_CAP_8BIT);
    UBaseType_t stack_low_water = uxTaskGetStackHighWaterMark(NULL);
    serial_output_printf(
        "{\"version\":1,\"event\":\"memory\",\"stage\":\"%s\",\"attempt\":%" PRIu32
        ",\"internalFree\":%u,\"internalLargest\":%u,\"internalMin\":%u"
        ",\"dmaFree\":%u,\"dmaLargest\":%u,\"psramFree\":%u,\"psramLargest\":%u"
        ",\"taskStackLowWater\":%u,\"core\":%d}\n",
        stage,
        attempt,
        (unsigned)internal_free,
        (unsigned)internal_largest,
        (unsigned)internal_min,
        (unsigned)dma_free,
        (unsigned)dma_largest,
        (unsigned)psram_free,
        (unsigned)psram_largest,
        (unsigned)stack_low_water,
        (int)xPortGetCoreID()
    );
#else
    (void)stage;
    (void)attempt;
#endif
}
