# Agent Handoff

## Status

**Handoff state: READY** (Updated 2026-09-05 09:29 EDT)

The full-duplex continuity candidate is implemented, committed, simulated,
built, and host-tested. It has **not** been flashed to the physical ESP32 or
deployed over the production Gateway. Production remains unchanged and
healthy. The remaining gate is an explicitly authorized candidate rollout and
long physical full-duplex acceptance.

## 1. Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a
persistent ChatGPT Web Voice session in headed Chromium on the ARM64 OpenWrt
router at `192.168.1.1`.

The current objective is continuous full-duplex conversation without creeping
latency, microphone starvation, premature session closure, watchdog resets, or
post-session ghost wakes.

## 2. Root-cause conclusion

The long-session failure had two independent mechanisms in series:

1. The Gateway formerly ran GStreamer's `rtpjitterbuffer` after Pion had already
   terminated Wi-Fi/WebRTC jitter. On localhost without RTCP sender reports,
   ESP32/Linux clock drift accumulated latency or eventually caused packet
   drops. Production image `snowball-voice:0.3.8-nojitter` already removes this
   redundant jitter buffer.
2. The ESP32 still performed A-law decode and the blocking
   `esp_codec_dev_write()` synchronously inside `peer_audio_callback`. Vendor
   disassembly confirms the callback runs inside `esp_peer_main_loop()`:

   ```text
   esp_peer_main_loop
     -> peer_recv_streams
        -> peer_insert_rtp_payload
           -> rtp_decoder_decode
              -> on_audio
                 -> peer_audio_callback
                    -> esp_codec_dev_write   (old blocking path)
   ```

   The Gateway supplies a 160-byte PCMA packet every 20 ms, while the physical
   firmware produces a 256-byte microphone packet every 32 ms. Continuous
   blocking playback prevented the media task from returning from
   `esp_peer_main_loop()` often enough to drain microphone audio. This is
   head-of-line blocking, not heap exhaustion.

Physical trace evidence from the old firmware:

| Attempt | Uplink delivered | Downlink played | Uplink audio / downlink time |
| --- | ---: | ---: | ---: |
| 2 | 3,444 frames = 110.208 s | 11,005 frames = 220.100 s | 50.1% |
| 8 | 2,642 frames = 84.544 s | 10,087 frames = 201.740 s | 41.9% |

Both ended with an ESP32 DTLS read failure and later closure. The largest
internal block remained about 31,744 bytes and the largest PSRAM block about
2.42 MiB, so there is no monotonic heap-collapse signature. Historical router
logs were lost on container replacement; the final browser-side close reason
is therefore an inference. The next physical run has counters at both ends to
prove the remaining boundary.

The complete analysis is in `docs/FULL_DUPLEX_STABILITY.md`.

## 3. Implemented candidate

Implementation commit: **`32d56ce fix: decouple ESP32 full-duplex audio paths`**.

### ESP32 transport and performance changes

- `peer_audio_callback` now performs only a bounded packet copy; it never calls
  the blocking codec writer.
- A dedicated priority-5 playback task decodes A-law and writes codec/I2S audio.
  The priority-6 peer task can continue sending microphone packets.
- The downlink queue holds eight 20 ms packets (160 ms). The uplink queue holds
  eight 32 ms packets (256 ms). Both discard the oldest packet on overload so
  latency cannot grow without bound.
- The 200-frame pre-Voice store is now an O(1) circular ring (6.4 s) instead of
  performing a roughly 65 KiB `memmove` while holding a cross-core critical
  section.
- Browser-ready replay is idempotent and sends only the latest 15 frames
  (480 ms) before live microphone traffic.
- Codec conversion uses a 160-sample/2.5 KiB stack block; a 320-sample input is
  processed in two blocks instead of keeping a 5 KiB array.
- Session lifecycle rejects a new session while an old playback task exists.
- Five-second and final telemetry reports generated/sent/dropped counts, queue
  high-water marks, send `WOULD_BLOCK`, playback maximum duration, and playback
  task stack low-water.

Final ESP-IDF link result:

- application image: `2,235,817` bytes (`0x221da9`), 28.9% app-partition free;
- static D/IRAM: `173,767 / 341,760` bytes, `167,993` bytes free;
- static IRAM remains the existing `16,384 / 16,384` configuration.

### Gateway observability

- Per-session PCMA uplink/downlink frame and payload-byte counters are exposed
  under `/api/status` -> `webrtc`.
