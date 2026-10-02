# Public release preparation handoff

**Handoff state: IN PROGRESS** — 2026-10-02 America/New_York.

## Active publication task (2026-10-02)

The user explicitly authorized publishing BOTH products through CI/CD,
checking the actual pipeline, and making the visitor-facing repository public.
Work is on `codex/publish-all-releases`, based on merged prep PR #1/main
`a36f3f7`. New `release.yml` validates exact tag/source, calls all security
checks, builds and scans both native images, packages locked firmware/all
platforms, collects exact corresponding source and notices, uploads a draft,
verifies all download hashes, and publishes the coordinated alpha.
Existing physical/VM acceptance limitations remain explicit. No production
restart, daemon migration, physical flashing or eFuse changes are authorized.
Publication PR #10 merged after all six required jobs passed:
https://github.com/fkiller/snowball/actions/runs/36966649736
Main/tag commit: `9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.
Annotated `v0.4.0-alpha.1` has been pushed; actual publish workflow is running:
https://github.com/fkiller/snowball/actions/runs/36967114243
The repository is now PUBLIC (authorized by the user), main has strict six-check
protection including admins, force-push/delete disabled, private vulnerability
reporting enabled, secret scanning/push protection enabled, Dependabot alerts
and security updates enabled, Actions default token read-only. No secret or
open Dependabot alerts were present when checked.

Exact next action: inspect run 36967114243 using `gh run view`; do not duplicate
or replace the tag. If a job fails, fix with a PR, preserve immutable tagged
source, and decide whether the un-published alpha tag must be replaced ONLY if
no release was published (prefer a new alpha version if tag changes required).
Draft retries use `gh workflow run release.yml -f tag=v0.4.0-alpha.1`.
Verify every download with:
`node tools/verify-release-downloads.mjs fkiller/snowball v0.4.0-alpha.1 9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.
The script needs no token after PUBLIC publication; do not expose gh tokens.
Finish docs/readiness and handoff with actual publication evidence and link.

New scripts: collect-image-sources.py runs only in a disposable image as root,
uses exact authenticated APT source versions (missing versions fail), retains
Node/noVNC source and copyright/common-license texts. package-image-release.sh
adds project/Go source, saves and load-checks the image, splits source over 1.8GB.
collect-idf-notices.py/package-firmware-release.mjs retain actual SDK and managed
component notices, enforce four non-NVS ranges and matching locked IDF/version.
prepare-release-manifest.mjs requires all five product/platform bundles, two
image archives, corresponding sources and three SBOMs. Publisher uploads only
a draft, verifies size/hash of every asset, publishes then verifies anonymously.
Published releases cannot be overwritten by retries.

Local validation on 21f4318: actionlint 1.7.12 clean; npm ci/audit zero findings;
lint and web build/test 17 pass/3 Windows POSIX skips; docs links pass 28 files;
firmware archive with SDK+managed notices built 3,793,052 bytes (no flash); exact
IDF notice collector retained 128 entries; npm notice collector succeeded.
Public GitHub visitor UI verified banner and icon both render correctly.
CI PR #10 verified both native container builds/scans/QA, full-history secrets,
Linux Go race/vet/vulnerability checks, web checks and locked IDF build.

## Earlier preparation objective and task

Prepare the public Snowball-Voice ESP32 client and Snowball-Voice-Gate Gateway
repository. User authorized repository updates, MIT for project-authored code
with Espressif constraints preserved, complete purchase/tool/build/run guides,
platform-specific Gateway bundles, and a release plan. User approved replacing
the OpenAI logo in the supplied artwork with an original audio waveform.
Production deployment, daemon migration, physical flashing, and eFuse changes
were not requested and must not occur during source preparation.

## Current state and completed work

- Prepared coordinated version 0.4.0-alpha.1; protocol remains 1. Container,
  volume, state, binary filenames, and production runtime stay compatible.
- MIT scope and Espressif restricted license texts documented; compiled
  firmware/container distribution retains vendor/copyleft obligations.
- README, complete getting-started guide, platform matrix, privacy/security,
  contribution/reporting templates, release plan, readiness record added.
- Public logo-free banner/icon and PWA branding added; original root images
  remain locally ignored.
- New USB enrollment defaults to the Snowball-Voice display name; existing
  user-assigned device names and protocol identifiers are preserved.
- Windows/macOS launchers control a LAN Linux VM/host; Linux ARM64/AMD64 image
  jobs and platform packaging are provided. Native desktop Docker cannot bind
  the desktop's LAN interface as required; do not claim its acceptance.
- npm scripts work in PowerShell; vulnerable tooling/transitives updated
  without changing frameworks. Current full npm audit reports zero findings.
