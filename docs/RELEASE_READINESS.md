# Release readiness

Prepared version: **0.4.0-alpha.1**, experimental source preview, protocol 1.
This is a verification record, not a declaration of production readiness.

## Baseline (2026-10-01, America/New_York)

- Local HEAD `39cfc44` included transport/watchdog fixes; the matching remote
  branch was eight commits behind and main was bootstrap-only.
- Router HEAD `b702a4c` had independent uncommitted Docker boot/migration work.
  Its patch/scripts were preserved in ignored local artifacts. No remote work
  was overwritten/discarded. Optional helpers were reconciled without changing
  the current dedicated daemon.
- Live `snowball-voice:0.3.10-full-duplex` was healthy. No deployment, migration,
  restart, or firmware flash occurs during source preparation.
- Logo-free public images were created with user authorization. Original
  OpenAI-logo images remain local/ignored.
- Gitleaks 8.30.1 baseline history scan: 35 commits, no findings.
- Windows baseline: web lint/build/15 tests and Go vet/devproto/emulator pass;
  npm runtime audit zero findings. Full Gateway tests require Linux POSIX
  permissions and `/tmp` semantics.

## Preparation verification (2026-10-02, America/New_York)

Final code verification: `4d0fe77`, [passing six-job CI run](https://github.com/fkiller/snowball/actions/runs/36963388762).
Both architecture jobs retain private synthetic screenshots and image SBOMs.
The canonical source update is tracked in [PR #1](https://github.com/fkiller/snowball/pull/1).
The subsequent documentation confirmation changes no runtime code; its own
CI result is linked from the PR. Merge requires all six checks to pass.

| Check | Result |
| --- | --- |
| Locked npm install, runtime/full audit, lint, build/test | Windows and Linux CI pass; npm audit zero findings; Windows 17 tests pass + 3 POSIX skips; Linux executes flash guards |
| History and tracked-file Gitleaks 8.30.1 | Local 41-commit history and 1.03 MB tracked-source scan clean; CI full-history scan passes |
| Local documentation links | Pass, 27 Markdown documents |
| Linux Gateway race/vet/vulnerability scan | ARM64 race/vet pass; Linux CI Go 1.26.8 race/vet/govulncheck 1.1.4 pass after toolchain/x/crypto updates |
| Fresh IDF 5.5.5 firmware build | Windows native and Linux CI pass; application 0x221e30 bytes, 29% partition free; not flashed |
| ARM64/AMD64 images and isolated smoke | Both native builds, Trivy 0.70.0 gate, authenticated start/stop media smoke, and CycloneDX image SBOM generation pass |
| Migration lifecycle/rollback mocks | Three Linux tests pass, including no-op without approval and failed-start rollback; no live migration |
| Firmware and platform release packaging | ESP32 + Linux ARM64/AMD64, Windows, macOS archives created from clean commits; archive SHA-256 and exclusion checks pass; npm runtime SBOM created |
| Desktop launcher checks | Windows command construction/invalid-input checks and macOS shell syntax/invalid-host checks pass; VM/media onboarding remains manual |
| GitHub repository settings | Dependabot alerts enabled; description/topics updated; source visibility remains private, no tag/release/registry image published |
| Required GitHub CI | All six jobs pass on 4d0fe77, including firmware and both architecture artifact uploads |
| Protected main | GitHub API returns 403: private repo requires Pro or public visibility. Apply required checks/private reporting as described in RELEASE_PLAN.md when available |

Local review bundles/SBOMs are ignored artifacts under
`artifacts/releases-eda00d9/` and `artifacts/ci-final/`. They are review inputs,
not published releases. Repackage from the eventual tag before publication.
Recorded CI image IDs (not registry digests): ARM64
`sha256:e6f7d7e24308f1d5171eb7818370f7a5eee229bf0c6bddeaa923d9dd8e1b67c3`;
AMD64 `sha256:9ba63f2ea845e311a4bfa13d02255fcdad1ea9eb222fa4db26a0c16730584588`.
Final-run SBOMs contain 569 ARM64 / 573 AMD64 component records; differences
are recorded by the inventories, rather than assuming both images are identical.

The isolated legacy Docker build on the router stalled while committing its
completed npm-install layer and was cancelled by interrupting only the verified
candidate build client. Native ARM64/AMD64 GitHub builds are the successful image
evidence. Production stayed healthy with its unchanged 2026-09-14 start time;
the router worktree, daemon, credentials, and ESP32 were not changed.

## Remaining manual/binary gates

- Three spoken wake/converse/end cycles, no ghost wake, acoustic barge-in,
  physical long-session confidence. Prior 10m30s evidence belongs to its
  recorded build; it is not fresh acceptance of this prerelease.
- Windows/macOS VM setup acceptance and optional real daemon migration/reboot.
- Speech model binary/license review, image source-offer/license review, final
  digest and SBOM review before public binary/image release.
- Production security: AEC disabled, plaintext development NVS, incomplete
  Secure Boot/flash encryption/signed OTA/anti-rollback.

See [release plan](RELEASE_PLAN.md) and [platform matrix](PLATFORMS.md).
