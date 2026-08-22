# Runtime resource budget

The two recent failures showed that “free memory” is not enough. The
contiguous block and the owner of the USB serial port are part of the runtime
budget as well. These values are measured during the current development run
and are acceptance gates, not promises that a future feature can ignore.

## ESP32-S3 development board

Measured after the current WakeNet command-tail candidate is ready:

| Resource | Measurement | Gate |
| --- | ---: | --- |
| App image (cache-safe command-tail candidate) | `0x21fef0` bytes | below the `0x300000` app partition |
| Static DIRAM (cache-safe command-tail candidate) | 173,615 / 341,760 bytes | keep at least 64 KiB free |
| Internal largest block after speech | 31,744 bytes | at least 16 KiB before TLS |
| PSRAM largest block after speech | 2,621,440 bytes | at least 128 KiB before TLS |
| SR model partition | 3,052,232 / 6 MiB | keep the 6 MiB partition |

The ESP-IDF heap policy keeps only allocations up to 4 KiB internal
(`CONFIG_SPIRAM_MALLOC_ALWAYSINTERNAL=4096`).  This is intentional: the
8 KiB `esp_peer` RTP/jitter pools otherwise consume the contiguous internal
block during `esp_peer_open`, which was the direct cause of the observed
15,360-byte TLS-gate failure.  The gate remains at 16 KiB; it is not lowered
to conceal an allocation failure.  The post-flash physical Voice acceptance
must still record the resulting `tls_memory_gate passed` line.

WakeNet and the AFE fragment internal DRAM. mbedTLS therefore uses the 8 MiB
PSRAM (`CONFIG_MBEDTLS_EXTERNAL_MEM_ALLOC=y`); the previous internal-only
configuration failed at `mbedtls_ssl_setup` with `-0x7F00` even though more
than 100 KiB total internal memory remained. The media task performs a
contiguous-block preflight before opening the HTTPS offer connection and emits
`tls_memory_gate_failed` instead of entering an unbounded retry loop.

The cache-safe command-tail build retains 29% app-partition headroom and leaves
168,145 bytes of DIRAM in the link map. It enables both
`CONFIG_SPIRAM_FETCH_INSTRUCTIONS=y` and `CONFIG_SPIRAM_RODATA=y`, the paired
ESP-IDF settings required for flash operations to coexist with PSRAM-backed
code/read-only data. This is a mitigation for the observed
`Cache disabled but cached memory region accessed` panic, not proof that a
physical board has passed the long-idle soak. The command-tail recognizer uses the authenticated, bounded catalog persisted
in NVS; a fresh board starts with only `Resume` until its first sync completes.
The pre-fix bare-wake baseline passed two fresh cycles, but the flashed
latency-fix candidate has no post-flash physical Voice cycle yet. Three
post-fix cycles plus command-tail acceptance are still pending, so this image
must not be treated as a production release. The router flash helper defaults to the
separately built `build-cache-safe2` directory and never falls back to an
older image.

Gateway browser actions are dispatched asynchronously after a signed device
event is accepted. The board treats `202 processing` as an in-flight receipt
and polls for at most 120 half-second intervals; terminal 4xx/5xx receipts are
not retried as processing. Transport failures are separately capped at six
backoff attempts, including after processing has started. This bounds
Chromium stalls to roughly one minute without replaying the browser action or
allowing a retry loop to consume the audio session indefinitely.

Every boot, audio, speech, media, feedback, TLS, and candidate-sync boundary emits a bounded
JSON memory snapshot over USB. The router collector rotates the output and
does not require a microSD card in the board. On minimal OpenWrt images it
falls back to the built-in `stty`/`cat` reader and shell framer; `socat` and Lua
are optional accelerators, not hidden installation requirements.

## Snowball-router

The current host has 7.7 GiB RAM and no swap. The development container is
limited to 6 GiB; its largest firmware build observed 3.65 GiB. The running
Gateway container is roughly 734 MiB and is monitored separately from build
jobs. Do not run a firmware build, web build, and production restart at the
same time.

The SD-backed Docker filesystem is 58 GiB with about 15 GiB free (74% used) in
the current post-build snapshot. Warn before a build when free space is below
10 GiB; stop and ask for cleanup below 5 GiB. Build output, npm caches,
Chromium profiles, and log archives are different consumers and must not be
counted as one “free disk” number.

## USB ownership

Only one process may read `/dev/ttyACM0`. The OpenWrt serial collector must be
stopped for flashing or Web Serial, then started again immediately afterward.
If it is left running, it consumes the ESP-ROM sync bytes and `esptool` reports
that the device returned no data. The flash helper owns this stop/start
sequence and never erases NVS. `tools/flash-esp32.sh` tries the serial path by
default and, when that path fails, falls back to the board's built-in
USB-JTAG transport. The JTAG path writes only bootloader, partition table,
application, and speech-model ranges; it reads each range back and compares it
byte-for-byte. It refuses a build whose flash arguments address the NVS range.
Byte readback is not the final flash gate: the helper keeps the USB collector
stopped, resets with GPIO0 deasserted, and requires a `Snowball speaker ready`
or `WakeNet detector ready` application marker. A `DOWNLOAD(USB/UART0)` result
is reported as a failed recovery, so a board left in ROM download mode cannot
be mistaken for a working firmware deployment.

The installed OpenWrt helper checks its SHA-256 against the checked-out
`tools/flash-esp32.sh` before it touches the board. If the host copy is stale,
it fails closed and the installer must be run first; changing the repository
alone cannot silently select an older image or flash procedure.

## Acceptance evidence

A Voice run is not accepted from a wake chime alone. The USB trace must show,
in order, `wake_detected`, `tls_memory_gate`/`tls_before_offer`, a successful
Gateway offer, `media_connected`, and eventual `media_ended` or
`media_failed`. The command-tail image additionally requires
`command_resolved`, a terminal device receipt, and one explicit `Hi ESP
Resume` cycle. Three consecutive post-fix fresh cycles are still required for
full physical acceptance; the pre-fix two-cycle baseline is recorded above.
