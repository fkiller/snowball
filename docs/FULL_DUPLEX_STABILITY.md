# Full-duplex stability analysis

## Status

The reproduced long-session cutoff is fixed and physically verified. Gateway
image `snowball-voice:0.3.10-full-duplex` is healthy in production and the
matching final firmware is flashed on the physical ESP32-S3. A 10 minute
30 second continuous bidirectional PCMA run completed with zero queue drops,
zero send `WOULD_BLOCK`, bounded queues, and no unplanned peer closure. The
broader acoustic release gates (ordinary spoken wake/barge-in cycles and a
30-minute physical run) remain separate follow-up validation.

## What was actually failing

There were three independent failure mechanisms in series.

1. The former Gateway uplink pipeline put a GStreamer `rtpjitterbuffer` after
   Pion had already terminated the Wi-Fi WebRTC transport. On loopback, without
   RTCP sender reports, that buffer compared the ESP32 RTP clock with the Linux
   clock. Drift either accumulated delay or crossed the drop threshold. The
   deployed `0.3.8-nojitter` image removes that second jitter buffer.
2. The ESP32 still decoded and played every 20 ms downlink packet synchronously
   inside the `esp_peer_main_loop()` receive callback. The codec write blocks
   for approximately the packet playout interval. The same media task can send
   queued 32 ms microphone frames only after the receive loop returns. A steady
   downlink therefore creates head-of-line blocking and starves microphone
   transmission even when CPU and RAM are otherwise healthy.
3. Once the ESP32 starvation was removed, a Gateway recovery watchdog exposed
   a separate deterministic cutoff. It treated only *non-silent* PCMA payloads
   as downlink liveness. ChatGPT Voice continuously sends valid 20 ms RTP
   packets while quiet, but those packets did not update
   `lastDeviceDownlinkAudio`. After two minutes of microphone activity the
   watchdog therefore closed a healthy peer as "stalled." The fix tracks
   `lastDeviceDownlinkPacket` after every successful current-peer RTP write and
   reserves `lastDeviceDownlinkAudio` for signal telemetry.

The second mechanism is confirmed by the `libpeer_default.a` call chain:

```text
esp_peer_main_loop
  -> peer_recv_streams
     -> peer_insert_rtp_payload
        -> rtp_decoder_decode
           -> on_audio
              -> peer_audio_callback        (synchronous user callback)
                 -> esp_codec_dev_write      (old blocking playback path)
```

`peer_recv_streams` can continue draining another ready packet before returning
to application code. With 20 ms downlink packets and a playout-duration codec
write in that callback, the media task has only intermittent opportunities to
call `send_queued_audio()`.

## Physical evidence

The old firmware reports 256-byte PCMA microphone frames (32 ms at 8 kHz),
while the Gateway sends 160-byte PCMA speaker frames (20 ms). Two long sessions
in `trace-physical.log` show the asymmetry directly:

| Attempt | Uplink delivered | Downlink played | Uplink audio / downlink time | End state |
| --- | ---: | ---: | ---: | --- |
| 2, 2026-09-03 21:50 | 3,444 frames = 110.208 s | 11,005 frames = 220.100 s | 50.1% | DTLS read failure, then closed about 30 s later |
| 8, 2026-09-03 23:11 | 2,642 frames = 84.544 s | 10,087 frames = 201.740 s | 41.9% | DTLS read failure, then closed about 30 s later |

The downlink count is nearly exactly one packet per 20 ms until failure. That
continuous receive load is the condition that suppresses the old task's uplink
work. At the same time, the largest internal block remained about 31,744 bytes
and the largest PSRAM block about 2.42 MiB at connection and cleanup. There is
no monotonic heap-collapse signature. Memory capacity was therefore not the
cause of this cutoff, although stack and allocation headroom remain acceptance
gates.

The historical router logs for those old sessions were lost when that container
was replaced. The instrumented physical candidate made the remaining boundary
directly observable:

| Physical run | Result | ESP32 counters | Gateway counters |
| --- | --- | --- | --- |
| Decoupled ESP32 + old watchdog | Forced close at 2m23s; Gateway logged `no PCMA response for 2m0s` | uplink sent 4,436, downlink received/played 7,052/7,052, drops 0 | uplink 4,390, downlink 7,058 |
| Decoupled ESP32 + packet-liveness watchdog | Operator stop after 10m30s; no recovery or unplanned close | uplink 19,539/19,673 generated, 133 intentionally skipped before browser-ready; downlink 31,351/31,357; drops 0 | uplink 19,539 exactly; downlink 31,615 including packets sent while disconnect propagated |

The passing run reached uplink/downlink queue high-water marks of 4/8 and 6/8,
respectively. Maximum codec playback time was 32.212 ms and playback-task stack
low-water was 3,700 bytes. The automated trace quality gate passed with no
panic or unexpected reset. This both rules out ESP32 throughput exhaustion in
the tested load and proves that the two-minute termination was the Gateway
liveness-classification defect.

## Deployed architecture

