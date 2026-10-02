#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"
ACTION=${1:-status}
command -v docker >/dev/null 2>&1 || { printf 'Install Docker Engine and Compose first.\n' >&2; exit 1; }
docker info >/dev/null
case "$ACTION" in
    download)
        [ "$(uname -s)" = Linux ] || { printf 'Run inside the Linux VM/host.\n' >&2; exit 1; }
        bash tools/download-gate-image.sh
        ;;
    build)
        docker build --network host -t snowball-voice:0.4.0-alpha.1 .
        ;;
    install)
        [ "$(uname -s)" = Linux ] || { printf 'Install inside the Linux VM/host described in docs/PLATFORMS.md.\n' >&2; exit 1; }
        LAN_IP=${SNOWBALL_LAN_IP:?set SNOWBALL_LAN_IP to the Linux host private IPv4}
        printf '%s\n' "$LAN_IP" | awk -F. 'NF==4 { for(i=1;i<=4;i++) if($i !~ /^[0-9]+$/ || $i>255) exit 1; if($1==10 || ($1==172 && $2>=16 && $2<=31) || ($1==192 && $2==168)) exit 0; exit 1 } NF!=4 { exit 1 }' || { printf 'A private IPv4 address is required.\n' >&2; exit 1; }
        command -v ip >/dev/null || { printf 'Install iproute2 and verify the Linux interface address.\n' >&2; exit 1; }
        ip -4 -o address show | awk -v wanted="$LAN_IP" '{split($4,a,"/"); if(a[1]==wanted) found=1} END{exit !found}' || { printf 'The address is not assigned to this Linux host.\n' >&2; exit 1; }
        if docker container inspect snowball-voice >/dev/null 2>&1; then
            printf 'Existing snowball-voice container found; use the documented upgrade procedure.\n' >&2
            exit 1
        fi
        # Refuse a second Docker context's instance already using the web port.
        command -v ss >/dev/null || { printf 'Install iproute2 (ss) before starting.\n' >&2; exit 1; }
        if ss -lntu | awk '$5 ~ /:(8088|8443|49000)$/ {found=1} END{exit !found}'; then
            printf 'A Snowball LAN port is occupied; do not start a second production-port instance.\n' >&2
            exit 1
        fi
        export SNOWBALL_LAN_IP
        docker compose config --quiet
        docker compose up -d
        printf 'Install started. Follow docs/GETTING_STARTED.md for HTTPS trust, admin setup, and ChatGPT login.\n'
        ;;
    status)
        docker ps --filter name='^snowball-voice$' --format '{{.Names}} {{.Image}} {{.Status}}'
        ;;
    *) printf 'Usage: tools/gate-start.sh download|build|install|status\n' >&2; exit 2 ;;
esac
