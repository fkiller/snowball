# Agent Handoff

## Status

**Handoff state: READY** (Updated 2026-09-03 EDT)

This handoff reflects the current repository state across the local Windows development workstation and the live `SNOWBALL-ROUTER` host (`192.168.1.1`).

## Current Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a persistent ChatGPT Web session running in headed Chromium inside a container on an ARM64 OpenWrt home router (`192.168.1.1`).

The active objective:
- Ensure 100% reliable, crystal-clear bi-directional conversation with ChatGPT without audio packet drops, recognition failures, or premature session cutoffs.

---

## Current Repository & Operational State

### Git State
- **Branch**: `codex/fix-post-bye-ghost-wake` (synced to router remote as `candidate-test`)
- **Latest Commit**: `c677918 fix(audio): restore 50ms drop-on-latency jitterbuffer and low-latency pulsesink`
- **Clean working tree**: All changes committed and verified.

---

### Test Findings & Transcript Analysis (Test 11)

In Test 11, physical testing confirmed that the PTS fix and pre-voice buffer changes worked:
- Connection delay was only 2–3s.
- Bi-directional audio was functional, clean, and recognized accurately.
- ChatGPT and the user held an 8-turn live conversation in Korean:
  - User: *"네, 좋아요 하나"*
  - ChatGPT: *"three."*
  - User: *"셋"*
  - ChatGPT: *"다섯."*
  - User: *"five"*
  - ChatGPT: *"일곱."*
  - User: *"뭔 소리야. 내가 내가 셋 하지 않았어, 셋? 야, 인마. 니가 혼자서 막 가면 어떡해"*
  - ChatGPT: *"아, 네, 네, 제가 조금 성급했네요. 다시 천천히 맞춰볼까요? 동원님이 하나 하시면, 제가 셋, 이렇게 한 템포씩 번갈아 가볼게요."*

#### Symptoms Observed:
1. **Accumulating Lag ("slowly lagging")**: Over the 50s session, speech recognition and response latency grew steadily.
2. **Delayed Interruption**: When the user jumped in to interrupt, ChatGPT did not stop speaking immediately.
3. **Turn Dropping After ~50s**: The conversation stopped progressing after ~50s.

#### Root Cause:
- In commit `4bd4d1e`, `container/start-gst-device-uplink.sh` had been speculatively modified:
  - `latency=200 drop-on-latency=false`
  - `buffer-time=200000 latency-time=20000`
- **Why `drop-on-latency=false` caused the lag**:
  - GStreamer's `rtpjitterbuffer` with `drop-on-latency=false` will **never** drop late packets to re-sync to real time.
  - Any Wi-Fi packet jitter or slight clock drift between the ESP32 hardware and the router clock permanently pushed the playout delay higher without ever recovering.
  - Over 50 seconds, buffer delay grew by several seconds.
  - Because of this multi-second delay, user interruptions ("셋! 야 인마...") took seconds to reach Chromium, preventing ChatGPT from cutting off in real time and eventually causing turn-taking desynchronization.

---

### Fixes Applied (Commit `c677918` & Container `0.3.6-fast`)

1. **Restored 50ms Real-Time Cap on Uplink Jitter Buffer**:
   - `rtpjitterbuffer latency=50 drop-on-latency=true`
   - Strict real-time pacing: packets arriving later than 50ms are dropped rather than delaying all subsequent conversation turns.
2. **Restored Low-Latency PulseAudio Sink**:
   - `buffer-time=20000 latency-time=5000` (20ms buffer, 5ms latency).
   - Total pipeline latency reduced from ~420ms+ to **~70ms** fixed.
3. **Preserved Clean Volume Boost**:
   - Retained `volume volume=1.5` (+3.5 dB) for microphone clarity.
4. **Deployed `snowball-voice:0.3.6-fast` on Router**:
   - Deployed via `tools/deploy-candidate.sh` with automatic rollback protection.
   - Container health, browser controller, and ChatGPT Voice readiness verified.

---

### Deployment & Verification Status

1. **Firmware on ESP32**:
   - Commit `705af7b` running on `COM3`.
   - ESP32 in `VOICE_STATE_IDLE` with serial tracer active.
2. **Router Container**:
   - Running `snowball-voice:0.3.6-fast` with 50ms drop-on-latency real-time pipeline.
   - ChatGPT session authenticated, healthy, and ready for voice.

---

## Exact Next Action

Conduct live physical verification:
1. Say *"Hi ESP"*.
2. Wait for the wake chime.
3. Talk with ChatGPT (e.g. count numbers or interrupt while it is speaking).
4. Verify:
   - Zero creeping lag throughout the entire conversation.
   - ChatGPT immediately stops talking when you jump in to interrupt.
   - Conversational turn-taking remains responsive past 50 seconds.
5. Say *"Hi ESP"* to cleanly end the session.
