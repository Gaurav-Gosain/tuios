package plot

import (
	"encoding/binary"
	"math"
	"testing"
)

// FuzzCharts draws every chart kind from fuzzed values and sizes, NaN, Inf
// and empty data among them. No chart may panic, and every grid must keep
// the invariants checkGrid asserts. The values decode as float64 bits, so
// the fuzzer reaches every special value.
func FuzzCharts(f *testing.F) {
	nan := math.Float64bits(math.NaN())
	inf := math.Float64bits(math.Inf(1))
	seed := func(vals ...uint64) []byte {
		b := make([]byte, 8*len(vals))
		for i, v := range vals {
			binary.LittleEndian.PutUint64(b[8*i:], v)
		}
		return b
	}
	f.Add(seed(), uint8(10), uint8(3), uint8(0), uint8(0))
	f.Add(seed(nan, inf, math.Float64bits(-1)), uint8(1), uint8(1), uint8(1), uint8(3))
	f.Add(seed(math.Float64bits(1e308), math.Float64bits(-1e308), math.Float64bits(5)), uint8(40), uint8(8), uint8(2), uint8(7))
	f.Add(seed(math.Float64bits(5e-324), 0, math.Float64bits(2)), uint8(3), uint8(0), uint8(0), uint8(2))
	f.Fuzz(func(t *testing.T, raw []byte, w, h, mode, kind uint8) {
		vals := make([]float64, len(raw)/8)
		for i := range vals {
			vals[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[8*i:]))
		}
		m := Mode(mode % 3)
		W, H := int(w%160), int(h%48)
		labels := make([]string, len(vals))
		for i := range labels {
			// Labels are ASCII here so the ASCII-mode check holds; the
			// golden cases cover wide and invalid text.
			b := []byte(string(raw[i*8 : i*8+min(8, len(raw)-i*8)]))
			for k := range b {
				b[k] = ' ' + b[k]%95
			}
			labels[i] = string(b)
			if i == 1 {
				// One wide label, for the wide-grapheme paths.
				labels[i] = "日本" + labels[i]
			}
		}
		var g *Grid
		switch kind % 12 {
		case 0:
			g = Bar{Values: vals, Labels: labels, Horizontal: true, Guide: true}.Draw(W, H, m)
		case 1:
			g = Bar{Values: vals, Labels: labels}.Draw(W, H, m)
		case 2:
			g = Line{Series: []Series{{Values: vals}, {Values: reverse(vals)}}, Axis: true}.Draw(W, H, m)
		case 3:
			g = Line{Series: []Series{{Values: vals}}, Fill: true}.Draw(W, H, m)
		case 4:
			g = Sparkline{Values: vals}.Draw(W, H, m)
		case 5:
			v, top := 0.0, 0.0
			if len(vals) > 0 {
				v = vals[0]
			}
			if len(vals) > 1 {
				top = vals[1]
			}
			g = Gauge{Value: v, Max: top, Style: GaugeStyle(kind % 2)}.Draw(W, H, m)
		case 6:
			g = Histogram{Values: vals, Bins: int(kind) * 10_000_000}.Draw(W, H, m)
		case 7:
			lo, hi := 0.0, 0.0
			if len(vals) > 1 {
				lo, hi = vals[0], vals[1]
			}
			g = Line{Series: []Series{{Values: vals}}, Min: lo, Max: hi, Axis: true}.Draw(W, H, m)
		case 8:
			g = Sparkline{Values: vals, Min: -1, Max: 1}.Draw(W, H, m)
		case 9:
			var rows [][]float64
			for i := 0; i < len(vals); i += 3 {
				rows = append(rows, vals[i:min(i+3, len(vals))])
			}
			g = Heatmap{Values: rows, RowLabels: labels, ColLabels: labels}.Draw(W, H, m)
		case 10:
			g = Pie{Values: vals, Labels: labels, Donut: kind%2 == 0, Aspect: float64(w%5) - 1}.Draw(W, H, m)
		case 11:
			g = Bar{Values: vals, Labels: labels, Axis: true}.Draw(W, H, m)
		}
		checkGrid(t, g, W, H, m)
		_ = g.String()
	})
}

func reverse(v []float64) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[len(v)-1-i] = x
	}
	return out
}