- Peer-close logs preserve the four final counter totals.
- Counters are updated only for the current peer and reset atomically with peer
  replacement/closure using the established `peerMu -> audioMu` lock order.

### Emulator and acceptance tooling

- The emulator now matches physical 256-byte/32 ms uplink frames and continuous
  Gateway 160-byte/20 ms downlink RTP.
- Microphone capture runs concurrently with WebRTC and command resolution.
- The delayed-browser case actually overflows the 200-frame ring and verifies
  oldest overwrite plus latest-15 replay.
- `gateway/emulator/transport_model.go` adds deterministic virtual-time 5- and
  30-minute transport soaks.
- `tools/esp32-voice-trace-report.sh` version 2 parses the new totals and fails a
  complete cycle for sustained uplink delivery below 99%, downlink drops over
  1%, playback over 100 ms, or playback stack headroom below 1 KiB.

## 4. Simulation results

| Model | Load | Uplink | Downlink | Observed bound |
| --- | --- | ---: | ---: | ---: |
| Old coupled callback | 5 min nominal saturation envelope | 0 / 9,375; 9,367 dropped | 15,000 / 15,000 | uplink queue 8 |
| Decoupled candidate | 5 min nominal | 9,375 / 9,375; 0 dropped | 15,000 / 15,000; 0 dropped | uplink age 8 ms |
| Decoupled stress | 30 min; downlink +2,000 ppm; 120 ms codec stall/min | 56,250 / 56,250; 0 dropped | 89,826 / 90,180; 347 bounded drops | downlink age 159.680 ms |

The zero-uplink old result is a saturation envelope, not a fitted prediction of
the physical board. The physical old firmware retained only 42--50% uplink.
The model isolates the scheduling mechanism and proves that playback stalls no
longer propagate into uplink processing in the candidate architecture.

## 5. Verification completed

- ESP-IDF firmware build: **PASS**.
- ESP-IDF `size`: **PASS**, values recorded above.
- `go test -count=1 ./devproto ./emulator`: **PASS**.
- Deterministic transport soak tests with verbose result logging: **PASS**.
- Linux ARM64 container `go test -race ./...`: **PASS** for `gateway`,
  `devproto`, and `emulator`.
- Linux ARM64 container `go vet ./...`: **PASS**.
- `npm ci --ignore-scripts`: **PASS**.
- `npm audit --omit=dev --audit-level=high`: **PASS**, zero production findings.
- `npm run lint`: **PASS**.
- `vinext build` plus `node --test tests/*.test.mjs`: **PASS**, 15/15 tests.
- Synthetic pass/fail fixtures for trace-report version 2: **PASS**; the failing
  media fixture correctly returns non-zero.
- ARM64 Docker candidate build: **PASS**.
  - tag: `snowball-voice:full-duplex-test-20260905`
  - image: `sha256:1181b97c0f0f0d5270a815767b7649870a40953e1ac0179c3e683f770ebff219`
  - size: `1,422,879,831` bytes
- `git diff --check`: **PASS**; only expected CRLF-to-LF checkout notices were
  printed before staging.

Known host-only command caveats:

- The literal `npm test` script uses POSIX inline environment syntax and fails
  in Windows PowerShell because `WRANGLER_LOG_PATH=...` is interpreted as a
  command. The equivalent PowerShell environment assignment followed by the
  same build/tests passed.
- Native Windows `go test ./...` still fails pre-existing POSIX-assumption tests
  (`0666` file-mode expectations and `/tmp` paths). The required Linux ARM64
  race suite passes completely.

## 6. Live and repository state

### Physical ESP32

- Still running firmware commit `705af7b`; the new candidate has not been
  flashed.
- Attached on the Windows workstation and continuously producing audio-level
  events in `trace-physical.log` as of 2026-09-05 09:29 EDT.
- No NVS contents were touched.

### Production router

- Host: `SNOWBALL-ROUTER` at `192.168.1.1`.
- Production container: `snowball-voice`.
- Production image: `snowball-voice:0.3.8-nojitter`.
- State at handoff: `Up 34 hours (healthy)`.
- Browser state: ready, authenticated, Voice inactive, voice button present.
- A stale ChatGPT dynamic-import error remains in browser status, but the
  current ready/authenticated result is authoritative.
- Production was not restarted, replaced, or stopped during candidate work.

### Candidate build workspace

