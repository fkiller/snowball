#!/bin/sh
set -u

# Read-only Snowball-minis environment audit. This script never installs
# packages, opens a serial port, flashes a board, or changes Gateway state.

repository=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
failed=0

report_command() {
    command_name=$1
    if command -v "$command_name" >/dev/null 2>&1; then
        printf 'tool.%s=present\n' "$command_name"
    else
        printf 'tool.%s=missing\n' "$command_name"
        failed=1
    fi
}

printf 'repository=%s\n' "$repository"
printf 'os=%s\n' "$(uname -s 2>/dev/null || printf unknown)"
printf 'arch=%s\n' "$(uname -m 2>/dev/null || printf unknown)"

for command_name in git docker; do
    report_command "$command_name"
done

if command -v sha256sum >/dev/null 2>&1; then
    printf 'tool.sha256=sha256sum\n'
elif command -v shasum >/dev/null 2>&1; then
    printf 'tool.sha256=shasum\n'
else
    printf 'tool.sha256=missing\n'
    failed=1
fi

if command -v docker >/dev/null 2>&1; then
    if docker info >/dev/null 2>&1; then
        printf 'docker.daemon=ready\n'
    else
        printf 'docker.daemon=unavailable\n'
        failed=1
    fi
    if docker image inspect espressif/idf:v5.5.5 >/dev/null 2>&1; then
        printf 'docker.idf_v5_5_5=present\n'
    else
        printf 'docker.idf_v5_5_5=missing\n'
    fi
    if docker compose version >/dev/null 2>&1; then
        printf 'docker.compose=present\n'
    else
        printf 'docker.compose=missing\n'
        failed=1
    fi
fi

available_kib=$(df -Pk "$repository" 2>/dev/null | awk 'NR == 2 { print $4 }')
case "$available_kib" in
    ''|*[!0-9]*)
        printf 'disk.available_kib=unknown\n'
        failed=1
        ;;
    *)
        printf 'disk.available_kib=%s\n' "$available_kib"
        if [ "$available_kib" -lt 5242880 ]; then
            printf 'disk.gate=stop_below_5_gib\n'
            failed=1
        elif [ "$available_kib" -lt 10485760 ]; then
            printf 'disk.gate=warn_below_10_gib\n'
        else
            printf 'disk.gate=ready\n'
        fi
        ;;
esac

if [ -r /proc/meminfo ]; then
    memory_kib=$(awk '/^MemTotal:/ { print $2 }' /proc/meminfo)
elif command -v sysctl >/dev/null 2>&1; then
    memory_bytes=$(sysctl -n hw.memsize 2>/dev/null || printf 0)
    memory_kib=$((memory_bytes / 1024))
else
    memory_kib=0
fi
printf 'memory.total_kib=%s\n' "$memory_kib"
if [ "$memory_kib" -gt 0 ] && [ "$memory_kib" -lt 6291456 ]; then
    printf 'memory.gate=below_6_gib\n'
    failed=1
elif [ "$memory_kib" -gt 0 ] && [ "$memory_kib" -lt 8388608 ]; then
    printf 'memory.gate=usable_but_below_8_gib_recommendation\n'
else
    printf 'memory.gate=ready\n'
fi

board_count=0
for device in /dev/ttyACM* /dev/ttyUSB* /dev/cu.usbmodem*; do
    [ -c "$device" ] || continue
    board_count=$((board_count + 1))
    printf 'serial.device=%s\n' "$device"
done
printf 'serial.device_count=%s\n' "$board_count"
if [ "$board_count" -eq 0 ]; then
    printf 'serial.gate=no_device\n'
elif [ "$board_count" -eq 1 ]; then
    printf 'serial.gate=single_device_present\n'
else
    printf 'serial.gate=multiple_devices_fail_closed\n'
    failed=1
fi

if git -C "$repository" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf 'git.branch=%s\n' "$(git -C "$repository" branch --show-current)"
    if [ -z "$(git -C "$repository" status --porcelain)" ]; then
        printf 'git.worktree=clean\n'
    else
        printf 'git.worktree=dirty\n'
    fi
    if git -C "$repository" remote get-url origin >/dev/null 2>&1; then
        printf 'git.origin=present\n'
    else
        printf 'git.origin=missing\n'
        failed=1
    fi
else
    printf 'git.repository=invalid\n'
    failed=1
fi

router_host=${SNOWBALL_ROUTER_HOST:-192.168.1.1}
if command -v ssh >/dev/null 2>&1; then
    if ssh -o BatchMode=yes -o ConnectTimeout=3 -o StrictHostKeyChecking=yes \
        "root@$router_host" true >/dev/null 2>&1; then
        printf 'router.ssh=ready\n'
    else
        printf 'router.ssh=unavailable_or_untrusted\n'
    fi
else
    printf 'router.ssh=ssh_missing\n'
fi

exit "$failed"
