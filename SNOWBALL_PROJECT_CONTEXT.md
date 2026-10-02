# Snowball — Project Context

> Historical development context. Current public setup and verification are in
> README.md, docs/GETTING_STARTED.md, docs/RELEASE_READINESS.md, and HANDOFF.md.
> Do not infer live state from the dated observations below.

This document summarizes the prior Snowball work so a new Codex project can continue without relying on the previous conversation.

## Product

Snowball is a LAN-only voice terminal for a persistent ChatGPT web session running in headed Chromium on an ARM64 FriendlyWrt/OpenWrt router. A phone or desktop browser connects to a local PWA, sends microphone audio over WebRTC, and receives ChatGPT audio back. The real Chromium window remains available through a recovery console for sign-in, CAPTCHA, permission prompts, and other human-only recovery.

Snowball is an unofficial integration with the ChatGPT web UI, not a stable API integration and not affiliated with OpenAI.

## Current continuation status (2026-08-22)

The following is the current runtime evidence for this continuation; the
historical notes below are not a substitute for these checks:

- The production `snowball-voice` container is healthy on
  `snowball-voice:final-20260821-live` (image ID
  `sha256:4e5d8bfd073389d14a37c2103b4d249bb0e07330c82812c5a0bbde30ed68721a`).
  The previous candidate container is retained as
  `snowball-voice-rollback-20260821T212922Z`; the persistent `/data`
  volume was preserved.
- `GET /api/auth/status` currently reports `authenticated:false` and
  `setupRequired:false`. The persistent administrator credential has not been
  reset by this work, and no initial setup code is present.
- The ESP32 remains connected to the router by USB for diagnostics. Its saved
  Wi-Fi address is on a routed access subnet, while Snowball is
  `192.168.1.1`; this is not a pairing failure. Runtime client addresses stay
  out of Git. The board's read-only network probe reached the Gateway HTTP and
  HTTPS endpoints, validated the pinned CA, and reached the Internet. The
  authenticated Gateway accepted the signed candidate-sync event, so
  control-plane pairing is active and the board's Wi-Fi/enrollment state
  survived the new flash. Two fresh `Hi ESP` attempts reached
  `media_connected`, sent microphone/downlink audio, and ended with
  `media_ended` before the current latency-fix firmware was flashed. Those
  are historical baseline evidence, not post-fix acceptance. The current
  candidate's three-cycle, `Resume`, and command-tail acceptance remains
  open.
- The latency-fix command-tail firmware was built with ESP-IDF 5.5.5 and
  flashed on 2026-08-21 through the router's USB-C internal USB-Serial/JTAG
  path at 115200 baud after stopping the host collector. Bootloader, partition
  table, application, and speech-model offsets were written explicitly and
  verified by esptool hashes; NVS at `0x9000` was not written, preserving
  Wi-Fi, enrollment, and identity state.
- The current safe3 latency-fix image keeps only allocations up to 4 KiB in internal
  DRAM (`CONFIG_SPIRAM_MALLOC_ALWAYSINTERNAL=4096`). This moves the 8 KiB
  `esp_peer` RTP/jitter pools to PSRAM instead of lowering the TLS gate. The
  image was rebuilt and flashed with the same NVS-preserving USB path after
  the board log showed `internalLargest=15360` immediately after
  `esp_peer_open` and `tls_memory_gate_failed`.
- The latest safe3 boot probe shows PSRAM cache-safety enabled, 15 active bounded
  English command phrases, Wi-Fi association, `internalLargest=31744`, and
  `WakeNet detector ready`. A post-flash physical Voice cycle has not yet
  been captured. The pre-fix cycles measured browser navigation and Voice
  start at about 9–12 seconds; that is a measured UX latency, not a transport
  failure. A single USB reader must remain attached for subsequent runs so
  the trace and Gateway event can be correlated.
