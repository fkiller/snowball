# Full-duplex stability analysis

## Status

The source contains a locally built candidate for the remaining ESP32
full-duplex starvation problem. It has passed deterministic virtual-time and
Pion WebRTC tests, but it has **not** been flashed to the physical board or
deployed to the production router. The running router image remains
`snowball-voice:0.3.8-nojitter`; the physical board remains on firmware commit
`705af7b`.

## What was actually failing

There were two independent latency mechanisms in series.

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

The historical router logs for these sessions were lost when that container was
replaced, so the final browser-side reason cannot be proven retrospectively.
The supported inference is that degraded/absent microphone delivery first made
the conversation unusable; an authoritative browser-idle observation or peer
closure then closed the Gateway peer, which the ESP32 surfaced as
`dtls_srtp_read failed`. The new cross-end counters are intended to make that
last causal step directly observable on the next physical run.

## Candidate architecture

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

Do not call this fixed until a candidate is flashed without touching NVS and
all of the following pass:

1. Three ordinary wake/converse/end cycles with no panic, watchdog, overlapping
   playback task, or TLS memory-gate failure.
2. One continuous 10-minute full-duplex conversation, including repeated
   barge-in while ChatGPT is speaking, followed by a clean `Hi ESP` end.
3. One 30-minute session or equivalent unattended audio soak if the 10-minute
   run is clean.
4. During steady state, ESP32 uplink drops remain below 1%, downlink drops remain
   below 1%, playback writes stay below 100 ms, and playback stack low-water
   remains at least 1,024 bytes. The normal target is zero drops.
5. ESP32 `uplink_sent` agrees with Gateway `uplinkFrames`, and Gateway
   `downlinkFrames` agrees with ESP32 `downlink_received`, allowing only packets
   already in flight at shutdown. A mismatch localizes loss to the Wi-Fi/DTLS
   boundary instead of the codec or browser bridge.
6. Internal largest-block and PSRAM-largest-block measurements do not trend
   downward across consecutive sessions.

`tools/esp32-voice-trace-report.sh` now reports these media metrics and fails a
complete cycle when sustained delivery is below 99%, downlink drops exceed 1%,
playback exceeds 100 ms, or playback stack headroom falls below 1 KiB.
