# Hardware acceptance scenarios

Do not call Voice successful until the three pairing gates and automatic first
runtime-activation gate in [Speaker pairing acceptance](PAIRING_ACCEPTANCE.md)
pass. The source and candidate image do not prove runtime pairing. Re-check
the authenticated Gateway registry and the board's current Wi-Fi route after
every reset or re-enrollment. A routed private access subnet is valid: the
board must explicitly pass the pinned Gateway HTTP/HTTPS and Internet checks;
matching the Gateway's own `192.168.1.0/24` address range is not required.
The board previously passed that control-plane reachability and candidate-sync
gate. Two fresh bare-wake physical Voice/media cycles passed on the pre-fix
firmware; they are historical baseline evidence. The current flashed candidate
still requires a fresh three-cycle and command-tail acceptance.

## 1. Board audio and wake

1. Leave the board connected by USB-C and open the trusted Snowball Admin page
   in desktop Chrome or Edge.
2. Connect the USB device and run `audio-levels` from the browser's board
   diagnostics if available. The result should contain non-zero `sampleFrames`
   and four `RMNM` input peaks. Channels 1 and 3 are the two microphone inputs;
   channel 0 is the playback reference and channel 2 is unused.
3. Say **Hi ESP** once. The USB `wake` event with `wake:"hi_esp"` is immediate.
   The command-tail firmware listens for a bounded English tail; if none is
   detected, it resolves the bare wake as a new ChatGPT session without an
   artificial pause.

## Audible debug feedback

Debug feedback is enabled by default. It distinguishes local recognition from
Gateway delivery, so a chime alone is never interpreted as end-to-end success.

| Sound | Meaning |
| --- | --- |
| wake acknowledgement | WakeNet recognized `Hi ESP`; no network claim |
| not-ready pattern | board is unpaired/offline or the required adapter is unavailable |
| failure pattern | media, TLS, authentication, browser Voice, or playback failed |
| actual ChatGPT speech | the downlink is carrying the remote answer |

The development Gateway now contains the enrollment and device-media
endpoints. Its expected result is a signed offer, authoritative browser Voice
state, and a `media_connected` trace; a wake acknowledgement alone is still
not a Voice success.

## 2. Pairing acceptance

Use Admin Web Serial to provision the board, then require all of these before
Admin says **Pairing complete**:

1. USB status is configured, Wi-Fi connected, Gateway reachable, and
   enrollment not pending. Wi-Fi association alone is not sufficient.
2. Admin's Gateway registry shows the same hardware ID and public-key
   fingerprint as `active`.
3. The Gateway accepted the one-time P-256 device-key proof.

The first signed media offer in the next section automatically verifies the
fresh runtime counter and rejects replay, unknown, pending, and revoked keys.

The Admin success message itself is not a fifth source of truth; it is derived
from the board and Gateway checks above.

## 3. `Hi ESP` full-duplex and command-tail baseline

1. Say **Hi ESP** once.
2. Confirm the board creates a signed PCMA WebRTC offer and the Gateway binds it
   to this active device fingerprint.
3. Confirm the USB trace passes the TLS memory gate, then confirm DTLS-SRTP reaches `connected` and only then the board sends the
   signed `start_voice` event.
4. Confirm the Gateway returns `executed` only when the browser controller
   reports authoritative `voiceActive:true`.
5. Confirm `/api/status` reports `voiceLive:true` only when that browser state
   and the connected, correctly bound media peer agree. A browser Voice flag
   by itself must never make the client show “Voice is live.”
6. Speak a short English or Korean message. The ESP32 streams audio and does no
   Korean STT locally.
7. Confirm ChatGPT audio plays through the board while its microphone remains
   open.
8. End Voice and confirm the browser, Gateway peer, on-demand audio pipelines,
   and board all return to idle/WakeNet state.
