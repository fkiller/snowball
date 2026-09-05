# Agent Handoff

## Status

**Handoff state: READY** (Updated 2026-09-05 00:32 EDT)

This handoff reflects the current repository state across the local Windows development workstation and the live `SNOWBALL-ROUTER` host (`192.168.1.1`).

---

## 1. Current Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a persistent ChatGPT Web session running in headed Chromium inside a container on an ARM64 OpenWrt home router (`192.168.1.1`).

The primary objective is to maintain seamless, continuous, full-duplex bi-directional voice conversation between the ESP32-S3 speaker and ChatGPT Voice without latency buildup, recognition dropouts, premature session cutoffs, or post-bye phantom re-triggering.

---

## 2. Current Task

Diagnose and verify the fix for the 30-to-50-second conversation cutoff and creeping audio latency ("slowly lagging") experienced during continuous voice sessions.

---

## 3. Current State

- **ESP32 Firmware**:
  - Running commit `705af7b` on physical hardware attached to `COM3` on the Windows host.
  - State: `VOICE_STATE_IDLE`, WakeNet active, listening for *"Hi ESP"*.
  - Background serial monitor (`task-3208`) active, logging to `trace-physical.log`.
- **Router Container (`snowball-voice`)**:
  - Running image `snowball-voice:0.3.8-nojitter` on `SNOWBALL-ROUTER` (`192.168.1.1`).
  - Container health: `{"ok":true}`.
  - Browser controller status: `{"state":"ready","reason":"ChatGPT is ready for voice.","voiceButtonPresent":true,"voiceActive":false,"authenticated":true}`.
  - GStreamer uplink and downlink scripts verified up and running as supervisor child processes.
- **Git State**:
  - Local branch: `codex/fix-post-bye-ghost-wake` (clean working tree).
  - Router branch: `candidate-test` / `work` in sync at commit `088681e`.

---

## 4. Completed Work

### A. Vendor Library Disassembly & PTS Discovery (`libpeer_default.a`)
- Disassembled vendor library `calc_timestamp` routine in `libpeer_default.a`:
  ```assembly
  slli  a11, a11, 3  # pts << 3 (pts * 8) for 8000 Hz PCMA
  ```
- Proved `esp_peer_send_audio` takes `frame->pts` in **milliseconds**, converting it automatically to the 8000 Hz RTP sample timestamp.
- Updated `firmware/esp32-s3-audio/main/media_session.c` to generate contiguous millisecond PTS:
  `audio.pts = uplink_pts_ms; uplink_pts_ms += (audio.size / 8);` (32ms per 256-byte frame).

### B. Pre-Voice Buffer Optimization (`media_session.c`)
- Pruned pre-voice opening replay queue to the latest 15 frames (~480ms) upon uplink enable, skipping stale chimes and initial silence.
- Drain loop sends the 15 preserved frames in a sub-millisecond burst upon browser-ready receipt.
- Increased `MEDIA_AUDIO_QUEUE_DEPTH` from 8 to 32 (1024ms) with sliding-window protection.

### C. Elimination of GStreamer `rtpjitterbuffer` (`container/start-gst-device-uplink.sh`)
- Discovered root cause of the 30-to-50-second conversation cutoff:
  - Pion WebRTC on the Go Gateway **already** terminates Wi-Fi network jitter and delivers clean, reassembled RTP packets to `127.0.0.1:49003`.
  - Local loopback UDP has zero jitter.
  - Having `rtpjitterbuffer` with `mode=slave` downstream on loopback caused GStreamer to compare RTP timestamps against the router's Linux clock without RTCP sender reports.
  - Any minor drift between the ESP32 crystal clock and the Linux host clock caused the jitter buffer to accumulate latency (when `drop-on-latency=false`), or silently drop 100% of subsequent packets once the 50ms threshold was reached at ~30 seconds (when `drop-on-latency=true`).
- Removed `rtpjitterbuffer` completely from `start-gst-device-uplink.sh`: packets now flow `udpsrc -> rtppcmadepay -> alawdec -> audioconvert -> audioresample -> volume -> pulsesink`.
- Right-sized `pulsesink` buffer from 20ms to `buffer-time=64000 latency-time=16000` (64ms buffer, 16ms latency) to comfortably buffer two 32ms frames and eliminate underruns.

