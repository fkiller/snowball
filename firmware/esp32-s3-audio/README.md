# Snowball-Voice ESP32-S3 audio firmware

For purchase, tool installation, Windows/Linux builds, protected flashing,
Gateway setup, pairing, and first use, follow the complete
[getting-started guide](../../docs/GETTING_STARTED.md). Project-authored source
is MIT; [Espressif component/model terms](../../THIRD_PARTY_NOTICES.md) remain
applicable to combined firmware. Prepared version: `0.4.0-alpha.1`.

Target: Waveshare ESP32-S3-AUDIO-Board, 16 MB flash, 8 MB octal PSRAM.

See the [hardware showcase](../../README.md#hardware-showcase) for board and
live-demo images; the purchasing guide contains connector details.

Current development milestone:

- initializes the ES7210 microphone ADC and ES8311 speaker DAC at 16 kHz;
- runs Espressif `wn9_hiesp` WakeNet and an English command grammar built from
  the last authenticated Gateway candidate catalog;
- delays the proven two-note wake confirmation until command resolution, so
  one-phrase requests are not covered by speaker output;
- emits distinct non-blocking sounds for the recognized command and for
  executed, not-ready, and failed delivery outcomes;
- retains the latest 32 high-level diagnostic transitions in RAM and exposes
  them through the strict USB `trace` operation;
- creates a P-256 device key and exposes only its public key/fingerprint;
- accepts strict, bounded NDJSON provisioning requests over USB Serial/JTAG;
- stores Wi-Fi, Gateway pin, and a one-time enrollment token without echoing
  secrets;
- downloads the local CA over the bootstrap HTTP port, verifies its DER SHA-256
  pin, and completes enrollment over TLS with a one-use token plus P-256
  proof-of-possession;
- queues wake, candidate-sync, and English command events for delivery over pinned HTTPS to the
  Gateway, signing each event with the device key and binding it to a boot
  persistent monotonic boot sequence plus strictly increasing counter;
- distinguishes development plaintext NVS from future production encrypted NVS.

No production voice or project names are compiled into the firmware. After a
short post-boot grace period, the board sends one signed `sync` event; the
Gateway returns the visible authenticated Chromium catalog, which is validated
and stored in NVS. The next boot rebuilds the enabled MultiNet command tail
from that catalog. A fresh board therefore has only the built-in control word
`Resume` until the first sync has completed.

The Gateway control-event verifier and authenticated candidate-sync transport
are implemented. The prior final development image passed a 10m30s physical
transport run; ordinary acoustic cycles, broader sync/revocation acceptance,
and signed OTA remain open. It must
not be treated as production-trusted until those paths, Secure Boot, flash
encryption, and anti-rollback are implemented and tested.

## Build

Use the pinned ESP-IDF container so the router host does not need a native IDF
installation:

```bash
docker run --rm \
  -v "$PWD/firmware/esp32-s3-audio:/project" \
  -w /project espressif/idf:v5.5.5 \
  bash -lc 'source /opt/esp/idf/export.sh && idf.py set-target esp32s3 && idf.py build'
```

For the current cache-safe candidate, use a persistent generated configuration
inside the build directory so a later flash does not depend on `/tmp`:

```bash
docker run --rm \
  -v "$PWD/firmware/esp32-s3-audio:/project" \
  -w /project espressif/idf:v5.5.5 \
  bash -lc 'source /opt/esp/idf/export.sh && \
    idf.py -D SDKCONFIG=/project/build-cache-safe2/sdkconfig \
      -D SDKCONFIG_DEFAULTS=/project/sdkconfig.defaults \
      -B build-cache-safe2 build'
```

The candidate deliberately sets `CONFIG_SPIRAM_MALLOC_ALWAYSINTERNAL=4096`.
The `esp_peer` RTP/jitter pools are 8 KiB and must use PSRAM; the TLS
preflight remains a real 16 KiB contiguous-internal-memory gate.

Build output and managed components are intentionally ignored by Git.

Do not flash with Secure Boot or flash-encryption release mode during
development; those operations can irreversibly burn eFuses. The board's full
factory flash should be backed up before the first write.

The router helper owns the USB port while flashing and restores the serial
collector afterward:

```sh
/usr/libexec/snowball-flash-esp32
```

It defaults to `SNOWBALL_FLASH_TRANSPORT=auto`: it tries an explicit
`esptool write_flash` of the four generated image ranges first, then uses the
ESP32-S3 built-in USB-JTAG adapter if the ROM serial handshake fails. The JTAG
recovery path performs an exact readback comparison; neither path erases or
writes NVS. Use
`SNOWBALL_FLASH_TRANSPORT=serial` or `jtag` only when diagnosing a transport.
Release BOOT before the final reset; a board left in `DOWNLOAD(USB/UART0)` has
not started the application even if the flash bytes are correct. The helper
opens the serial port before reset and requires the firmware's `Snowball
speaker ready` or `WakeNet detector ready` marker, so matching flash bytes
alone are not reported as a successful deployment.

## USB protocol examples

One JSON object per line, maximum 2048 bytes:

```json
{"version":1,"id":"hello-1","op":"hello"}
{"version":1,"id":"status-1","op":"status"}
{"version":1,"id":"audio-1","op":"audio-self-test"}
{"version":1,"id":"network-1","op":"network-test"}
{"version":1,"id":"trace-1","op":"trace"}
{"version":1,"id":"debug-1","op":"debug-mode","payload":{"enabled":true}}
```

`network-test` is read-only. It reports the board's assigned IPv4 route,
performs an actual request to the configured Gateway CA endpoint (including
the existing CA pin check), an HTTPS `/api/health` request using that pinned
certificate, and a diagnostic-only HTTP request to `example.com`. It does not
write Wi-Fi credentials, NVS, or device identity.
The Gateway may be on a routed private subnet; `sameSubnet:false` is
diagnostic information, not an automatic failure.

Provisioning adds a `payload` containing `ssid`, `password`, private IPv4
`gateway`, `gatewayHttpPort`, `gatewayPort`, lowercase or uppercase 64-digit `caSha256`,
`enrollmentToken`, and `expiresInSeconds` (30–900). Responses never repeat
passwords or tokens.

Factory reset additionally requires the exact payload
`{"confirm":"ERASE SNOWBALL"}` and erases the device key as well as network
configuration.
