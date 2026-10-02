#!/bin/sh
set -eu

# Workstation flashing only. No daemon, collector, erase, NVS, or eFuse writes.
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PROJECT="$ROOT/firmware/esp32-s3-audio"
BUILD=${1:-$PROJECT/build-release}
PORT=${SNOWBALL_SERIAL_PORT:?set SNOWBALL_SERIAL_PORT to the selected USB device}
PYTHON=${SNOWBALL_IDF_PYTHON:-python}
BUILD=$(CDPATH= cd -- "$BUILD" && pwd)
case "$BUILD" in "$PROJECT"/*) ;; *) printf 'Build directory must be inside the firmware project.\n' >&2; exit 1 ;; esac
case "$PORT" in /dev/ttyACM*|/dev/ttyUSB*|/dev/cu.usbmodem*|/dev/cu.usbserial*) ;; *) printf 'Unsupported local serial device.\n' >&2; exit 1 ;; esac

[ -r "$BUILD/flash_args" ] || { printf 'Missing generated flash_args.\n' >&2; exit 1; }
if grep -Eiq '(^|[[:space:]])0x9000([[:space:]]|$)|(^|[[:space:]])nvs([[:space:]]|$)' "$BUILD/flash_args"; then
    printf 'Flash arguments address NVS; refusing.\n' >&2
    exit 1
fi
for entry in '0x0 bootloader/bootloader.bin' '0x8000 partition_table/partition-table.bin' '0x10000 snowball_speaker.bin' '0x310000 srmodels/srmodels.bin'; do
    grep -Fxq "$entry" "$BUILD/flash_args" || { printf 'Unexpected flash layout: missing %s\n' "$entry" >&2; exit 1; }
done
for file in bootloader/bootloader.bin partition_table/partition-table.bin snowball_speaker.bin srmodels/srmodels.bin; do
    [ -s "$BUILD/$file" ] || { printf 'Missing image: %s\n' "$file" >&2; exit 1; }
done
check_size() {
    bytes=$(wc -c < "$BUILD/$1")
    [ "$bytes" -le "$2" ] || { printf 'Image exceeds its safe non-NVS range: %s\n' "$1" >&2; exit 1; }
}
check_size bootloader/bootloader.bin 32768
check_size partition_table/partition-table.bin 4096
check_size snowball_speaker.bin 3145728
check_size srmodels/srmodels.bin 6291456
"$PYTHON" -m esptool version >/dev/null
"$PYTHON" -m esptool --chip esp32s3 --port "$PORT" -b 460800 \
    --before default_reset --after hard_reset write_flash \
    --flash_mode dio --flash_freq 80m --flash_size 16MB \
    0x0 "$BUILD/bootloader/bootloader.bin" \
    0x8000 "$BUILD/partition_table/partition-table.bin" \
    0x10000 "$BUILD/snowball_speaker.bin" \
    0x310000 "$BUILD/srmodels/srmodels.bin"
printf 'flash_verified_by_esptool; NVS 0x9000 was not addressed; verify application boot separately\n'
