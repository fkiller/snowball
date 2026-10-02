#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/wait-for-pulse.sh
active=/tmp/snowball-device-media.active
child=
cleanup() {
  if [[ -n "${child}" ]]; then
    kill "${child}" 2>/dev/null || true
    wait "${child}" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 0' INT TERM
while true; do
  while [[ ! -f "${active}" ]]; do sleep 0.25; done
  gst-launch-1.0 -q \
    pulsesrc device=chatgpt_output_sink.monitor do-timestamp=true buffer-time=200000 latency-time=20000 \
    ! audioconvert ! audioresample ! audio/x-raw,rate=8000,channels=1 \
    ! alawenc ! rtppcmapay pt=8 min-ptime=20000000 max-ptime=20000000 \
    ! udpsink host=127.0.0.1 port="${SNOWBALL_DEVICE_DOWNLINK_RTP_PORT}" sync=false async=false &
  child=$!
  while [[ -f "${active}" ]] && kill -0 "${child}" 2>/dev/null; do sleep 0.25; done
  kill "${child}" 2>/dev/null || true
  wait "${child}" 2>/dev/null || true
  child=
done