- The latest user-observed physical behavior is better than the last USB trace:
  `Hi ESP` plays the two-note wake acknowledgement, then the one-note
  `new_chat` command acknowledgement, and ChatGPT Voice starts. After the user
  says `Bye`, Voice ends, but with no new speech the same wake and command
  acknowledgements play again. Treat this as a post-session ghost-wake defect,
  not as a completed second command. The most recent router collector session
  contains only `audio_level` records despite those audible transitions, so
  the next run must correlate a fresh board trace, Gateway event receipt, and
  authoritative browser state before changing timing constants.
- The Gateway browser watcher had an active/inactive boolean inversion that
  closed a healthy device peer two polls after Voice started. It now accepts
  only stable `ready`/Voice-control observations and debounces actual idle
  state. The browser controller also closes the current ChatGPT Voice picker
  with its supported `Back to chat` action and reloads the authenticated
  frontend shell after Chromium startup if it briefly renders the anonymous
  Voice picker. These fixes were deployed at 2026-08-21 21:29 UTC and passed
  production controller start/stop smoke; physical board acceptance is still
  pending.
- The flash helper now compares its SHA-256 with the checked-out source,
  rejects any NVS offset, and uses the one-core USB-JTAG recovery configuration
  with explicit offsets and read-back comparison. This prevents another stale
  helper or an unverified `idf.py flash` attempt.
- Older retained USB logs contain cache, watchdog, and stack failures from
  pre-fix images and failed flash/reset attempts. They must not be conflated
  with the latest boot session. The new candidate has booted cleanly; the
  post-deployment 20-minute idle soak completed without a reset, panic, or
  Gateway Voice event. The latest collector session has audio-level samples
  but no `wake_detected` event. Physical Voice acceptance remains open.
- The isolated QA runner creates a disposable Snowball administrator service
  account and fake browser controller, then verifies login, WebRTC, the
  authoritative Voice state, and stop-to-idle without production credentials,
  `/data`, Chromium, or the physical board.
- A read-only headless browser check of the deployed `/admin` route returned
  HTTP 200 for `/api/auth/status`, rendered the **Welcome back** administrator
  form, and reported no page errors. The remaining login issue is therefore
  credential validity/recovery, not a current gateway-page hydration failure.
- The router's persistent USB Serial/JTAG logger currently owns the board
  port. Do not open a second reader; only one process may own the port, and it
  must be stopped before flashing or Web Serial. The isolated QA runner has its
  own disposable administrator service account and does not need this USB port
  or production credentials.
- The router helper installation is synchronized with the checked-out source;
  `snowball-esp32-debug report 3` now runs the secret-free
  `snowball-esp32-voice-report` parser against the latest collector session and
  returns non-zero until three complete physical cycles are present. The
  current safe3 collector session recorded one complete and five incomplete
  attempts before the browser fixes were deployed; the soak added no wake/media
  events and there is no post-deployment physical Voice cycle yet. Treat
  `report 3` as the hardware acceptance gate.
- IoT build, flash, and deployment work is moving to the saved
  **Snowball-minis** development environment. Keep the Gateway `/data` volume
  and production Chromium session on Snowball-router; migrate source and build
  artifacts, not runtime credentials. The target-environment procedure and
  continuation prompt are in `docs/SNOWBALL_MINIS_HANDOFF.md`.

## Repository and deployment

- GitHub repository: https://github.com/fkiller/snowball
- Visibility: private
- Default branch: `main`
- Initial commit: `ec08ba5eae564730a5b67399123967dbb48569e7` (`Bootstrap Snowball`)
- Local source directory on the router: `/root/snowball-voice`
- Application display name: `Snowball`
- Current production image (runtime-verified 2026-08-21):
  `snowball-voice:final-20260821-live`. The checked-in `0.3.1` value is
  release metadata only; it is not a claim about the image currently running.
- Current container: `snowball-voice`
- Current container state: healthy
- The `snowball-voice` prefix is retained in internal service, volume, socket, and daemon names to preserve existing router installations and the persistent ChatGPT session.

