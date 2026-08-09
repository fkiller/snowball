# Snowball

Snowball turns an ARM64 home router into a private, LAN-only voice terminal for a persistent ChatGPT web session. A phone or desktop browser supplies the microphone and speaker; Snowball bridges that audio to a headed Chromium session and keeps a recovery console available for sign-in, CAPTCHA, and permission prompts.

> [!IMPORTANT]
> Snowball is an experimental, unofficial project. It drives the ChatGPT web interface rather than a stable automation API, so upstream UI or anti-bot changes can require maintenance. It is not affiliated with or endorsed by OpenAI.

## What it does

- Provides an installable voice PWA for iOS, iPadOS, and desktop browsers.
- Carries bidirectional Opus audio over a LAN-only WebRTC connection.
- Keeps the ChatGPT login in a persistent, headed Chromium profile.
- Separates authentication from voice activation: opening the recovery console never starts Voice.
- Exposes the real Chromium window through a touch-friendly noVNC console.
- Sends Web Push alerts when login, CAPTCHA, or human recovery is required.
- Restarts Chromium and reconnects the controller if the browser window is closed.
- Grants microphone capture only to `https://chatgpt.com` and its HTTPS subdomains.
- Runs as one read-only, non-root ARM64 container under a dedicated Docker daemon.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the component model, media flows, trust boundaries, and Mermaid diagrams.

## Network surface

Snowball intentionally exposes only three sockets on the configured private LAN address:

| Listener | Purpose |
| --- | --- |
| `192.168.1.1:8088/tcp` | Local CA download and HTTPS redirect |
| `192.168.1.1:8443/tcp` | PWA, API, and browser recovery console |
| `192.168.1.1:49000/udp` | WebRTC ICE and media |

Component ports `3000`, `3100`, `5900`, `6080`, `8080`, `9222`, `49001`, and `49002` bind to loopback only. No listener binds the WAN address.

## Requirements

- An ARM64 Linux host or OpenWrt/FriendlyWrt router with Docker
- A private IPv4 LAN address assigned to the host
- About 1 GB of shared memory for Chromium
- A client browser with microphone, WebRTC, service-worker, and Web Push support
- Network access from Chromium to ChatGPT

The checked-in defaults target the current router at `192.168.1.1`. Change the `SNOWBALL_*` values in `compose.yaml` for a different LAN.

## Build and run

The current image version is `0.1.6`.

```bash
docker build --network host -t snowball-voice:0.1.6 .
docker compose up -d
docker compose ps
```

`--network host` is used only while downloading build dependencies. At runtime, nginx and the WebRTC gateway explicitly bind the configured private LAN address, while internal services bind loopback.

### Dedicated Docker daemon on OpenWrt

The production router keeps its normal Docker daemon stopped so unrelated `unless-stopped` containers cannot start. Snowball uses the included daemon configuration and init script:

- socket: `/var/run/snowball-voice-docker.sock`
- data root: `/mnt/sdcard/snowball-voice-docker`
- config: `runtime/daemon.json`
- init script: `openwrt/snowball-voice-dockerd.init`

After installing the init script as `/etc/init.d/snowball-voice-dockerd`:

```bash
export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock
/etc/init.d/snowball-voice-dockerd start
docker build --network host -t snowball-voice:0.1.6 .
docker compose up -d
```

The dedicated daemon disables Docker bridge creation, IP forwarding, masquerading, and Docker-managed iptables rules. Snowball therefore does not add routes or modify the router's WAN, VPN, or policy-based routing rules.

`Dockerfile.update` is a router-specific shortcut that overlays source changes on the locally retained `snowball-voice:0.1.1` base image. New installations should use the full `Dockerfile`.

## First-time setup

1. Open `http://192.168.1.1:8088` from the client and install the generated local CA.
2. On iPhone or iPad, enable full trust under **Settings → General → About → Certificate Trust Settings**.
3. Open `https://192.168.1.1:8443` and optionally add Snowball to the Home Screen.
4. Select **Open Browser Console** and sign in to ChatGPT. Signing in never starts Voice.
5. Return to Snowball and tap the center voice control to start a conversation.
6. Enable alerts if you want recovery notifications for future login or CAPTCHA prompts.

The console scales the `1360×900` Chromium desktop to the client viewport. Tap to click and use a two-finger gesture to scroll on touch screens.

## Persistent and sensitive state

The `/data` volume contains:

- the Chromium profile and ChatGPT login session
- the generated local CA and server certificate
- VAPID keys and Web Push subscriptions
- gateway state

Treat this volume like a credential store. None of that runtime state belongs in this repository or a Docker image.

## Verification

```bash
export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock
docker inspect --format '{{.State.Health.Status}}' snowball-voice
curl -k https://192.168.1.1:8443/api/status
netstat -lntup | grep -E '8088|8443|49000'
npm run lint
npm test
```

The browser smoke test opens the real Snowball UI with a synthetic client microphone, establishes WebRTC, confirms ChatGPT Voice becomes active, captures screenshots, and stops the session:

```bash
mkdir -p artifacts
docker run --rm --network host --user 0 --shm-size 512m \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -e SNOWBALL_TEST_OUTPUT=/artifacts \
  -v "$PWD/tests/browser-smoke.mjs:/opt/snowball/tests/browser-smoke-runtime.mjs:ro" \
  -v "$PWD/artifacts:/artifacts" \
  --entrypoint node snowball-voice:0.1.6 \
  /opt/snowball/tests/browser-smoke-runtime.mjs
```

## Repository layout

| Path | Responsibility |
| --- | --- |
| `app/` | Snowball PWA and voice/recovery controls |
| `gateway/` | Go WebRTC gateway, browser orchestration API, and Web Push |
| `services/` | Chromium controller attached through loopback CDP |
| `container/` | nginx, PulseAudio, GStreamer, Chromium, noVNC, and Supervisor configuration |
| `openwrt/` | Dedicated Docker daemon init script |
| `runtime/` | Dedicated daemon configuration |
| `tests/` | Source assertions and end-to-end browser smoke test |

## Current limitations

- Home-LAN use only; there is no STUN or TURN configuration.
- The ChatGPT web UI is an unstable integration boundary.
- Web Push depends on the client browser and its push service.
- The local CA must be explicitly trusted on every client.
- Chromium currently runs with `--no-sandbox` inside a capability-dropped, non-root container; the container and LAN boundary are part of the security model.

The internal service, volume, and daemon identifiers retain the `snowball-voice` prefix so existing router installations can upgrade without losing their persistent ChatGPT session.
