package emulator

import "time"

// TransportStall models a bounded period during which the ESP32 playback task
// cannot accept another frame (for example, codec or scheduler contention).
type TransportStall struct {
	Start    time.Duration
	Duration time.Duration
}

// TransportModelConfig describes the timing-sensitive portion of the
// physical ESP32 full-duplex path. It deliberately runs on virtual time so a
// many-minute soak completes in milliseconds and is deterministic in CI.
type TransportModelConfig struct {
	Duration              time.Duration
	UplinkFrameInterval   time.Duration
	DownlinkFrameInterval time.Duration
	PeerPollInterval      time.Duration
	PlaybackFrameDuration time.Duration
	UplinkQueueCapacity   int
	DownlinkQueueCapacity int
	DownlinkClockDriftPPM int
	CoupledPlayback       bool
	PlaybackStalls        []TransportStall
}

// TransportModelResult exposes continuity and latency bounds for a virtual
// soak. A dropped frame is preferable to an ever-growing queue, but a healthy
// nominal run is expected to have zero drops in both directions.
type TransportModelResult struct {
	UplinkGenerated      int
	UplinkSent           int
	UplinkDropped        int
	DownlinkGenerated    int
	DownlinkReceived     int
	DownlinkPlayed       int
	DownlinkDropped      int
	MaxUplinkQueue       int
	MaxDownlinkQueue     int
	MaxUplinkAge         time.Duration
	MaxDownlinkAge       time.Duration
	PendingNetworkFrames int
}

func physicalTransportModelConfig() TransportModelConfig {
	return TransportModelConfig{
		Duration:              5 * time.Minute,
		UplinkFrameInterval:   FrameDurationMs * time.Millisecond,
		DownlinkFrameInterval: 20 * time.Millisecond,
		PeerPollInterval:      10 * time.Millisecond,
		PlaybackFrameDuration: 20 * time.Millisecond,
		UplinkQueueCapacity:   8,
		DownlinkQueueCapacity: 8,
	}
}

func driftedInterval(interval time.Duration, ppm int) time.Duration {
	if ppm == 0 {
		return interval
	}
	denominator := int64(1_000_000 + ppm)
	if denominator <= 0 {
		return interval
	}
	adjusted := time.Duration(int64(interval) * 1_000_000 / denominator)
	if adjusted < time.Millisecond {
		return time.Millisecond
	}
	return adjusted
}

func appendRealtimeFrame(queue []time.Duration, at time.Duration, capacity int) ([]time.Duration, bool) {
	if capacity <= 0 {
		return queue, true
	}
	if len(queue) < capacity {
		return append(queue, at), false
	}
	copy(queue, queue[1:])
	queue[len(queue)-1] = at
	return queue, true
}

func playbackStalledAt(stalls []TransportStall, at time.Duration) bool {
	for _, stall := range stalls {
		if stall.Duration > 0 && at >= stall.Start && at < stall.Start+stall.Duration {
			return true
		}
	}
	return false
}

// SimulateTransportSoak compares the former synchronous receive-callback
// architecture with the decoupled playback queue. In coupled mode, a steady
// 20 ms downlink can keep peer_recv_streams inside the user callback forever;
// in decoupled mode, the callback only enqueues and the peer loop remains free
// to drain microphone frames.
func SimulateTransportSoak(config TransportModelConfig) TransportModelResult {
	if config.Duration <= 0 || config.UplinkFrameInterval <= 0 ||
		config.DownlinkFrameInterval <= 0 || config.PeerPollInterval <= 0 ||
		config.PlaybackFrameDuration <= 0 {
		return TransportModelResult{}
	}
	downlinkInterval := driftedInterval(config.DownlinkFrameInterval, config.DownlinkClockDriftPPM)
	step := time.Millisecond
	nextUplink := time.Duration(0)
	nextDownlink := time.Duration(0)
	nextPeer := time.Duration(0)
	nextPlayback := time.Duration(0)
	var uplinkQueue []time.Duration
	var networkDownlink []time.Duration
	var playbackQueue []time.Duration
	result := TransportModelResult{}

	for now := time.Duration(0); now < config.Duration; now += step {
		for nextUplink <= now {
			result.UplinkGenerated++
			var dropped bool
			uplinkQueue, dropped = appendRealtimeFrame(
				uplinkQueue,
				nextUplink,
				config.UplinkQueueCapacity,
			)
			if dropped {
				result.UplinkDropped++
			}
			if len(uplinkQueue) > result.MaxUplinkQueue {
				result.MaxUplinkQueue = len(uplinkQueue)
			}
			nextUplink += config.UplinkFrameInterval
		}

		for nextDownlink <= now {
			result.DownlinkGenerated++
			// The socket receive queue is bounded in the model too. This makes
			// callback starvation visible as packet loss rather than unbounded RAM.
			var dropped bool
			networkDownlink, dropped = appendRealtimeFrame(
				networkDownlink,
				nextDownlink,
				config.DownlinkQueueCapacity,
			)
			if dropped {
				result.DownlinkDropped++
			}
			nextDownlink += downlinkInterval
		}

		if now >= nextPeer {
			if config.CoupledPlayback && len(networkDownlink) > 0 {
				arrival := networkDownlink[0]
				networkDownlink = networkDownlink[1:]
				result.DownlinkReceived++
				result.DownlinkPlayed++
				age := now - arrival
				if age > result.MaxDownlinkAge {
					result.MaxDownlinkAge = age
				}
				// The synchronous callback does not return to the microphone
				// sender while the codec write is in progress.
				nextPeer = now + config.PlaybackFrameDuration
			} else {
				for len(networkDownlink) > 0 {
					arrival := networkDownlink[0]
					networkDownlink = networkDownlink[1:]
					result.DownlinkReceived++
					var dropped bool
					playbackQueue, dropped = appendRealtimeFrame(
						playbackQueue,
						arrival,
						config.DownlinkQueueCapacity,
					)
					if dropped {
						result.DownlinkDropped++
					}
					if len(playbackQueue) > result.MaxDownlinkQueue {
						result.MaxDownlinkQueue = len(playbackQueue)
					}
				}
				for len(uplinkQueue) > 0 {
					age := now - uplinkQueue[0]
					if age > result.MaxUplinkAge {
						result.MaxUplinkAge = age
					}
					uplinkQueue = uplinkQueue[1:]
					result.UplinkSent++
				}
				nextPeer = now + config.PeerPollInterval
			}
		}

		if !config.CoupledPlayback && now >= nextPlayback && len(playbackQueue) > 0 &&
			!playbackStalledAt(config.PlaybackStalls, now) {
			arrival := playbackQueue[0]
			playbackQueue = playbackQueue[1:]
			result.DownlinkPlayed++
			age := now - arrival
			if age > result.MaxDownlinkAge {
				result.MaxDownlinkAge = age
			}
			nextPlayback = now + config.PlaybackFrameDuration
		}
	}
	result.PendingNetworkFrames = len(networkDownlink)
	return result
}