The router's current upgrade used the same persistent Docker volume and
preserved the authenticated ChatGPT session. The candidate was deployed with
health, authentication bootstrap, admin, and console-gate smoke checks; the
runtime image recorded above is the verified production image.

## Hardware and host

- FriendlyWrt / OpenWrt `22.03.5`, target `rockchip/armv8`
- ARM64 (`aarch64`), musl libc
- LAN address: `192.168.1.1`
- The normal OpenWrt Docker daemon is stopped.
- A dedicated Docker daemon runs from `/root/snowball-voice/runtime/daemon.json`:
  - socket: `/var/run/snowball-voice-docker.sock`
  - data root: `/mnt/sdcard/snowball-voice-docker`
  - init service: `/etc/init.d/snowball-voice-dockerd`
- The dedicated daemon disables Docker bridge creation, forwarding, masquerade, and Docker-managed iptables rules.
- Existing unrelated Docker data remains untouched.

## Runtime network boundary

Only these listeners are exposed on the private LAN address:

| Listener | Purpose |
| --- | --- |
| `192.168.1.1:8088/tcp` | Local CA download and HTTPS redirect |
| `192.168.1.1:8443/tcp` | PWA, API, and recovery console |
| `192.168.1.1:49000/udp` | WebRTC ICE/media |

The following bind to loopback only: web app `3000`, browser controller `3100`, x11vnc `5900`, noVNC/websockify `6080`, gateway API `8080`, Chromium CDP `9222`, and RTP ports `49001`/`49002`.

No inbound WAN listener is intended. Outbound access is required for ChatGPT, identity-provider login, and Web Push delivery.

## Architecture

The authoritative architecture diagrams are in [ARCHITECTURE.md](ARCHITECTURE.md). Important components:

- `app/voice-console.tsx`: PWA UI, status display, recovery console link, client microphone, WebRTC lifecycle, Voice start/stop, and Web Push subscription.
- `gateway/main.go`: Go/Pion WebRTC gateway, RTP forwarding, status API, browser-controller proxy, and Web Push.
- `services/browser-controller.mjs`: Playwright attached to Chromium through loopback CDP; detects login/ready/Voice state and controls the real ChatGPT UI.
- `container/start-browser.sh`: starts headed Chromium with a persistent `/data/chromium` profile and CDP on `127.0.0.1:9222`.
- `container/supervisord.conf`: restarts Xvfb, PulseAudio, gateway, Chromium, controller, GStreamer, web app, noVNC, and nginx.
- `container/nginx.conf.template`: LAN-only HTTP/HTTPS edge and proxy for PWA, API, and recovery console.
- `container/pulse/default.pa`: virtual ChatGPT output sink and remapped `chatgpt_mic_source` input.
- `container/start-gst-uplink.sh` and `container/start-gst-downlink.sh`: WebRTC/PulseAudio RTP conversion.
- `runtime/daemon.json`: isolated Docker daemon configuration.

Media path:

```text
Client microphone
  -> WebRTC UDP 49000
  -> Pion gateway
  -> loopback RTP 49001
  -> GStreamer decode
  -> PulseAudio chatgpt_mic_sink
  -> remapped chatgpt_mic_source
  -> Chromium getUserMedia / ChatGPT

ChatGPT page audio
  -> PulseAudio chatgpt_output_sink monitor
  -> GStreamer encode
  -> loopback RTP 49002
  -> Pion gateway
  -> WebRTC UDP 49000
  -> client speaker
```

## Authentication and Voice behavior

Authentication and Voice activation are intentionally separate.

1. The user opens **Browser Console**.
2. The user signs in to ChatGPT or solves CAPTCHA/permission prompts in the real Chromium window.
3. The browser controller detects the authenticated session cookie and reports `authenticated:true`.
4. Only a later explicit PWA Voice button press may call `/api/voice/start`.

Opening Browser Console never starts Voice. A fresh unauthenticated profile calling `/api/voice/start` receives HTTP `409` and the message:

