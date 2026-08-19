package vt_test

// Fuzzing the emulator with generated terminal input rather than random bytes.
//
// The existing FuzzEmulatorWrite targets feed the parser raw bytes, which is
// worth having but spends nearly its whole budget in the parser's ground state:
// almost no random byte string is a sequence, so almost nothing downstream of
// the parser is ever reached. These targets draw from internal/fuzz/vtgen,
// which builds real sequences carrying hostile parameters, and check invariants
// after every step rather than only watching for a panic.
//
// The generator lives in internal/fuzz/vtgen because the shipped binary must
// not link it, which cmd/tuios/imports_test.go asserts. A test file may import
// it freely: `go list -deps` on the binary does not follow test imports.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// replay runs a script into a fresh emulator and returns the first invariant it
// broke, or "" if it survived. A panic is caught and reported as a finding
// rather than taking the test binary down, so the shrinker can keep going.
func replay(s vtgen.Script) (broken string) {
	defer func() {
		if r := recover(); r != nil {
			broken = fmt.Sprintf("panic: %v", r)
		}
	}()

	emu := vt.NewEmulator(80, 24)
	for i, seq := range s {
		if seq.Kind == "resize" {
			emu.Resize(seq.Cols, seq.Rows)
		} else if _, err := emu.WriteString(seq.Bytes); err != nil {
			return fmt.Sprintf("step %d: write returned %v", i+1, err)
		}
		if bad := invariants(emu); bad != "" {
			return fmt.Sprintf("step %d (%s): %s", i+1, seq.Desc, bad)
		}
	}
	// Rendering is where a bad cell width turns into a bad row, so the whole
	// screen is read back once at the end.
	_ = emu.String()
	_ = emu.Render()
	return ""
}

// invariants are the things that must hold after any input at all. Each one is
// a class of bug that has actually shipped here: a scroll region past the end
// of the screen was a daemon-wide panic, and a cell claiming more columns than
// the row has left is a character drawn over the pane next door.
func invariants(emu *vt.Emulator) string {
	w, h := emu.Width(), emu.Height()
	if w < 0 || h < 0 {
		return fmt.Sprintf("the screen is %dx%d", w, h)
	}

	r := emu.ScrollRegion()
	if r.Min.Y < 0 || r.Max.Y > h || r.Min.X < 0 || r.Max.X > w {
		return fmt.Sprintf("the scroll region %v escapes a %dx%d screen", r, w, h)
	}
	if r.Min.Y >= r.Max.Y || r.Min.X >= r.Max.X {
		return fmt.Sprintf("the scroll region %v is empty", r)
	}

	p := emu.CursorPosition()
	if p.X < 0 || p.Y < 0 || (w > 0 && p.X >= w) || (h > 0 && p.Y >= h) {
		return fmt.Sprintf("the cursor is at %d,%d on a %dx%d screen", p.X, p.Y, w, h)
	}

	for y := range h {
		for x := range w {
			c := emu.CellAt(x, y)
			if c == nil {
				continue
			}
			if c.Width < 0 || c.Width > 2 {
				return fmt.Sprintf("cell (%d,%d) claims %d columns", x, y, c.Width)
			}
			if c.Width > 1 && x+c.Width > w {
				return fmt.Sprintf("cell (%d,%d) holds %q claiming %d columns, which runs off a %d-column row",
					x, y, c.Content, c.Width, w)
			}
		}
	}
	return ""
}

// FuzzEmulatorScript is the coverage-guided target. A corpus entry decodes to a
// script, so the mutator is steering which sequences get generated rather than
// which bytes get rejected.
func FuzzEmulatorScript(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		{0x01},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		[]byte("tuios"),
		[]byte("the quick brown fox jumps over the lazy dog"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		script := vtgen.FromBytes(data).Script(120)
		if broken := replay(script); broken != "" {
			small := vtgen.Shrink(script, func(s vtgen.Script) bool { return replay(s) != "" })
			t.Fatalf("%s\n\nreduced from %d steps to %d:\n%s",
				broken, len(script), len(small), small)
		}
	})
}

