# Paired speaker emulator plan

## Goal

Move most Snowball voice iteration off the physical Waveshare
ESP32-S3-AUDIO-Board. A machine-local emulator should pair as a real device,
send authenticated control messages, establish the real PCMA WebRTC path, feed
repeatable audio scenarios, capture the returned audio, and produce a local
transcription and timing report without requiring a person for every cycle.

The emulator is a Snowball speaker protocol and media emulator, not a claim of
cycle-accurate ESP32-S3 hardware emulation. WakeNet, MultiNet, I2S codecs,
PSRAM/cache behavior, and microphone acoustics remain a small physical-board
acceptance surface.

## Test topology

```text
scenario manifest + fixed WAV/TTS
              |
              v
paired device emulator
  P-256 identity and replay cursor
  wake/command state timeline
  bounded pre-Voice audio buffer
  PCMA/8000 WebRTC client
              |
              v
actual LAN Snowball Gateway
              |
              v
Chromium / ChatGPT Voice
              |
              v
downlink WAV + local STT + JSON report
```

## Safety boundary

- Keep Snowball LAN-only. The emulator must never add a public listener, cloud
  relay, STUN, or TURN dependency.
- Do not add a development authentication bypass. Pair through the existing
  administrator-protected enrollment endpoint and complete the normal P-256
  proof flow.
- Store the emulator private key, administrator session, counters, generated
  audio, captures, and reports outside tracked source. Never print credentials
  or complete device identities in logs.
- Use an isolated Gateway state volume and fake browser controller for the fast
  suite. Real-Gateway runs are explicit integration tests and must not restart,
  replace, or mutate the production container configuration.
- A WebRTC connection alone is not success. Browser state and the correctly
  bound device peer must both make the Gateway report authoritative Voice live.

## Proposed implementation

### 1. Shared device protocol

Extract the wire types, canonical proof messages, signing helpers, and replay
rules from the Gateway into a small Go package used by both the Gateway and the
emulator. Add fixed P-256 test vectors for enrollment, device events, and media
offers so the Go implementation stays compatible with the firmware C code.

Do not maintain a second handwritten version of signature canonicalization in
the emulator.

### 2. Persistent emulator identity and pairing

Add a Go command under `gateway/cmd/device-emulator` with separate `pair`,
`run`, and `suite` operations. `pair` should:

1. create or load a P-256 identity from a user-private state directory;
2. authenticate to Snowball without persisting the administrator password;
3. request enrollment through the protected Admin endpoint;
4. sign and submit the one-time enrollment proof;
5. verify that the Gateway registry contains the matching active device.

Boot nonce and monotonically increasing counters must survive emulator process
restarts. A reset or replay scenario must be explicit in the scenario file.

### 3. Real PCMA media client

Use Pion WebRTC to generate a PCMA/8000 offer, sign the complete SDP envelope,
and send it through the existing device media endpoint. Reproduce firmware
ordering:

1. establish DTLS-SRTP;
2. submit the signed command event;
3. handle `202 processing` with an identical-envelope retry;
4. enable microphone uplink only after the terminal executed receipt;
5. capture PCMA downlink until the session ends;
6. verify that Gateway and browser state return to idle.

Convert 16 kHz fixture PCM to 8 kHz G.711 A-law using a tested implementation.
Preserve the firmware's 40 ms pacing instead of sending audio in a burst.

### 4. Scenario manifest and audio fixtures

Use a bounded YAML or JSON manifest that describes semantic controls and a
timed audio sequence. The emulator consumes the whole synthetic microphone
timeline, but only recognition annotations trigger wake or command events;
this does not pretend to run WakeNet on the desktop.

The initial matrix is:

- bare `Hi ESP`, 1.8 second command-tail timeout, then `Hello`;
- `Hi ESP Resume`, then `Hello`;
- `Hi ESP with Cove`, then `Hello`;
- wake followed immediately by speech;
- wake followed by 0, 0.5, 1.8, 3, 8, and 9 seconds of delay;
- wake without a message and explicit session termination;
- duplicate receipt polling, stale counter, fresh boot nonce, revoked device;
- packet delay, loss, duplication, reordering, and command-response loss.

