#!/bin/sh
set -eu

SERVICE=/etc/init.d/snowball-esp32-debug
DOCKER_SOCKET=unix:///var/run/snowball-voice-docker.sock
PROJECT=/root/snowball-voice/firmware/esp32-s3-audio
BUILD_DIR=${SNOWBALL_FIRMWARE_BUILD_DIR:-$PROJECT/build-cache-safe2}
BUILD_NAME=${BUILD_DIR##*/}
FLASH_TRANSPORT=${SNOWBALL_FLASH_TRANSPORT:-auto}
FLASHER_SOURCE=${SNOWBALL_FLASHER_SOURCE:-/root/snowball-voice/tools/flash-esp32.sh}
logger_was_running=false

# The router keeps an installed copy of this helper.  Refuse to flash when
# that copy has silently drifted from the checked-out source that owns the
# firmware build.  This turns a stale host install into an explicit repair
# step instead of another unverified 16-hour attempt.
if [ -r "$FLASHER_SOURCE" ] && command -v sha256sum >/dev/null 2>&1; then
    installed_hash=$(sha256sum "$0" | awk '{print $1}')
    source_hash=$(sha256sum "$FLASHER_SOURCE" | awk '{print $1}')
    if [ "$installed_hash" != "$source_hash" ]; then
        printf 'Installed ESP32 flasher is stale; run openwrt/install-esp32-debug.sh before flashing.\n' >&2
        exit 1
    fi
fi

case "$FLASH_TRANSPORT" in
    auto|serial|jtag) ;;
    *)
        printf 'SNOWBALL_FLASH_TRANSPORT must be auto, serial, or jtag.\n' >&2
        exit 2
        ;;
esac

