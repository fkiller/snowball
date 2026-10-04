# Snowball development handoff

## Current objective

Maintain the public Snowball-Voice ESP32 client and Snowball-Voice-Gate LAN
Gateway. The user requested a home-page header matching Snowball_Control,
one visible Web UI screenshot, removal of duplicate material and unused
template files, and an understandable public repository layout.

## Current task and state

Implementation is delivered through `codex/public-repo-cleanup`, based on main
`0a8548f` (PR #15). PR #16 is attached; its live page records merge status:
https://github.com/fkiller/snowball/pull/16
The user explicitly authorized merging all open PRs and the final pre-Day-1
check. All nine Dependabot PR heads are integrated into this branch. All six
combined gates pass on code commit `8b75fe7` in CI run 37214673807:
https://github.com/fkiller/snowball/actions/runs/37214673807
Final checks use pinned govulncheck 1.8.0. The old 1.1.4 scanner panicked on
Go 1.27 AST KeyValueExpr during a subsequent run after race/vet passed; this
is a scanner compatibility failure, not an accepted waiver. Every final gate
must pass before merge.
No production deployment, restart, daemon
migration, physical flash or eFuse change is authorized by this cleanup.

## Completed changes

- README: centered banner, inline 48-pixel icon/title, description, navigation
  and badges; one Web UI image, concise product/setup links and source map.
- Removed unused D1/Drizzle examples, schema/journal/config, Sites metadata
  plugin and hosting config, empty next.config, and obsolete Dockerfile.update.
- Kept the existing vinext/Cloudflare bundler and local Node fetch runtime.
  Entry now lives in services/web-entry.ts; no cloud/database bindings.
- OpenWrt integration now lives in deploy/openwrt, with installers, docs,
  flash-helper guidance and migration tests referencing the new location.
- runtime/daemon.json remains at the existing path for dedicated-daemon
  compatibility. Container/volume/state identities are unchanged.
- Removed the second Web UI image, repeated galleries, unused hardware still,
  historical project/container/minis handoffs and obsolete emulator proposal.
  Roadmap retains unfinished product/tooling work. Historical code and deleted
  documents remain recoverable in Git; transport evidence remains in
  docs/FULL_DUPLEX_STABILITY.md.
- HTML href/src links are included in the documentation checker. The runtime
  regression test covers Voice/Admin hydration and public icon serving.

## Verification and exact next action

Fresh locked npm install, runtime and full audit (zero findings), lint and web build passed.
The existing roadmap-wording assertion was removed from the runtime security
test; all behavior assertions remain. The final Node test run passed 19 cases
with three Windows POSIX skips. HTML/Markdown links passed for 26 documents.
Whole-file hashes found no identical tracked files; exact normalized prose
blocks of 150+ characters found no duplicates in public guides. Root tracked
directories fell from 19 to 13. Manual review removed semantic/gallery overlap.

Resolved GHSA-vfj7-8cjw-p6xm without an audit exception: tools/fast-glob-compat
adapts tinyglobby for the actual Next/Vite consumers, replacing fast-glob's
micromatch/braces tree via a local npm override. Fresh lock generation was
necessary: npm's in-place update retained an invalid nested fast-glob package.
Both upstream paths now resolve to the adapter; npm ls is clean. Its fixtures
test directories, extension braces, ignores, absolute paths, CommonJS/ESM and
deeply nested input. Satori's fflate is overridden to patched 0.7.5.
ESLint 9.39.5 and TypeScript 6.0.3 satisfy existing plugin peer constraints;
Dependabot defers incompatible major updates until peers support them.
vinext 1.0.1 injects optional Cloudflare tracing when it detects the bundler;
vite.config.ts excludes only that integration for our Node runtime. The runtime
test caught the unsupported cloudflare: import and now passes after the fix.
Dockerfile copies the local adapter before the builder's locked install;
runtime-only locked install also passes without the dev adapter directory.
Go builder/CI is 1.27.1; Docker Node is 26. All updated Actions retain SHA pins.

Exact next action: inspect PR #16. If still open, wait for the final six checks
and merge with the already-authorized merge strategy; do not squash its PR
ancestry. Confirm PRs #2-#9, #16 and #17 are merged, fetch current main, and
verify its visitor home page. If already merged, continue from current main.
All four local Gateway source bundles and full/runtime npm SBOM generation
pass. Anonymous release-manifest download and all 30 asset size/GitHub digest
matches pass; full 5.96 GB downloads were previously checked by publication CI.
GitHub branch-preview verification passed: banner, inline icon/title, all badges,
hardware media and the single Web UI image load; no details disclosure remains.
The previous PR CI had five passing jobs but failed its development audit.
The integrated run now passes all six, including that audit, Linux migration
tests, firmware compilation and native ARM64/AMD64 build/scan/browser smoke.

The web entry and moved installer references are the material risk to verify.
Existing migration mocks must still pass on Linux. No local hardware or VM
acceptance is implied by software CI.

## Published release (immutable)

Both products are public in v0.4.0-alpha.1:
https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1
Tag/source: `9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.
31 assets, 5,959,749,839 bytes; 30 downloadable file hashes verified both
authenticated and anonymously by successful CI:
https://github.com/fkiller/snowball/actions/runs/36998111226

Do not move that tag or overwrite its assets. This cleanup affects future
source/package trees. Already published archives retain their original layout.
Linux ARM64/AMD64 run Docker images; Windows/macOS bundles are SSH launchers
to a reachable LAN Linux VM/host. Own source is MIT; vendor/component/model
conditions still apply to combined artifacts. Exact source/notices/SBOMs
accompany images and firmware.

## Remaining product gates

See docs/ROADMAP.md, docs/TEST_SCENARIOS.md and docs/RELEASE_READINESS.md.
Fresh spoken/acoustic/ghost-wake/barge-in acceptance, VM onboarding,
upgrade/reboot and production security remain open. Prior 10m30s physical
transport evidence is separate. Wake phrase Hi ESP, AEC disabled, plaintext
development NVS and incomplete Secure Boot/encrypted flash/signed OTA/
anti-rollback remain explicit alpha limits. The web adapter is unofficial.

## Operating constraints

- LAN-only; no wildcard/WAN listener, STUN/TURN or cloud relay.
- Preserve snowball-voice, /data, snowball_speaker.bin and existing daemon paths.
- Never write or erase NVS at 0x9000. Never commit profiles, keys, credentials,
  certificates, NVS or full factory backups.
- Router /root/snowball-voice has independent dirty changes at b702a4c;
  never overwrite/reset. Ignored snapshot: artifacts/router-prep-snapshot.
- Router socket: unix:///var/run/snowball-voice-docker.sock.
  Last recorded production image: snowball-voice:0.3.10-full-duplex;
  this is historical evidence, not a current health check.
- Approved production swaps use tools/deploy-candidate.sh with explicit
  SNOWBALL_DEPLOY_APPROVAL=YES and a retained rollback container.

## Useful commands

```powershell
codexbar --format json
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
node tools/check-doc-links.mjs
# Linux CI runs POSIX, Go race and container gates.
```

Main requires an up-to-date PR with all six checks. No direct main push or
force push. Inspect git status/log/diff before continuing; the repository and
tests take precedence over dated notes.