### D. Line Endings Normalization & Container Deployment
- Fixed Windows CRLF (`\r\n`) line endings in `container/*.sh` that previously caused `/usr/bin/env: 'bash\r': No such file or directory`.
- Built and deployed `snowball-voice:0.3.8-nojitter` to the router container with automated rollback guard.
- Tested simulated media activation inside the container: verified both uplink and downlink GStreamer pipelines spawn instantly and terminate cleanly.

---

## 5. Remaining Work

1. **Conduct Physical Live Verification (Test 13)**:
   - Speak *"Hi ESP"*, verify immediate wake chime and fast Voice connection (~2-3s).
   - Conduct continuous multi-turn dialogue past 1–2 minutes (counting numbers, asking questions).
   - Verify zero creeping latency and zero premature session cutoff at 30–50 seconds.
   - Verify user interruption cuts off ChatGPT speech promptly.
   - Say *"Hi ESP"* to verify clean session termination without ghost wakes.
2. **Long-Term Session Stability**:
   - If turn-taking remains responsive indefinitely, maintain `snowball-voice:0.3.8-nojitter` as the new baseline image tag.
   - Monitor memory usage across multiple consecutive sessions via `trace-physical.log` memory events.

---

## 6. Exact Next Action

**Run physical verification test (Test 13):**
1. Say **"Hi ESP"**.
2. Wait for the wake chime.
3. Converse with ChatGPT in Korean or English for > 1 minute (e.g., count numbers alternating turns).
4. Verify:
   - ChatGPT speaks and responds clearly.
   - Conversation stays active continuously beyond 30–50 seconds.
   - Recognition speed stays fast without accumulating delay.
5. Say **"Hi ESP"** to end the session.

If inspecting logs during or after the test:
- UART trace: `Get-Content trace-physical.log -Tail 50`
- Router logs: `ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock logs --tail 50 snowball-voice"`
- Browser status: `ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock exec snowball-voice curl -s http://127.0.0.1:3100/status"`

---

## 7. Architecture & Key Decisions

1. **Full-Duplex PCMA Architecture**:
   - ESP32 feeds 16 kHz microphone audio through Espressif AFE (`speech.c`).
   - Frames are averaged to 8 kHz, encoded to G.711A (PCMA), and sent via DTLS-SRTP WebRTC (`libpeer`) to Gateway.
   - Gateway decrypts WebRTC packets and forwards RTP to GStreamer on `127.0.0.1:49003`.
   - GStreamer decodes PCMA to 48 kHz PCM and pipes it directly into PulseAudio `chatgpt_mic_sink`.
   - Chromium reads from `chatgpt_mic_source` (monitor of `chatgpt_mic_sink`) and streams to ChatGPT WebRTC servers.
   - Reverse path: Chromium outputs to `chatgpt_output_sink`, GStreamer encodes to PCMA, Gateway forwards over WebRTC to ESP32 DAC/speaker (`board_audio.c`).
2. **No Jitter Buffer on Localhost UDP**:
   - Jitter buffers belong only at network edges where packet reordering and Wi-Fi latency occur.
   - Pion WebRTC terminates network jitter. Loopback UDP between Gateway and GStreamer is strictly FIFO; removing `rtpjitterbuffer` eliminates clock-drift synchronization failures.
3. **5-Second Conversation Grace Window (`speech.c`)**:
   - During the first 5 seconds of an active conversation, any false WakeNet detection (from speaker feedback or echo) is ignored to prevent mid-stream reboots or premature terminations.

---

## 8. Files Changed

| File | Purpose | Commits |
|---|---|---|
| `firmware/esp32-s3-audio/main/media_session.c` | Fixed PCMA PTS calculation, pruned pre-voice queue, increased queue depth | `705af7b` |
| `firmware/esp32-s3-audio/main/speech.c` | Fixed AFE concurrency race during live conversation | `7565bea` |
| `container/start-gst-device-uplink.sh` | Removed `rtpjitterbuffer`, tuned PulseAudio buffer to 64ms/16ms, normalized LF | `92cd71c`, `088681e` |
| `container/*.sh` | Converted all shell scripts to POSIX LF line endings | `088681e` |
| `HANDOFF.md` | System handoff and operational status tracking | `088681e`, `353d977` |

