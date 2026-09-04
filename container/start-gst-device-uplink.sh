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
    udpsrc address=127.0.0.1 port="${SNOWBALL_DEVICE_UPLINK_RTP_PORT}" \
      caps="application/x-rtp,media=audio,encoding-name=PCMA,clock-rate=8000,channels=1,payload=8" \
    ! rtpjitterbuffer latency=50 drop-on-latency=true \
    ! rtppcmadepay ! alawdec ! audioconvert ! audioresample \
    ! audio/x-raw,rate=48000,channels=1 \
    ! volume volume=1.5 \
    ! pulsesink device=chatgpt_mic_sink sync=false async=false buffer-time=20000 latency-time=5000 &
  child=$!
  while [[ -f "${active}" ]] && kill -0 "${child}" 2>/dev/null; do sleep 0.25; done
  kill "${child}" 2>/dev/null || true
  wait "${child}" 2>/dev/null || true
  child=
done
