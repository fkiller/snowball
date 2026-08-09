#!/usr/bin/env bash
set -euo pipefail
for _ in $(seq 1 120); do
  [[ -S /tmp/pulse/native ]] && exit 0
  sleep 0.25
done
echo "PulseAudio did not create /tmp/pulse/native" >&2
exit 1
