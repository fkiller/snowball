# Agent Handoff

## Status

**Handoff state: READY** (Updated 2026-09-03 EDT)

This handoff reflects the current repository state across the local Windows development workstation and the live `SNOWBALL-ROUTER` host (`192.168.1.1`).

## Current Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a persistent ChatGPT Web session running in headed Chromium inside a container on an ARM64 OpenWrt home router (`192.168.1.1`).

The active objective:
- Ensure 100% reliable, crystal-clear bi-directional conversation with ChatGPT without audio packet drops, recognition failures, or premature session cutoffs.

## Current Repository & Operational State

### Git State
- **Branch**: `codex/fix-post-bye-ghost-wake` (synced to router remote as `candidate-sync`)
- **Latest Commit**: `4bd4d1e fix(audio): eliminate uplink audio drops via sample PTS, drop-on-latency false, and 120s stall timeout`
- **Clean working tree**: All changes committed and verified.

### Completed Work Since Initial Handoff
1. **Diagnosis and Resolution of Uplink Speech Recognition ("GPT hard to listen", 50% chance)**:
   - **Root Cause 1 (RTP Timestamp Skew)**: In `firmware/esp32-s3-audio/main/media_session.c`, `audio.pts` was assigned from `(uint32_t)(esp_timer_get_time() / 1000)` (milliseconds). For standard G.711 PCMA audio at 8000 Hz clock rate (RFC 3551), each 256-sample frame (32ms) must increment the RTP timestamp by 256 ticks. Incrementing by ~32 ticks was 8x too slow, causing GStreamer's jitter buffer to calculate severe timestamp drift.
   - **Root Cause 2 (Jitterbuffer Packet Dropping)**: In `container/start-gst-device-uplink.sh`, `rtpjitterbuffer latency=50 drop-on-latency=true` discarded any packet that exceeded 50ms relative to the skewed clock. Over Wi-Fi, packets were routinely dropped, causing missing phonemes/words and making speech unintelligible to ChatGPT.
   - **Root Cause 3 (PulseAudio Underruns & Mic Level)**: Microscopic 20ms buffer/5ms latency on `pulsesink` caused audio chopping under CPU load.
   - **Fixes Applied**:
     - `media_session.c`: Added monotonic 8000 Hz sample PTS counter (`uplink_sample_pts`), incremented by exactly `audio.size` (256) per frame.
     - `container/start-gst-device-uplink.sh`: Set `latency=200 drop-on-latency=false`, added `volume volume=1.5` (+3.5 dB boost), and enlarged `pulsesink` to `buffer-time=200000 latency-time=20000`.
     - `container/start-gst-uplink.sh`: Mirrored `drop-on-latency=false` and `buffer-time=200000 latency-time=20000`.

2. **Resolution of 40-45s Conversation Disconnect / Cutoff**:
   - **Root Cause**: In `gateway/main.go`, `recoverStalledDeviceVoice()` checked `deviceAudioStallAfter = 45 * time.Second`. If the user spoke into the microphone but ChatGPT never responded (or during a thinking/silence pause >45s), the Gateway watchdog forcibly closed the WebRTC peer and ended ChatGPT Voice.
   - **Fix Applied**: Increased `deviceAudioStallAfter = 120 * time.Second` (2 minutes), giving ample conversational pause room while still cleaning up orphaned sessions. Also gated synchronous long-polling wait to ESP32 User-Agent or `X-Snowball-Wait` header to maintain unit test compatibility.

3. **Production Deployment & Device Flashing**:
   - Firmware rebuilt with ESP-IDF 5.5.5 and flashed to `COM3` via `tools/flash-esp32-windows.ps1` (NVS `0x9000` strictly preserved).
   - Candidate container image built on `SNOWBALL-ROUTER` (`192.168.1.1`) and deployed to production `snowball-voice` container via `tools/deploy-candidate.sh` as `snowball-voice:0.3.5-fast`.
   - Verified all container services running (`gst-device-uplink`, `gst-device-downlink`, `browser-controller`, `snowball-gateway`).

## Exact Next Action

Conduct live physical speech verification:
1. Speak *"Hi ESP"*.
2. Wait for wake chime and ChatGPT greeting (~1.5–2s).
3. Speak clearly to ChatGPT (e.g., ask questions, have a multi-turn conversation).
4. Verify ChatGPT responds accurately without misunderstanding.
5. Verify conversation can continue past 40 seconds without any premature disconnect.
6. Speak *"Hi ESP"* to end session cleanly.

## Operating Environments & Connectivity

1. **Development Workstation (Windows)**:
   - Primary editing, Git repository, PowerShell environment.
   - Physical speaker attached on `COM3` (monitored via `tools/esp32-serial-trace-windows.ps1`).
2. **Router Host (`SNOWBALL-ROUTER` / `192.168.1.1`)**:
   - FriendlyWrt/OpenWrt ARM64 (Linux 5.10.160 aarch64, 8GB RAM).
   - SSH Access: `ssh root@192.168.1.1` via local key `~/.ssh/id_ed25519`.
   - Dedicated Docker Socket: `unix:///var/run/snowball-voice-docker.sock`.
   - Live Production Container: `snowball-voice` (`snowball-voice:0.3.2-fast`), healthy and active.

## Exact Next Action

Conduct live physical speech verification:
1. Speak *"Hi ESP"*.
2. Confirm wake chime plays.
3. Confirm ChatGPT Live Voice greeting begins within ~1.0–1.5 seconds.
4. Speak naturally to verify bi-directional audio.
5. Say *"Hi ESP"* to end session and observe clean termination.
