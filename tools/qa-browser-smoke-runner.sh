#!/usr/bin/env bash
set -euo pipefail

# This runner deliberately does not start Chromium or touch /data. It uses a
# disposable Gateway state directory, a known QA-only administrator password,
# and a tiny browser-controller stub so WebRTC/auth/state lifecycle tests never
# depend on a user's Snowball or ChatGPT credentials.
umask 077

QA_STATE=/tmp/snowball-qa-state
QA_WEB_PORT=${SNOWBALL_QA_WEB_PORT:-18300}
QA_GATEWAY_PORT=${SNOWBALL_QA_GATEWAY_PORT:-18080}
QA_CONTROLLER_PORT=${SNOWBALL_QA_CONTROLLER_PORT:-13100}
QA_HTTPS_PORT=${SNOWBALL_QA_HTTPS_PORT:-18443}
QA_ICE_PORT=${SNOWBALL_QA_ICE_PORT:-49010}
QA_UPLINK_PORT=${SNOWBALL_QA_UPLINK_PORT:-49011}
QA_DOWNLINK_PORT=${SNOWBALL_QA_DOWNLINK_PORT:-49012}
QA_DEVICE_UPLINK_PORT=${SNOWBALL_QA_DEVICE_UPLINK_PORT:-49013}
QA_DEVICE_DOWNLINK_PORT=${SNOWBALL_QA_DEVICE_DOWNLINK_PORT:-49014}
QA_SETUP_CODE="snowball-qa-setup-$(openssl rand -hex 18)"
QA_ADMIN_PASSWORD="Snowball-QA-$(openssl rand -hex 18)!"

rm -rf "$QA_STATE"
mkdir -p "$QA_STATE/state" "$QA_STATE/certs"
printf '%s\n' "$QA_SETUP_CODE" >"$QA_STATE/state/initial-setup-code"

openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -subj '/CN=Snowball QA CA' \
  -keyout "$QA_STATE/certs/ca.key" -out "$QA_STATE/certs/ca.crt" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -subj '/CN=127.0.0.1' \
  -keyout "$QA_STATE/certs/server.key" -out "$QA_STATE/certs/server.csr" >/dev/null 2>&1
printf '%s\n' '[v3_req]' 'subjectAltName=IP:127.0.0.1' >"$QA_STATE/certs/server.ext"
openssl x509 -req -days 2 -sha256 \
  -in "$QA_STATE/certs/server.csr" \
  -CA "$QA_STATE/certs/ca.crt" -CAkey "$QA_STATE/certs/ca.key" -CAcreateserial \
  -extfile "$QA_STATE/certs/server.ext" -extensions v3_req \
  -out "$QA_STATE/certs/server.crt" >/dev/null 2>&1

cat > /tmp/snowball-qa-browser-controller.mjs <<'NODE'
import http from "node:http";

let voiceActive = false;
const status = () => ({
  state: "ready",
  reason: "QA browser-controller service account",
  url: "https://chatgpt.com/",
  title: "ChatGPT QA",
  voiceButtonPresent: !voiceActive,
  voiceActive,
  authenticated: true,
  checkedAt: new Date().toISOString(),
});

function reply(response, value, code = 200) {
  response.writeHead(code, { "content-type": "application/json" });
  response.end(JSON.stringify(value));
}

const server = http.createServer((request, response) => {
  const path = new URL(request.url || "/", "http://127.0.0.1").pathname;
  if (request.method === "GET" && path === "/status") return reply(response, status());
  if (request.method === "GET" && path === "/candidates") {
    return reply(response, {
      version: 1,
      source: "qa-browser-controller",
      authenticated: true,
      voiceState: "available",
      voices: ["Cove"],
      projects: ["Snowball"],
    });
  }
  if (request.method === "GET" && (path === "/voices" || path === "/projects")) {
    return reply(response, { names: path === "/voices" ? ["Cove"] : ["Snowball"] });
  }
  if (request.method === "POST" && path === "/voice/start") {
    voiceActive = true;
    return reply(response, status());
  }
  if (request.method === "POST" && (path === "/voice/stop" || path === "/project/stop")) {
    voiceActive = false;
    return reply(response, status());
  }
  if (request.method === "POST" && path === "/navigate") return reply(response, status());
  if (request.method === "POST" && path === "/voice/select") return reply(response, status());
  if (request.method === "POST" && path === "/project/turn") {
    return reply(response, { action: "project_turn_completed", mode: "turn_based" });
  }
  return reply(response, { error: "QA controller endpoint not implemented" }, 404);
});

server.listen(Number(process.env.QA_CONTROLLER_PORT), "127.0.0.1", () => {
  console.log("QA browser controller listening on " + process.env.QA_CONTROLLER_PORT);
});
NODE

