#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/wait-for-pulse.sh
exec gst-launch-1.0 -q \
  pulsesrc device=chatgpt_output_sink.monitor do-timestamp=true \
  ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 \
  ! opusenc bitrate=48000 frame-size=20 inband-fec=true \
  ! rtpopuspay pt=111 \
  ! udpsink host=127.0.0.1 port="${SNOWBALL_DOWNLINK_RTP_PORT}" sync=false async=false
