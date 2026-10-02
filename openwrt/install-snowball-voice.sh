#!/bin/sh
set -eu

REPOSITORY=${1:-/root/snowball-voice}
DOCKER_INIT=${SNOWBALL_DOCKER_INIT:-/etc/init.d/dockerd}
DOCKER_HOST=${SNOWBALL_DOCKER_HOST:-unix:///var/run/docker.sock}
export DOCKER_HOST
unset DOCKER_CONTEXT

command -v docker >/dev/null 2>&1 || {
    printf '%s\n' 'The normal host Docker CLI is not installed.' >&2
    exit 1
}

if [ -x "$DOCKER_INIT" ]; then
    "$DOCKER_INIT" enable >/dev/null 2>&1 || true
    "$DOCKER_INIT" start >/dev/null 2>&1 || true
fi

ready=false
for _ in $(seq 1 60); do
    if docker info >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 2
done
[ "$ready" = true ] || {
    printf '%s\n' 'The normal host Docker daemon did not become ready.' >&2
    exit 1
}

docker inspect snowball-voice >/dev/null 2>&1 || {
    printf '%s\n' 'snowball-voice is not present in the normal Docker daemon.' >&2
    printf '%s\n' 'Run openwrt/migrate-to-existing-docker.sh first, then run this installer again.' >&2
    exit 1
}

mkdir -p /usr/bin
cp "$REPOSITORY/tools/snowball-voice-start" /usr/bin/snowball-voice-start
cp "$REPOSITORY/openwrt/snowball-voice.init" /etc/init.d/snowball-voice
chmod 0755 /usr/bin/snowball-voice-start /etc/init.d/snowball-voice

# Disable the old project-specific daemon if a previous installation left it
# enabled. Do not remove its data; migration and cleanup remain explicit.
if [ -x /etc/init.d/snowball-voice-dockerd ]; then
    /etc/init.d/snowball-voice-dockerd disable >/dev/null 2>&1 || true
fi

if [ -x "$DOCKER_INIT" ]; then
    "$DOCKER_INIT" enable >/dev/null 2>&1 || true
fi

docker update --restart always snowball-voice >/dev/null
/etc/init.d/snowball-voice enable
/etc/init.d/snowball-voice start

printf '%s\n' 'Snowball now uses the existing host Docker daemon and will start after reboot.'
