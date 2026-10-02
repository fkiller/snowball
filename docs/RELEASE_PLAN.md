# Release plan

## Prepared first public release

Target: **v0.4.0-alpha.1**, a developer preview of Snowball-Voice-Gate and
Snowball-Voice from the same commit. This is a prepared version, not a claim
that an image, tag, or release already exists. Protocol version remains **1**;
existing `snowball-voice` container, volume, NVS, and state identifiers remain.

Use one repository and one coordinated tag initially. The application/PWA
identifies Snowball-Voice-Gate; the ESP32 identifies Snowball-Voice. Preserve
the internal `snowball_speaker.bin` filename so protected flash tools remain compatible.

## Milestones and gates

| Milestone | Required evidence | Distribution |
| --- | --- | --- |
| Source preview | Canonical source reconciled, MIT scope/vendor terms clear, history + tracked-file secret scans clean, complete setup docs, CI run recorded | Public source repository; limitations visible |
| Binary alpha | All source gates + Linux race/vet/vulnerability checks, web checks, clean IDF build, ARM64 image build + scan + isolated smoke, license/source-offer review | GitHub prerelease with checksummed firmware and optionally reviewed ARM64 image |
| Hardware acceptance | Three ordinary wake/converse/end cycles, no ghost wake, pairing/replay/revocation cases, acoustic barge-in assessment, physical long-session evidence | Updated prerelease evidence; no invented CI coverage |
| Production release | Secure Boot, flash/NVS encryption, signed OTA + anti-rollback, rotation/revocation/recovery workflows, supported-browser and upgrade/rollback acceptance | Stable release only after the threat model and support commitments are updated |

The existing 10m30s paired full-duplex result is transport evidence, not a
pass for the still-open acoustic/product cases. AEC is disabled. A source
preview does not promise production-trusted devices or unrestricted MIT binaries.

## Execution order

1. Preserve and review the router's independent changes; never overwrite its
   worktree or migrate the live daemon during source preparation.
2. Prepare a PR based on the latest local transport fixes; merge into `main`
   after required CI passes. Use an up-to-date protected default branch.
3. Enable private security reporting, secret scanning/push protection when
   available, and required checks. Review all public history and metadata.
4. Run the repository checks from a clean checkout. The firmware job must build
   the locked ESP32-S3 target; the image job must validate ARM64, not assume an
   x64-only build establishes router compatibility.
5. Use disposable state and different QA ports for browser smoke. Do not mount
   production `/data` or start a second production-port container.
6. Record actual test results, tool versions, image digest, commit, and manual
   cases not executed in `docs/RELEASE_READINESS.md`.
7. After binary gates pass, create and push `v0.4.0-alpha.1`; run the manually
   dispatched release preparation workflow against that exact tag.
8. Download/review the workflow artifact. Create a **draft prerelease**, attach
   its reviewed archive, SBOMs, and checksums, then publish only when all binary
   gates are recorded. No automatic public image publication is configured.

## Firmware archive

Use `node tools/package-firmware-release.mjs` after a clean IDF build.
The resulting archive must include:

- Four binaries only: bootloader, partition table, application, speech models.
- `flash-layout.json` listing the four exact offsets and protocol/product/version.
- Source commit, IDF version, dependency lock, sdkconfig.defaults, checksums.
- Root MIT license, third-party notices, and exact discovered component license texts.
- Instructions to unpack into the firmware project's `build-release` directory
  and use protected flash helpers. Never include NVS, full-flash dumps, device
  configuration, keys, credentials, or test hooks.

Tag and source archive remain available for rebuilding. Model-specific binary
license review is a human release gate even when a packaging script succeeds.

## Gateway image

Keep the runtime container named `snowball-voice`. An initial reviewed registry
location can be `ghcr.io/fkiller/snowball-voice-gate:0.4.0-alpha.1`; this is a
planned destination, not an existing downloadable image.

Build and scan ARM64 and AMD64 in separate CI jobs. Advertise physical
acceptance separately from build/smoke results. Platform bundles for Linux
ARM64/AMD64, Windows, and macOS are produced by `tools/package-gate-release.mjs`.
Windows/macOS bundles use SSH launchers to a LAN Linux VM/host, as documented
in [platform-specific installation and execution](PLATFORMS.md).
Record immutable digests, package/license inventories, an image SBOM, a high/
critical vulnerability scan, and applicable source distribution obligations.
Preserve Debian copyright files. Never bake a Chromium profile or `/data` into
the image. Publishing an image is separate from deploying it to a user's router.

## Release notes template

```text
Snowball-Voice-Gate + Snowball-Voice 0.4.0-alpha.1 (developer preview)
Source commit: <verified SHA>
Protocol: 1
Supported board: Waveshare ESP32-S3-AUDIO-Board, 16 MB flash / 8 MB PSRAM
Gateway: ARM64 Linux; tested host/platform: <actual result>
Build/check results: <link to passing CI and readiness record>
Assets: <firmware archive, checksums, inventories/SBOMs, reviewed image digest>
Install: docs/GETTING_STARTED.md
License: project source MIT; restricted vendor components keep their own terms
Known limits: unofficial ChatGPT web adapter, Hi ESP wake model, AEC disabled,
plaintext development NVS, no production Secure Boot/encrypted flash/signed OTA
Manual acceptance not executed: <list honestly>
Upgrade: preserve /data and NVS 0x9000; no automatic production deployment
```

## Rollback and support

Keep the prior image/container and a private, consistent `/data` backup before
Gateway updates. Flash only the approved image ranges for ESP32 rollback;
never overwrite NVS. A future anti-rollback policy must document which old
firmware versions become intentionally unavailable. Do not invent support
SLAs, response guarantees, or a stable compatibility promise for this alpha.