```text
Sign in through Browser Console first. Authentication never starts Voice automatically.
```

The controller must not infer readiness merely because a voice-shaped button is visible on a signed-out ChatGPT page.

## Microphone fix

The original failure was not only permission. Chromium had microphone permission but no exposed `audioinput` device; `getUserMedia({audio:true})` failed with `NotFoundError`.

The fix has two parts:

1. `container/chromium-policy.json` allows audio capture only for:

```json
{
  "AudioCaptureAllowed": false,
  "AudioCaptureAllowedUrls": [
    "https://chatgpt.com/",
    "https://[*.]chatgpt.com/"
  ]
}
```

2. PulseAudio remaps the WebRTC uplink sink monitor into a real input source named `chatgpt_mic_source`, exposed as `ChatGPT_Microphone`.

`--use-fake-ui-for-media-stream` was removed. The persistent Chromium policy is the intended permission mechanism, and the browser controller does not grant permissions programmatically.

## Recovery behavior

- Chromium is headed, not a Playwright-launched browser. Playwright attaches through loopback CDP.
- If Chromium is closed accidentally, Supervisor recreates it and the controller reconnects to the persistent profile.
- noVNC is proxied under `/console/` and opens in local scaling mode for the `1360×900` Chromium desktop.
- Touch guidance: tap to click; use a two-finger gesture to scroll.
- The PWA can send Web Push notifications when login, CAPTCHA, or human recovery is required.

## Persistent data and security

The Docker `/data` volume contains the Chromium profile/login session, generated local CA/server certificates, VAPID keys, Web Push subscriptions, and gateway state. Treat it as sensitive router credential data. It must never be committed or copied into the Git repository.

The image uses a read-only root filesystem, tmpfs runtime directories, dropped capabilities, `no-new-privileges`, and a non-root UID. Chromium currently requires `--no-sandbox` inside this container, so the container restrictions and router firewall are important security boundaries.

## Historical verification (not current physical acceptance)

- `0.3.1` image build and isolated smoke test completed successfully.
- GitHub repository contains `README.md` and `ARCHITECTURE.md`.
- Router container `snowball-voice:0.3.1` is healthy.
- API reports `gateway:ready`, `authenticated:true`, `voiceActive:false`, and `browser.state:ready`.
- HTML title reports `Snowball`.
- LAN listener check shows only `8088`, `8443`, and UDP `49000`; CDP `9222` is loopback-only.
- Previous end-to-end smoke test on `0.1.5` established WebRTC, activated actual ChatGPT Voice, stopped it, and passed idle/live recovery checks.
- A fresh unauthenticated Chromium profile was verified to reject `/api/voice/start` with HTTP `409`.
- Browser microphone permission was verified as granted, `audioinput` devices were visible, and `getUserMedia()` returned a live unmuted track.

The router host does not have Node/npm, so host-side `npm run lint` cannot run directly. The image build itself runs the production frontend build. Temporary test containers on this router's overlay storage were slow to start and were cleaned up; no test container remains running.

## Source files to read first

1. `README.md`
2. `ARCHITECTURE.md`
3. `app/voice-console.tsx`
4. `services/browser-controller.mjs`
5. `gateway/main.go`
6. `container/chromium-policy.json`
7. `container/pulse/default.pa`
8. `compose.yaml`

## Likely next work

- Continue the ESP32 ghost-wake investigation from Snowball-minis using the
  handoff document and require three consecutive clean `Hi ESP` → Voice →
  `Bye` cycles before testing secondary commands.
- Decide whether to rename internal `snowball-voice` runtime identifiers in a future migration; do not rename them casually because they protect persistent volume and service compatibility.
- Add CI on GitHub Actions for TypeScript/static tests and Go build if desired.
- Consider a versioned release/tag and optional GHCR image publication.
- Keep ChatGPT UI selectors and Voice behavior under observation because the web UI is not a stable automation API.