cat > /tmp/snowball-qa-nginx.conf <<EOF
user root;
pid /tmp/snowball-qa-nginx.pid;
error_log /tmp/snowball-qa-nginx-error.log warn;
events { worker_connections 64; }
http {
  access_log /tmp/snowball-qa-nginx-access.log;
  client_body_temp_path /tmp/snowball-qa-nginx-body;
  proxy_temp_path /tmp/snowball-qa-nginx-proxy;
  fastcgi_temp_path /tmp/snowball-qa-nginx-fastcgi;
  uwsgi_temp_path /tmp/snowball-qa-nginx-uwsgi;
  scgi_temp_path /tmp/snowball-qa-nginx-scgi;
  client_max_body_size 2m;
  proxy_http_version 1.1;
  proxy_set_header X-Forwarded-Proto https;
  proxy_set_header X-Forwarded-Host \$http_host;
  proxy_set_header X-Real-IP \$remote_addr;
  server {
    listen 127.0.0.1:${QA_HTTPS_PORT} ssl;
    ssl_certificate ${QA_STATE}/certs/server.crt;
    ssl_certificate_key ${QA_STATE}/certs/server.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    location /api/ {
      proxy_pass http://127.0.0.1:${QA_GATEWAY_PORT};
    }
    location / {
      proxy_pass http://127.0.0.1:${QA_WEB_PORT};
    }
  }
}
EOF
mkdir -p /tmp/snowball-qa-nginx-body /tmp/snowball-qa-nginx-proxy \
  /tmp/snowball-qa-nginx-fastcgi /tmp/snowball-qa-nginx-uwsgi /tmp/snowball-qa-nginx-scgi

export STATE_DIR="$QA_STATE/state"
export SNOWBALL_CA_CERT="$QA_STATE/certs/ca.crt"
export SNOWBALL_LAN_IP=192.168.1.1
export SNOWBALL_HTTPS_PORT="$QA_HTTPS_PORT"
export SNOWBALL_HTTP_PORT=18088
export SNOWBALL_ICE_PORT="$QA_ICE_PORT"
export SNOWBALL_UPLINK_RTP_PORT="$QA_UPLINK_PORT"
export SNOWBALL_DOWNLINK_RTP_PORT="$QA_DOWNLINK_PORT"
export SNOWBALL_DEVICE_UPLINK_RTP_PORT="$QA_DEVICE_UPLINK_PORT"
export SNOWBALL_DEVICE_DOWNLINK_RTP_PORT="$QA_DEVICE_DOWNLINK_PORT"
export GATEWAY_LISTEN="127.0.0.1:${QA_GATEWAY_PORT}"
export BROWSER_CONTROLLER_URL="http://127.0.0.1:${QA_CONTROLLER_PORT}"
export QA_CONTROLLER_PORT

node /tmp/snowball-qa-browser-controller.mjs >/tmp/snowball-qa-controller.log 2>&1 &
controller_pid=$!
/usr/local/bin/snowball-gateway >/tmp/snowball-qa-gateway.log 2>&1 &
gateway_pid=$!
WEB_LISTEN_HOST=127.0.0.1 WEB_LISTEN_PORT="$QA_WEB_PORT" \
  node /opt/snowball/services/web-server.mjs >/tmp/snowball-qa-web.log 2>&1 &
web_pid=$!
nginx -c /tmp/snowball-qa-nginx.conf -g 'daemon off;' >/tmp/snowball-qa-nginx.log 2>&1 &
nginx_pid=$!

cleanup() {
  kill "$nginx_pid" "$web_pid" "$gateway_pid" "$controller_pid" 2>/dev/null || true
  wait "$nginx_pid" "$web_pid" "$gateway_pid" "$controller_pid" 2>/dev/null || true
  rm -rf "$QA_STATE" /tmp/snowball-qa-*.mjs /tmp/snowball-qa-nginx.conf
}
trap cleanup EXIT INT TERM

for attempt in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${QA_GATEWAY_PORT}/api/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$gateway_pid" 2>/dev/null; then
    cat /tmp/snowball-qa-gateway.log >&2 || true
    exit 1
  fi
  sleep 0.5
  if [ "$attempt" -eq 60 ]; then
    cat /tmp/snowball-qa-gateway.log >&2 || true
    exit 1
  fi
done

for attempt in $(seq 1 60); do
  if curl -kfsS "https://127.0.0.1:${QA_HTTPS_PORT}/api/auth/status" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$nginx_pid" 2>/dev/null; then
    cat /tmp/snowball-qa-nginx.log /tmp/snowball-qa-nginx-error.log /tmp/snowball-qa-web.log >&2 || true
    exit 1
  fi
  sleep 0.5
  if [ "$attempt" -eq 60 ]; then
    cat /tmp/snowball-qa-nginx.log /tmp/snowball-qa-nginx-error.log /tmp/snowball-qa-web.log >&2 || true
    exit 1
  fi
done

SNOWBALL_TEST_URL="https://127.0.0.1:${QA_HTTPS_PORT}" \
SNOWBALL_TEST_SETUP_CODE="$QA_SETUP_CODE" \
SNOWBALL_TEST_ADMIN_PASSWORD="$QA_ADMIN_PASSWORD" \
node /opt/snowball/tests/browser-smoke-runtime.mjs

printf '%s\n' 'qa_browser_smoke_passed'
