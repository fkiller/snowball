# Snowball implementation roadmap

This is the working checklist for the current hardware milestone. A checked
item means that the repository has an implementation and an automated or
repeatable verification path; it does not mean that production trust has been
granted to development firmware.

## Current milestone: development speaker + Web Client

- [x] LAN-only container boundary and isolated Docker daemon.
- [x] Separate Snowball administrator authentication and ChatGPT browser login.
- [x] CSRF, strict JSON decoding, bounded request bodies, private state files,
      and nginx Browser Console gate.
- [x] Dynamic Admin settings for wake aliases, localized project prompts,
      silence/turn timing, and discovery values.
- [x] Web Serial pairing boundary: USB remains between desktop Chrome/Edge and
      the board; Docker has no USB device mapping.
- [x] P-256 device identity, one-use enrollment token, pinned Gateway CA, and
      proof-of-possession enrollment.
- [x] Waveshare microphone/speaker initialization and four-channel AFE input.
- [x] `Hi ESP` WakeNet model and fixed English development command grammar.
- [x] Gateway parser and name-resolution tests for ChatGPT/Codex Project
      precedence and imperfect English names.
- [x] Web Client WebRTC full-duplex smoke test: media connected, ChatGPT Voice
      became active, and the stop path returned to idle.
- [~] Physical wake+command acceptance run on the speaker, including the
      latency-fix candidate's 20-minute idle soak (completed without a
      reset/panic). The latest post-fix user run reached ChatGPT Voice after
      the local wake and `new_chat` tones, but `Bye` was followed by the same
      tones with no new speech, so no clean post-fix cycle is accepted. Two fresh bare-wake
      full-duplex cycles passed on the pre-fix image
      (`media_connected`, microphone/downlink frames, `Bye`/`media_ended`,
      and Gateway `voiceActive:false`); post-fix three-cycle, `Resume`, and
      command-tail cases remain.
- [x] Audible recognition/delivery debug patterns, RAM trace, and bounded
      router-side USB logging. Physical command acceptance is still required.

## Next implementation block: device-to-Gateway runtime

- [~] Authenticated device control session after enrollment. Gateway-side
      signature verification, active-device checks, and replay cursors are in;
      physical device delivery and revocation enforcement remain.
- [~] Candidate synchronization for visible ChatGPT voices/projects. The
      authenticated `sync` event snapshots the visible Chromium catalog,
      bounds/normalizes it in Gateway, persists it in the board's NVS, and
      rebuilds the bounded MultiNet grammar immediately without a reboot;
      physical command recognition and stale-catalog refresh acceptance remain.
- [~] Device media capability: authenticated, LAN-only full-duplex audio
      transport, PCMA/DTLS-SRTP memory gates, one active session, and an
      explicit takeover policy are implemented and Gateway-tested; physical
      board transport acceptance remains.
- [~] Gateway adapter for project turn mode: the authenticated web adapter
      handles visible ChatGPT project selection, localized prompt speech,
      narrow dictation capture, typed turn submission, answer completion, and
      Read aloud; Gateway/firmware media wiring is in place and fresh physical
      acceptance remains.
- [x] Explicit capability error for Codex local projects until a paired desktop
      host adapter exists; the router Chromium never pretends to access local
      desktop folders.
- [~] Reconcile board/Gateway/Browser state so “Voice is live” is only shown
      after the authoritative browser state and the device media session agree;
      the pre-fix bare-wake baseline passed, while post-fix command and
      repeated-cycle acceptance is still open.

## Production blockers

- [ ] Editable `ChatGPT` WakeNet model trained and validated against the board.
- [ ] Signed firmware updates, Secure Boot, flash encryption, and anti-rollback.
- [ ] Device revocation, key rotation, firmware compatibility, and recovery UI.
- [ ] CI integration tests for authenticated device events/media and revocation.
- [ ] Physical iPhone/iPad WebRTC, Web Push, and Browser Console recovery runs.

## Verification commands

The required repository gates remain:

```text
npm ci --ignore-scripts
npm audit --omit=dev --audit-level=high
npm run lint
npm test
cd gateway && go test -race ./... && go vet ./...
docker build --network host -t snowball-voice:test .
```

The router production container must not be restarted for unapproved
development work. When deployment is explicitly approved, use a separately
tagged image, preserve the existing `/data` volume, retain a stopped rollback
container, and verify health, Browser Console gating, and Chromium readiness
before accepting the swap.

ESP32 build, flash, and physical acceptance now continue from Snowball-minis.
Use `docs/SNOWBALL_MINIS_HANDOFF.md`; keep the router's persistent `/data` and
Chromium session in place and move only repository source/build work.
