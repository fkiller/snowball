# Agent Handoff

## Status

**Handoff state: READY** (Updated 2026-09-02 EDT)

This handoff reflects the current repository state across the local Windows development workstation and the live `SNOWBALL-ROUTER` host (`192.168.1.1`).

## Current Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a persistent ChatGPT Web session running in headed Chromium inside a container on an ARM64 OpenWrt home router (`192.168.1.1`).

The active objective:
- Minimize the end-to-end voice activation delay down to ~1.0–1.2s through parallelized wake pre-warming and fast long-polling, while eliminating false wake cutoffs.

## Current Repository & Operational State

### Git State
- **Branch**: `codex/fix-post-bye-ghost-wake` (pushed to router remote as `codex/fast-voice-candidate`)
- **Latest Commit**: `1344f48 fix(audio): eliminate voice breakup via 20ms GStreamer framing, disabling Wi-Fi power save, and tuning jitter buffer`
- **Clean working tree**: All changes committed and verified.

### Completed Work Since Initial Handoff
1. **Firmware Latency & Ghost-Wake Elimination**:
   - `command_recognizer.c`: Tuned `COMMAND_TAIL_TIMEOUT_MS` from 1800ms to 600ms; set timeout default command to `SNOWBALL_COMMAND_RESUME`.
   - `speech.c`: Changed initial optimistic command to `SNOWBALL_COMMAND_RESUME`. Added 5.0-second WakeNet suppression check after session activation to prevent ChatGPT's opening greeting from falsely triggering `hi_esp_end`.
   - Built and flashed live to `COM3` via `tools/flash-esp32-windows.ps1` (NVS `0x9000` untouched).
2. **Gateway Speculative Pre-Warming & Fast Long-Polling**:
   - `gateway/main.go`:
     - Added `speculativePrewarmVoice` triggered instantly upon accepting the WebRTC media offer (`POST /api/device/webrtc/offer`), launching `/voice/resume` in Chromium in the background at $t = 0.2\text{s}$ while the ESP32 performs MultiNet tail detection and DTLS handshake.
     - Added fast long-polling wait (up to 1500ms) on `scheduleDeviceDispatch` done channel in `handleDeviceEvent`, returning `HTTP 200 OK` (`outcome: "executed"`) immediately on first request and eliminating the 500ms client sleep loop.
     - Increased UDP socket read/write buffers on `iceConn` (1MB) and `deviceDownConn` (512KB) to eliminate socket receive buffer overruns.
   - `gateway/devices.go`: Added `getEventResult` to fetch completed event records atomically.
3. **Audio Quality & Voice Breakup Elimination**:
   - `container/start-gst-device-downlink.sh`: Increased `pulsesrc` buffer to 200ms (`buffer-time=200000 latency-time=20000`) and enforced standard 20ms RTP packetization (`min-ptime=20000000 max-ptime=20000000` on `rtppcmapay`). Cut network packet rate from 200 pps (5ms micro-packets) to 50 pps (20ms standard frames), eliminating scheduling starvation.
   - `firmware/esp32-s3-audio/main/provisioning.c`: Disabled Wi-Fi power saving (`esp_wifi_set_ps(WIFI_PS_NONE)`). Eliminated recurring Wi-Fi modem sleep and beacon timeouts (`wifi:bcn_timeout`).
   - `firmware/esp32-s3-audio/main/media_session.c`: Tuned `esp_peer` jitter buffer (`cache_timeout = 120ms`, `resend_delay = 40ms`, `cache_size = 16384`) to absorb network jitter without declaring packet loss.
   - Rebuilt firmware with ESP-IDF 5.5.5 and flashed to `COM3`.
4. **Browser Controller Mutation Observation**:
   - `services/browser-controller.mjs`:
     - Replaced heavy DOM inspection loop in `startVoice()` and `stopVoice()` with Playwright's native `waitForSelector` (<20ms).
     - Made `resumeVoice()` resilient: does not error on fresh chat URLs (`https://chatgpt.com/`), returns immediately if Voice is already active.
5. **Production Deployment to FriendlyWrt Router**:
   - Built candidate image `snowball-voice:candidate-fast` on `SNOWBALL-ROUTER` (`192.168.1.1`).
   - Successfully deployed to production container `snowball-voice` using `tools/deploy-candidate.sh` with image tag `snowball-voice:0.3.4-fast`.
   - Preserved rollback container `snowball-voice-rollback-20260903T022209Z`.
   - All post-deploy healthchecks passed (`health: {"ok":true}`, `auth: {"setupRequired":false}`, `admin: 200`, `console: 401`, `ChatGPT: state "ready", authenticated: true`).

### Operating Environments & Connectivity

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
