#pragma once

#include <stddef.h>
#include <stdint.h>

#define SNOWBALL_DIAGNOSTIC_CAPACITY 32
#define SNOWBALL_DIAGNOSTIC_STAGE_MAX 40
#define SNOWBALL_DIAGNOSTIC_DETAIL_MAX 64

typedef struct {
    uint64_t sequence;
    uint32_t uptime_ms;
    uint32_t attempt;
    float confidence;
    char stage[SNOWBALL_DIAGNOSTIC_STAGE_MAX];
    char detail[SNOWBALL_DIAGNOSTIC_DETAIL_MAX];
} snowball_diagnostic_event_t;

void diagnostics_record(const char *stage, uint32_t attempt, const char *detail, float confidence);
size_t diagnostics_snapshot(snowball_diagnostic_event_t *events, size_t capacity);
void diagnostics_memory_snapshot(const char *stage, uint32_t attempt);
