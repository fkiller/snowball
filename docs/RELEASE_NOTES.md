# Snowball-Voice + Snowball-Voice-Gate 0.4.0-alpha.1

Coordinated experimental developer preview; device protocol **1**.
Published 2026-10-02. Source commit: `9b260cc16d3999890ad12a1450dc5ca61a23fcf6`.

## Download and install

- **Snowball-Voice**: ESP32-S3 firmware archive for Waveshare
  ESP32-S3-AUDIO-Board, 16 MB flash / 8 MB PSRAM. Includes bootloader,
  partition table, application, speech models, flash layout, and licenses.
- **Snowball-Voice-Gate**: Linux ARM64 and AMD64 source/setup bundles and
  ready-to-load Docker image archives. Windows x64/ARM64 and macOS
  Intel/Apple Silicon bundles contain SSH launchers and platform guides;
  the Gateway executes on a reachable LAN Linux VM/host.
- Exact image IDs, checksums, CycloneDX SBOMs, complete corresponding
  image source and license notices accompany the binaries. Large source
  archives may be split: concatenate numbered parts before extracting.
- `release-manifest.json` records the exact source commit and every asset's
  size and SHA-256. CI downloads and verifies every asset before publication
  and repeats the check anonymously after publication.

[Buy hardware, install tools, compile, flash, pair, and run](https://github.com/fkiller/snowball/blob/v0.4.0-alpha.1/docs/GETTING_STARTED.md).
[Install and run each Gateway platform](https://github.com/fkiller/snowball/blob/v0.4.0-alpha.1/docs/PLATFORMS.md).
[Build/security/package jobs](https://github.com/fkiller/snowball/actions/runs/36967114243).
[Successful CI publication and all-download verification](https://github.com/fkiller/snowball/actions/runs/36998111226).

Project-authored code is MIT. Espressif firmware components retain ESP-only
terms; Debian, Chromium, GStreamer, noVNC and other components retain their
own licenses. The combined binaries are not unrestricted MIT distributions.

## Limits and upgrades

Unofficial ChatGPT web-interface adapter; upstream UI/account restrictions
may require maintenance. LAN-only listeners; no WAN tunnel or cloud relay.
Wake phrase **Hi ESP**. AEC disabled, development NVS plaintext, production
Secure Boot/encrypted flash/signed OTA/anti-rollback incomplete.

CI verifies native ARM64/AMD64 builds, vulnerabilities, authenticated browser
media lifecycle, Go race/vet, web checks, and clean IDF firmware compilation.
New physical spoken cycles, acoustic barge-in, Windows/macOS VM onboarding,
host reboot and real migration acceptance remain manual and unexecuted for
this tag. Prior 10m30s transport evidence is documented separately.

Preserve Gateway `/data` and the `snowball-voice` container/volume identity.
Firmware upgrades write only the four approved ranges; **never erase or
write NVS at 0x9000**. Publication performs no production deployment or flash.
