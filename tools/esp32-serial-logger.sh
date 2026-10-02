#!/bin/sh
set -eu

BOARD_HELPER=${SNOWBALL_BOARD_HELPER:-"$(CDPATH= cd -- "$(dirname "$0")" && pwd)/snowball-board.sh"}
# shellcheck disable=SC1090
. "$BOARD_HELPER"
BOARD_ID=$BOARD_LOG_ID
LOG_ROOT='/mnt/sdcard'
LOG_DIR="$LOG_ROOT/snowball-dev/device-logs/$BOARD_ID"
LOG_FILE="$LOG_DIR/serial.current.log"
LOCK_FILE=$BOARD_LOCK_FILE
MAX_BYTES=5242880
ARCHIVES=7
MAX_LINE_BYTES=16384
MISSING_LOG_INTERVAL=1800
FRAMER=${SNOWBALL_LOG_FRAMER_LUA:-/usr/libexec/snowball-esp32-log-framer.lua}
SHELL_FRAMER=${SNOWBALL_LOG_FRAMER_SH:-/usr/libexec/snowball-esp32-log-framer.sh}
capture_pid=
framer_pid=
socat_pid=
session_dir=
raw_fifo=
line_fifo=
last_missing_log=0

cleanup() {
    [ -n "$socat_pid" ] && kill "$socat_pid" 2>/dev/null || true
    [ -n "$framer_pid" ] && kill "$framer_pid" 2>/dev/null || true
    [ -n "$capture_pid" ] && kill "$capture_pid" 2>/dev/null || true
    [ -n "$socat_pid" ] && wait "$socat_pid" 2>/dev/null || true
    [ -n "$framer_pid" ] && wait "$framer_pid" 2>/dev/null || true
    [ -n "$capture_pid" ] && wait "$capture_pid" 2>/dev/null || true
    [ -n "$raw_fifo" ] && rm -f "$raw_fifo" || true
    [ -n "$line_fifo" ] && rm -f "$line_fifo" || true
    [ -n "$session_dir" ] && rmdir "$session_dir" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! mountpoint -q "$LOG_ROOT" || ! awk '$2 == "/mnt/sdcard" && $4 ~ /(^|,)rw(,|$)/ { found=1 } END { exit !found }' /proc/mounts; then
    logger -t snowball-esp32-debug 'refusing to log: /mnt/sdcard is not a writable mount'
    exit 1
fi

available_kib=$(df -Pk "$LOG_ROOT" | awk 'NR == 2 { print $4 }')
case "$available_kib" in (*[!0-9]*|'') exit 1 ;; esac
if [ "$available_kib" -lt 102400 ]; then
    logger -t snowball-esp32-debug 'refusing to log: less than 100 MiB is free on /mnt/sdcard'
    exit 1
fi

mkdir -p "$LOG_DIR" "$(dirname "$LOCK_FILE")" /var/lock
chmod 0700 "$LOG_DIR"
touch "$LOG_FILE"
chmod 0600 "$LOG_FILE"

exec 9>"$LOCK_FILE"
if ! flock -n 9; then
    logger -t snowball-esp32-debug 'serial port collector is already running or reserved for flashing'
    exit 1
fi
[ -r "$FRAMER" ] || [ -r "$SHELL_FRAMER" ] || {
    logger -t snowball-esp32-debug 'serial log framer is not installed'
    exit 1
}

rotate_logs() {
    bytes=$(stat -c '%s' "$LOG_FILE" 2>/dev/null || printf '0')
    [ "$bytes" -lt "$MAX_BYTES" ] && return 0
    archive_tmp=$(mktemp "$LOG_DIR/.serial.1.XXXXXX")
    if gzip -c "$LOG_FILE" > "$archive_tmp"; then
        chmod 0600 "$archive_tmp"
        index=$ARCHIVES
        while [ "$index" -gt 1 ]; do
            previous=$((index - 1))
            [ -f "$LOG_DIR/serial.$previous.log.gz" ] && \
                mv -f "$LOG_DIR/serial.$previous.log.gz" "$LOG_DIR/serial.$index.log.gz"
            index=$previous
        done
        mv -f "$archive_tmp" "$LOG_DIR/serial.1.log.gz"
        : > "$LOG_FILE"
        sync -d "$LOG_DIR/serial.1.log.gz" "$LOG_FILE" 2>/dev/null || sync
    else
        rm -f "$archive_tmp"
        return 1
    fi
}

capture_board() {
    device=$1
    session=$(date '+%Y%m%dT%H%M%S%z')
    logger -t snowball-esp32-debug "capturing device output from $device"
    printf '%s board=%s session=%s collector=connected device=%s\n' \
        "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$BOARD_ID" "$session" "$device" >> "$LOG_FILE"
    line_number=0
    # A named pipe lets cleanup own the producer and consumer explicitly.
    # The Lua framer reads one byte at a time so each complete line is emitted
    # immediately while damaged no-newline input remains bounded.
    session_dir=$(mktemp -d "/tmp/snowball-esp32.XXXXXX")
    chmod 0700 "$session_dir"
    raw_fifo="$session_dir/raw"
    line_fifo="$session_dir/lines"
    mkfifo -m 0600 "$raw_fifo" "$line_fifo"
    # The ESP32-S3 USB-Serial/JTAG bridge exposes modem-control lines that
    # can reset the chip or leave GPIO0 sampled as DOWNLOAD when a reader
    # opens/closes the port.  This collector is passive: keep the local
    # connection, disable hangup and hardware flow control, and never let
    # log collection change the board's reset state.
    if command -v socat >/dev/null 2>&1 && command -v lua >/dev/null 2>&1 && [ -r "$FRAMER" ]; then
        socat -u "OPEN:$device,b115200,raw,echo=0,clocal=1,hupcl=0,crtscts=0" STDOUT > "$raw_fifo" &
        socat_pid=$!
    else
        # Minimal OpenWrt images do not always include socat/Lua.  Keep the
        # collector useful without pulling packages onto the router: stty
        # selects the UART framing and cat passively reads the USB bridge.
        # clocal/hupcl prevent modem-control changes from resetting GPIO0.
        stty -F "$device" 115200 raw -echo clocal -crtscts -hupcl 2>/dev/null || true
        cat "$device" > "$raw_fifo" &
        socat_pid=$!
    fi
    if command -v lua >/dev/null 2>&1 && [ -r "$FRAMER" ]; then
        lua "$FRAMER" "$MAX_LINE_BYTES" < "$raw_fifo" > "$line_fifo" &
    else
        "$SHELL_FRAMER" "$MAX_LINE_BYTES" < "$raw_fifo" > "$line_fifo" &
    fi
    framer_pid=$!
    while IFS= read -r line; do
            line_number=$((line_number + 1))
            timestamp=$(date '+%Y-%m-%dT%H:%M:%S%z')
            printf '%s board=%s session=%s seq=%u %s\n' \
                "$timestamp" "$BOARD_ID" "$session" "$line_number" "$line" >> "$LOG_FILE"
            rotate_logs
        done < "$line_fifo" &
    capture_pid=$!
    wait "$socat_pid" || true
    socat_pid=
    wait "$framer_pid" || true
    framer_pid=
    wait "$capture_pid" || true
    capture_pid=
    rm -f "$raw_fifo" "$line_fifo"
    raw_fifo=
    line_fifo=
    rmdir "$session_dir"
    session_dir=
    printf '%s board=%s session=%s collector=disconnected\n' \
        "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$BOARD_ID" "$session" >> "$LOG_FILE"
}

while true; do
    if device=$(find_board); then
        capture_board "$device"
    else
        now=$(date +%s)
        if [ $((now - last_missing_log)) -ge "$MISSING_LOG_INTERVAL" ]; then
            logger -t snowball-esp32-debug 'board is not connected; collector is waiting'
            last_missing_log=$now
        fi
    fi
    sleep 2
done
