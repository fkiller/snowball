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

## Final preparation checks

| Check | Result |
| --- | --- |
| Locked npm install, runtime audit, lint, build/test | Pending |
| History and tracked-file Gitleaks | Pending |
| Local documentation links | Pending |
| Linux Gateway race/vet/vulnerability scan | Pending |
| Fresh IDF 5.5.5 firmware build | Pending |
| ARM64/AMD64 images and isolated smoke | Pending |
| Migration lifecycle/rollback mocks | Pending |
| Firmware and platform release packaging | Pending |
| GitHub CI, protected main, source update | Pending |

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
