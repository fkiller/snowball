# Release plan

## First public alpha

Target: **v0.4.0-alpha.1**, a developer preview of Snowball-Voice-Gate and
Snowball-Voice from the same commit. The tag-triggered CI pipeline publishes all product/platform assets together;
see the actual Releases page and workflow run for publication status. Protocol version remains **1**;
existing `snowball-voice` container, volume, NVS, and state identifiers remain.

The coordinated [public alpha](https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1)
was published by [successful CI](https://github.com/fkiller/snowball/actions/runs/36998111226).
Both products, all four Gateway platform bundles, two prebuilt Linux images,
firmware, corresponding source, licenses and SBOMs are available. Complete
authenticated and anonymous download verification passed; manual hardware/VM
acceptance remains separately tracked in RELEASE_READINESS.md.

Use one repository and one coordinated tag initially. The application/PWA
identifies Snowball-Voice-Gate; the ESP32 identifies Snowball-Voice. Preserve
the internal `snowball_speaker.bin` filename so protected flash tools remain compatible.

## Milestones and gates

| Milestone | Required evidence | Distribution |
| --- | --- | --- |
| Source preview | Canonical source reconciled, MIT scope/vendor terms clear, history + tracked-file secret scans clean, complete setup docs, CI run recorded | Public source repository; limitations visible |
| Binary alpha | All source gates + Linux race/vet/vulnerability checks, web checks, clean IDF build, ARM64/AMD64 image build + scan + isolated smoke, license/source-offer review | GitHub prerelease with checksummed firmware, platform bundles, and optionally reviewed multi-architecture image |
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
7. Push the coordinated alpha tag after its PR and required checks pass.
   `.github/workflows/release.yml` validates tag/version/commit ancestry and
   reruns the complete reusable security gate against the exact tag commit.
8. Native ARM64/AMD64 jobs build, scan and smoke-test the actual shipped image;
   save/load roundtrip verifies the archive and image ID. Retain exact Debian,
   Node/noVNC and Go corresponding source, licenses, and SBOMs. A separate
   locked IDF build packages firmware and all four platform bundles.
9. Only the final publisher has write permission. It assembles a complete
   asset manifest, uploads a draft prerelease, streams every download and
   checks size/hash/commit, publishes, and repeats verification anonymously.
   A missing source/license, failed check or corrupt download blocks publication.
10. To retry a failed run, dispatch the workflow with the same existing tag.
    Only drafts can have assets replaced; published versions are immutable.
    Make a new version/tag for corrections to published binaries. CI does not
    deploy to a router, flash hardware, change NVS or burn eFuses.

If build/security/package jobs passed but publication failed, use
`finish-release.yml` with the same tag and original `source_run`. It requires
the exact tag SHA and all ten successful original jobs, then repeats complete
download verification using those artifacts. Draft lookup happens in the
write-scoped publisher; read-only validation checks provenance only. This
path completed the first alpha without moving the tag or rebuilding binaries.
Published releases cannot be used with this recovery workflow.

## Repository publication settings

The repository is public. Private vulnerability reporting, secret scanning,
push protection, Dependabot alerts/security updates are enabled. `main`
requires an up-to-date PR and these six checks, including for administrators:

- Full-history secret scan
- Web build, lint, and audit
- Gateway test, vet, and vulnerability scan
- ESP32-S3 firmware build
- ARM64 container build, scan, and isolated smoke
- AMD64 container build, scan, and isolated smoke

Force pushes and branch deletion are disabled. Actions default to read-only;
only the final release publisher gets contents-write permission. No mandatory
reviewer approval is configured for this single-maintainer repository. Release
publication never deploys or flashes devices.

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

Keep the runtime container named `snowball-voice`. GitHub release image
archives cover Linux ARM64 and AMD64 and carry immutable image IDs/checksums.
Windows/macOS use those same Linux images through SSH launchers to a LAN VM.
There is no registry visibility prerequisite for downloading these archives.

The source collector enables authenticated Debian source repositories and
requires the exact installed source versions. A missing version fails release.
All corresponding source and build scripts accompany each image at the same
release URL. Debian copyright/common license files, application dependency
notices and exact Node/noVNC sources are retained. Large archives are split
below GitHub's asset size limit. Never bake profiles or `/data` into the image.

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