- Protected local flash helpers preserve NVS and reject range overflows.
- Router boot helpers preserved and optional migration rewritten to stop the
  old instance before state copy, preserve environment/image, and roll back.
  Three Linux mocked lifecycle/rollback tests pass; no live migration performed.
- Fresh Windows ESP-IDF 5.5.5 build passes, app 0x221e30 bytes (29% free).
  Windows vendor model-report encoding requires PYTHONUTF8/PYTHONIOENCODING;
  setup documentation includes those settings.
- Latest Windows web install/audit/lint/build pass; 15 web and 2 flash-guard tests pass and 3
  POSIX migration tests skip. Linux migration tests pass separately.
- Linux ARM64 Gateway race/vet pass. GitHub Linux Go 1.26.8 race/vet/govulncheck
  pass after fixing vulnerable Go 1.26.5 and x/crypto 0.53.0 (now 0.55.0).
- Linux CI IDF 5.5.5 firmware build, ARM64/AMD64 image build, Trivy gate,
  authenticated media start/stop smoke, and image SBOM generation pass.
  Screenshot artifact export required a host-group/setgid fix. All six jobs now
  pass on 4d0fe77 in run 36963388762, including screenshots and both image SBOMs.
  No failing gate is bypassed; the documentation confirmation has its own CI.
- ESP32 plus four Gateway platform bundles and checksums were generated from
  clean commits, most recently eda00d9, in ignored artifacts/releases-eda00d9/.
  Final image SBOMs/screenshots are in artifacts/ci-final/. No binary/tag/image publication.
- Dependabot vulnerability alerts enabled and GitHub description/topics updated.
  Repository remains private. Branch protection API returns 403 requiring Pro
  or public visibility; private vulnerability reporting is not available yet.

## Exact next action and remaining work

Read docs/RELEASE_READINESS.md and inspect PR #1 checks:
https://github.com/fkiller/snowball/pull/1 . Source preparation is complete;
confirm the PR is merged into main after its final documentation CI is green.
If it is already merged, do not redo preparation. The next product milestone
is the manual/binary checklist in docs/RELEASE_READINESS.md. Source preparation
does not itself change private visibility. At the source-publication milestone
apply the documented security/protection settings.
Tag/binary publication stays separate until binary/license/manual gates pass.
Repackage from the final coordinated tag before publishing reviewed artifacts.
Do not advertise a registry image or successful physical cases that do not exist.

## Architecture and reconciliation

The local source started at 39cfc44, including all transport/watchdog fixes.
Router HEAD b702a4c has independent uncommitted Docker changes. Its exact patch
and original new scripts are preserved locally in ignored
artifacts/router-prep-snapshot/. The router worktree is untouched. Keep its
changes: never overwrite it with a sync or reset. Build under the separate
/tmp/snowball-public-prep checkout and snowball-voice:public-prep-test tag.
The live router still uses unix:///var/run/snowball-voice-docker.sock and runs
snowball-voice:0.3.10-full-duplex, healthy at inspection.

## Verification, commands, and known limits

Use CONTRIBUTING.md for required checks, docs/GETTING_STARTED.md for firmware,
docs/PLATFORMS.md for platform installation, and docs/RELEASE_PLAN.md for release
packaging. Final results belong in docs/RELEASE_READINESS.md. Gitleaks 8.30.1
baseline full-history scan of 35 commits and final 41-commit scan found no secrets;
tracked-source 1.03 MB scan passes. Final CI scans all history; repeat for the tag.
Native Windows full Gateway tests still have POSIX-permission and /tmp failures;
Linux race results are authoritative.

Physical three-cycle/acoustic/barge-in acceptance, Windows/macOS VM onboarding,
real migration/reboot, binary license/source-offer review remain open. AEC is
disabled; development NVS is plaintext; production Secure Boot/flash encryption/
signed OTA/anti-rollback are incomplete. Normal flashing never writes NVS 0x9000.

## Git state and important files

Work branch: codex/public-release-prep, PR #1 to main. Major preparation commits:
8675ab3 (guides/license/branding/tools), 63cc3a3 (Go security + CI toolchain),
9611909/c09aa8e/eda00d9 (isolated QA host/home/artifact access and binary notices).
4d0fe77 fixes private screenshot group access; all six checks pass. The subsequent
documentation confirmation records the final evidence. Inspect actual git
status/log and PR state before continuing. Runtime/
build artifacts and original artwork are ignored. No stash was created.
Root README links all user guides; tools/package-*-release.mjs
and .github/workflows/prepare-release.yml prepare review artifacts without
publishing or deploying. Optional operations are documented in docs/OPENWRT.md.

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
