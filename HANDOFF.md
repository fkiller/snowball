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
- **Branch**: `codex/fix-post-bye-ghost-wake` (synced to router remote as `candidate-sync`)
- **Latest Commit**: `705af7b fix(audio): correct PCMA millisecond PTS and fast-drain prevoice queue`
- **Clean working tree**: All changes committed and verified.

---

### Root Cause Analysis & Technical Discoveries

#### 1. `esp_peer` Timestamp Contract (`libpeer_default.a` Disassembly)
- Disassembly of `.text.calc_timestamp` in Espressif's proprietary WebRTC library (`libpeer_default.a`):
  ```assembly
  00000000 <calc_timestamp>:
       c: beqi a2, 8, 38      // If payload_type == 8 (PCMA)
      38: slli a2, a3, 3      // a2 = a3 << 3 (a3 * 8)
      3b: j 46
      40: addx2 a3, a3, a3
      43: slli a2, a3, 4      // If payload_type == 111 (OPUS): a3 * 48
  ```
- **Discovery**: `esp_peer_send_audio` explicitly takes `frame->pts` in **MILLISECONDS**. `calc_timestamp` computes `pts * 8` for PCMA (8000 Hz) or `pts * 48` for Opus (48000 Hz) to obtain the RTP packet timestamp.
- **Flaw in commit `4bd4d1e`**: Passing raw sample counts (256 ticks per frame) as `audio.pts` caused `calc_timestamp` to multiply it by 8, producing **2048 ticks/frame** (8x faster than real-time). GStreamer/Chromium jitter buffers rejected or dropped packets 8x in the future, rendering microphone audio completely silent to ChatGPT in test 10.

#### 2. Pre-Voice Buffer Live Speech Starvation
- In `firmware/esp32-s3-audio/main/media_session.c`:
  - When `uplink_enabled` became true, `prevoice_count` had accumulated ~137 frames (~4.4s of chime/silence).
  - `send_prevoice_audio` paced at 40ms per frame ($137 \times 40\text{ms} = 5.5\text{ seconds}$).
  - During those 5.5 seconds, `replaying_prevoice` was `true`, locking out `send_queued_audio` from sending live microphone audio.
  - Because `MEDIA_AUDIO_QUEUE_DEPTH` was only 8 (256ms), the queue overflowed within 256ms and **all live user speech was dropped**.
  - ChatGPT received only 5.5s of stale chime and silence; by the time live audio unlocked, the user had stopped talking, causing ChatGPT to remain completely silent.

---

### Fixes Applied (Commit `705af7b`)

1. **Strictly Compliant Millisecond PTS**:
   - `audio.pts = uplink_pts_ms;`
   - `uplink_pts_ms += (uint32_t)(audio.size / 8);` (32ms per 256-sample frame).
   - In `esp_peer`, `calc_timestamp` multiplies $32 \times 8 = 256$ ticks, exactly matching RFC 3551 standard RTP clock rate (8000 Hz).
2. **Pre-Voice Backlog Pruning**:
   - In `media_session_enable_uplink`: if `prevoice_count > 15`, set `prevoice_read = prevoice_count - 15;`.
   - Discards stale wake chimes and silence from seconds ago, keeping at most the last 15 frames (~480ms).
3. **Burst Drain of Preserved Speech**:
   - `send_prevoice_audio` drains the 15 preserved frames in a tight burst (<10ms).
   - Once drained, `send_queued_audio` immediately takes over for live streaming without any lockout.
4. **Queue Depth & Overflow Protection**:
   - Increased `MEDIA_AUDIO_QUEUE_DEPTH` from 8 to 32 (1024ms buffer).
   - Implemented `memmove` sliding window in `media_session_push_pcm16k` to keep newest audio even if connection setup is extended.

---

### Deployment & Verification Status

1. **Firmware Built & Flashed**:
   - Compiled with ESP-IDF 5.5 (`idf.py build`) without errors.
   - Flashed via `tools/flash-esp32-windows.ps1 -Port COM3` (NVS `0x9000` strictly preserved).
   - ESP32 booted cleanly, connected to Wi-Fi, synchronized candidates with Gateway, and armed WakeNet.
2. **Serial Tracer Running**:
   - Background daemon logging to `trace-physical.log` on `COM3`.
3. **Automated Tests**:
   - `npm run lint`: PASSED (0 errors).
   - `node --test tests/*.test.mjs`: PASSED (15/15 tests passed).

---

## Exact Next Action

Conduct live physical speech test:
1. Speak *"Hi ESP"*.
2. Listen for the wake chime.
3. Speak a question immediately (e.g. *"What is the distance between the Earth and the Moon?"* or count 1 to 5).
4. Verify ChatGPT Voice responds clearly and answers the question.
5. Have a multi-turn conversation to verify ongoing bi-directional audio.
6. Speak *"Hi ESP"* to end the session.
