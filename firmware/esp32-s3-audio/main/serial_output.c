#include "serial_output.h"

#include <stdio.h>

#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"

static SemaphoreHandle_t output_mutex;

static int locked_vprintf(const char *format, va_list arguments) {
    if (!output_mutex) return vprintf(format, arguments);
    if (xSemaphoreTakeRecursive(output_mutex, portMAX_DELAY) != pdTRUE) return -1;
    int result = vprintf(format, arguments);
    fflush(stdout);
    xSemaphoreGiveRecursive(output_mutex);
    return result;
}

esp_err_t serial_output_init(void) {
    output_mutex = xSemaphoreCreateRecursiveMutex();
    if (!output_mutex) return ESP_ERR_NO_MEM;
    esp_log_set_vprintf(locked_vprintf);
    return ESP_OK;
}

void serial_output_lock(void) {
    if (output_mutex) (void)xSemaphoreTakeRecursive(output_mutex, portMAX_DELAY);
}

void serial_output_unlock(void) {
    fflush(stdout);
    if (output_mutex) xSemaphoreGiveRecursive(output_mutex);
}

int serial_output_printf(const char *format, ...) {
    serial_output_lock();
    va_list arguments;
    va_start(arguments, format);
    int result = vprintf(format, arguments);
    va_end(arguments);
    serial_output_unlock();
    return result;
}

void serial_output_line(const char *line) {
    if (!line) return;
    serial_output_lock();
    /* USB Serial/JTAG can reset the board when a browser opens the port. The
       ROM boot log may end on a partial line, so force a fresh line before an
       NDJSON response. Parsers can safely ignore the resulting blank line. */
    fputc('\n', stdout);
    fputs(line, stdout);
    fputc('\n', stdout);
    serial_output_unlock();
}
