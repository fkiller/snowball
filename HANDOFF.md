# Snowball public alpha publication

**Current task: COMPLETE** — 2026-10-02 America/New_York.
This section is current. Archived transport notes below are historical evidence.

## Objective and current state

The user authorized publishing BOTH Snowball-Voice and Snowball-Voice-Gate
through working CI/CD, verifying the actual pipeline, complete purchase/tool/
compile/flash/install/run documentation, and supplied public artwork.

Both products are now published together in the PUBLIC developer prerelease:
https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1
Published 2026-10-02 10:56:22 UTC. Immutable tag/source:
`9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.
31 release assets including manifest; 5,959,749,839 total bytes.

## Completed work and verification

- Source prep PR #1: MIT scope/vendor restrictions, locked firmware, purchase/
  tool/build/flash/pair/run guides, platform guides, privacy/security/contribution,
  protected flash helpers, public artwork and six-check security workflow.
- PR #10: full native ARM64/AMD64 image build/scan/QA, locked IDF build,
  four Gateway platform bundles, corresponding source/notices/SBOMs, draft and
  download verification publisher. Its six PR checks passed in run 36966649736.
- Actual original release run 36967114243: all TEN validation/security/build/
  package jobs PASSED. Image save/load exact-ID roundtrips, complete source
  retention and firmware packaging passed. Initial publisher failed on draft
  metadata lookup after uploading all assets; no gate was bypassed.
- PR #11: authenticated draft-list fallback and provenance-checked recovery;
  all six PR checks passed in run 36968500072. Its first recovery attempt
  36968844009 stopped on a redundant read-only draft lookup.
- PR #12: keep draft lookup only in write-scoped publisher. Six PR checks passed:
  https://github.com/fkiller/snowball/actions/runs/36997747291
- SUCCESSFUL CI publication, with authenticated then anonymous size/SHA-256
  verification of ALL 30 downloadable files (~5.96 GB):
  https://github.com/fkiller/snowball/actions/runs/36998111226
  Both validate and publish jobs passed. Logs contain two complete verification
  messages at 10:56:21 UTC and 10:56:46 UTC. No manual binary publication.
- Anonymous API/manifest/image-ID downloads independently confirmed public
  visibility, both product names, exact source SHA, 31 assets and image IDs.
- Local actual authenticated verification of all 30 draft downloads also passed.
  Tokens stayed in temporary process environment and were removed afterward.
- actionlint 1.7.12 and documentation links pass. Prior local npm ci/audit zero
  findings, lint/build/test 17 pass and three Windows POSIX skips; Linux CI
  runs migration/flash guards, Go race/vet/govulncheck, Gitleaks and IDF builds.
- Approved logo-free banner and icon both render in GitHub visitor README;
  application/PWA branding applied. Original supplied logo images remain ignored.

## Architecture and release decisions

One coordinated alpha tag covers both products and protocol 1. Preserve
`snowball-voice` container/volume/state, `/data`, and `snowball_speaker.bin`.
Linux ARM64/AMD64 use ready-to-load Docker archives. Windows x64/ARM64 and
macOS Intel/Apple Silicon bundles use SSH to a reachable LAN Linux VM/host;
these are not native desktop Chromium/audio-server executables.

Each image accompanies exact authenticated Debian source descriptors/orig/
debian/build scripts, Node/noVNC sources, Go dependency/project source,
copyright/common-license texts and SBOMs. Missing versions fail publication.
Large source archives are split below 1.8 GB per part with checksums and rebuild
instructions. Own code MIT does not relicense combined binaries. ESP-only
esp-sr/esp_peer/models and SDK notices accompany the ESP32 firmware.
Supported firmware: Waveshare ESP32-S3-AUDIO-Board, 16 MB flash / 8 MB PSRAM.
Firmware archive includes only four approved non-NVS ranges and exact IDF 5.5.5
build metadata. Never erase or write NVS at 0x9000.

## Files and operating commands

- `release.yml`: tag-triggered full two-product pipeline; exact-ref reusable
  `security-gate.yml`; publisher alone has contents-write permission.
- `finish-release.yml`: recovery requires original tag SHA/run identity and all
  ten successful original jobs, then downloads/revalidates their artifacts.
  Read-only validate checks provenance; write-scoped publisher checks draft.
- `verify-release-downloads.mjs`: strict commit/tag/count/size/hash verifier,
  authenticated draft-list fallback, anonymous verification after publication.
- `prepare-release-manifest.mjs`, `package-*-release.*`, `collect-*-notices.*`,
  `collect-image-sources.py`: complete artifact/source/license packaging.
- `download-gate-image.sh`, `gate-start.sh`, `gate-windows.ps1`, `gate-macos.sh`:
  checksum/load, LAN-only installation, and Linux-VM SSH launchers.
- README, GETTING_STARTED, PLATFORMS, RELEASE_PLAN, RELEASE_NOTES,
  RELEASE_READINESS: public navigation, installation, exact evidence and limits.

```powershell
codexbar --format json
& ./artifacts/actionlint/actionlint.exe -shellcheck '' -pyflakes ''
node tools/check-doc-links.mjs
gh run view 36998111226
gh release view v0.4.0-alpha.1 --json isDraft,isPrerelease,assets,url
# Anonymous verifier (large download; all files already passed in CI):
node tools/verify-release-downloads.mjs fkiller/snowball v0.4.0-alpha.1 9b260cc16d3999890ad12a1450dc5ca61a23fcf6
```

Published artifacts/tags are immutable. Do not retry recovery for this already
published release. For future versions, bump all versioned source/config/helper
references, pass a PR's checks and push a new coordinated alpha tag. Do not
move v0.4.0-alpha.1 or overwrite its binaries/source/checksums.

## Known limits, constraints and next action

No publication work remains. The next product milestones are manual physical
spoken/acoustic/ghost-wake/barge-in acceptance, Windows/macOS VM onboarding,
real upgrade/reboot acceptance and production security. They were NOT executed
for this tag. Prior 10m30s transport evidence is separate. Hi ESP wake phrase,
AEC disabled, development NVS plaintext, incomplete Secure Boot/encrypted flash/
signed OTA/anti-rollback remain explicit alpha limitations. ChatGPT web adapter
is unofficial and can break with upstream UI/account restrictions.

No production deployment, restart/migration, physical flash or eFuse change
occurred during publication; those actions require explicit user authorization.
LAN-only: no wildcard/WAN/STUN/TURN/cloud relay. Never commit profiles, /data,
keys, credentials, NVS or full factory backups. Router /root/snowball-voice has
independent dirty changes at b702a4c; never overwrite/reset. Snapshot remains
ignored in artifacts/router-prep-snapshot. Use the dedicated Docker socket
unix:///var/run/snowball-voice-docker.sock if inspecting. Production image remains
snowball-voice:0.3.10-full-duplex; no current health claim replaces an actual check.

## Repository and Git state

Public; strict six-check up-to-date main protection including admins; force-push
and deletion disabled. Secret scanning/push protection/private vulnerability
reporting and Dependabot alerts/updates enabled; Actions default read-only.
No open secret/Dependabot alerts at publication settings check. Use a new PR,
not direct main push, for future changes. The final evidence update is on
`codex/published-alpha-evidence`, based on main merge `0c685eb` (PR #12).
No stash or uncommitted runtime work. Ignored original artwork, review bundles,
QA screenshots and local build artifacts remain local; none entered source
archives. The earlier quota handoff is resolved after the usage window reset.

---

# Archived transport handoff (2026-09-05)

The original physical transport evidence below is retained as historical
context. Current objective, source, and release checks above take precedence.

# Agent Handoff

## Status

**Handoff state: READY** (updated 2026-09-05 10:27 EDT)

The reproduced long-running full-duplex cutoff is fixed, deployed, flashed,
and physically verified for 10 minutes 30 seconds. Production Gateway image
`snowball-voice:0.3.10-full-duplex` is healthy. The final, test-hook-free
firmware is running on the ESP32-S3 and has resynchronized with the Gateway.

The transport problem is no longer an untested hypothesis. The remaining work
is broader product acceptance: three ordinary spoken wake/converse/end cycles,
acoustic barge-in behavior, and an optional 30-minute physical soak.

## 1. Objective

Snowball is a LAN-only voice terminal pairing an ESP32-S3 audio speaker with a
persistent ChatGPT Web Voice session in headed Chromium on the ARM64 OpenWrt
router at `192.168.1.1`.

The objective is continuous full-duplex conversation without creeping latency,
microphone starvation, false stall recovery, watchdog resets, or post-session
ghost wakes.

## 2. Proven root causes

Three independent mechanisms existed in series:

1. The Gateway had a redundant GStreamer `rtpjitterbuffer` after Pion had
   already terminated the Wi-Fi WebRTC transport. Without RTCP sender reports,
   clock-domain drift accumulated latency or crossed the drop threshold.
   Production `0.3.8-nojitter` removed this stage.
2. ESP32 A-law decode plus blocking `esp_codec_dev_write()` ran synchronously
   in the receive callback inside `esp_peer_main_loop()`. Continuous 20 ms
   downlink packets starved the same media task that had to send 32 ms
   microphone packets. This was head-of-line blocking, not heap exhaustion.
3. After decoupling playback exposed a healthy transport, the Gateway recovery
   watchdog still treated only non-silent PCMA samples as downlink liveness.
   ChatGPT Voice sends continuous valid silent RTP while quiet. Because silence
   did not update `lastDeviceDownlinkAudio`, the watchdog forcibly closed the
   peer after exactly two minutes of microphone activity.

The final Gateway fix records `lastDeviceDownlinkPacket` after every successful
current-peer RTP write and uses that timestamp for transport recovery.
`lastDeviceDownlinkAudio` remains signal-only telemetry.

Full evidence and the vendor callback chain are in
`docs/FULL_DUPLEX_STABILITY.md`.

## 3. Implemented solution

ESP32 transport implementation: commit
**`32d56ce fix: decouple ESP32 full-duplex audio paths`**.

- The receive callback now only copies into a bounded eight-packet queue.
- A dedicated priority-5 playback task performs decode and blocking codec/I2S
  writes; the priority-6 media task remains free to send microphone frames.
- Eight-frame sliding queues cap downlink at 160 ms and uplink at 256 ms. On
  overload they discard oldest audio instead of accumulating latency.
- The 200-frame pre-Voice store is an O(1) circular buffer. Browser-ready replay
  is idempotent and preserves only the latest 15 frames (480 ms).
- Codec conversion uses a 160-sample/2.5 KiB block rather than a permanent
  320-sample/5 KiB stack array.
- Session lifecycle prevents overlapping playback tasks.
- Five-second and final telemetry exposes queue depth/high-water, drops,
  `WOULD_BLOCK`, codec duration, stack low-water, and cross-end RTP counters.

Gateway watchdog implementation and regression test: commit
**`e0ef73d fix: keep silent full-duplex RTP sessions alive`**.

- Silent PCMA RTP packets now count as downlink transport liveness.
- A regression test proves continuous silent downlink does not close the peer.
- The existing no-packet recovery test still proves a truly stalled peer is
  closed and browser Voice is stopped.

AFE AEC remains disabled because the earlier AEC/SE profile caused watchdog
failures. That is an acoustic echo-quality limitation, not the continuity
failure fixed here.

## 4. Simulation and host verification

| Model | Load | Result |
| --- | --- | --- |
| Old coupled callback | 5 min nominal saturation | 0/9,375 uplink delivered; queue reached 8 |
| Decoupled playback | 5 min nominal | 9,375/9,375 uplink and 15,000/15,000 downlink; zero drops |
| Decoupled stress | 30 min, downlink +2,000 ppm, 120 ms codec stall/min | all 56,250 uplink delivered; bounded 159.680 ms downlink age |

Completed checks:

- Linux ARM64 `go test -race ./...`: **PASS**.
- Linux ARM64 `go vet ./...`: **PASS**.
- Focused silent-packet and true-stall Gateway recovery tests: **PASS**.
- Real-cadence Pion emulator and deterministic 5/30-minute models: **PASS**.
- Frontend dependency audit, lint, build, and 15/15 tests: **PASS**.
- Final ESP-IDF build: **PASS**, application `0x221e40` bytes with 29% of the
  smallest app partition free.
- ARM64 production image build: **PASS**, image
  `sha256:e7755e9c1d282443522f5c9beb4be02115e9056f7650b31e9a62cd7b7d90c043`.

Native Windows `go test ./...` retains known pre-existing POSIX-only failures
for `0666` mode expectations and `/tmp`; the authoritative Linux ARM64 race
suite passes.

## 5. Physical reproduction and acceptance

### Run 1: ESP32 fix with old Gateway watchdog

The decoupled ESP32 remained healthy, but the peer still closed after 2m23s.
Gateway logged:

```text
recovering stalled device Voice: no PCMA response for 2m0s after microphone activity
connectedFor=2m23.035s uplinkFrames=4390 downlinkFrames=7058
```

ESP32 reported uplink sent 4,436, downlink received/played 7,052/7,052, zero
drops. This isolated the remaining failure to the Gateway watchdog rather than
ESP32 CPU, RAM, codec, Wi-Fi, or DTLS capacity.

### Run 2: final Gateway logic with decoupled ESP32

The physical board ran continuous bidirectional PCMA for 10m30s and was then
stopped deliberately:

| Metric | Result |
| --- | ---: |
| ESP32 uplink generated | 19,673 frames |
| ESP32 uplink sent | 19,539 frames |
| Pre-browser-ready frames intentionally skipped | 133 frames |
| Gateway uplink received | 19,539 frames (exact match) |
| ESP32 downlink received / played | 31,357 / 31,351 frames |
| Gateway downlink sent | 31,615 frames, including close-propagation tail |
| Uplink / downlink drops | 0 / 0 |
| Send `WOULD_BLOCK` | 0 |
| Uplink / downlink queue high-water | 4/8 / 6/8 |
| Maximum playback write | 32.212 ms |
| Playback task stack low-water | 3,700 bytes |
| Panic / unexpected reset / forced recovery | 0 / 0 / 0 |

`tools/esp32-voice-trace-report.sh` reports `quality=pass` and one complete
cycle. The USB-only trigger used to enter the normal wake -> command -> WebRTC
media path was removed after testing. The final source tree and flashed image
contain no soak command or test hook.

## 6. Live state

### Physical ESP32

- Final application SHA-256:
  `14A76BED30E8B5AB6006AD5CB5B0F6B2CD735741E955B3FFD90EE02F894A13DE`.
- Protected esptool flash verified all written images by hash.
- Written offsets were only bootloader `0x0`, partition table `0x8000`,
  application `0x10000`, and speech models `0x310000`.
- NVS at `0x9000` was not addressed; identity, Wi-Fi, enrollment, and keys were
  preserved.
- Final boot produced audio-level events and Gateway accepted a new `sync`
  event at 2026-09-05 10:24:37 EDT.
- UART collection resumed in `trace-physical.log`.

### Production router

- Container: `snowball-voice`.
- Image: `snowball-voice:0.3.10-full-duplex`.
- State at handoff: healthy.
- Browser: ready, authenticated, Voice inactive, voice button present.
- Immediate rollback: `snowball-voice-rollback-20260905T140909Z`, image
  `snowball-voice:0.3.9-full-duplex`.
- Earlier known-good rollback is also preserved as
  `snowball-voice-rollback-20260905T133601Z`, image
  `snowball-voice:0.3.8-nojitter`.
- Router Docker socket: `unix:///var/run/snowball-voice-docker.sock`.

