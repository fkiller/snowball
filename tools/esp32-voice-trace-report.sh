#!/bin/sh
set -eu

# Produce a bounded, secret-free acceptance report from the router's rotated
# USB trace.  This is deliberately POSIX shell/awk so it can run on the
# FriendlyWrt host without Node, Python, or a second serial-port reader.
#
# Usage:
#   esp32-voice-trace-report.sh LOG_FILE [collector-session] [minimum-cycles]
#
# The exit status is non-zero until the selected session contains the
# requested number of complete successful cycles.  A cycle is considered
# complete only when the board reports wake detection, command resolution,
# media connection, a terminal command receipt, and media end.  A wake chime
# alone therefore never makes this report pass.

usage() {
    printf 'usage: %s LOG_FILE [collector-session] [minimum-cycles]\n' "$0" >&2
    exit 2
}

log_file=${1:-}
session=${2:-}
minimum_cycles=${3:-3}
[ -n "$log_file" ] || usage
[ "$log_file" = "-" ] || [ -r "$log_file" ] || {
    printf 'trace log is not readable: %s\n' "$log_file" >&2
    exit 1
}
case "$minimum_cycles" in
    ''|*[!0-9]*) printf 'minimum-cycles must be a non-negative integer\n' >&2; exit 2 ;;
esac

# If the caller omits a session, use the most recent collector session rather
# than accidentally mixing an old pre-fix run with the current board boot.
if [ -z "$session" ] && [ "$log_file" != "-" ]; then
    session=$(grep -a 'collector=connected' "$log_file" | tail -n 1 | sed -n 's/.*session=\([^ ]*\).*/\1/p' || true)
fi
if [ -z "$session" ]; then
    printf 'collector session is required when the input has no connected marker\n' >&2
    exit 2
fi

awk -v selected_session="$session" -v minimum_cycles="$minimum_cycles" '
function string_value(line, key, marker, start, rest, finish) {
    marker = "\"" key "\":\""
    start = index(line, marker)
    if (!start) return ""
    rest = substr(line, start + length(marker))
    finish = index(rest, "\"")
    if (!finish) return ""
    return substr(rest, 1, finish - 1)
}

function number_value(line, key, marker, start, rest, finish, value) {
    marker = "\"" key "\":"
    start = index(line, marker)
    if (!start) return ""
    rest = substr(line, start + length(marker))
    finish = match(rest, /[,}]/)
    if (!finish) return ""
    value = substr(rest, 1, finish - 1)
    return value + 0
}

function remember_attempt(attempt) {
    if (attempt == "" || attempt == 0 || seen_attempt[attempt]) return
    seen_attempt[attempt] = 1
    attempt_order[++attempt_count] = attempt
}

function mark_stage(stage, attempt, detail, timestamp) {
    if (attempt == "" || attempt == 0) return
    remember_attempt(attempt)
    if (stage == "wake_detected") {
        wake[attempt] = timestamp
        wake_count[attempt]++
    } else if (stage == "command_resolved") {
        resolved[attempt] = timestamp
        resolved_detail[attempt] = detail
    } else if (stage == "media_connected") {
        connected[attempt] = timestamp
    } else if (stage == "command_executed") {
        executed[attempt] = timestamp
        executed_detail[attempt] = detail
    } else if (stage == "media_ended") {
        ended[attempt] = timestamp
        ended_detail[attempt] = detail
    } else if (stage == "media_failed") {
        failed[attempt] = timestamp
        failed_detail[attempt] = detail
    }
}

BEGIN {
    complete_cycles = 0
    guard_reset = 0
    guard_cooldown = 0
    guard_quiet = 0
    panic = 0
    unexpected_reset = 0
}

{
    # The collector prepends a timestamp and session token to every line.
    if (index($0, "session=" selected_session " ") == 0) next
    timestamp = $1

    if (index($0, "AFE buffer reset; collecting fresh audio before WakeNet re-arm") > 0) guard_reset++
    if (index($0, "WakeNet suppressed after media end") > 0) guard_cooldown++
    if (index($0, "WakeNet re-arm waiting for quiet microphone audio") > 0) guard_quiet++
    if (index($0, "Guru Meditation") > 0 || index($0, "Cache disabled but cached memory region accessed") > 0) panic++
    if (index($0, "unexpected reset") > 0 || index($0, "watchdog") > 0) unexpected_reset++

    if (index($0, "\"event\":\"debug\"") > 0) {
        stage = string_value($0, "stage")
        attempt = number_value($0, "attempt")
        detail = string_value($0, "detail")
        mark_stage(stage, attempt, detail, timestamp)
    }
}

END {
    printf "TRACE_REPORT version=1 session=%s\n", selected_session
    printf "guard afe_reset=%d cooldown=%d quiet_window=%d\n", guard_reset, guard_cooldown, guard_quiet
    printf "faults panic=%d unexpected_reset=%d\n", panic, unexpected_reset
    for (i = 1; i <= attempt_count; i++) {
        attempt = attempt_order[i]
        complete = (wake[attempt] != "" && resolved[attempt] != "" &&
            connected[attempt] != "" && executed[attempt] != "" &&
            ended[attempt] != "" && failed[attempt] == "")
        if (complete) complete_cycles++
        printf "attempt=%s wake=%s resolved=%s(%s) connected=%s executed=%s(%s) ended=%s(%s) failed=%s(%s) complete=%s\n",
            attempt,
            (wake[attempt] != "" ? wake[attempt] : "-"),
            (resolved[attempt] != "" ? resolved[attempt] : "-"),
            (resolved_detail[attempt] != "" ? resolved_detail[attempt] : "-"),
            (connected[attempt] != "" ? connected[attempt] : "-"),
            (executed[attempt] != "" ? executed[attempt] : "-"),
            (executed_detail[attempt] != "" ? executed_detail[attempt] : "-"),
            (ended[attempt] != "" ? ended[attempt] : "-"),
            (ended_detail[attempt] != "" ? ended_detail[attempt] : "-"),
            (failed[attempt] != "" ? failed[attempt] : "-"),
            (failed_detail[attempt] != "" ? failed_detail[attempt] : "-"),
            (complete ? "yes" : "no")
    }
    printf "complete_cycles=%d required_cycles=%d\n", complete_cycles, minimum_cycles
    if (complete_cycles < minimum_cycles || panic > 0 || unexpected_reset > 0) exit 1
}
' "$log_file"
