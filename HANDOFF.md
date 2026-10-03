# Snowball development handoff

## Current objective

Maintain the public Snowball-Voice ESP32 client and Snowball-Voice-Gate LAN
Gateway. The user requested a home-page header matching Snowball_Control,
one visible Web UI screenshot, removal of duplicate material and unused
template files, and an understandable public repository layout.

## Current task and state

Implementation is complete on `codex/public-repo-cleanup`, based on main
`0a8548f` (PR #15). Cleanup PR #16 is open and attached:
https://github.com/fkiller/snowball/pull/16
Main remains unchanged because the full development audit blocks merge.
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

Locked npm install, runtime audit (zero findings), lint and web build passed.
The existing roadmap-wording assertion was removed from the runtime security
test; all behavior assertions remain. The final Node test run passed 17 cases
with three Windows POSIX skips. HTML/Markdown links passed for 25 documents.
Whole-file hashes found no identical tracked files; exact normalized prose
blocks of 150+ characters found no duplicates in public guides. Root tracked
directories fell from 19 to 13. Manual review removed semantic/gallery overlap.

BLOCKER: full development audit fails on GHSA-vfj7-8cjw-p6xm (braces <=3.0.3),
newly reviewed 2026-10-02. No patched npm release exists at the current check.
It is transitive through @next/eslint-plugin-next and vinext's commonjs plugin;
runtime audit is clean. Do not use npm audit fix --force, downgrade frameworks,
disable the audit or bypass protected-main checks. The full audit runs after
web/migration validation so tests produce evidence, but remains a blocking step.

Exact next action: inspect PR #16's latest checks, then resolve the transitive
development dependency advisory with a verified patch or compatible tool
change. No upstream patched npm version was available at the last check.
Rerun all six checks after a dependency fix; merge only when all pass.
GitHub branch-preview verification passed: banner, inline icon/title, all badges,
hardware media and the single Web UI image load; no details disclosure remains.
Initial PR CI confirms lint/build/test, documentation links and Linux migration
tests pass before the blocking development audit. Secret and Gateway jobs pass.
Use the live PR checks for the final image/firmware results and latest commit.

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