### Local repository

- Branch: `codex/fix-post-bye-ghost-wake`.
- Transport commit: `32d56ce`.
- Silent-RTP watchdog commit: `e0ef73d`.
- Physical evidence logs are intentionally untracked runtime artifacts:
  `trace-full-duplex-soak-20260905.log` and
  `trace-full-duplex-soak2-20260905.log`.

## 7. Remaining validation

The reproduced transport cutoff has passed its 10-minute gate. Do not conflate
the following remaining acoustic/product checks with the fixed transport bug:

1. Perform three ordinary spoken `Hi ESP` wake/converse/`Hi ESP` end cycles.
2. Exercise repeated acoustic barge-in while ChatGPT is speaking; AEC is still
   disabled, so echo behavior must be judged separately.
3. Run a 30-minute physical session for additional unattended soak confidence.
4. Keep the same limits: under 1% drops, playback under 100 ms, stack low-water
   at least 1,024 bytes, bounded queue depth, and no memory-block degradation.

## 8. Constraints

- LAN-only; no WAN listeners, relays, or wildcard external binds.
- Never flash NVS at `0x9000`.
- Production deployment uses `tools/deploy-candidate.sh` with explicit
  `SNOWBALL_DEPLOY_APPROVAL=YES` and a preserved rollback container.
- Firmware flashing uses a protected helper and the serial-collector ownership
  sequence.

## 9. Key files

| File | Purpose |
| --- | --- |
| `docs/FULL_DUPLEX_STABILITY.md` | Root causes, architecture, models, physical evidence |
| `firmware/esp32-s3-audio/main/media_session.c` | Decoupled queues/tasks and metrics |
| `firmware/esp32-s3-audio/main/board_audio.c` | Bounded block codec conversion |
| `gateway/main.go` | Watchdog liveness and cross-end RTP counters |
| `gateway/main_test.go` | Silent-packet and true-stall recovery regression tests |
| `gateway/emulator/transport_model.go` | Deterministic long-duration model |
| `tools/esp32-voice-trace-report.sh` | Physical media-quality gate |

## 10. Useful read-only checks

```powershell
Get-Content trace-physical.log -Tail 100

ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock ps --filter name=snowball-voice"
ssh root@192.168.1.1 "docker -H unix:///var/run/snowball-voice-docker.sock exec snowball-voice curl -fsS http://127.0.0.1:3100/status"

Push-Location gateway
go test -count=1 -run 'TestRecoverStalledDeviceVoice' -v .
Pop-Location
```
