package vt

import (
	"image/color"
	"math/rand/v2"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// randomSgrParams builds SGR parameter lists biased towards the colour forms:
// 38, 48 and 58 followed by a colour type and components, with ';' and ':'
// mixed at random and some parameters missing or out of range.
func randomSgrParams(rng *rand.Rand) ansi.Params {
	values := []int{38, 48, 58, 2, 2, 2, 5, 0, 1, 4, 7, 255, 256, 300, 17, parser.MissingParam}
	params := make(ansi.Params, 1+rng.IntN(10))
	for i := range params {
		v := values[rng.IntN(len(values))]
		if rng.IntN(3) == 0 {
			v = rng.IntN(1000)
		}
		if rng.IntN(2) == 0 {
			v |= parser.HasMoreFlag
		}
		params[i] = ansi.Param(v)
	}
	return params
}

// TestRGBParamsMatchReadStyleColor holds the truecolor shortcut to
// ansi.ReadStyleColor: wherever rgbParams accepts a list, ReadStyleColor reads
// it as five parameters and the same colour.
func TestRGBParamsMatchReadStyleColor(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	e := NewEmulator(4, 2)
	accepted := 0
	for range 200000 {
		params := randomSgrParams(rng)
		r, g, b, ok := rgbParams(params)
		if !ok {
			continue
		}
		accepted++
		var want color.Color
		if n := ansi.ReadStyleColor(params, &want); n != 5 {
			t.Fatalf("%v: rgbParams accepts it, ReadStyleColor reads %d parameters", params, n)
		}
		if got := e.rgbColor(r, g, b); got != want {
			t.Fatalf("%v: shortcut gives %#v, ReadStyleColor %#v", params, got, want)
		}
	}
	if accepted < 100 {
		t.Fatalf("only %d random lists took the shortcut; the test is not exercising it", accepted)
	}
}

// TestHandleSgrMatchesReadStyle drives the unthemed SGR path, which takes a
// shortcut for a lone truecolor colour, with random parameter lists and
// compares the pen with what uv.ReadStyle makes of the same list.
func TestHandleSgrMatchesReadStyle(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 4))
	e := NewEmulator(4, 2)
	if e.hasThemeColors() {
		t.Skip("a fresh emulator has theme colours")
	}
	for range 100000 {
		start := uv.Style{Fg: ansi.BasicColor(3), Bg: ansi.TrueColor(0x102030), Attrs: uint8(rng.IntN(256))}
		params := randomSgrParams(rng)
		if rng.IntN(2) == 0 {
			// The exact shape the shortcut takes, so it runs often.
			params = ansi.Params{ansi.Param([]int{38, 48, 58}[rng.IntN(3)]), 2,
				ansi.Param(rng.IntN(300)), ansi.Param(rng.IntN(300)), ansi.Param(rng.IntN(300))}
			if rng.IntN(2) == 0 {
				for i := range 4 {
					params[i] |= parser.HasMoreFlag
				}
			}
		}
		want := start
		uv.ReadStyle(params, &want)
		e.scr.cur.Pen = start
		e.handleSgr(params)
		if got := e.scr.cur.Pen; !got.Equal(&want) {
			t.Fatalf("%v: pen %#v, uv.ReadStyle %#v", params, got, want)
		}
	}
}