Commit small deterministic speech fixtures only when their provenance and
license allow it. Generated variants and run captures belong in ignored
artifact directories.

### 5. Pre-Voice preservation

Mirror the firmware's bounded pre-Voice behavior. Audio captured after command
resolution but before the Gateway confirms browser Voice should be retained
and replayed at its original 40 ms cadence. The current firmware candidate has
an eight-second capacity, so the 8/9-second boundary is a required regression.

The report must distinguish:

- audio generated before media capture starts;
- audio preserved and eventually delivered;
- audio dropped because the bounded buffer was exceeded;
- audio delivered live after the browser-ready gate.

### 6. Local transcription and oracle

Create a pinned, CPU-capable tooling image containing ffmpeg plus either
`whisper.cpp` or an int8 faster-whisper runtime. Keep model files in a local
cache, not Git or the Snowball production image.

For each run, retain ignored artifacts containing:

- the source scenario and random seed;
- Gateway-input and emulator-downlink WAV files;
- local transcripts with model/version metadata;
- control receipts and redacted state transitions;
- latency, audio continuity, CER/WER, and pass/fail assertions.

Input audio has known text and can use a strict CER/WER threshold. ChatGPT
answers are nondeterministic, so real-browser assertions should check audible
downlink, non-empty transcription, language or expected keywords, time to first
audio, and continuity rather than exact answer equality. Literal language
translation, if needed, is an optional step after transcription and is not the
transport correctness oracle.

### 7. Deterministic network faults

Start with the clean real LAN path. Then add seeded fault injection around the
emulator's HTTPS and RTP transports. Every failed run must print the seed so it
can be replayed exactly. A Linux `tc netem` integration can be a secondary
cross-check; the primary suite should remain runnable from the Windows host.

### 8. Two execution tiers

The fast tier uses temporary state, a fake browser controller, and fixed answer
audio. It verifies pairing, signatures, replay protection, PCMA media, command
receipts, audio preservation, and cleanup on every development iteration.

The real tier targets the authenticated LAN Gateway and persistent Chromium
only when explicitly selected. It verifies the unstable browser/ChatGPT
boundary without turning normal local tests into conversations or modifying
production deployment state.

Suggested commands after implementation:

```text
npm run test:device-fast
npm run test:device-real -- --scenario bare-wake-delayed-hello
```

## Delivery sequence

1. Shared protocol package and Go/C golden vectors.
2. Emulator identity, pairing, registry verification, and secret-safe state.
3. Signed Pion PCMA offer, paced uplink, and downlink capture.
4. Scenario runner and the wake/command/delay matrix.
5. Pre-Voice boundary assertions and deterministic reports.
6. Pinned local transcription tooling and audio quality metrics.
7. Seeded network faults, retry/replay cases, and automated fast suite.
8. Explicit real-Gateway suite, documentation, and physical-board handoff.

## Acceptance criteria

- A fresh emulator completes all three pairing gates and its first signed
  runtime request passes the activation gate.
- Unknown, stale, replayed, and revoked identities fail closed.
- PCMA uplink and downlink contain real, paced audio rather than signaling-only
  success.
- Immediate `Hello` survives when it fits inside the pre-Voice budget, while
  overflow is reported rather than silently accepted.
- Retrying an in-flight signed command cannot repeat the browser action.
- Local STT recovers deterministic input fixtures within the configured
  CER/WER threshold and detects non-empty response speech.
- Every session ends with device peer, media bridge, Gateway Voice state, and
  browser state idle.
- The fast suite runs unattended and leaves no key, credential, device
  identity, log, capture, or generated runtime state eligible for commit or
  inclusion in a Docker build context.

## Remaining physical acceptance

Keep a short three-cycle physical-board gate for WakeNet/MultiNet recognition,
ES7210/ES8311 and I2S behavior, PSRAM/cache safety, acoustic echo, and actual
speaker/microphone performance. Passing the emulator suite must never be
reported as passing those hardware-specific gates.