// TestVTGen_Sweep is the deterministic half: a fixed set of seeds run on every
// `go test`, so the generator earns its keep in CI rather than only when
// somebody remembers to start a fuzzing campaign. A failure names the seed,
// which reproduces it exactly.
func TestVTGen_Sweep(t *testing.T) {
	seeds, steps := 400, 150
	if testing.Short() {
		seeds, steps = 40, 60
	}

	for seed := range uint64(seeds) {
		script := vtgen.New(seed).Script(steps)
		if broken := replay(script); broken != "" {
			small := vtgen.Shrink(script, func(s vtgen.Script) bool { return replay(s) != "" })
			t.Errorf("seed %d: %s\n\nreduced from %d steps to %d:\n%s",
				seed, broken, len(script), len(small), small)
			if t.Failed() {
				return
			}
		}
	}
}

// TestVTGen_ReachesTheInterestingStates is the check that the generator is
// actually generating what it claims to. A generator that silently stopped
// emitting DCS, or never picked a parameter past the end of the screen, would
// leave every test above passing while covering nothing.
func TestVTGen_ReachesTheInterestingStates(t *testing.T) {
	want := map[string]bool{
		"resize":               false,
		"DECSTBM":              false,
		"parameter past 65535": false,
		"omitted parameter":    false,
		"tmux passthrough":     false,
		"screen passthrough":   false,
		"OSC 8 hyperlink":      false,
		"OSC 52 clipboard":     false,
		"OSC 9;4 progress":     false,
		"OSC 777":              false,
		"truecolour":           false,
		"underline colour":     false,
		"alternate screen":     false,
		"wide character":       false,
		"combining mark":       false,
		"zero-width joiner":    false,
		"regional indicator":   false,
		"invalid encoding":     false,
		"kitty graphics":       false,
		"DECALN":               false,
	}

	for seed := range uint64(60) {
		for _, seq := range vtgen.New(seed).Script(200) {
			d, b := seq.Desc, seq.Bytes
			mark := func(key string, hit bool) {
				if hit {
					want[key] = true
				}
			}
			mark("resize", seq.Kind == "resize")
			mark("DECSTBM", strings.Contains(d, "top and bottom margins"))
			mark("parameter past 65535", strings.Contains(b, "65536") || strings.Contains(b, "2147483647"))
			mark("omitted parameter", strings.Contains(b, "[;") || strings.Contains(b, ";;"))
			mark("tmux passthrough", strings.Contains(d, "tmux passthrough"))
			mark("screen passthrough", strings.Contains(d, "screen passthrough"))
			mark("OSC 8 hyperlink", strings.Contains(d, "OSC 8"))
			mark("OSC 52 clipboard", strings.Contains(d, "OSC 52"))
			mark("OSC 9;4 progress", strings.Contains(d, "OSC 9;4"))
			mark("OSC 777", strings.Contains(d, "OSC 777"))
			mark("truecolour", strings.Contains(d, "truecolour"))
			mark("underline colour", strings.Contains(d, "underline"))
			mark("alternate screen", strings.Contains(d, "alternate screen"))
			mark("wide character", strings.Contains(d, "wide characters"))
			mark("combining mark", strings.Contains(d, "combining mark"))
			mark("zero-width joiner", strings.Contains(d, "zero-width-joiner"))
			mark("regional indicator", strings.Contains(d, "regional indicator"))
			mark("invalid encoding", strings.Contains(d, "overlong") || strings.Contains(d, "cannot start"))
			mark("kitty graphics", strings.Contains(d, "kitty graphics"))
			mark("DECALN", strings.Contains(d, "DECALN"))
		}
	}

	for k, hit := range want {
		if !hit {
			t.Errorf("60 seeds of 200 steps never produced %s, so nothing below is testing it", k)
		}
	}
}
