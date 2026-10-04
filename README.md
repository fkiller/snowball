<p align="center">
  <img src="public/branding/banner.png" alt="Snowball voice banner" width="100%">
</p>

<h1 align="center">
  <img src="public/branding/icon.png" width="48" height="48" valign="middle" alt="Snowball icon">
  Snowball-Voice · Snowball-Voice-Gate — Preview
</h1>

<p align="center">
  <strong>An ESP32 voice companion and your own LAN Gateway for ChatGPT web Voice</strong>
</p>

<p align="center">
  <a href="https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1">Download</a> |
  <a href="docs/GETTING_STARTED.md">Getting started</a> |
  <a href="docs/PLATFORMS.md">Platforms</a> |
  <a href="#web-ui">Web UI</a>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/Project_source-MIT-blue.svg" alt="Project source: MIT"></a>
  <a href="https://github.com/fkiller/snowball/releases/tag/v0.4.0-alpha.1"><img src="https://img.shields.io/badge/Release-0.4.0--alpha.1-orange.svg" alt="Release: 0.4.0-alpha.1"></a>
  <a href="docs/PLATFORMS.md"><img src="https://img.shields.io/badge/Gateway-Linux_ARM64_%7C_AMD64-green.svg" alt="Gateway: Linux ARM64 and AMD64"></a>
  <a href="https://github.com/fkiller/snowball/actions/workflows/security-gate.yml"><img src="https://github.com/fkiller/snowball/actions/workflows/security-gate.yml/badge.svg?branch=main" alt="Security gate"></a>
</p>

---

## Overview

**Snowball-Voice** is the ESP32-S3 speaker client. **Snowball-Voice-Gate** is
the LAN Gateway, Web Client, and recovery console for a persistent ChatGPT
web Voice session. The Web Client also works without an ESP32.

This is an experimental developer preview. The unofficial ChatGPT web adapter
can break when the upstream interface or account requirements change. The
project is not affiliated with or endorsed by OpenAI.

## Hardware showcase

<p align="center">
  <img src="docs/images/hardware/waveshare-esp32-s3-audio.jpg" width="300" alt="Waveshare ESP32-S3-AUDIO-Board">
  &nbsp;&nbsp;&nbsp;&nbsp;
  <img src="docs/images/hardware/snowball-hardware-live.gif" width="265" alt="Snowball-Voice hardware in operation">
</p>
<p align="center">
  <em>Supported board: <a href="https://www.waveshare.com/esp32-s3-audio-board.htm">Waveshare ESP32-S3-AUDIO-Board</a> · <a href="https://x.com/fkiller/status/2088063549369102555">Live hardware demo on X</a></em>
</p>

## Web UI

The Voice Web Client provides explicit start/stop, ChatGPT Browser Console
access, and connection/recovery status. Admin provides pairing and settings.

![Snowball-Voice-Gate Web Client](docs/images/web-ui/voice-ready.png)

Actual application capture from isolated synthetic QA; status and addresses
are test values. For local access and controls, see the [Web UI guide](docs/WEB_UI.md).

## Getting started

| Product | Runtime | Guide |
| --- | --- | --- |
| Snowball-Voice-Gate | Linux ARM64/AMD64 container; Windows/macOS launchers use a LAN Linux VM/host | [Platform installation](docs/PLATFORMS.md) |
| Snowball-Voice | Waveshare ESP32-S3-AUDIO-Board, 16 MB flash / 8 MB PSRAM | [Purchase → tools → compile → flash → pair → run](docs/GETTING_STARTED.md) |

Both products ship together in the release linked above, with image archives,
firmware, checksums, corresponding source, licenses and SBOMs.
[CI publication and anonymous download verification passed](https://github.com/fkiller/snowball/actions/runs/36998111226).
Manual acceptance still has the limits below.

## Boundaries and known limits

- **LAN-only:** the configured private IPv4 exposes TCP 8088/8443 and UDP
  49000. Internal services remain loopback-only. No WAN forwarding, STUN/TURN
  or cloud relay. ChatGPT and optional Web Push use outbound Internet;
  conversation audio goes to ChatGPT. See [privacy](docs/PRIVACY.md).
- **Separate logins:** Snowball administrator authentication protects the UI,
  APIs and Browser Console. Opening the console or signing into ChatGPT never
  starts Voice. The Gateway/browser state is authoritative for live status.
- **Development firmware:** wake phrase `Hi ESP`, AEC disabled, plaintext
  NVS, and incomplete Secure Boot, flash encryption, signed OTA and anti-rollback.
  Fresh acoustic, repeated-cycle, barge-in and VM acceptance remain open.
  Prior [10m30s transport evidence](docs/FULL_DUPLEX_STABILITY.md) is separate.
- **Browser adapter:** Chromium uses `--no-sandbox` inside a constrained
  non-root container. Visible ChatGPT Projects support bounded turn mode;
  Codex local projects need a future paired desktop adapter. USB pairing
  requires desktop Chrome/Edge; supported phone browsers can use the Web Client.

## Repository structure

| Path | Purpose |
| --- | --- |
| `app/`, `public/` | Voice/Admin UI, styles, service worker and branding |
| `gateway/` | Go API, authentication, device protocol and emulator tests |
| `services/`, `container/` | Browser controller, web entry/server and container processes |
| `firmware/esp32-s3-audio/` | ESP32 source, pinned components and board configuration |
| `deploy/openwrt/` | Optional OpenWrt boot services, USB collector installation and daemon migration |
| `runtime/daemon.json` | Legacy dedicated-daemon configuration; retained at its existing path for compatibility |
| `tools/`, `tests/`, `.github/` | Install/flash/release helpers, verification and CI/CD |
| `docs/`, `LICENSES/` | User guides, acceptance evidence and vendor licenses |

The Gateway is built with the root `Dockerfile`. Persistent container/volume
identifiers remain `snowball-voice` and `/data`; firmware remains
`snowball_speaker.bin`. Never erase NVS at `0x9000`.

## Further documentation

| Document | Purpose |
| --- | --- |
| [Architecture](ARCHITECTURE.md) | Components, media and trust boundaries |
| [Firmware](firmware/esp32-s3-audio/README.md) | Implementation and USB protocol |
| [Pairing acceptance](docs/PAIRING_ACCEPTANCE.md) | Device identity and runtime gates |
| [Wake commands](docs/WAKE_COMMANDS.md) | Command tails and project behavior |
| [Test scenarios](docs/TEST_SCENARIOS.md) | Physical/browser acceptance |
| [OpenWrt operations](docs/OPENWRT.md) | Optional boot/migration and upgrade/rollback |
| [Release plan](docs/RELEASE_PLAN.md) / [readiness](docs/RELEASE_READINESS.md) | Publication gates and measured results |
| [Roadmap](docs/ROADMAP.md) | Remaining product work |
| [Security](SECURITY.md) / [Contributing](CONTRIBUTING.md) | Reports and development checks |

## License

Project-authored Gateway and ESP32 source is **MIT**: [LICENSE](LICENSE).
Espressif components/models retain Espressif-only conditions. Combined
firmware and container images include other licenses and are not exclusively
MIT. Read [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) before redistribution.
Third-party trademarks are not licensed by the project.
