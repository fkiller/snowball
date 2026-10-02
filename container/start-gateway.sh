#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/prepare-runtime.sh
exec /usr/local/bin/snowball-gateway