9. Keep one session in continuous full-duplex use for at least 10 minutes. Talk
   while ChatGPT is speaking several times, and verify that recognition and
   interruption latency do not grow with elapsed time.
10. Compare the final ESP32 media totals with the Gateway `/api/status` or peer
   close totals. Uplink and downlink counts should agree apart from packets in
   flight at shutdown; sustained drops above 1%, a playback write above 100 ms,
   or playback stack low-water below 1 KiB fails the run.
11. Leave the board idle for at least 20 minutes, then inspect the USB trace:
   the boot log must include `PSRAM cache-safety enabled` and there must be no
   `Guru Meditation`, `Cache disabled but cached memory region accessed`, or
   unexpected reset before another `Hi ESP` attempt.

For a device command, the first signed HTTP response can be `202 processing`.
This is an accepted in-flight browser transaction, not a failure: the board
keeps its media session alive and retries the same signed envelope until the
Gateway stores a terminal `executed`, `not_ready`, or `failed` receipt.

The Gateway watcher tolerates one transient inactive browser observation during
a ChatGPT UI transition; it tears down a device peer only after two consecutive
inactive observations. The browser status remains authoritative, so this
debounce protects the transport without claiming Voice is live after the
authoritative browser state has ended.

Wake, a chime, or WebRTC alone cannot satisfy this scenario. Use the resource
limits and failure interpretation in [Runtime resource budget](RESOURCE_BUDGET.md);
do not retry a failed offer by repeatedly replaying the same feedback tone.

The previous USB trace showed `media_connected`, microphone and downlink frames,
then `device event delivery failed: ESP_ERR_INVALID_RESPONSE` during the old
synchronous command path. That separates the earlier failure from the audio
memory gate: media was live, but the command receipt contract timed out. The
current Gateway/firmware path uses an in-flight `202 processing` receipt and
bounded polling instead. A later trace also showed `DOWNLOAD(USB/UART0)` and
`waiting for download`; that is a bootloader waiting for a flash operation, not
a running Voice session.

The pre-fix physical baseline included two fresh bare `Hi ESP` cycles. Both
reached `media_connected`, sent microphone and downlink frames, and ended
with `media_ended` while the Gateway returned `voiceActive:false`. This
does not prove the flashed latency-fix candidate. The remaining acceptance
work is three consecutive post-fix cycles, `Hi ESP Resume`, and the
command-tail matrix; the measured pre-fix browser path was about 9--12 seconds
to command completion, not an ESP32 TLS-memory failure.

## 4. Wake + command candidate

After section 3's bare-wake path is stable, test the enabled one-phrase English
controls. They are still an acceptance candidate until the board passes three
fresh physical cycles:

| Say | Intended action |
| --- | --- |
| **Hi ESP Resume** | Continue the current ChatGPT conversation |
| **Hi ESP Project Snowball** | ChatGPT Project `Snowball` turn mode |
| **Hi ESP Codex Project Snowball** | Codex Project `Snowball` turn mode |
| **Hi ESP with Cove** | Select `Cove` and start Voice |

Spoken project/voice names are candidates for Gateway-side fuzzy resolution;
the ESP32 does not need exact spelling or Korean transcription.

### 4.1 Candidate synchronization

After enrollment and a short idle grace period, the board sends one signed
`event:"sync"` request. The Gateway obtains the visible authenticated
Chromium catalog, rejects malformed/oversized names, and returns bounded
`voices`/`projects` arrays. The board stores that catalog in NVS and rebuilds
the enabled MultiNet command-tail grammar on the next boot. A missing catalog
leaves only the built-in `Resume` control; production names are never compiled
into the firmware. Inspect the USB trace for `candidate_sync_completed`.

### 4.2 Project capability outcomes