---

## 9. Tests and Verification

- `npm run lint`: **PASS** (0 errors, 0 warnings).
- `node --test tests/*.test.mjs`: **PASS** (15 of 15 tests passed).
- `go test ./devproto ./emulator`: **PASS** (all unit tests passed).
- Container health: **PASS** (`http://127.0.0.1:8080/api/health` returned `{"ok":true}`).
- Browser controller: **PASS** (`http://127.0.0.1:3100/status` returned `ready`, authenticated, voiceButtonPresent).
- Active GStreamer pipeline test: **PASS** (both device uplink and downlink pipelines spawned cleanly and handled audio).

---

## 10. Known Problems & Notes

- **Chromium WebGL warnings**: `WebGL1 blocklisted / WebGL2 blocklisted` are expected inside headless container environments and do not impact WebRTC audio.
- **Dbus errors in container logs**: `Failed to connect to the bus: Could not parse server address` are standard benign chromium artifacts inside stripped containers without dbus daemon.

---

## 11. Failed Approaches (Do Not Repeat)

- **`rtpjitterbuffer drop-on-latency=false`**: Causes playout delay to accumulate indefinitely without recovery when slight clock drift exists, resulting in multi-second lag and broken turn-taking.
- **`rtpjitterbuffer latency=50 drop-on-latency=true`**: Without RTCP reports from the ESP32, clock drift causes all packets to be dropped after ~30 seconds, killing microphone audio entirely.
- **Windows CRLF line endings in container scripts**: Breaks `#!/usr/bin/env bash` inside Debian container. Always ensure LF endings on all `.sh` files.
- **Calling `afe->reset_buffer` during active conversation**: Not thread-safe across cores with concurrent `afe->feed` and causes ESP32 panic/reboot.

---

## 12. Constraints

- **LAN-Only**: No external WAN listeners, cloud relays, or wildcard binds.
- **Never flash NVS at `0x9000`**: Contains device P-256 keys, Wi-Fi credentials, and enrollment tokens.
- **Router Docker socket**: Must use `unix:///var/run/snowball-voice-docker.sock`.
- **Router deployment**: Must use `tools/deploy-candidate.sh` with `SNOWBALL_DEPLOY_APPROVAL=YES`.

---

## 13. Useful Commands

```powershell
# Windows development checks
npm run lint
node --test tests/*.test.mjs
Push-Location gateway; go test ./devproto ./emulator; Pop-Location

# ESP32 serial monitoring
powershell -ExecutionPolicy Bypass -File tools/esp32-serial-trace-windows.ps1 -Port COM3 -LogPath trace-physical.log -DurationSeconds 86400

# Router container checks via SSH
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock ps"
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock logs --tail 50 snowball-voice"
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock exec snowball-voice curl -s http://127.0.0.1:3100/status"

# Router container deployment
ssh root@192.168.1.1 "cd /root/snowball-voice && docker -H unix:///var/run/snowball-voice-docker.sock build --network host -t snowball-voice:candidate ."
ssh root@192.168.1.1 "cd /root/snowball-voice && export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock && SNOWBALL_CANDIDATE_IMAGE=snowball-voice:candidate SNOWBALL_DEPLOY_IMAGE=snowball-voice:candidate SNOWBALL_DEPLOY_APPROVAL=YES tools/deploy-candidate.sh"
```

---

## 14. Git State

- **Branch**: `codex/fix-post-bye-ghost-wake`
- **HEAD Commit**: `088681e docs: update HANDOFF.md with 0.3.8-nojitter and LF fix`
- **Remote router branch**: `candidate-test` / `work` in sync at `088681e`
- **Working Tree**: Clean

---

## 15. Recommended Next Steps

1. Test wake-word *"Hi ESP"* on physical speaker.
2. Conduct multi-turn conversation past 1–2 minutes.
3. Check `trace-physical.log` for audio frame delivery and verify zero latency accumulation.
4. If verified, tag the release and close out the issue.