- Router test clone: `/root/snowball-full-duplex-test-20260905`.
- This is an isolated build/test clone, not `/root/snowball-voice` and not the
  production deployment source.
- The separate candidate image tag above is built from the tested Gateway
  source. It has not been run as the production container.

### Local Git

- Branch: `codex/fix-post-bye-ghost-wake`.
- Candidate implementation commit: `32d56ce`.
- `HANDOFF.md` is updated in the documentation-only commit immediately after
  that implementation commit.
- Expected working tree after the handoff commit: clean.

## 7. Remaining work and exact next action

State-changing rollout was intentionally not inferred from the diagnosis and
implementation request. The next action requires explicit user authorization:

1. Deploy the already-built Gateway candidate through
   `tools/deploy-candidate.sh` with its explicit approval/rollback guard.
2. Flash the matching firmware application with the protected helper. The only
   allowed offsets are bootloader `0x0`, partition table `0x8000`, application
   `0x10000`, and speech models `0x310000`. **Never write NVS at `0x9000`.**
3. Immediately run three ordinary wake/converse/end cycles.
4. Run one continuous 10-minute full-duplex/barge-in test, then a 30-minute
   conversation or equivalent bidirectional audio soak.
5. Compare ESP32 `uplink_sent` with Gateway `uplinkFrames`, and Gateway
   `downlinkFrames` with ESP32 `downlink_received`, allowing only packets in
   flight at shutdown.

Do not label the issue physically fixed until those gates pass.

## 8. Physical acceptance gates

- No panic, watchdog, overlapping playback task, TLS memory-gate failure, or
  unplanned DTLS closure in all three ordinary cycles.
- Continuous Voice remains responsive past 10 minutes, including repeated
  interruption while ChatGPT is speaking.
- Steady-state uplink and downlink loss each remain below 1%; normal target is
  zero.
- Playback writes stay below 100 ms and playback stack low-water remains at
  least 1,024 bytes.
- Queue depths remain bounded; no increasing turn-to-turn latency.
- Internal and PSRAM largest-block values do not trend downward across sessions.
- Session ends cleanly on `Hi ESP` without ghost wake or media-task overlap.

## 9. Known limitation

AFE AEC remains disabled because the prior AEC/SE configuration caused watchdog
failures. This affects acoustic echo and barge-in quality but is not the
transport starvation mechanism above. Treat AEC as a separate measured change
only after the transport candidate passes long physical acceptance.

## 10. Constraints

- LAN-only; no WAN listeners, relays, or wildcard external binds.
- Never flash NVS at `0x9000`; it contains device P-256 keys, Wi-Fi credentials,
  enrollment state, and tokens.
- Router Docker socket is
  `unix:///var/run/snowball-voice-docker.sock`.
- Never restart/replace/stop production without explicit approval.
- Deployment must use `tools/deploy-candidate.sh` with
  `SNOWBALL_DEPLOY_APPROVAL=YES` after explicit maintenance approval.
- Flashing must use the protected Windows or router helper and preserve the
  serial collector ownership sequence.

## 11. Key files

| File | Purpose |
| --- | --- |
| `docs/FULL_DUPLEX_STABILITY.md` | Root-cause evidence, architecture, simulation, acceptance |
| `firmware/esp32-s3-audio/main/media_session.c` | Decoupled queues/tasks, pre-Voice ring, transport metrics |
| `firmware/esp32-s3-audio/main/board_audio.c` | Smaller block-based codec conversion |
| `gateway/main.go` | Cross-end PCMA telemetry |
| `gateway/emulator/transport_model.go` | Deterministic long-duration transport model |
| `gateway/emulator/emulator_test.go` | Real Pion continuous bidirectional media scenarios |
| `tools/esp32-voice-trace-report.sh` | Physical-cycle media-quality gate |
| `docs/RESOURCE_BUDGET.md` | Firmware size and memory budget |
| `docs/TEST_SCENARIOS.md` | Physical 10/30-minute acceptance procedure |

## 12. Useful read-only checks

```powershell
# Physical UART evidence
Get-Content trace-physical.log -Tail 100

# Production state (do not restart it)
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock ps --filter name=snowball-voice"
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock exec snowball-voice curl -fsS http://127.0.0.1:3100/status"

# Local focused tests
Push-Location gateway
go test -count=1 ./devproto ./emulator
go test -count=1 -run TransportSoak -v ./emulator
Pop-Location
```
