#!/usr/bin/env bash
set -euo pipefail

for _ in $(seq 1 120); do
  if curl -fsS http://127.0.0.1:9222/json/version >/dev/null; then
    exec node /opt/snowball/services/browser-controller.mjs
  fi
  sleep 0.25
done

echo "Chromium DevTools endpoint did not become ready" >&2
exit 1
