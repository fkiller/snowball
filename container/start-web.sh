#!/usr/bin/env bash
set -euo pipefail
cd /opt/snowball
export WRANGLER_LOG_PATH=.wrangler/wrangler.log
exec npx vinext start --hostname 127.0.0.1 --port 3000
