#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/prepare-runtime.sh
exec nginx -c /tmp/nginx.conf
