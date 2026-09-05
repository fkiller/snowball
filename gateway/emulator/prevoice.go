package emulator

import (
	"sync"
)

const (
	// PreVoiceFrameCapacity is the maximum number of physical 32 ms frames
	// stored in the pre-Voice buffer (200 frames = 6.4 seconds), matching the ESP32
	// firmware's MEDIA_PREVOICE_FRAME_CAPACITY.
	PreVoiceFrameCapacity = 200
	// PreVoiceReplayFrames is the latest opening-audio window sent when the
	// authoritative browser receipt arrives (15 frames = 480 ms).
	PreVoiceReplayFrames = 15
)

// PreVoiceBuffer holds microphone audio frames captured between wake detection
// and authoritative Voice startup.
type PreVoiceBuffer struct {
	mu        sync.Mutex
	capacity  int
	frames    [][]byte
	head      int
	preserved int
	dropped   int
	readIndex int
	remaining int
	prepared  bool
}

// NewPreVoiceBuffer creates a PreVoiceBuffer with the standard 200-frame capacity.
func NewPreVoiceBuffer() *PreVoiceBuffer {
	return &PreVoiceBuffer{
		capacity: PreVoiceFrameCapacity,
		frames:   make([][]byte, 0, PreVoiceFrameCapacity),
	}
}

// Push adds a frame to the pre-Voice buffer while uplink is disabled. Once
// full, it replaces the oldest frame so the physical firmware always retains
// the newest bounded opening rather than stale audio.
func (b *PreVoiceBuffer) Push(frame []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)
	b.preserved++
	if len(b.frames) < b.capacity {
		b.frames = append(b.frames, frameCopy)
		return true
	}
	b.frames[b.head] = frameCopy
	b.head = (b.head + 1) % b.capacity
	b.dropped++
	return true
}

// Stats returns the number of preserved and dropped frames.
func (b *PreVoiceBuffer) Stats() (preserved, dropped, remaining int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining = len(b.frames)
	if b.prepared {
		remaining = b.remaining
	}
	return b.preserved, b.dropped, remaining
}

// PrepareReplay selects the same latest-15-frame window used by the physical
// firmware and returns the selected and intentionally skipped counts.
func (b *PreVoiceBuffer) PrepareReplay() (selected, skipped int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	selected = len(b.frames)
	if selected > PreVoiceReplayFrames {
		selected = PreVoiceReplayFrames
	}
	skipped = len(b.frames) - selected
	b.readIndex = (b.head + skipped) % b.capacity
	b.remaining = selected
	b.prepared = true
	return selected, skipped
}

// PopNext returns the next preserved frame in FIFO order, or nil when the
// buffer is drained.
func (b *PreVoiceBuffer) PopNext() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.prepared {
		selected := len(b.frames)
		if selected > PreVoiceReplayFrames {
			selected = PreVoiceReplayFrames
		}
		b.readIndex = (b.head + len(b.frames) - selected) % b.capacity
		b.remaining = selected
		b.prepared = true
	}
	if b.remaining == 0 {
		return nil
	}
	frame := b.frames[b.readIndex]
	b.readIndex = (b.readIndex + 1) % b.capacity
	b.remaining--
	return frame
}

// DrainImmediate mirrors firmware's bounded fast drain into esp_peer. The
// 480 ms selection fits inside its send pool; live capture therefore resumes
// without adding another 480 ms of startup latency.
func (b *PreVoiceBuffer) DrainImmediate(onFrame func(frame []byte)) int {
	b.PrepareReplay()
	count := 0
	for {
		frame := b.PopNext()
		if frame == nil {
			break
		}
		onFrame(frame)
		count++
	}
	return count
}

// Reset clears the buffer for a new session.
func (b *PreVoiceBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = b.frames[:0]
	b.head = 0
	b.preserved = 0
	b.dropped = 0
	b.readIndex = 0
	b.remaining = 0
	b.prepared = false
}
