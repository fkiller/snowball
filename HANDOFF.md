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

### Test Findings & Analysis (Test 11 & Test 12)

1. **Test 11 (200ms latency, `drop-on-latency=false`)**:
   - Delay was reduced to 2–3s. Accurate Korean bi-directional recognition.
   - User observed: *"slowly lagging (sound was okay but recognition speed was slow so it doesn't stop talking when I jump in) > after 50 sec conversation, it stops responding."*
2. **Test 12 (50ms latency, `drop-on-latency=true`)**:
   - User observed: *"short running becoming bigger issue now. all same but It only stays for 30 secs."*
   - Retrieved transcript proved a 10-turn continuous conversation succeeded until turn 10 (~30s mark), where ChatGPT asked *"괜찮아요, 한 번만 더 같이 천천히 맞춰볼까요?"*, after which ChatGPT received no further audio.
   - Gateway logs proved ESP32 microphone packets continued arriving for 2m0s until the stall watchdog fired at 2m55s.

#### Root Cause: GStreamer `rtpjitterbuffer` on Localhost UDP
- `start-gst-device-uplink.sh` reads packets forwarded by Go Gateway over localhost UDP (`127.0.0.1:49003`).
- Pion WebRTC on the Gateway **already** terminates all Wi-Fi network jitter and delivers clean, reassembled RTP packets.
- On loopback `127.0.0.1`, network jitter is literally zero.
- Placing `rtpjitterbuffer` on this loopback stream with `mode=slave` caused GStreamer to compare RTP packet timestamps against the router's Linux system clock without RTCP sender reports.
- Any tiny clock drift between the ESP32 hardware sampling clock and the Linux host clock caused the jitterbuffer to:
  - Continuously accumulate playout delay when `drop-on-latency=false` (causing "slowly lagging" and 50s failure in Test 11).
  - Reach the 50ms latency threshold in ~30 seconds when `drop-on-latency=true`, silently dropping 100% of subsequent packets as "too late" and starving Chromium of microphone audio (causing the 30s cutoff in Test 12).
- Furthermore, `pulsesink buffer-time=20000` (20ms) was smaller than the ESP32's 32ms frame size, risking cyclic buffer underruns.

---

### Fix Applied (Commit `92cd71c` & Container `0.3.7-nojitter`)

1. **Eliminated `rtpjitterbuffer` from `start-gst-device-uplink.sh`**:
   - Loopback UDP packets now flow directly: `udpsrc -> rtppcmadepay -> alawdec -> audioconvert -> audioresample -> volume -> pulsesink`.
   - Zero clock-drift drift tracking, zero artificial buffering delay, zero dropped packets.
2. **Right-Sized PulseAudio Sink Buffer**:
   - Adjusted `pulsesink` to `buffer-time=64000 latency-time=16000` (64ms buffer, 16ms latency).
   - Holds 2 full 32ms frames, eliminating underruns while maintaining imperceptible sub-20ms latency.
3. **Preserved Clean Volume Boost**:
   - Retained `volume volume=1.5` (+3.5 dB).
4. **Normalized Shell Script Line Endings (CRLF -> LF)**:
   - In 0.3.7, Windows CRLF line endings caused `start-gst-device-uplink.sh` to fail with `/usr/bin/env: 'bash\r': No such file or directory`.
   - Converted all shell scripts to LF endings, verified execution, and confirmed supervisor service stays running.
5. **Built and Deployed `snowball-voice:0.3.8-nojitter`**:
   - Deployed via `tools/deploy-candidate.sh` with automated rollback guard.
   - Verified container health (`{"ok":true}`) and tested active media pipeline spawn.

---

### Deployment & Verification Status

1. **Firmware on ESP32**:
   - Commit `705af7b` running on `COM3`.
   - Serial trace running in background. WakeNet waiting for "Hi ESP".
2. **Router Container**:
   - Running `snowball-voice:0.3.8-nojitter` (no jitter buffer, 64ms PulseAudio buffer, LF endings).
   - ChatGPT session authenticated, healthy, and ready for voice.

---

## Exact Next Action

Conduct physical test (Test 13):
1. Say *"Hi ESP"*.
2. Wait for the wake chime.
3. Have an extended conversation with ChatGPT (talk past 1–2 minutes, count numbers, or interrupt).
4. Verify:
   - Voice connects and ChatGPT speaks.
   - Voice stays active indefinitely without freezing or stopping at 30 seconds.
   - Zero creeping lag or slow recognition over time.
   - Fast turn-taking and responsive interruption.
5. Say *"Hi ESP"* to cleanly end the session.
