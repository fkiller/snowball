#!/bin/sh
set -eu

# Controlled production swap. This is intentionally opt-in: a development
# image is never deployed just because it exists locally.
CONTAINER=${SNOWBALL_CONTAINER:-snowball-voice}
CANDIDATE=${SNOWBALL_CANDIDATE_IMAGE:?set SNOWBALL_CANDIDATE_IMAGE}
DEPLOY_IMAGE=${SNOWBALL_DEPLOY_IMAGE:?set SNOWBALL_DEPLOY_IMAGE}
APPROVAL=${SNOWBALL_DEPLOY_APPROVAL:-}
LAN_IP=${SNOWBALL_LAN_IP:-192.168.1.1}
HTTP_PORT=${SNOWBALL_HTTP_PORT:-8088}
HTTPS_PORT=${SNOWBALL_HTTPS_PORT:-8443}
ICE_PORT=${SNOWBALL_ICE_PORT:-49000}
UPLINK_PORT=${SNOWBALL_UPLINK_RTP_PORT:-49001}
DOWNLINK_PORT=${SNOWBALL_DOWNLINK_RTP_PORT:-49002}
DEVICE_UPLINK_PORT=${SNOWBALL_DEVICE_UPLINK_RTP_PORT:-49003}
DEVICE_DOWNLINK_PORT=${SNOWBALL_DEVICE_DOWNLINK_RTP_PORT:-49004}
DATA_SOURCE=
ROLLBACK=
SWAPPED=false
TEMP_DIR=$(mktemp -d /tmp/snowball-deploy.XXXXXX)

cleanup() {
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

rollback() {
    set +e
    if [ "$SWAPPED" = true ]; then
        docker stop --time 30 "$CONTAINER" >/dev/null 2>&1 || true
        docker rm "$CONTAINER" >/dev/null 2>&1 || true
        docker rename "$ROLLBACK" "$CONTAINER" >/dev/null 2>&1 || true
        docker start "$CONTAINER" >/dev/null 2>&1 || true
        for _ in $(seq 1 60); do
            state=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER" 2>/dev/null || true)
            [ "$state" = healthy ] && break
            sleep 2
        done
    fi
}

fail() {
    printf 'candidate deployment failed: %s\n' "$*" >&2
    if [ "$SWAPPED" = true ]; then
        rollback
        printf 'rollback target restored as %s\n' "$CONTAINER" >&2
    fi
    exit 1
}

[ "$APPROVAL" = YES ] || {
    printf 'refusing deployment: set SNOWBALL_DEPLOY_APPROVAL=YES after an explicit maintenance approval\n' >&2
    exit 2
}

docker inspect "$CONTAINER" >/dev/null 2>&1 || fail "production container is missing"
docker inspect "$CANDIDATE" >/dev/null 2>&1 || fail "candidate image is missing: $CANDIDATE"

current_image=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")
container_state=$(docker inspect -f '{{.State.Status}}' "$CONTAINER")
health_state=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CONTAINER")
[ "$container_state" = running ] || fail "production container is not running ($container_state)"
[ "$health_state" = healthy ] || fail "production container is not healthy ($health_state)"

DATA_SOURCE=$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Source}}{{end}}{{end}}' "$CONTAINER")
[ -n "$DATA_SOURCE" ] || fail "production /data mount could not be resolved"

stamp=$(date -u '+%Y%m%dT%H%M%SZ')
ROLLBACK="${CONTAINER}-rollback-${stamp}"
if docker inspect "$ROLLBACK" >/dev/null 2>&1; then
    fail "rollback container already exists: $ROLLBACK"
fi

docker tag "$CANDIDATE" "$DEPLOY_IMAGE"
printf 'deploying %s as %s; preserving %s (%s)\n' "$CANDIDATE" "$DEPLOY_IMAGE" "$current_image" "$ROLLBACK"

