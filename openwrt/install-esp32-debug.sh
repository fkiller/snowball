#!/bin/sh
set -eu

REPOSITORY=${1:-/root/snowball-voice}

mkdir -p /usr/libexec
cp "$REPOSITORY/tools/esp32-serial-logger.sh" /usr/libexec/snowball-esp32-serial-logger
cp "$REPOSITORY/tools/esp32-log-framer.lua" /usr/libexec/snowball-esp32-log-framer.lua
cp "$REPOSITORY/tools/esp32-log-framer.sh" /usr/libexec/snowball-esp32-log-framer.sh
cp "$REPOSITORY/tools/snowball-board.sh" /usr/libexec/snowball-board.sh
cp "$REPOSITORY/openwrt/snowball-esp32-debug.init" /etc/init.d/snowball-esp32-debug
cp "$REPOSITORY/tools/snowball-esp32-debug" /usr/bin/snowball-esp32-debug
cp "$REPOSITORY/tools/esp32-voice-trace-report.sh" /usr/bin/snowball-esp32-voice-report
cp "$REPOSITORY/tools/flash-esp32.sh" /usr/bin/snowball-flash-esp32
chmod 0755 /usr/libexec/snowball-esp32-serial-logger
chmod 0755 /usr/libexec/snowball-board.sh
chmod 0644 /usr/libexec/snowball-esp32-log-framer.lua
chmod 0755 /usr/libexec/snowball-esp32-log-framer.sh
chmod 0755 /etc/init.d/snowball-esp32-debug /usr/bin/snowball-esp32-debug /usr/bin/snowball-esp32-voice-report /usr/bin/snowball-flash-esp32

# Keep board selection out of the repository. With no serial selector the
# helper accepts exactly one Espressif USB-JTAG board and fails closed when
# more than one is attached. An administrator may add SNOWBALL_BOARD_SERIAL
# here after pairing. The helper's default port lock is on the shared SD card,
# so a container-side logger/flasher cannot bypass the host logger's lock.
# This file is intentionally mode 0600.
if [ ! -e /etc/snowball-esp32-board.conf ]; then
    (umask 077; printf '%s\n' '# Optional local selector; never commit this file.' 'SNOWBALL_BOARD_NAME=snowball-esp32' > /etc/snowball-esp32-board.conf)
fi
chmod 0600 /etc/snowball-esp32-board.conf
/etc/init.d/snowball-esp32-debug enable
