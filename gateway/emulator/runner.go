package emulator

import (
	"fmt"
	"time"
)

// Runner executes declarative test scenarios against a Gateway.
type Runner struct {
	client *Client
}

// NewRunner creates a scenario Runner with the given emulator Client.
func NewRunner(client *Client) *Runner {
	return &Runner{client: client}
}

// RunScenario executes a complete test scenario and returns structured results.
func (r *Runner) RunScenario(sc Scenario) ScenarioResult {
	start := time.Now()
	result := ScenarioResult{
		ScenarioName: sc.Name,
		Passed:       false,
	}

	r.client.Reset()
	r.client.transitionTo(StateCommandWindow, "wake word detected: "+sc.WakeWord)

	// Step 1: Synthesize all scenario audio frames
	allFrames := GenerateScenarioFrames(sc.AudioTimeline)

	// Step 2: Concurrently start media session (CONNECTING)
	mediaStart := time.Now()
	media, err := r.client.StartMediaSession()
	if err != nil {
		result.Error = fmt.Sprintf("start media session failed: %v", err)
		result.Duration = time.Since(start)
		result.States = r.client.GetTransitions()
		return result
	}
	defer r.client.CloseMedia()

	result.TimeToConnect = time.Since(mediaStart)
	r.client.transitionTo(StateConnecting, "media session connected")

	// Step 3: During connection / tail resolution, push initial frames into pre-voice buffer
	// Simulate user speaking while connection / command resolution takes place
	commandResolved := make(chan struct{})
	go func() {
		if sc.TailDelay > 0 {
			time.Sleep(sc.TailDelay)
		}
		close(commandResolved)
	}()

	// Feed audio frames into pre-voice buffer until command tail resolves
	frameIndex := 0
	for {
		select {
		case <-commandResolved:
			goto CommandReady
		default:
			if frameIndex < len(allFrames) {
				r.client.preVoiceBuf.Push(allFrames[frameIndex])
				frameIndex++
			}
			time.Sleep(FrameDurationMs * time.Millisecond)
		}
	}

CommandReady:
	// Step 4: Submit command event and poll for terminal receipt
	commandCounter := r.client.identityMgr.NextCounter()
	cmdStart := time.Now()

	command := sc.Command
	if command == "" {
		command = "new_chat" // Default bare wake command
	}
	target := sc.Target
	if target == "" {
		target = "chatgpt"
	}
	confidence := sc.Confidence
	if confidence <= 0 {
		confidence = 1.0
	}

	receipt, err := r.client.SubmitEventWithRetry(
		"command",
		sc.WakeWord,
		command,
		target,
		sc.TargetName,
		confidence,
		commandCounter,
	)
	if err != nil {
		result.Error = fmt.Sprintf("command dispatch failed: %v", err)
		result.Duration = time.Since(start)
		result.States = r.client.GetTransitions()
		return result
	}

	result.TimeToExecuted = time.Since(cmdStart)
	result.Receipt = receipt
	r.client.transitionTo(StateConversation, "terminal receipt executed")

	// Step 5: Drain preserved frames from pre-voice buffer with 40ms pacing
	preserved, dropped, _ := r.client.preVoiceBuf.Stats()
	result.PreservedFrames = preserved
	result.DroppedFrames = dropped

	replayed := r.client.preVoiceBuf.DrainPaced(func(frame []byte) {
		_ = media.SendFrame(frame)
	})
	result.ReplayedFrames = replayed
	result.UplinkFramesSent += replayed

	// Step 6: Stream any remaining live audio frames
	if frameIndex < len(allFrames) {
		remainingFrames := allFrames[frameIndex:]
		sent := media.SendFrames(remainingFrames, nil)
		result.UplinkFramesSent += sent
	}

	// Step 7: Handle in-conversation session end if configured
	if sc.EndSessionAfter > 0 {
		time.Sleep(sc.EndSessionAfter)
		r.client.transitionTo(StateEnding, "session end trigger")
		// End session event
		endCounter := r.client.identityMgr.NextCounter()
		_, _, _ = r.client.SubmitEvent(
			"command",
			sc.WakeWord,
			"new_chat",
			target,
			"",
			1.0,
			endCounter,
		)
		r.client.CloseMedia()
		r.client.transitionTo(StateIdle, "session terminated")
	}

	// Step 8: Collect downlink audio metrics
	totalDownlink, signalDownlink := media.GetDownlinkStats()
	result.DownlinkFramesRecv = totalDownlink
	result.DownlinkSignalFrames = signalDownlink

	result.Duration = time.Since(start)
	result.States = r.client.GetTransitions()

	// Step 9: Verify expectations
	if sc.ExpectedStatus > 0 && receipt.Status != sc.ExpectedStatus {
		result.Error = fmt.Sprintf("receipt status mismatch: got %d, want %d", receipt.Status, sc.ExpectedStatus)
		return result
	}
	if sc.ExpectedPreservedFrames > 0 && result.PreservedFrames != sc.ExpectedPreservedFrames {
		result.Error = fmt.Sprintf("preserved frames mismatch: got %d, want %d", result.PreservedFrames, sc.ExpectedPreservedFrames)
		return result
	}
	if sc.ExpectedDroppedFrames > 0 && result.DroppedFrames != sc.ExpectedDroppedFrames {
		result.Error = fmt.Sprintf("dropped frames mismatch: got %d, want %d", result.DroppedFrames, sc.ExpectedDroppedFrames)
		return result
	}

	result.Passed = true
	return result
}
