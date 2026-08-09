#!/usr/bin/env bash
set -euo pipefail
/opt/snowball/container/wait-for-pulse.sh
exec gst-launch-1.0 -q \
  udpsrc address=127.0.0.1 port="${SNOWBALL_UPLINK_RTP_PORT}" \
    caps="application/x-rtp,media=audio,encoding-name=OPUS,clock-rate=48000,channels=1,payload=111" \
  ! rtpjitterbuffer latency=35 drop-on-latency=true \
  ! rtpopusdepay ! opusdec ! audioconvert ! audioresample \
  ! audio/x-raw,rate=48000,channels=1 \
  ! pulsesink device=chatgpt_mic_sink sync=false
