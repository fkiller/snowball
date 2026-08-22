#pragma once

#include <stdarg.h>

#include "esp_err.h"

esp_err_t serial_output_init(void);
void serial_output_lock(void);
void serial_output_unlock(void);
int serial_output_printf(const char *format, ...);
void serial_output_line(const char *line);