The receive callback now performs only a bounded copy into an eight-packet
sliding queue. A dedicated priority-5 playback task performs A-law decode and
the blocking codec write. The priority-6 media task remains free to return from
`esp_peer_main_loop()` and drain microphone frames.

```text
20 ms network packet -> fast receive callback -> 8-frame downlink queue
                                               -> playback task -> codec/I2S

32 ms AFE frame       -> 8-frame uplink queue  -> media task -> esp_peer
```

Both queues discard the oldest frame when full. For interactive speech, a
short gap is recoverable; indefinitely increasing delay is not. This caps the
stored downlink at 160 ms and uplink at 256 ms.

Additional ESP32 safeguards are part of the same candidate:

- the pre-Voice store is an O(1) 200-frame circular buffer (6.4 s), avoiding a
  roughly 65 KiB `memmove` while holding a cross-core critical section;
- browser-ready replay is idempotent and selects only the latest 15 frames
  (480 ms), then drains them immediately before live audio;
- the codec conversion block is 160 input samples (2.5 KiB) rather than a
  permanent 320-sample/5 KiB stack array; a 320-sample packet is processed as
  two blocks;
- an old playback task must terminate before another session can start;
- five-second and final metrics report generated/sent/dropped frames, queue
  high-water marks, `WOULD_BLOCK`, playback write maximum, and playback-task
  stack low-water mark;
- the Gateway reports and logs per-session PCMA RTP frame and byte counters in
  both directions.

AFE AEC remains disabled in this stable profile because the prior AFE AEC/SE
configuration caused watchdog failures. That is an echo-quality limitation,
not this transport cutoff's cause. It should be optimized only as a separate
measured change after transport continuity passes.

## Simulation results

`gateway/emulator/transport_model.go` models the physical 32 ms microphone
cadence, the 20 ms Gateway downlink, a 10 ms peer poll, blocking playback,
clock drift, bounded queues, and scheduled codec stalls on deterministic
virtual time.

| Model | Duration and load | Uplink | Downlink | Bound observed |
| --- | --- | ---: | ---: | ---: |
| Old coupled callback | 5 min nominal | 0 / 9,375, 9,367 dropped | 15,000 / 15,000 | uplink queue reached 8 |
| Decoupled playback | 5 min nominal | 9,375 / 9,375, 0 dropped | 15,000 / 15,000, 0 dropped | uplink age 8 ms |
| Decoupled stress | 30 min, 2,000 ppm faster downlink and a 120 ms stall every minute | 56,250 / 56,250, 0 dropped | 89,826 / 90,180, 347 intentionally dropped | maximum downlink age 159.680 ms |

The zero-uplink coupled result is a saturation envelope, not a fitted prediction
of the physical board. Real scheduler gaps allowed the old board to deliver
about 42--50% in the sessions above. The comparison isolates the architectural
failure: synchronous playback can consume every receive opportunity, while a
bounded independent playback worker prevents that load from leaking into the
uplink.

The fast emulator now also uses the real 256-byte/32 ms firmware frames and a
continuous 160-byte/20 ms Pion downlink. Its 9-second delayed-browser scenario
fills the 200-frame rolling buffer, proves oldest-frame overwrite and latest-15
replay, and exercises real bidirectional RTP rather than signaling alone.

## Physical acceptance

The transport-continuity gate for the reproduced cutoff has passed. The USB
test trigger used only to enter the normal wake -> command -> WebRTC media path
was removed before the final build and flash. NVS at `0x9000` was never
addressed.

- **PASS:** one continuous 10m30s physical full-duplex transport run.
- **PASS:** no panic, watchdog, TLS memory-gate failure, task overlap, or
  unplanned DTLS closure.
- **PASS:** steady-state uplink/downlink drops under 1% (both were zero),
  playback under 100 ms (32.212 ms), and stack headroom over 1 KiB (3.7 KiB).
- **PASS:** ESP32 uplink count exactly matched the Gateway; downlink difference
  was confined to packets sent while the deliberate close propagated.
- **PENDING:** three ordinary spoken wake/converse/end cycles including acoustic
  barge-in and `Hi ESP` end behavior.
- **PENDING:** one 30-minute physical run for additional soak confidence.

For subsequent release validation, retain these gates:

1. During steady state, ESP32 uplink drops remain below 1%, downlink drops remain
   below 1%, playback writes stay below 100 ms, and playback stack low-water
   remains at least 1,024 bytes. The normal target is zero drops.
2. ESP32 `uplink_sent` agrees with Gateway `uplinkFrames`, and Gateway
   `downlinkFrames` agrees with ESP32 `downlink_received`, allowing only packets
   already in flight at shutdown. A mismatch localizes loss to the Wi-Fi/DTLS
   boundary instead of the codec or browser bridge.
3. Internal largest-block and PSRAM-largest-block measurements do not trend
   downward across consecutive sessions.

`tools/esp32-voice-trace-report.sh` now reports these media metrics and fails a
complete cycle when sustained delivery is below 99%, downlink drops exceed 1%,
playback exceeds 100 ms, or playback stack headroom falls below 1 KiB.
