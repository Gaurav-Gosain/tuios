package sound

import (
	"encoding/binary"
	"math"
)

// The cues are 16-bit mono PCM WAV at 22050 Hz, about 30 KB for the pair.
// WAV rather than a compressed format because every system player decodes it,
// including the ALSA and PipeWire tools that decode nothing, which is what lets
// the probe list stay short and lets aplay be on it at all.
//
// They are synthesized from the notes below the first time a cue plays, not
// embedded. The arithmetic is a few hundred bytes of code where the rendered
// files were 30 KB of binary, and it is what a reviewer reads either way. A
// cue renders once per process, in well under a millisecond, on the worker
// goroutine, never on the caller's.

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

// cueNotes holds the notes of each cue.
var cueNotes = map[Cue][]note{
	// done: a rising fifth, soft and short. It says the machine stopped, which
	// is information rather than a request, so it stays out of the way.
	CueDone: {
		{0, 130, 659.26, 0.30},   // E5
		{110, 170, 987.77, 0.26}, // B5
	},
	// needs-input: three notes with a repeated pitch and a rise at the end,
	// higher and louder than done. The repetition is what makes it read as a
	// question from across a room; the two cues have to be told apart in under
	// half a second and by ear alone.
	CueAttention: {
		{0, 90, 1046.50, 0.42},    // C6
		{120, 90, 1046.50, 0.42},  // C6
		{240, 180, 1396.91, 0.46}, // F6
	},
}

// cueFiles names each cue's spilled file. The name reaches a player's argv and
// sometimes a log, so it says what it is.
var cueFiles = map[Cue]string{
	CueDone:      "tuios-done.wav",
	CueAttention: "tuios-needs-input.wav",
}

// renderWAV renders the notes as a complete WAV file.
func renderWAV(notes []note) []byte {
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

	pcm := 2 * len(samples)
	out := make([]byte, 0, 44+pcm)
	le := binary.LittleEndian
	out = append(out, "RIFF"...)
	out = le.AppendUint32(out, uint32(36+pcm)) //nolint:gosec // a cue is well under 4 GB
	out = append(out, "WAVEfmt "...)
	out = le.AppendUint32(out, 16)           // fmt chunk size
	out = le.AppendUint16(out, 1)            // PCM
	out = le.AppendUint16(out, 1)            // mono
	out = le.AppendUint32(out, sampleRate)   // sample rate
	out = le.AppendUint32(out, sampleRate*2) // byte rate
	out = le.AppendUint16(out, 2)            // block align
	out = le.AppendUint16(out, 16)           // bits per sample
	out = append(out, "data"...)
	out = le.AppendUint32(out, uint32(pcm)) //nolint:gosec // a cue is well under 4 GB
	for _, v := range samples {
		out = le.AppendUint16(out, uint16(int16(max(min(v, 1), -1)*fullScale))) //nolint:gosec // clamped above
	}
	return out
}

func ms(n int) int { return n * sampleRate / 1000 }
