//go:build ignore

// Command gencues writes the two embedded cue files.
//
// The cues are generated rather than sourced so they can be reviewed as the
// twenty lines of arithmetic that produced them instead of as a binary nobody
// can diff. Run it from this directory after changing a note:
//
//	go run gencues.go
//
// The output format is 16-bit mono PCM WAV at 22050 Hz. WAV rather than MP3
// because every system audio player decodes it, including the ALSA and
// PipeWire tools that cannot decode a compressed stream, and because it needs
// no encoder in the build.
package main

import (
	"encoding/binary"
	"math"
	"os"
)

const (
	sampleRate = 22050
	// Headroom below full scale. An alert is a notification, not a klaxon, and
	// a cue that clips is the one people turn off first.
	fullScale = 32767.0
)

// note is one tone in a cue: where it starts, how long it lasts, its pitch and
// its share of the peak level.
type note struct {
	startMS, lenMS int
	freq           float64
	level          float64
}

func main() {
	// done: a rising fifth, soft and short. It says the machine stopped, which
	// is information rather than a request, so it stays out of the way.
	write("assets/done.wav", []note{
		{0, 130, 659.26, 0.30},  // E5
		{110, 170, 987.77, 0.26}, // B5
	})

	// needs-input: three notes with a repeated pitch and a rise at the end,
	// higher and louder than done. The repetition is what makes it read as a
	// question from across a room; the two cues have to be told apart in under
	// half a second and by ear alone.
	write("assets/needs-input.wav", []note{
		{0, 90, 1046.50, 0.42},   // C6
		{120, 90, 1046.50, 0.42}, // C6
		{240, 180, 1396.91, 0.46}, // F6
	})
}

// write renders the notes and writes the WAV.
func write(path string, notes []note) {
	total := 0
	for _, n := range notes {
		if end := n.startMS + n.lenMS; end > total {
			total = end
		}
	}
	// A few milliseconds of silence at the end so a player that cuts the last
	// buffer short does not clip the decay into a click.
	samples := make([]float64, ms(total+20))

	for _, n := range notes {
		start, length := ms(n.startMS), ms(n.lenMS)
		for i := range length {
			t := float64(i) / sampleRate
			// A touch of second harmonic keeps a pure sine from sounding like a
			// test tone, and the envelope is a fast attack into an exponential
			// decay, which is what a struck note does.
			v := math.Sin(2*math.Pi*n.freq*t) + 0.18*math.Sin(4*math.Pi*n.freq*t)
			env := math.Exp(-4.5*float64(i)/float64(length)) *
				min(float64(i)/float64(ms(4)), 1)
			samples[start+i] += n.level * env * v / 1.18
		}
	}

	pcm := make([]byte, 2*len(samples))
	for i, v := range samples {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(max(min(v, 1), -1)*fullScale))) //nolint:gosec // clamped above
	}

	out, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer out.Close()
	head := func(vs ...any) {
		for _, v := range vs {
			if err := binary.Write(out, binary.LittleEndian, v); err != nil {
				panic(err)
			}
		}
	}
	out.WriteString("RIFF")
	head(uint32(36 + len(pcm)))
	out.WriteString("WAVEfmt ")
	head(uint32(16), uint16(1), uint16(1), uint32(sampleRate),
		uint32(sampleRate*2), uint16(2), uint16(16))
	out.WriteString("data")
	head(uint32(len(pcm)))
	if _, err := out.Write(pcm); err != nil {
		panic(err)
	}
}

func ms(n int) int { return n * sampleRate / 1000 }
