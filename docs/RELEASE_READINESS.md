# Release readiness

Published version: **0.4.0-alpha.1**, experimental binary alpha, protocol 1.
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
CI result is linked from the PR. A final display-name consistency change defaults
new USB enrollment to Snowball-Voice without renaming existing devices. Its
verification is also on the PR; merge requires all six checks to pass.

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

## Coordinated publication verification (2026-10-02)

Publication PR [#10](https://github.com/fkiller/snowball/pull/10) merged after
[all six required checks passed](https://github.com/fkiller/snowball/actions/runs/36966649736).
The immutable alpha tag points to `9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.
[Two-product build/package pipeline](https://github.com/fkiller/snowball/actions/runs/36967114243)
rebuilds, scans and smoke-tests the shipped ARM64/AMD64 images, collects exact
corresponding source/notices, compiles locked firmware with SDK/component
notices, and packages all four Gateway platforms. Native image save/load
roundtrips passed. All ten validation/security/build/package jobs passed;
the original publisher stopped on draft metadata lookup. The tag and tested
artifacts were preserved while the lookup and permission boundary were fixed.

[Successful CI publication and download verification](https://github.com/fkiller/snowball/actions/runs/36998111226)
published [both products in v0.4.0-alpha.1](https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1)
at **2026-10-02 10:56:22 UTC**. Recovery validation required the exact tag
commit and all ten original jobs to pass. The publisher checked all 30
download files before publishing, then repeated every size/SHA-256 check
without authentication; both full verifications passed. The public release
has **31 assets** including its manifest, totaling **5,959,749,839 bytes**.
All publication occurred through CI; no production deployment or flash occurred.

| Shipped artifact | Verified identity |
| --- | --- |
| ESP32-S3 firmware archive | SHA-256 `91e7587a9b81536f36df36a99acc8e669ca62ff38434d5da8c1b13aebb2f3cb4`, 3,807,540 bytes |
| ARM64 saved Docker image | `sha256:5db0bfc3a10821f618d98a0080bb4b1a57afb51f1a640eb50d3c4f1abf06e5da` |
| AMD64 saved Docker image | `sha256:04fb5a6609750a86b18a6a587ea4eb6e0dba2f0cb3b8d097f65bf0596671a87f` |

The manifest pins all file sizes/hashes and source commit. The image identities
above are Docker image IDs; archive checksums are separate manifest entries.
The physical/VM/manual cases below remain open despite successful publication.

The repository is now public. Strict required-check main protection, secret
scanning/push protection, private vulnerability reporting and Dependabot updates
are enabled. Actions default permissions are read-only. No open secret or
Dependabot alerts were present at the publication setting check.

The approved banner and icon both render in GitHub's visitor README. Original
OpenAI-logo assets remain ignored. Local actionlint 1.7.12 validates all workflows;
web build/tests pass 17 cases with three Windows POSIX skips, npm audit has zero
findings, and documentation links pass across 28 Markdown files.

ESP-IDF 5.5.5/esp-sr 2.4.7/esp_peer 1.2.7 terms and bundled model/component
notices were reviewed for this ESP32-S3-only development firmware distribution.
ESP-only restrictions remain in the firmware archive and THIRD_PARTY_NOTICES;
project MIT does not relicense vendor binaries. Each Gateway architecture
includes exact authenticated Debian source packages/build scripts, Node/noVNC
source, project/Go source, copyright/common license files and SBOMs.

## Pre-Day-1 source verification (2026-10-04 UTC)

The public layout cleanup removes unused D1/Drizzle/Sites template material
and historical duplicate documents, groups platform helpers under
`deploy/openwrt/`, and retains one home-page Web UI capture. Existing
`v0.4.0-alpha.1` artifacts and their verified hashes remain immutable.

All nine outstanding dependency/Actions PR heads are integrated into
[PR #16](https://github.com/fkiller/snowball/pull/16) for combined validation.
Incompatible ESLint 10/TypeScript 7 updates are corrected to their plugins'
supported versions. Go CI and the builder use 1.27.1; container Node uses 26.

The unpatched `braces` path was removed by the scoped
[tinyglobby compatibility adapter](../tools/fast-glob-compat/README.md),
and Satori's fflate dependency is pinned to its patched 0.7.5 release.
Fresh locked installs pass with zero runtime and full-development audit findings.
Glob fixtures cover both upstream consumers and deeply nested brace input.
The worker bundler's optional Cloudflare tracing is excluded from the local
Node bundle; Voice/Admin rendering and hydration assets pass runtime tests.

Windows web build/tests pass **19 cases**, with **three POSIX migration skips**.
Linux CI executes those migration cases and all six protected-main gates:
full-history secrets, web/lint/audits, Gateway race/vet/vulnerability scan,
ESP32-S3 build, and ARM64/AMD64 image build/scan/isolated browser smoke.
All six jobs passed on integrated code commit `8b75fe7`:
[combined CI evidence](https://github.com/fkiller/snowball/actions/runs/37214673807).
Use the linked PR's final checks for the subsequent documentation commit.
No audit waiver or protected-main bypass is part of this change.
The final scan uses pinned govulncheck 1.8.0: the old 1.1.4 tool panicked on
Go 1.27 AST syntax during the documentation rerun after race/vet passed.

All four Gateway source bundles package successfully from the clean Git tree;
full build and runtime CycloneDX SBOM generation also succeeds. A fresh
anonymous manifest download matches its GitHub SHA-256, and all 30 listed
file sizes/digests still match the 31 public release assets. This metadata
recheck complements the original CI's complete authenticated/anonymous file
download verification; it does not claim a new download of the 5.96 GB set.

The published alpha remains the exact original tag and binaries. Updated main
source, layout and dependency versions apply to future builds; download users
must follow the immutable release's included installation/flash guides.

## Remaining manual and production gates

### Unexecuted acceptance

- Three spoken wake/converse/end cycles, no ghost wake, acoustic barge-in,
  physical long-session confidence. Prior 10m30s evidence belongs to its
  recorded build; it is not fresh acceptance of this prerelease.
- Windows/macOS VM setup acceptance and optional real daemon migration/reboot.
- Production security: AEC disabled, plaintext development NVS, incomplete
  Secure Boot/flash encryption/signed OTA/anti-rollback.

See [release plan](RELEASE_PLAN.md) and [platform matrix](PLATFORMS.md).
