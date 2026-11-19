# Snowball roadmap

Implemented behavior is documented in [architecture](../ARCHITECTURE.md) and
the [firmware guide](../firmware/esp32-s3-audio/README.md). Measured results and
unexecuted acceptance cases belong in [release readiness](RELEASE_READINESS.md).
This page tracks remaining work rather than repeating installation or test commands.

## Physical and platform acceptance

- Three consecutive spoken `Hi ESP` → Voice → end cycles, each followed by
  60 seconds of silence without ghost wake, stale command or spontaneous reopen.
- Correlate board trace, signed Gateway receipt, media lifecycle and authoritative
  browser state; use the [scenario checklist](TEST_SCENARIOS.md).
- Acoustic barge-in/echo evaluation with AEC disabled, a 30-minute physical soak,
  and bounded latency, queue and memory metrics.
- Candidate refresh, configured voice/project command recognition and
  physical device revocation enforcement.
- Windows/macOS LAN Linux VM onboarding, phone media/alerts/recovery,
  reboot/autostart, and approved real upgrade/rollback acceptance.

## Maintenance priorities

| Priority | Review finding | Follow-up and acceptance |
| --- | --- | --- |
| 1 | Physical evidence predates the current Gateway and firmware. Container health does not establish that Chromium can start Voice or that the board can receive audio. | Validate the checked-in firmware and isolated Gateway image, deploy them with the original state, then run the signed-offer, audible-response, end, and silence cases above. |
| 2 | The Gateway shares one browser and one media peer. Peer replacement is guarded by peer identity, while browser actions are serialized independently. | Add a lifecycle integration scenario that overlaps a device disconnect, a project cancellation, and a new PWA or device session. Use the result to guide consolidation of browser-operation ownership in the Gateway. |
| 3 | The Playwright adapter depends on the current ChatGPT DOM. Synthetic container smoke tests prove the plumbing and auth boundary against a controlled page. | Run a periodic, explicitly initiated live smoke against the signed-in profile, recording control availability and timing without storing conversations or credentials. |
| 4 | The device registry recognizes revoked keys but Admin has no revocation workflow. | Add an authenticated revocation operation and test active-media teardown followed by rejection of events and offers from the revoked board. |

## Production security

- Custom wake model trained and validated on the supported board.
- Secure Boot, flash encryption, encrypted NVS, signed OTA and anti-rollback.
- Device key rotation, compatibility/recovery UI and physical revocation tests.
- Reassess Chromium sandbox support without weakening the container boundary.

## Development tooling

The Go device protocol, emulator and deterministic transport models already
live in `gateway/devproto/` and `gateway/emulator/`. They do not emulate
WakeNet, MultiNet, I2S, PSRAM/cache behavior or microphone acoustics.

Future work: a contributor CLI for explicit pair/run/suite operations,
repeatable audio scenario fixtures, local transcription and timing reports,
and automated authenticated event/media/revocation integration coverage.
Keep generated audio, device keys and reports in ignored local artifacts.
Real-Gateway runs require an explicit test target and must not mutate production.

## Future adapters

A paired desktop adapter is required before Codex local projects can execute.
The current Gateway returns a capability error. Preserve explicit Codex versus
ChatGPT project precedence, administrator authentication and the LAN boundary.

Use [Contributing](../CONTRIBUTING.md) for build/test commands and
[HANDOFF.md](../HANDOFF.md) for current development state.