ChatGPT Project commands now keep the authenticated PCMA media peer alive and
run the turn-based browser adapter. The browser speaks the localized ready
prompt, starts the narrow ChatGPT **dictation** control (never full Voice),
waits for the configured silence threshold or maximum utterance, submits the
captured text to the uniquely resolved project, and clicks **Read aloud** for
the answer. It repeats with the localized next prompt until an exact localized
exit command, an explicit stop, or the bounded 32-turn limit. The terminal
receipt is `action:"project_turn_completed"` (or a structured
`project_annotation_not_ready` result) with `mode:"turn_based"`.

If the browser build does not expose dictation, speech synthesis, the composer,
or Read aloud, the Gateway returns `chatgpt_project_turn_ready` as the
capability but the device receives an audible not-ready outcome and the trace
records the specific adapter reason. The Gateway must never open a guessed
project or claim that router Chromium can access a local Codex folder. Codex
continues to return `codex_project_host_unavailable` until a paired desktop
host adapter exists.

## Router USB trace

The OpenWrt collector stores only device output (never host-to-device Wi-Fi or
enrollment secrets) in a 5 MiB current log plus seven compressed archives,
bounded to roughly 40 MiB:

```text
snowball-esp32-debug status
snowball-esp32-debug probe
snowball-esp32-debug events 100
snowball-esp32-debug report 3
snowball-esp32-debug watch
```

`report 3` selects the most recent collector session and requires three
complete wake→command→media→terminal→media-end cycles. It prints each attempt
with its semantic command, terminal detail, transport delivery ratios, queue
drops, codec-write maximum, and playback stack headroom. It flags
cache/watchdog/media-quality faults and returns non-zero until the requested
acceptance count is actually present.
Pass an explicit collector session as the second argument to
`esp32-voice-trace-report.sh` when reviewing an archived run; do not mix a
pre-fix session with the current firmware.

The collector and flash helper select the board from the local
`/etc/snowball-esp32-board.conf` only. They accept one Espressif USB-JTAG board
by default and fail closed if multiple boards are attached; a board serial,
if needed, belongs only in that mode-0600 runtime file and is never committed
to the project.

Stop the collector before Web Serial pairing and start it again afterward,
because USB Serial/JTAG has one owner at a time. The board also keeps the latest
32 high-level states in RAM. With Web Serial connected, request them using:

```json
{"version":1,"id":"trace-1","op":"trace"}
```

No board microSD card is required. The router collector fails closed if
`/mnt/sdcard` is not mounted read-write or has less than 100 MiB free.

`snowball-esp32-debug probe` performs a bounded local diagnostic session. It
temporarily stops the collector, sends only `hello`, `status`,
`audio-self-test`, `network-test`, and `trace`, redacts the board identity/public key from its
terminal output, and restores the collector even when the USB session fails.
The network test reports the board's actual IP/DHCP route, the configured
Gateway HTTP/CA pin/TLS result, and a diagnostic-only Internet HTTP result; a
different private subnet is not rejected before the request. It does not
provision Wi-Fi, write NVS, or flash firmware. `probe` requires the
optional router `socat` package; the collector and flash helper still have a
no-`socat` fallback.

For a ROM handshake failure, run the router flash helper with its default
`SNOWBALL_FLASH_TRANSPORT=auto`. It first attempts the normal serial flash and
then uses built-in USB-JTAG. A successful JTAG recovery is reported only after
the bootloader, partition table, application, and model ranges have been read
back and compared; NVS is not touched. A `DOWNLOAD(USB/UART0)` line after that
check means BOOT was still asserted at reset, not that the application image
was erased. The helper now also requires an application boot marker before it
reports success; otherwise it fails with the BOOT/RESET recovery instruction.

## 5. Web Client regression

This is already automated by `tests/browser-smoke.mjs`:

1. Open the Snowball PWA.
2. Start Voice.
3. Confirm WebRTC is connected and the authoritative browser status reports
   `voiceActive:true`.
4. Confirm the remote ChatGPT audio track plays.
5. Stop Voice and confirm both the PWA and browser status return to idle.
