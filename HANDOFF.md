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
- **Latest Commit**: `a90273b perf: optimize voice session latency via speculative pre-warm and fast long-polling`
- **Clean working tree**: All changes committed and verified.

### Completed Work Since Initial Handoff
1. **Firmware Latency & Ghost-Wake Elimination**:
   - `command_recognizer.c`: Tuned `COMMAND_TAIL_TIMEOUT_MS` from 1800ms to 600ms; set timeout default command to `SNOWBALL_COMMAND_RESUME`.
   - `speech.c`: Changed initial optimistic command to `SNOWBALL_COMMAND_RESUME`. Added 5.0-second WakeNet suppression check after session activation to prevent ChatGPT's opening greeting from falsely triggering `hi_esp_end`.
   - Built and flashed live to `COM3` via `tools/flash-esp32-windows.ps1` (NVS `0x9000` untouched).
2. **Gateway Speculative Pre-Warming & Fast Long-Polling**:
   - `gateway/main.go`:
     - Added `speculativePrewarmVoice` triggered instantly when `event: "wake"` arrives from enrolled device, launching `/voice/resume` in the background in parallel with WebRTC connection.
     - Added fast long-polling wait (up to 1500ms) on `scheduleDeviceDispatch` done channel in `handleDeviceEvent`, returning `HTTP 200 OK` (`outcome: "executed"`) immediately on first request and eliminating the 500ms client sleep loop.
   - `gateway/devices.go`: Added `getEventResult` to fetch completed event records atomically.
3. **Browser Controller Mutation Observation**:
   - `services/browser-controller.mjs`:
     - Replaced heavy DOM inspection loop in `startVoice()` and `stopVoice()` with Playwright's native `waitForSelector` (<20ms).
     - Made `resumeVoice()` resilient: does not error on fresh chat URLs (`https://chatgpt.com/`), returns immediately if Voice is already active.
4. **Production Deployment to FriendlyWrt Router**:
   - Built candidate image `snowball-voice:candidate-fast` on `SNOWBALL-ROUTER` (`192.168.1.1`).
   - Successfully deployed to production container `snowball-voice` using `tools/deploy-candidate.sh` with image tag `snowball-voice:0.3.2-fast`.
   - Preserved rollback container `snowball-voice-rollback-20260902T232643Z`.
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