case "$BUILD_DIR" in
    "$PROJECT"/*) ;;
    *)
        printf 'Firmware build directory must remain inside %s.\n' "$PROJECT" >&2
        exit 1
        ;;
esac
case "$BUILD_NAME" in
    ''|*[!A-Za-z0-9._-]*)
        printf 'Firmware build directory name is invalid.\n' >&2
        exit 1
        ;;
esac

BOARD_HELPER=${SNOWBALL_BOARD_HELPER:-/usr/libexec/snowball-board.sh}
if [ ! -r "$BOARD_HELPER" ]; then
    BOARD_HELPER="$(CDPATH= cd -- "$(dirname "$0")" && pwd)/snowball-board.sh"
fi
# shellcheck disable=SC1090
. "$BOARD_HELPER"
LOCK_FILE=$BOARD_LOCK_FILE

cleanup() {
    flock -u 9 2>/dev/null || true
    exec 9>&- 2>/dev/null || true
    if [ "$logger_was_running" = true ]; then
        "$SERVICE" start >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if "$SERVICE" running >/dev/null 2>&1; then
    logger_was_running=true
    "$SERVICE" stop
fi

mkdir -p "$(dirname "$LOCK_FILE")" /var/lock
exec 9>"$LOCK_FILE"
lock_attempt=0
while ! flock -n 9; do
    lock_attempt=$((lock_attempt + 1))
    if [ "$lock_attempt" -ge 10 ]; then
        printf 'Snowball ESP32 serial port is still owned by another process.\n' >&2
        exit 1
    fi
    sleep 1
done

device=$(find_board) || {
    printf 'The configured Snowball ESP32 board is not connected.\n' >&2
    exit 1
}
port_attempt=0
while fuser "$device" >/dev/null 2>&1; do
    port_attempt=$((port_attempt + 1))
    if [ "$port_attempt" -ge 10 ]; then
        printf '%s is still open; close Web Serial or another terminal before flashing.\n' "$device" >&2
        exit 1
    fi
    sleep 1
done

DOCKER_HOST="$DOCKER_SOCKET" docker image inspect espressif/idf:v5.5.5 >/dev/null 2>&1 || {
    printf 'The 8.9 GB ESP-IDF image is not installed; install it only after checking router free space.\n' >&2
    exit 1
}
for image in \
    "$BUILD_DIR/bootloader/bootloader.bin" \
    "$BUILD_DIR/partition_table/partition-table.bin" \
    "$BUILD_DIR/snowball_speaker.bin" \
    "$BUILD_DIR/srmodels/srmodels.bin"; do
    if [ ! -s "$image" ]; then
        printf 'The approved command-tail firmware is missing: %s; build it before flashing.\n' "$image" >&2
        exit 1
    fi
done

if grep -Eq '(^|[[:space:]])0x9000([[:space:]]|$)|(^|[[:space:]])nvs' "$BUILD_DIR/flash_args"; then
    printf 'The selected flash arguments address NVS; refusing this recovery path.\n' >&2
    exit 1
fi

serial_flash() {
    DOCKER_HOST="$DOCKER_SOCKET" docker run --rm --pull=never --network host \
        --device "$device:$device" \
        -v "$PROJECT:/project" -w /project espressif/idf:v5.5.5 \
        bash -lc "source /opt/esp/idf/export.sh >/dev/null && \
            python -m esptool --chip esp32s3 -b 460800 \
              --before default_reset --after hard_reset write_flash \
              --flash_mode dio --flash_freq 80m --flash_size 16MB \
              0x0 '$BUILD_NAME/bootloader/bootloader.bin' \
              0x8000 '$BUILD_NAME/partition_table/partition-table.bin' \
              0x10000 '$BUILD_NAME/snowball_speaker.bin' \
              0x310000 '$BUILD_NAME/srmodels/srmodels.bin'"
}

jtag_flash() {
    # Keep this list deliberately explicit.  The development image contains
    # no NVS write and therefore cannot erase Wi-Fi, enrollment, or identity
    # state while recovering a serial/JTAG bootloader failure.
    for image in \
        "$BUILD_DIR/bootloader/bootloader.bin" \
        "$BUILD_DIR/partition_table/partition-table.bin" \
        "$BUILD_DIR/snowball_speaker.bin" \
        "$BUILD_DIR/srmodels/srmodels.bin"; do
        if [ ! -s "$image" ]; then
            printf 'JTAG flash image is missing: %s\n' "$image" >&2
            return 1
        fi
    done
    if ! grep -Eq '^0x0[[:space:]]+bootloader/bootloader\.bin$' "$BUILD_DIR/flash_args" \
        || ! grep -Eq '^0x8000[[:space:]]+partition_table/partition-table\.bin$' "$BUILD_DIR/flash_args" \
        || ! grep -Eq '^0x10000[[:space:]]+snowball_speaker\.bin$' "$BUILD_DIR/flash_args" \
        || ! grep -Eq '^0x310000[[:space:]]+srmodels/srmodels\.bin$' "$BUILD_DIR/flash_args"; then
        printf 'The selected build has unexpected flash offsets; refusing JTAG flash.\n' >&2
        return 1
    fi
    if grep -Eq '(^|[[:space:]])0x9000([[:space:]]|$)|(^|[[:space:]])nvs' "$BUILD_DIR/flash_args"; then
        printf 'The selected flash arguments address NVS; refusing this recovery path.\n' >&2
        return 1
    fi

    boot_size=$(stat -c '%s' "$BUILD_DIR/bootloader/bootloader.bin")
    partition_size=$(stat -c '%s' "$BUILD_DIR/partition_table/partition-table.bin")
    app_size=$(stat -c '%s' "$BUILD_DIR/snowball_speaker.bin")
    model_size=$(stat -c '%s' "$BUILD_DIR/srmodels/srmodels.bin")

    DOCKER_HOST="$DOCKER_SOCKET" docker run --rm --pull=never --privileged --network host \
        -v /dev:/dev -v /sys:/sys:ro -v "$PROJECT:/project" -w /project \
        -e "BUILD_NAME=$BUILD_NAME" \
        -e "BOOT_SIZE=$boot_size" -e "PARTITION_SIZE=$partition_size" \
        -e "APP_SIZE=$app_size" -e "MODEL_SIZE=$model_size" \
        espressif/idf:v5.5.5 bash -s <<'JTAG_SCRIPT'
set -eu
openocd_bin=$(find /opt/esp/tools -path '*/openocd-esp32/bin/openocd' -type f -executable | head -1)
[ -n "$openocd_bin" ]
openocd_scripts=$(dirname "$(dirname "$openocd_bin")")/share/openocd/scripts
build_root="/project/$BUILD_NAME"
probe_log=$(mktemp)
read_log=$(mktemp)
cleanup_jtag() {
    rm -f "$probe_log" "$read_log" \
        /tmp/snowball-read-boot.bin /tmp/snowball-read-partition.bin \
        /tmp/snowball-read-app.bin /tmp/snowball-read-model.bin
}
trap cleanup_jtag EXIT

