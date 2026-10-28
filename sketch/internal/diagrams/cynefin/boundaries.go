// Ported from pele src/diagrams/cynefin/boundaries.ts at 8aff183be57021b533f319a29341a529be934b08.

package cynefin

import (
	"math"
	"strings"
	"unicode/utf16"

	"tuios.dev/sketch/internal/svg"
)

// The geometry of Mermaid's Cynefin boundaries: seeded wavy lines between the domains and the
// S-shaped cliff between Clear and Chaotic. Curves are flat lists of numbers: a start point, then
// six numbers (two control points and an end point) per cubic segment.

// toUint32 is ToInt32 of a number, as its 32 bits.
func toUint32(x float64) uint32 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(x), 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return uint32(m)
}

// seededRandom is mulberry32, one step.
func seededRandom(seed float64) float64 {
	t := toUint32(seed + 0x6d2b79f5)
	t = (t ^ (t >> 15)) * (t | 1)
	t ^= t + (t^(t>>7))*(t|61)
	return float64(t^(t>>14)) / 4294967296
}

func hashString(str string) int32 {
	var hash int32
	for _, r := range str {
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			hash = (hash << 5) - hash + hi
			hash = (hash << 5) - hash + lo
			continue
		}
		hash = (hash << 5) - hash + r
	}
	return hash
}

// resolveSeed: a configured seed other than zero wins; otherwise the seed comes from the diagram's id.
func resolveSeed(configured float64, id string) float64 {
	if !math.IsNaN(configured) && !math.IsInf(configured, 0) && configured != 0 {
		return configured
	}
	return float64(hashString(id))
}

type WaveOptions struct {
	segments int
	vertical bool
	// Multipliers Mermaid uses to vary the seed per point and per control point.
	pointStep    float64
	controlStep  float64
	controlShift float64
	pinStart     bool
	pinEnd       bool
}

var FOLD = WaveOptions{segments: 7, vertical: true, pointStep: 17, controlStep: 31, controlShift: 7}
var HORIZON = WaveOptions{segments: 7, vertical: false, pointStep: 23, controlStep: 37, controlShift: 11}

// wave is a wavy line along one axis from `from` to `to`, centered on `center` in the other axis.
// Points jitter by up to the amplitude; a pinned end stays on the center line.
func wave(from, to, center, seed, amplitude float64, o WaveOptions) []float64 {
	step := (to - from) / float64(o.segments)
	along := make([]float64, 0, o.segments+1)
	across := make([]float64, 0, o.segments+1)
	for i := 0; i <= o.segments; i++ {
		pinned := (i == 0 && o.pinStart) || (i == o.segments && o.pinEnd)
		along = append(along, from+float64(float64(i)*step))
		if pinned {
			across = append(across, center)
		} else {
			r := seededRandom(seed + float64(float64(i)*o.pointStep))
			across = append(across, center+float64(float64(r*amplitude)*2)-amplitude)
		}
	}
	out := make([]float64, 0, 2+6*o.segments)
	if o.vertical {
		out = append(out, across[0], along[0])
	} else {
		out = append(out, along[0], across[0])
	}
	for i := 0; i < o.segments; i++ {
		mid := (along[i] + along[i+1]) / 2
		sign := 1.0
		if i%2 != 0 {
			sign = -1
		}
		offset := float64(float64(float64(amplitude*1.5)*sign) * seededRandom(seed+float64(float64(i)*o.controlStep)+o.controlShift))
		if o.vertical {
			out = append(out, across[i]+offset, mid, across[i+1]-offset, mid, across[i+1], along[i+1])
		} else {
			out = append(out, mid, across[i]+offset, mid, across[i+1]-offset, along[i+1], across[i+1])
		}
	}
	return out
}

// cliff is a steep S from (cx, top) down to (cx, bottom).
func cliff(cx, top, bottom, amplitude float64) []float64 {
	h := bottom - top
	return []float64{
		cx, top,
		cx + amplitude, top + float64(h*0.2), cx - float64(amplitude*1.5), top + float64(h*0.55), cx + float64(amplitude*0.5), top + float64(h*0.75),
		cx - amplitude, top + float64(h*0.85), cx + float64(amplitude*0.3), top + float64(h*0.95), cx, bottom,
	}
}

// forward writes path commands that continue from the curve's start point to its end.
func forward(b *strings.Builder, curve []float64) {
	for i := 2; i+5 < len(curve); i += 6 {
		svg.WriteStrings(b, "C", svg.Num(curve[i]), ",", svg.Num(curve[i+1]), " ", svg.Num(curve[i+2]), ",", svg.Num(curve[i+3]), " ", svg.Num(curve[i+4]), ",", svg.Num(curve[i+5]))
	}
}

// backward writes path commands that continue from the curve's end point back to its start.
func backward(b *strings.Builder, curve []float64) {
	for i := len(curve) - 6; i >= 2; i -= 6 {
		svg.WriteStrings(b, "C", svg.Num(curve[i+2]), ",", svg.Num(curve[i+3]), " ", svg.Num(curve[i]), ",", svg.Num(curve[i+1]), " ", svg.Num(curve[i-2]), ",", svg.Num(curve[i-1]))
	}
}

func curvePath(b *strings.Builder, curve []float64) {
	svg.WriteStrings(b, "M", svg.Num(curve[0]), ",", svg.Num(curve[1]))
	forward(b, curve)
}

func ellipsePath(cx, cy, rx, ry float64) string {
	r := svg.Num(rx) + "," + svg.Num(ry)
	return "M" + svg.Num(cx-rx) + "," + svg.Num(cy) + "A" + r + " 0 1,1 " + svg.Num(cx+rx) + "," + svg.Num(cy) + "A" + r + " 0 1,1 " + svg.Num(cx-rx) + "," + svg.Num(cy) + "Z"
}
