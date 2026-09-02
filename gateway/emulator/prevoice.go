package emulator

import (
	"sync"
	"time"
)

const (
	// PreVoiceFrameCapacity is the maximum number of 40 ms frames stored in the
	// pre-Voice buffer (200 frames = 8.0 seconds of audio), matching the ESP32
	// firmware's MEDIA_PREVOICE_FRAME_CAPACITY.
	PreVoiceFrameCapacity = 200
)

// PreVoiceBuffer holds microphone audio frames captured between wake detection
// and authoritative Voice startup.
type PreVoiceBuffer struct {
	mu        sync.Mutex
	capacity  int
	frames    [][]byte
	preserved int
	dropped   int
	readIndex int
}

// NewPreVoiceBuffer creates a PreVoiceBuffer with the standard 200-frame capacity.
func NewPreVoiceBuffer() *PreVoiceBuffer {
	return &PreVoiceBuffer{
		capacity: PreVoiceFrameCapacity,
		frames:   make([][]byte, 0, PreVoiceFrameCapacity),
	}
}

// Push adds a frame to the pre-Voice buffer while uplink is disabled.
// If the buffer has reached its capacity, the frame is dropped and counted as overflow.
func (b *PreVoiceBuffer) Push(frame []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.frames) >= b.capacity {
		b.dropped++
		return false
	}

	frameCopy := make([]byte, len(frame))
	copy(frameCopy, frame)
	b.frames = append(b.frames, frameCopy)
	b.preserved++
	return true
}

// Stats returns the number of preserved and dropped frames.
func (b *PreVoiceBuffer) Stats() (preserved, dropped, remaining int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.preserved, b.dropped, len(b.frames) - b.readIndex
}

// PopNext returns the next preserved frame in FIFO order, or nil when the
// buffer is drained.
func (b *PreVoiceBuffer) PopNext() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.readIndex >= len(b.frames) {
		return nil
	}
	frame := b.frames[b.readIndex]
	b.readIndex++
	return frame
}

// DrainPaced drains all preserved frames into a channel or callback with
// accurate 40 ms pacing.
func (b *PreVoiceBuffer) DrainPaced(onFrame func(frame []byte)) int {
	count := 0
	for {
		frame := b.PopNext()
		if frame == nil {
			break
		}
		onFrame(frame)
		count++
		time.Sleep(FrameDurationMs * time.Millisecond)
	}
	return count
}

// Reset clears the buffer for a new session.
func (b *PreVoiceBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = b.frames[:0]
	b.preserved = 0
	b.dropped = 0
	b.readIndex = 0
}