if ! "$openocd_bin" -d0 -s "$openocd_scripts" \
    -c 'set ESP_ONLYCPU 1' -f board/esp32s3-builtin.cfg \
    -c 'adapter speed 20000; init; shutdown' >"$probe_log" 2>&1; then
    printf 'USB-JTAG probe failed; keep the board connected and retry.\n' >&2
    exit 1
fi

# The Espressif OpenOCD flash driver accepts programming without its generic
# verify command.  We therefore read each exact range back and compare it
# byte-for-byte below; this is the verification gate for this transport.
"$openocd_bin" -d0 -s "$openocd_scripts" \
    -c 'set ESP_ONLYCPU 1' -f board/esp32s3-builtin.cfg \
    -c "adapter speed 20000; init; reset halt; program $build_root/bootloader/bootloader.bin 0x0; program $build_root/partition_table/partition-table.bin 0x8000; program $build_root/snowball_speaker.bin 0x10000; program $build_root/srmodels/srmodels.bin 0x310000; reset run; shutdown" \
    >"$read_log" 2>&1

"$openocd_bin" -d0 -s "$openocd_scripts" \
    -c 'set ESP_ONLYCPU 1' -f board/esp32s3-builtin.cfg \
    -c "adapter speed 20000; init; reset halt; flash read_bank 0 /tmp/snowball-read-boot.bin 0x0 $BOOT_SIZE; flash read_bank 0 /tmp/snowball-read-partition.bin 0x8000 $PARTITION_SIZE; flash read_bank 0 /tmp/snowball-read-app.bin 0x10000 $APP_SIZE; flash read_bank 0 /tmp/snowball-read-model.bin 0x310000 $MODEL_SIZE; shutdown" \
    >>"$read_log" 2>&1
cmp "$build_root/bootloader/bootloader.bin" /tmp/snowball-read-boot.bin >/dev/null
cmp "$build_root/partition_table/partition-table.bin" /tmp/snowball-read-partition.bin >/dev/null
cmp "$build_root/snowball_speaker.bin" /tmp/snowball-read-app.bin >/dev/null
cmp "$build_root/srmodels/srmodels.bin" /tmp/snowball-read-model.bin >/dev/null

# Leave the target running after the readback invocation halted it.
"$openocd_bin" -d0 -s "$openocd_scripts" \
    -c 'set ESP_ONLYCPU 1' -f board/esp32s3-builtin.cfg \
    -c 'adapter speed 20000; init; reset run; shutdown' >/dev/null 2>&1
printf 'jtag_flash_verified\n'
JTAG_SCRIPT
}

