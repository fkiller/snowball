#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/wait-for-pulse.sh
for _ in $(seq 1 120); do
  if xdpyinfo -display "${DISPLAY}" >/dev/null 2>&1; then
    exec /usr/bin/chromium \
      --user-data-dir=/data/chromium \
      --remote-debugging-address=127.0.0.1 \
      --remote-debugging-port=9222 \
      --no-sandbox \
      --disable-background-mode \
      --disable-dev-shm-usage \
      --disable-session-crashed-bubble \
      --noerrdialogs \
      --autoplay-policy=no-user-gesture-required \
      --no-first-run \
      --no-default-browser-check \
      --password-store=basic \
      --window-size=1360,900 \
      "${CHATGPT_URL:-https://chatgpt.com/}"
  fi
  sleep 0.25
done
echo "Xvfb did not become ready" >&2
exit 1
