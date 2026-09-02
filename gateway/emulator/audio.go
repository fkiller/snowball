package emulator

import (
	"bytes"
	"encoding/binary"
	"math"
	"time"
)

const (
	// SampleRate8k is the PCMA audio sample rate.
	SampleRate8k = 8000
	// SampleRate16k is the raw microphone input sample rate.
	SampleRate16k = 16000
	// FrameDurationMs is the duration of one PCMA frame (40 ms).
	FrameDurationMs = 40
	// FrameSamples8k is the number of samples in one 40 ms frame at 8 kHz (320 samples).
	FrameSamples8k = 320
	// FrameBytes8k is the number of G.711 A-law bytes in one 40 ms frame (320 bytes).
	FrameBytes8k = 320
)

// LinearToALaw encodes a 16-bit linear PCM sample to G.711 A-law.
// Matches the ESP32 firmware linear_to_alaw implementation bit-for-bit.
func LinearToALaw(sample int16) byte {
	segmentEnd := [8]uint16{
		0x001f, 0x003f, 0x007f, 0x00ff, 0x01ff, 0x03ff, 0x07ff, 0x0fff,
	}
	value := int(sample)
	var mask byte
	if value >= 0 {
		mask = 0xd5
	} else {
		mask = 0x55
		value = -value - 1
	}
	if value > 32767 {
		value = 32767
	}
	value >>= 3
	segment := 0
	for segment < 8 && uint16(value) > segmentEnd[segment] {
		segment++
	}
	if segment >= 8 {
		return 0x7f ^ mask
	}
	encoded := byte(segment << 4)
	if segment < 2 {
		encoded |= byte((value >> 1) & 0x0f)
	} else {
		encoded |= byte((value >> segment) & 0x0f)
	}
	return encoded ^ mask
}

// ALawToLinear decodes a G.711 A-law byte to a 16-bit linear PCM sample.
func ALawToLinear(encoded byte) int16 {
	encoded ^= 0x55
	value := int(encoded&0x0f) << 4
	segment := int((encoded & 0x70) >> 4)
	switch segment {
	case 0:
		value += 8
	case 1:
		value += 0x108
	default:
		value += 0x108
		value <<= segment - 1
	}
	if encoded&0x80 != 0 {
		return int16(value)
	}
	return int16(-value)
}

// SynthesizeTone generates a pure sine wave PCM sequence at the given frequency.
func SynthesizeTone(frequency float64, duration time.Duration, sampleRate int, amplitude float64) []int16 {
	if amplitude <= 0 {
		amplitude = 0.8
	}
	numSamples := int(float64(sampleRate) * duration.Seconds())
	pcm := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		sample := math.Sin(2*math.Pi*frequency*t) * amplitude * 32767
		pcm[i] = int16(sample)
	}
	return pcm
}

// SynthesizeSpeechPattern generates a synthetic multi-harmonic acoustic pattern
// resembling human speech formants with envelope modulation (syllables).
func SynthesizeSpeechPattern(duration time.Duration, sampleRate int, amplitude float64) []int16 {
	if amplitude <= 0 {
		amplitude = 0.8
	}
	numSamples := int(float64(sampleRate) * duration.Seconds())
	pcm := make([]int16, numSamples)

	// Fundamental frequency and speech formants (F0 ~130 Hz, F1 ~500 Hz, F2 ~1500 Hz, F3 ~2500 Hz)
	f0 := 130.0
	f1 := 520.0
	f2 := 1480.0
	f3 := 2450.0

	// Syllable rate (approx 4 syllables per second)
	syllableRate := 4.0

	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)

		// Envelope shaping: smooth raised cosine pulses for syllables
		envelope := 0.5 * (1.0 - math.Cos(2*math.Pi*syllableRate*t))

		// Formant synthesis
		s0 := 0.40 * math.Sin(2*math.Pi*f0*t)
		s1 := 0.30 * math.Sin(2*math.Pi*f1*t)
		s2 := 0.20 * math.Sin(2*math.Pi*f2*t)
		s3 := 0.10 * math.Sin(2*math.Pi*f3*t)

		signal := (s0 + s1 + s2 + s3) * envelope * amplitude * 32767
		if signal > 32767 {
			signal = 32767
		} else if signal < -32768 {
			signal = -32768
		}
		pcm[i] = int16(signal)
	}
	return pcm
}

