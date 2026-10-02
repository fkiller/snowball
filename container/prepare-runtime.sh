#!/usr/bin/env bash
set -euo pipefail

mkdir -p /data/certs /data/chromium /data/state /data/home/pwuser /tmp/pulse /tmp/runtime-pwuser \
  /tmp/nginx-client /tmp/nginx-proxy /tmp/nginx-fastcgi /tmp/nginx-uwsgi /tmp/nginx-scgi
chmod 700 /data/certs /data/chromium /data/state /data/home/pwuser /tmp/pulse /tmp/runtime-pwuser

# Gateway and nginx both call this helper during supervisor startup. Hold one
# persistent descriptor while creating the CA/server pair so concurrent
# invocations cannot mix a newly generated key with another process's cert.
exec 9>/data/certs/.prepare-runtime.lock
flock -x 9

if [[ ! -s /data/certs/ca.key || ! -s /data/certs/ca.crt ]]; then
  openssl genrsa -out /data/certs/ca.key 3072
  openssl req -x509 -new -sha256 -days 3650 \
    -key /data/certs/ca.key \
    -subj "/CN=Snowball Local CA" \
    -out /data/certs/ca.crt
fi

if [[ ! -s /data/certs/server.key || ! -s /data/certs/server.crt ]]; then
  envsubst '${SNOWBALL_LAN_IP}' \
    </opt/snowball/container/server.ext.template \
    >/data/certs/server.ext
  openssl genrsa -out /data/certs/server.key 2048
  openssl req -new \
    -key /data/certs/server.key \
    -subj "/CN=${SNOWBALL_LAN_IP}" \
    -out /data/certs/server.csr
  openssl x509 -req -sha256 -days 825 \
    -in /data/certs/server.csr \
    -CA /data/certs/ca.crt \
    -CAkey /data/certs/ca.key \
    -CAcreateserial \
    -extfile /data/certs/server.ext \
    -out /data/certs/server.crt
  rm -f /data/certs/server.csr
fi

chmod 600 /data/certs/ca.key /data/certs/server.key
chmod 644 /data/certs/ca.crt /data/certs/server.crt

envsubst '${SNOWBALL_LAN_IP} ${SNOWBALL_HTTP_PORT} ${SNOWBALL_HTTPS_PORT}' \
  </opt/snowball/container/nginx.conf.template \
  >/tmp/nginx.conf
