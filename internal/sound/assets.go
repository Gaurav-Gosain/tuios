package sound

import (
	"encoding/binary"
	"math"
)

const (
	sampleRate = 22050
	fullScale  = 32767.0
)

type note struct {
	startMS, lenMS int
	freq           float64
	level          float64
}

var cueNotes = map[Cue][]note{
	CueDone: {
		{0, 130, 659.26, 0.30},
		{110, 170, 987.77, 0.26},
	},
	CueAttention: {
		{0, 90, 1046.50, 0.42},
		{120, 90, 1046.50, 0.42},
		{240, 180, 1396.91, 0.46},
	},
}

var cueFiles = map[Cue]string{
	CueDone:      "tuios-done.wav",
	CueAttention: "tuios-needs-input.wav",
}

func renderWAV(notes []note) []byte {
	total := 0
	for _, n := range notes {
		if end := n.startMS + n.lenMS; end > total {
			total = end
		}
	}
	samples := make([]float64, ms(total+20))

	for _, n := range notes {
		start, length := ms(n.startMS), ms(n.lenMS)
		for i := range length {
			t := float64(i) / sampleRate
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
	out = le.AppendUint32(out, 16)
	out = le.AppendUint16(out, 1)
	out = le.AppendUint16(out, 1)
	out = le.AppendUint32(out, sampleRate)
	out = le.AppendUint32(out, sampleRate*2)
	out = le.AppendUint16(out, 2)
	out = le.AppendUint16(out, 16)
	out = append(out, "data"...)
	out = le.AppendUint32(out, uint32(pcm)) //nolint:gosec // a cue is well under 4 GB
	for _, v := range samples {
		out = le.AppendUint16(out, uint16(int16(max(min(v, 1), -1)*fullScale))) //nolint:gosec // clamped above
	}
	return out
}

func ms(n int) int { return n * sampleRate / 1000 }