docker stop --time 30 "$CONTAINER" >/dev/null || fail "could not stop production container"
if ! docker rename "$CONTAINER" "$ROLLBACK"; then
    docker start "$CONTAINER" >/dev/null 2>&1 || true
    fail "could not retain rollback container"
fi
SWAPPED=true

docker create \
    --name "$CONTAINER" \
    --hostname SNOWBALL-ROUTER \
    --network host \
    --cgroupns host \
    --restart unless-stopped \
    --read-only \
    --shm-size 1g \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --tmpfs '/tmp:rw,nosuid,nodev,size=1g,mode=1777' \
    --tmpfs '/run:rw,nosuid,nodev,size=32m,mode=755' \
    --user pwuser \
    --mount "type=bind,src=$DATA_SOURCE,dst=/data" \
    -e "SNOWBALL_LAN_IP=$LAN_IP" \
    -e "SNOWBALL_HTTP_PORT=$HTTP_PORT" \
    -e "SNOWBALL_HTTPS_PORT=$HTTPS_PORT" \
    -e "SNOWBALL_ICE_PORT=$ICE_PORT" \
    -e "SNOWBALL_UPLINK_RTP_PORT=$UPLINK_PORT" \
    -e "SNOWBALL_DOWNLINK_RTP_PORT=$DOWNLINK_PORT" \
    -e "SNOWBALL_DEVICE_UPLINK_RTP_PORT=$DEVICE_UPLINK_PORT" \
    -e "SNOWBALL_DEVICE_DOWNLINK_RTP_PORT=$DEVICE_DOWNLINK_PORT" \
    -e 'CHATGPT_URL=https://chatgpt.com/' \
    -e 'TZ=America/New_York' \
    "$DEPLOY_IMAGE" || fail "could not create candidate container"

docker start "$CONTAINER" >/dev/null || fail "candidate container did not start"

healthy=false
for _ in $(seq 1 90); do
    state=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER" 2>/dev/null || true)
    if [ "$state" = healthy ]; then
        healthy=true
        break
    fi
    sleep 2
done
[ "$healthy" = true ] || {
    docker logs --tail 120 "$CONTAINER" >&2 || true
    fail "candidate did not become healthy"
}

health=$(curl -skS --max-time 5 "https://$LAN_IP:$HTTPS_PORT/api/health" || true)
auth=$(curl -skS --max-time 5 "https://$LAN_IP:$HTTPS_PORT/api/auth/status" || true)
admin_code=$(curl -skS --max-time 5 -o "$TEMP_DIR/admin.html" -w '%{http_code}' "https://$LAN_IP:$HTTPS_PORT/admin" || true)
console_code=$(curl -skS --max-time 5 -o /dev/null -w '%{http_code}' "https://$LAN_IP:$HTTPS_PORT/console/" || true)
controller=$(docker exec "$CONTAINER" curl -fsS --max-time 5 http://127.0.0.1:3100/status 2>/dev/null || true)

printf '%s\n' "$health" | grep -q '"ok":true' || fail "health endpoint failed: $health"
printf '%s\n' "$auth" | grep -q '"setupRequired":false' || fail "authentication state changed: $auth"
[ "$admin_code" = 200 ] || fail "admin returned HTTP $admin_code"
[ "$console_code" = 401 ] || fail "Browser Console gate returned HTTP $console_code"
printf '%s\n' "$controller" | grep -q '"state":"ready"' || fail "browser controller is not ready: $controller"
printf '%s\n' "$controller" | grep -q '"authenticated":true' || fail "ChatGPT browser session is not authenticated: $controller"
printf '%s\n' "$controller" | grep -q '"voiceActive":false' || fail "Voice did not start idle: $controller"

printf 'candidate deployment verified: image=%s health=%s auth=%s admin=%s console=%s rollback=%s\n' \
    "$DEPLOY_IMAGE" "$health" "$auth" "$admin_code" "$console_code" "$ROLLBACK"
