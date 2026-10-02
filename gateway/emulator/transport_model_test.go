package emulator

import (
	"testing"
	"time"
)

func TestTransportSoakModelCoupledPlaybackStarvesUplink(t *testing.T) {
	config := physicalTransportModelConfig()
	config.CoupledPlayback = true
	result := SimulateTransportSoak(config)
	t.Logf("coupled 5m: uplink=%d/%d dropped=%d downlink=%d/%d dropped=%d max_queues=%d/%d",
		result.UplinkSent, result.UplinkGenerated, result.UplinkDropped,
		result.DownlinkPlayed, result.DownlinkGenerated, result.DownlinkDropped,
		result.MaxUplinkQueue, result.MaxDownlinkQueue)

	if result.UplinkGenerated < 9_000 {
		t.Fatalf("five-minute model generated too few uplink frames: %d", result.UplinkGenerated)
	}
	if result.UplinkSent >= result.UplinkGenerated/2 {
		t.Fatalf("coupled callback unexpectedly preserved full duplex: generated=%d sent=%d", result.UplinkGenerated, result.UplinkSent)
	}
	if result.UplinkDropped == 0 || result.MaxUplinkQueue != config.UplinkQueueCapacity {
		t.Fatalf("coupled callback did not expose bounded-queue loss: %+v", result)
	}
}

func TestTransportSoakModelDecoupledPlaybackPreservesNominalFullDuplex(t *testing.T) {
	config := physicalTransportModelConfig()
	result := SimulateTransportSoak(config)
	t.Logf("decoupled 5m: uplink=%d/%d dropped=%d downlink=%d/%d dropped=%d max_age=%s/%s",
		result.UplinkSent, result.UplinkGenerated, result.UplinkDropped,
		result.DownlinkPlayed, result.DownlinkGenerated, result.DownlinkDropped,
		result.MaxUplinkAge, result.MaxDownlinkAge)

	if result.UplinkDropped != 0 || result.DownlinkDropped != 0 {
		t.Fatalf("nominal decoupled soak dropped audio: %+v", result)
	}
	if missing := result.UplinkGenerated - result.UplinkSent; missing > 1 {
		t.Fatalf("nominal decoupled soak left %d uplink frames unsent: %+v", missing, result)
	}
	if missing := result.DownlinkGenerated - result.DownlinkPlayed; missing > 1 {
		t.Fatalf("nominal decoupled soak left %d downlink frames unplayed: %+v", missing, result)
	}
	if result.MaxUplinkAge > config.PeerPollInterval {
		t.Fatalf("uplink latency exceeded one peer poll: %s", result.MaxUplinkAge)
	}
	if result.MaxDownlinkAge > config.PlaybackFrameDuration {
		t.Fatalf("downlink latency exceeded one frame: %s", result.MaxDownlinkAge)
	}
}

func TestTransportSoakModelBoundsLatencyDuringDriftAndCodecStalls(t *testing.T) {
	config := physicalTransportModelConfig()
	config.Duration = 30 * time.Minute
	// This is deliberately far above normal crystal error. It forces the
	// bounded real-time policy to exercise drops during a short virtual test.
	config.DownlinkClockDriftPPM = 2_000
	for at := time.Minute; at < config.Duration; at += time.Minute {
		config.PlaybackStalls = append(config.PlaybackStalls, TransportStall{
			Start:    at,
			Duration: 120 * time.Millisecond,
		})
	}

	result := SimulateTransportSoak(config)
	t.Logf("decoupled 30m stress: uplink=%d/%d dropped=%d downlink=%d/%d dropped=%d max_age=%s max_queue=%d",
		result.UplinkSent, result.UplinkGenerated, result.UplinkDropped,
		result.DownlinkPlayed, result.DownlinkGenerated, result.DownlinkDropped,
		result.MaxDownlinkAge, result.MaxDownlinkQueue)
	if result.UplinkDropped != 0 {
		t.Fatalf("playback stalls leaked into uplink transport: %+v", result)
	}
	if result.DownlinkDropped == 0 {
		t.Fatal("stress soak did not exercise the downlink sliding window")
	}
	latencyBound := time.Duration(config.DownlinkQueueCapacity+2) * config.PlaybackFrameDuration
	if result.MaxDownlinkAge > latencyBound {
		t.Fatalf("downlink latency grew beyond bounded queue: got %s, bound %s", result.MaxDownlinkAge, latencyBound)
	}
}
