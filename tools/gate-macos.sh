#!/bin/sh
set -eu
HOST=${1:?usage: gate-macos.sh user@linux-vm /absolute/source/path build|install|status [lan-ip]}
REMOTE=${2:?provide the absolute source path in the Linux VM}
ACTION=${3:-status}
LAN_IP=${4:-}
case "$HOST" in -*|*[!A-Za-z0-9_.@-]*) printf 'Invalid SSH host.\n' >&2; exit 1 ;; esac
case "$REMOTE" in /*) ;; *) printf 'Remote source path must be absolute.\n' >&2; exit 1 ;; esac
case "$REMOTE" in *[!A-Za-z0-9_./-]*) printf 'Unsupported remote path characters.\n' >&2; exit 1 ;; esac
case "$ACTION" in build|install|status) ;; *) printf 'Invalid action.\n' >&2; exit 1 ;; esac
case "$LAN_IP" in *[!0-9.]*) printf 'Invalid LAN IP.\n' >&2; exit 1 ;; esac
[ "$ACTION" != install ] || [ -n "$LAN_IP" ] || { printf 'Provide the Linux VM private LAN IPv4.\n' >&2; exit 1; }
ssh "$HOST" "cd '$REMOTE' && SNOWBALL_LAN_IP='$LAN_IP' sh tools/gate-start.sh $ACTION"