serial_boot_check() {
    # Flash readback proves that bytes reached the chip; it does not prove
    # that GPIO0 was released and the application actually booted. Keep the
    # USB collector stopped while this one-shot probe owns the port. The
    # probe opens the port before resetting so ROM/application output cannot
    # be lost between two processes.
    DOCKER_HOST="$DOCKER_SOCKET" docker run --rm --pull=never \
        --device "$device:$device" \
        -e "SNOWBALL_PROBE_PORT=$device" \
        espressif/idf:v5.5.5 bash -s <<'BOOT_PROBE'
set -eu
source /opt/esp/idf/export.sh >/dev/null
python3 - <<'PY'
import os
import sys
import time
import serial

port_name = os.environ["SNOWBALL_PROBE_PORT"]
port = None
output = bytearray()

def open_port():
    return serial.Serial(port_name, 115200, timeout=0.15, rtscts=False, dsrdtr=False)

def close_port():
    global port
    if port is not None:
        try:
            port.close()
        except (OSError, serial.SerialException):
            pass
        port = None

def read_for(seconds):
    """Read bounded boot output, reopening if USB-JTAG re-enumerates."""
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            chunk = port.read(4096)
        except (OSError, serial.SerialException):
            # USB-JTAG may briefly re-enumerate during reset. Reopen the
            # same explicitly selected port and continue the bounded probe.
            close_port()
            time.sleep(0.25)
            try:
                globals()["port"] = open_port()
            except (OSError, serial.SerialException):
                time.sleep(0.25)
                continue
        if chunk:
            output.extend(chunk)

def hard_reset():
    """Toggle EN only, preserving GPIO0 high to leave ROM download mode."""
    if port is None:
        return
    port.rts = True
    # pyserial's USB-JTAG workaround sends a dummy DTR update after RTS.
    port.dtr = port.dtr
    time.sleep(0.25)
    port.rts = False
    port.dtr = port.dtr
    time.sleep(0.25)

try:
    port = open_port()
    # Match esptool's USBJTAGSerialReset sequence.  The USB-Serial/JTAG
    # bridge has an inverted EN/BOOT control path; a simple RTS pulse can
    # leave the chip in ROM download mode and falsely look like a flash
    # success.  DTR must be deasserted when EN is released so GPIO0 is
    # sampled for the normal flash boot path.
    port.rts = False
    port.dtr = False
    time.sleep(0.10)
    port.dtr = True
    port.rts = False
    time.sleep(0.10)
    port.rts = True
    port.dtr = False
    port.rts = True
    time.sleep(0.10)
    port.dtr = False
    port.rts = False
    read_for(4.0)
    # Some USB-JTAG revisions leave GPIO0 sampled low after the esptool
    # sequence. A second EN-only reset is safe and boots the already-written
    # application without entering DOWNLOAD(USB/UART0) again.
    if b"Snowball speaker ready:" not in output and b"WakeNet detector ready" not in output:
        hard_reset()
        read_for(8.0)
finally:
    if port is not None:
        try:
            port.dtr = False
            port.rts = False
        except (OSError, serial.SerialException):
            pass
    close_port()

text = output.decode("utf-8", "replace")
if "Snowball speaker ready:" in text or "WakeNet detector ready" in text:
    print("application_boot_verified")
    raise SystemExit(0)
if "waiting for download" in text or "DOWNLOAD(USB/UART0)" in text:
    print("board_remained_in_download_mode", file=sys.stderr)
else:
    print("application_boot_marker_missing", file=sys.stderr)
raise SystemExit(1)
PY
BOOT_PROBE
}

flash_ok=false
case "$FLASH_TRANSPORT" in
    serial)
        serial_flash && flash_ok=true
        ;;
    jtag)
        jtag_flash && flash_ok=true
        ;;
    auto)
        if serial_flash; then
            flash_ok=true
        else
            printf '%s\n' 'Serial flash failed; trying the protected USB-JTAG recovery path.' >&2
            jtag_flash && flash_ok=true
        fi
        ;;
esac

if [ "$flash_ok" != true ]; then
    printf '%s\n' 'ESP32 flash did not complete. If the port reported no data, hold BOOT, tap RESET, then release BOOT and run the helper again.' >&2
    exit 1
fi

if ! serial_boot_check; then
    printf '%s\n' 'ESP32 flash bytes were written, but application boot was not verified. Release BOOT, tap RESET once, and retry; NVS was not modified by the recovery path.' >&2
    exit 1
fi
