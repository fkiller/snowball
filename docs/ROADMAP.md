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
