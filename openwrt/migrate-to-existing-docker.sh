#!/bin/sh
set -eu
umask 077

# Explicit, offline state transfer. Never run both production-port containers
# together, and never copy a live Chromium database. Old state remains intact.
CONTAINER=${SNOWBALL_CONTAINER:-snowball-voice}
OLD_HOST=${SNOWBALL_OLD_DOCKER_HOST:-unix:///var/run/snowball-voice-docker.sock}
NEW_HOST=${SNOWBALL_DOCKER_HOST:-unix:///var/run/docker.sock}
VOLUME=${SNOWBALL_VOLUME:-snowball-voice_snowball-voice-data}
DOCKER=${SNOWBALL_DOCKER_BIN:-docker}
TEMP_ROOT=${SNOWBALL_MIGRATE_TMP_ROOT:-/mnt/sdcard}
OLD_STOPPED=false
NEW_CREATED=false
NEW_VOLUME_CREATED=false
COMPLETE=false
WORK=

fail() { printf 'Snowball migration failed: %s\n' "$*" >&2; exit 1; }
[ "${SNOWBALL_MIGRATE_APPROVAL:-}" = YES ] || fail 'set SNOWBALL_MIGRATE_APPROVAL=YES after maintenance approval'
[ "$(id -u)" = 0 ] || fail 'run as root on the OpenWrt host'
[ "$OLD_HOST" != "$NEW_HOST" ] || fail 'old and new Docker sockets must differ'
case "$OLD_HOST:$NEW_HOST" in unix://*:unix://*) ;; *) fail 'both daemons must use local Unix sockets' ;; esac
command -v "$DOCKER" >/dev/null 2>&1 || fail 'Docker CLI missing'
command -v jq >/dev/null 2>&1 || fail 'jq is required to preserve configuration'
unset DOCKER_CONTEXT
old() { DOCKER_HOST="$OLD_HOST" "$DOCKER" "$@"; }
new() { DOCKER_HOST="$NEW_HOST" "$DOCKER" "$@"; }

cleanup() {
    result=$?
    trap - EXIT HUP INT TERM
    if [ "$COMPLETE" != true ]; then
        if [ "$NEW_CREATED" = true ]; then
            new stop --time 30 "$CONTAINER" >/dev/null 2>&1 || true
            new rm "$CONTAINER" >/dev/null 2>&1 || true
        fi
        if [ "$NEW_VOLUME_CREATED" = true ]; then new volume rm "$VOLUME" >/dev/null 2>&1 || true; fi
        if [ "$OLD_STOPPED" = true ]; then
            if old start "$CONTAINER" >/dev/null 2>&1; then
                printf 'Old container restarted; its original data was preserved.\n' >&2
            else
                printf 'URGENT: automatic rollback failed; start the old container using its original Docker socket.\n' >&2
            fi
        fi
    fi
    if [ -n "$WORK" ] && [ -d "$WORK" ]; then rm -rf "$WORK"; fi
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

old info >/dev/null 2>&1 || fail 'old daemon unavailable'
new info >/dev/null 2>&1 || fail 'existing host daemon unavailable; start/verify it separately'
old_id=$(old info --format '{{.ID}}')
new_id=$(new info --format '{{.ID}}')
[ -n "$old_id" ] && [ -n "$new_id" ] && [ "$old_id" != "$new_id" ] || fail 'sockets resolve to the same or unidentified daemon'
new inspect "$CONTAINER" >/dev/null 2>&1 && fail 'target container already exists; refusing overwrite'
new volume inspect "$VOLUME" >/dev/null 2>&1 && fail 'target volume already exists; refusing overwrite'
WORK=$(mktemp -d "$TEMP_ROOT/snowball-migrate.XXXXXX")
old inspect "$CONTAINER" >"$WORK/container.json"
jq -e '.[0] | .State.Running == true and .State.Health.Status == "healthy" and .HostConfig.NetworkMode == "host" and .Config.User == "pwuser" and ([.Mounts[] | select(.Destination == "/data")] | length) == 1 and (.Mounts | length) == 1' "$WORK/container.json" >/dev/null || fail 'unsupported or unhealthy source configuration'
source_data=$(jq -r '.[0].Mounts[] | select(.Destination == "/data") | .Source' "$WORK/container.json")
case "$source_data" in /*/_data) ;; *) fail 'source is not a Docker-managed data directory' ;; esac
[ -d "$source_data" ] || fail 'source data unavailable'
image_id=$(jq -r '.[0].Image' "$WORK/container.json")
hostname=$(jq -r '.[0].Config.Hostname' "$WORK/container.json")
jq -r '.[0].Config.Env[]' "$WORK/container.json" >"$WORK/environment"
old save "$image_id" >"$WORK/image.tar"
new load <"$WORK/image.tar" >/dev/null
new image inspect "$image_id" >/dev/null || fail 'image transfer/digest verification failed'

# The downtime starts here. Copy only after all original processes have exited.
old stop --time 30 "$CONTAINER" >/dev/null || fail 'cannot stop old container'
OLD_STOPPED=true
new volume create "$VOLUME" >/dev/null
NEW_VOLUME_CREATED=true
target_data=$(new volume inspect -f '{{.Mountpoint}}' "$VOLUME")
case "$target_data" in /*/_data) ;; *) fail 'unexpected target volume path' ;; esac
[ "$target_data" != "$source_data" ] && [ -d "$target_data" ] || fail 'unsafe target data directory'
[ -z "$(find "$target_data" -mindepth 1 -maxdepth 1 -print -quit)" ] || fail 'target volume is not empty'
# Separate archives make failures visible in POSIX sh (no hidden pipeline error).
tar -C "$source_data" -cf "$WORK/data.tar" .
tar -C "$target_data" -xf "$WORK/data.tar"

new create --name "$CONTAINER" --hostname "$hostname" --network host \
    --cgroupns host --restart unless-stopped --read-only --shm-size 1g \
    --cap-drop ALL --security-opt no-new-privileges:true \
    --tmpfs '/tmp:rw,nosuid,nodev,size=1g,mode=1777' \
    --tmpfs '/run:rw,nosuid,nodev,size=32m,mode=755' --user pwuser \
    --mount "type=volume,src=$VOLUME,dst=/data" --env-file "$WORK/environment" \
    "$image_id" >/dev/null
NEW_CREATED=true
new start "$CONTAINER" >/dev/null
healthy=false
for _ in $(seq 1 90); do
    state=$(new inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$CONTAINER")
    if [ "$state" = healthy ]; then healthy=true; break; fi
    [ "$state" != exited ] || break
    sleep 2
done
[ "$healthy" = true ] || fail 'replacement did not become healthy'
new exec "$CONTAINER" curl -fsS --max-time 5 http://127.0.0.1:8080/api/auth/status >"$WORK/auth.json"
jq -e '.setupRequired == false' "$WORK/auth.json" >/dev/null || fail 'administrator state was not preserved'
new exec "$CONTAINER" curl -fsS --max-time 5 http://127.0.0.1:3100/status >"$WORK/browser.json"
jq -e '.authenticated == true and .state == "ready" and .voiceActive == false' "$WORK/browser.json" >/dev/null || fail 'browser session was not preserved or is not idle'

COMPLETE=true
printf 'Migration verified. Old stopped container and data remain on %s for rollback.\n' "$OLD_HOST"
printf 'Install boot integration separately after administrator/console/media acceptance.\n'
