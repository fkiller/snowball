#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
IMAGE=${SNOWBALL_QA_IMAGE:-snowball-voice:candidate-sync-20260821}
ARTIFACT_DIR=${SNOWBALL_TEST_OUTPUT:-$ROOT_DIR/artifacts/qa}
mkdir -p "$ARTIFACT_DIR"

# The test runner is the only repository file copied into the disposable
# container. No source tree, /data volume, browser profile, or USB device is
# shared with the production router container.
docker run --rm --network host --user 0 --shm-size 256m \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -v "$ROOT_DIR/tools/qa-browser-smoke-runner.sh:/opt/qa-browser-smoke-runner.sh:ro" \
  -v "$ROOT_DIR/tests/browser-smoke.mjs:/opt/snowball/tests/browser-smoke-runtime.mjs:ro" \
  -v "$ARTIFACT_DIR:/artifacts" \
  --entrypoint /opt/qa-browser-smoke-runner.sh "$IMAGE"
