#!/bin/sh
set -eu

# Board selection is runtime configuration. A hardware serial or MAC must
# never be committed to this repository or echoed into diagnostics.
SNOWBALL_BOARD_CONFIG=${SNOWBALL_BOARD_CONFIG:-/etc/snowball-esp32-board.conf}
SNOWBALL_BOARD_VENDOR_ID=${SNOWBALL_BOARD_VENDOR_ID:-303a}
SNOWBALL_BOARD_PRODUCT_ID=${SNOWBALL_BOARD_PRODUCT_ID:-1001}

if [ -r "$SNOWBALL_BOARD_CONFIG" ]; then
    # The installer creates this file mode 0600; it is local administrator
    # configuration, not input received from the board or from the network.
    # shellcheck disable=SC1090
    . "$SNOWBALL_BOARD_CONFIG"
fi

BOARD_NAME=${SNOWBALL_BOARD_NAME:-snowball-esp32}
BOARD_SERIAL=${SNOWBALL_BOARD_SERIAL:-}
case "$BOARD_NAME" in
    ''|*[!A-Za-z0-9._-]*)
        printf '%s\n' 'invalid Snowball board name in local configuration' >&2
        exit 2
        ;;
esac
case "$BOARD_SERIAL" in
    '') ;;
    *[!A-Za-z0-9:._-]*)
        printf '%s\n' 'invalid Snowball board selector in local configuration' >&2
        exit 2
        ;;
esac

BOARD_LOG_ID=$(printf '%s' "$BOARD_NAME" | tr '[:upper:]' '[:lower:]')
# The router host and the development container do not share /var/lock.  A
# lock there therefore cannot prevent a second container-side reader from
# opening the USB Serial/JTAG device while the host logger owns it.  Put the
# default lock on the persistent SD volume that both environments share.  A
# local administrator may override this for a non-router development host.
if [ -n "${SNOWBALL_BOARD_LOCK_FILE:-}" ]; then
    BOARD_LOCK_FILE=$SNOWBALL_BOARD_LOCK_FILE
else
    BOARD_LOCK_FILE=/mnt/sdcard/snowball-dev/device-logs/$BOARD_LOG_ID/serial.port.lock
fi

find_board() {
    found=
    count=0
    for device in /dev/ttyACM*; do
        [ -c "$device" ] || continue
        tty_name=${device##*/}
        usb_parent="/sys/class/tty/$tty_name/device/.."
        [ -r "$usb_parent/idVendor" ] || continue
        [ "$(tr -d '\r\n' < "$usb_parent/idVendor")" = "$SNOWBALL_BOARD_VENDOR_ID" ] || continue
        [ "$(tr -d '\r\n' < "$usb_parent/idProduct")" = "$SNOWBALL_BOARD_PRODUCT_ID" ] || continue
        if [ -n "$BOARD_SERIAL" ]; then
            [ -r "$usb_parent/serial" ] || continue
            [ "$(tr -d '\r\n' < "$usb_parent/serial")" = "$BOARD_SERIAL" ] || continue
        fi
        found=$device
        count=$((count + 1))
    done
    if [ "$count" -eq 1 ]; then
        printf '%s\n' "$found"
        return 0
    fi
    if [ "$count" -gt 1 ]; then
        printf '%s\n' 'multiple Snowball USB boards found; set SNOWBALL_BOARD_SERIAL in the local config' >&2
        return 2
    fi
    return 1
}
