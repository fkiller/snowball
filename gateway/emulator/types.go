// Package emulator implements a machine-local paired Snowball speaker protocol
// and media emulator for automated testing and voice iteration without physical
// ESP32-S3 hardware.
package emulator

import (
	"crypto/ecdsa"
	"time"

	"snowball.local/voice-gateway/devproto"
)

// State represents the lifecycle state of the emulated speaker.
type State string

const (
	StateIdle          State = "IDLE"
	StateCommandWindow State = "COMMAND_WINDOW"
	StateConnecting    State = "CONNECTING"
	StateConversation  State = "CONVERSATION"
	StateEnding        State = "ENDING"
)

// Identity represents the cryptographic credentials and replay cursor of the
// emulated speaker.
type Identity struct {
	HardwareID           string            `json:"hardwareId"`
	PublicKeyPEM         string            `json:"publicKeyPem"`
	PublicKeyFingerprint string            `json:"publicKeyFingerprint"`
	PrivateKey           *ecdsa.PrivateKey `json:"-"`
	BootNonce            uint32            `json:"bootNonce"`
	Counter              uint32            `json:"counter"`
}

// Config specifies the runtime configuration for the emulator.
type Config struct {
	GatewayURL     string
	AdminSession   string
	StateDir       string
	DeviceName     string
	HardwareID     string
	CommandTimeout time.Duration
	PollInterval   time.Duration
}

// AudioSegment defines a timed segment of synthetic audio in a test scenario.
type AudioSegment struct {
	Kind      string        // "tone", "speech", "silence"
	Frequency float64       // For tones (e.g. 440 Hz, 1000 Hz)
	Duration  time.Duration // Duration of the segment
	Amplitude float64       // 0.0 - 1.0 (defaults to 0.8)
}

// Scenario represents a declarative test scenario to execute.
type Scenario struct {
	Name            string         // Descriptive identifier (e.g. "bare-wake-default-chat")
	WakeWord        string         // Wake phrase ("hi_esp")
	Command         string         // Command ("new_chat", "resume", "voice", "project", "")
	Target          string         // Command target ("chatgpt", "codex", "")
	TargetName      string         // Target voice or project name
	Confidence      float64        // Recognition confidence (e.g. 1.0)
	TailDelay       time.Duration  // Delay before resolving command tail (e.g. 1.8s)
	AudioTimeline   []AudioSegment // Audio played during connection / conversation
	EndSessionAfter time.Duration  // Time to send in-conversation session end (0 to disable)

	// Expectations for verification
	ExpectedStatus          int  // Expected HTTP status for terminal receipt (200)
	ExpectedPreservedFrames int  // Expected frames captured in pre-voice buffer
	ExpectedDroppedFrames   int  // Expected overflow frames dropped
	ExpectVoiceLive         bool // Whether Gateway Voice should report active
}

// ScenarioResult records the metrics and outcome of executing a Scenario.
type ScenarioResult struct {
	ScenarioName   string        `json:"scenarioName"`
	Passed         bool          `json:"passed"`
	Error          string        `json:"error,omitempty"`
	Duration       time.Duration `json:"duration"`
	TimeToConnect  time.Duration `json:"timeToConnect"`
	TimeToExecuted time.Duration `json:"timeToExecuted"`

	// Pre-Voice buffer metrics
	PreservedFrames int `json:"preservedFrames"`
	DroppedFrames   int `json:"droppedFrames"`
	ReplayedFrames  int `json:"replayedFrames"`

	// Media metrics
	UplinkFramesSent     int `json:"uplinkFramesSent"`
	DownlinkFramesRecv   int `json:"downlinkFramesRecv"`
	DownlinkSignalFrames int `json:"downlinkSignalFrames"`

	// Protocol events
	Receipt devproto.EventResult `json:"receipt"`
	States  []StateTransition    `json:"states"`
}

// StateTransition records a timestamped state transition in the emulator.
type StateTransition struct {
	From      State     `json:"from"`
	To        State     `json:"to"`
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
}
