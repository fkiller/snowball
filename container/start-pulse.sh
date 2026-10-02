#!/usr/bin/env bash
set -euo pipefail
mkdir -p /tmp/pulse /tmp/runtime-pwuser
chmod 700 /tmp/pulse /tmp/runtime-pwuser
exec pulseaudio --daemonize=no --disable-shm --exit-idle-time=-1 --log-target=stderr --log-level=warning \
  --file=/opt/snowball/container/pulse/default.pa