// SynthesizeSilence generates a sequence of zeroed PCM samples.
func SynthesizeSilence(duration time.Duration, sampleRate int) []int16 {
	numSamples := int(float64(sampleRate) * duration.Seconds())
	return make([]int16, numSamples)
}

// PCM16ToALaw8k converts 16-bit PCM (at 8 kHz or 16 kHz) to 8 kHz G.711 A-law.
func PCM16ToALaw8k(samples []int16, inputSampleRate int) []byte {
	if inputSampleRate == SampleRate16k {
		// 2:1 downsampling by averaging pairs, matching firmware
		alaw := make([]byte, len(samples)/2)
		for i := 0; i < len(alaw); i++ {
			s1 := int32(samples[i*2])
			s2 := int32(samples[i*2+1])
			avg := int16((s1 + s2) / 2)
			alaw[i] = LinearToALaw(avg)
		}
		return alaw
	}

	// Already 8 kHz
	alaw := make([]byte, len(samples))
	for i, s := range samples {
		alaw[i] = LinearToALaw(s)
	}
	return alaw
}

// ChunkALawIntoFrames splits an A-law byte slice into 40 ms (320-byte) frames.
// Any trailing incomplete frame is padded with A-law silence (0xd5).
func ChunkALawIntoFrames(alaw []byte) [][]byte {
	if len(alaw) == 0 {
		return nil
	}
	numFrames := (len(alaw) + FrameBytes8k - 1) / FrameBytes8k
	frames := make([][]byte, numFrames)
	for i := 0; i < numFrames; i++ {
		frame := make([]byte, FrameBytes8k)
		for j := range frame {
			frame[j] = 0xd5 // silence byte
		}
		start := i * FrameBytes8k
		end := start + FrameBytes8k
		if end > len(alaw) {
			end = len(alaw)
		}
		copy(frame, alaw[start:end])
		frames[i] = frame
	}
	return frames
}

// HasSignal determines whether an A-law payload contains non-silent audio.
// Uses the identical algorithm as Gateway's pcmaHasSignal.
func HasSignal(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	signal := 0
	for _, encoded := range payload {
		sample := ALawToLinear(encoded)
		if sample > 512 || sample < -512 {
			signal++
		}
	}
	return signal >= (len(payload)+7)/8
}

// GenerateScenarioFrames synthesizes an entire audio scenario timeline into
// sequential 40 ms G.711 A-law frames.
func GenerateScenarioFrames(timeline []AudioSegment) [][]byte {
	var allPCM []int16
	for _, seg := range timeline {
		var pcm []int16
		switch seg.Kind {
		case "tone":
			pcm = SynthesizeTone(seg.Frequency, seg.Duration, SampleRate8k, seg.Amplitude)
		case "speech":
			pcm = SynthesizeSpeechPattern(seg.Duration, SampleRate8k, seg.Amplitude)
		case "silence":
			fallthrough
		default:
			pcm = SynthesizeSilence(seg.Duration, SampleRate8k)
		}
		allPCM = append(allPCM, pcm...)
	}
	alaw := PCM16ToALaw8k(allPCM, SampleRate8k)
	return ChunkALawIntoFrames(alaw)
}

// EncodeWAV writes standard 16-bit mono RIFF WAV bytes.
func EncodeWAV(samples []int16, sampleRate int) []byte {
	buf := new(bytes.Buffer)
	dataSize := uint32(len(samples) * 2)

	// RIFF header
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")

	// fmt chunk
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16)) // Subchunk1Size (16 for PCM)
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // AudioFormat (1 for PCM)
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // NumChannels (1 mono)
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*2)) // ByteRate
	_ = binary.Write(buf, binary.LittleEndian, uint16(2))            // BlockAlign
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))           // BitsPerSample

	// data chunk
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, dataSize)
	_ = binary.Write(buf, binary.LittleEndian, samples)

	return buf.Bytes()
}
