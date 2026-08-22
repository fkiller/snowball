#!/bin/sh
set -eu

limit=${1:-16384}
case "$limit" in
    ''|*[!0-9]*) exit 2 ;;
esac
[ "$limit" -gt 0 ] || exit 2

# Keep this fallback deliberately small and dependency-free for OpenWrt.  The
# Lua implementation remains preferred when available; this path is only for
# line-oriented ESP32 logs on minimal router images.
while IFS= read -r line || [ -n "$line" ]; do
    line=$(printf '%s' "$line" | tr -d '\000-\010\013-\037\177')
    if [ "${#line}" -gt "$limit" ]; then
        line="$(printf '%s' "$line" | cut -c "1-${limit}") [truncated]"
    fi
    printf '%s\n' "$line"
done
