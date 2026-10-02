#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
IMAGE=${SNOWBALL_QA_IMAGE:-snowball-voice:test}
ARTIFACT_DIR=${SNOWBALL_TEST_OUTPUT:-$ROOT_DIR/artifacts/qa}
mkdir -p "$ARTIFACT_DIR"
# Retain only synthetic QA screenshots in a new private directory. Give the
# capability-free container its host owner's group for writing the bind mount.
RUN_ARTIFACT_DIR=$(mktemp -d "$ARTIFACT_DIR/run.XXXXXX")
chmod g+rwx "$RUN_ARTIFACT_DIR"

# The Gateway requires an assigned private IPv4. CI runners do not have the
# production router address; detect a private host interface for test ports.
QA_LAN_IP=${SNOWBALL_QA_LAN_IP:-$(ip -4 -o address show scope global | awk '{split($4,a,"/"); split(a[1],o,"."); if(o[1]==10 || (o[1]==172 && o[2]>=16 && o[2]<=31) || (o[1]==192 && o[2]==168)) {print a[1]; exit}}')}
[ -n "$QA_LAN_IP" ] || { printf 'Set SNOWBALL_QA_LAN_IP to an assigned private host IPv4.\n' >&2; exit 1; }

# The test runner is the only repository file copied into the disposable
# container. No source tree, /data volume, browser profile, or USB device is
# shared with the production router container.
docker run --rm --network host --user 0 --shm-size 256m \
  --group-add "$(id -g)" \
  -e SNOWBALL_QA_LAN_IP="$QA_LAN_IP" \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -v "$ROOT_DIR/tools/qa-browser-smoke-runner.sh:/opt/qa-browser-smoke-runner.sh:ro" \
  -v "$ROOT_DIR/tests/browser-smoke.mjs:/opt/snowball/tests/browser-smoke-runtime.mjs:ro" \
  -v "$RUN_ARTIFACT_DIR:/artifacts" \
  --entrypoint /opt/qa-browser-smoke-runner.sh "$IMAGE"
