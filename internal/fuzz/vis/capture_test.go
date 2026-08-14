package vis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Frame captures. The display's own capture round caught two bugs that every
// unit assertion had passed: a band with an unfilled interior, and a non-ASCII
// glyph leaking into the ASCII frame. So the frames are written out and the
// assertions are made against the drawn output, not against the state behind it.
//
// TUIOS_VIS_CAPTURE=<dir> writes every frame this file builds as an ANSI dump
// for a human to look at. Without it the test still renders every frame and
// still asserts, it just keeps nothing.

func captureDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TUIOS_VIS_CAPTURE")
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func save(t *testing.T, name, frame string) {
	t.Helper()
	dir := captureDir(t)
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name+".ansi"), []byte(frame+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// demoRules is a stand-in registry with the shape a real one has: several
// families, several rules each. The real display takes the target's registry;
// tests take this so they do not depend on the oracle's current contents.
func demoRules() []fuzz.RuleInfo {
	return []fuzz.RuleInfo{
		{Name: "panic", Family: "process", Doc: "Update swallowed a panic"},
		{Name: "render-panic", Family: "process", Doc: "a rule panicked composing a frame"},
		{Name: "focus-index", Family: "model", Doc: "the focused pane index is outside the pane slice"},
		{Name: "workspace-range", Family: "model", Doc: "a workspace number is outside the configured range"},
		{Name: "nil-pane", Family: "model", Doc: "the pane slice holds a nil"},
		{Name: "pane-size", Family: "geometry", Doc: "a guest was told a size its emulator does not hold"},
		{Name: "pane-overflow", Family: "geometry", Doc: "a pane rendered larger than its box"},
		{Name: "spurious-winch", Family: "geometry", Doc: "a pane whose drawable size did not move was resized anyway"},
		{Name: "layout-overlap", Family: "layout", Doc: "two tiled panes claim the same cell"},
		{Name: "frame-size", Family: "layout", Doc: "the frame handed to the host is larger than the host"},
		{Name: "stuck-gesture", Family: "input", Doc: "a drag is live with no button held, so the pane stops taking input"},
		{Name: "scrollbar-column", Family: "render", Doc: "a scrollbar sits outside its own pane's last column"},
		{Name: "guest-cells", Family: "render", Doc: "something painted into a cell a pane's guest owns"},
		{Name: "divider-glyph", Family: "render", Doc: "a divider cell holds a glyph from outside the active style"},
		{Name: "rail-hit-band", Family: "rail", Doc: "a rail hit target leaves the rail's own column band"},
		{Name: "rail-cursor", Family: "rail", Doc: "the rail's cursor points at a row that is not selectable"},
		{Name: "rail-signature", Family: "rail", Doc: "the rail's drawn output moved and its cache signature did not"},
	}
}

// demoScreen stands in for the app under test. It is deliberately not a real
// tuios frame: the viewport's contract is that it passes content through
// untouched, and content the display could have produced itself would not test
// that.
func demoScreen(w, h int) func() string {
	return func() string {
		var out []byte
		for r := range h {
			for c := range w {
				switch {
				case r == 0 || r == h-1:
					out = append(out, '=')
				case c == 0 || c == w-1:
					out = append(out, '|')
				default:
					out = append(out, byte('a'+(r*7+c)%26))
				}
			}
			if r < h-1 {
				out = append(out, '\n')
			}
		}
		return string(out)
	}
}

// drive feeds a display the events a real run produces, so a captured frame is
// composed the way a live one is.
type driver struct {
	d     *Display
	rules []fuzz.RuleInfo
	step  int
}

func newDriver(t *testing.T, o Options) *driver {
	t.Helper()
	if o.Rules == nil {
		o.Rules = demoRules()
	}
	if o.Width == 0 {
		o.Width, o.Height = 120, 34
	}
	overlay.SetASCII(o.ASCII)
	t.Cleanup(func() { overlay.SetASCII(false) })
	d := New(o)
	d.Start(0x9f3ac41d7e22b100, 4182)
	return &driver{d: d, rules: o.Rules}
}

func (dr *driver) actions(n int) {
	kinds := []fuzz.Kind{
		fuzz.Key, fuzz.MousePress, fuzz.MouseMotion, fuzz.Resize, fuzz.NewPane,
		fuzz.SwitchWorkspace, fuzz.OpenOverlay, fuzz.Guest, fuzz.Chord, fuzz.Tick,
		fuzz.MouseWheel, fuzz.FocusPane, fuzz.Setting, fuzz.Burst, fuzz.MouseRelease,
	}
	for range n {
		k := kinds[dr.step%len(kinds)]
		a := fuzz.Action{Kind: k, A: dr.step % 97, B: dr.step % 31, S: payload(k, dr.step)}
		dr.d.Step(dr.step, a, nil)
		for _, r := range dr.rules {
			dr.d.Rule(dr.step, r.Name, true)
		}
		dr.step++
	}
}

func payload(k fuzz.Kind, i int) string {
	switch k {
	case fuzz.Key:
		return []string{"ctrl+b", "j", "esc", "tab", "q"}[i%5]
	case fuzz.Chord:
		return []string{"c", "n", "z", "x"}[i%4]
	case fuzz.Guest:
		// A real Guest payload is an escape sequence aimed at a terminal, which
		// is exactly what the ledger has to launder before drawing it.
		return "\x1b[?1049h\x1b[2J"
	}
	return ""
}

func (dr *driver) breaks(rule string) {
	a := fuzz.Action{Kind: fuzz.Resize, A: 61, B: 20}
	vs := []fuzz.Violation{{Rule: rule, Detail: "pane fuzzpane0002 was told 60x19 and its grid is 60x20 [after resize 61 20, 3 panes, 61x20]"}}
	for _, r := range dr.rules {
		dr.d.Rule(dr.step, r.Name, r.Name != rule)
		if r.Name == rule {
			break
		}
	}
	dr.d.Step(dr.step, a, vs)
	dr.step++
}

// shrinks replays a plausible minimisation: the block pass halving the
// sequence, then a long tail of single-action attempts that mostly hold.
func (dr *driver) shrinks() {
	for sz := dr.step / 2; sz > 3; sz /= 2 {
		dr.d.Shrink("block", sz, true)
		dr.d.Shrink("block", sz-1, false)
	}
	for i := range 40 {
		dr.d.Shrink("single", 3+i%2, i%9 == 0)
	}
	dr.d.Shrink("simplify", 3, true)
}

func TestCaptureFrames(t *testing.T) {
	screen := demoScreen(80, 18)

	t.Run("rest", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen})
		dr.actions(700)
		f := dr.d.Frame()
		save(t, "rest", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("red", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen})
		dr.actions(700)
		dr.breaks("pane-size")
		f := dr.d.Frame()
		save(t, "red", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("shrink", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen})
		dr.actions(700)
		dr.breaks("pane-size")
		dr.shrinks()
		f := dr.d.Frame()
		save(t, "shrink", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("end-fail", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen, Command: "tuios-fuzz --seed 9f3ac41d7e22b100 --replay"})
		dr.actions(700)
		dr.breaks("pane-size")
		dr.shrinks()
		dr.d.Done(fuzz.Result{
			Seed: 0x9f3ac41d7e22b100, Failed: true, Step: 2,
			Violations: []fuzz.Violation{{Rule: "pane-size", Detail: "pane fuzzpane0002 was told 60x19 and its grid is 60x20"}},
			Actions: []fuzz.Action{
				{Kind: fuzz.NewPane}, {Kind: fuzz.ToggleShared}, {Kind: fuzz.Resize, A: 61, B: 20},
			},
			Executed: 4182, Replays: 173,
		})
		f := dr.d.Frame()
		save(t, "end-fail", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("end-pass", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen})
		dr.actions(700)
		dr.d.Done(fuzz.Result{Seed: 0x9f3ac41d7e22b100, Executed: 4182, Replays: 1})
		f := dr.d.Frame()
		save(t, "end-pass", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("ascii-rest", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen, ASCII: true})
		dr.actions(700)
		f := dr.d.Frame()
		save(t, "ascii-rest", f)
		assertASCII(t, f)
	})

	t.Run("ascii-end-fail", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen, ASCII: true, Command: "tuios-fuzz --seed 9f3ac41d7e22b100 --replay"})
		dr.actions(700)
		dr.breaks("rail-cursor")
		dr.shrinks()
		dr.d.Done(fuzz.Result{
			Seed: 0x9f3ac41d7e22b100, Failed: true, Step: 1,
			Violations: []fuzz.Violation{{Rule: "rail-cursor", Detail: "the rail cursor is on row 9 of 7"}},
			Actions:    []fuzz.Action{{Kind: fuzz.ToggleSidebar}, {Kind: fuzz.Key, S: "j"}},
			Executed:   4182, Replays: 173,
		})
		f := dr.d.Frame()
		save(t, "ascii-end-fail", f)
		assertASCII(t, f)
	})

	t.Run("mono-red", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: screen, Mono: true})
		dr.actions(700)
		dr.breaks("guest-cells")
		f := dr.d.Frame()
		save(t, "mono-red", f)
		assertRectangular(t, f, 120, 34)
	})

	t.Run("small", func(t *testing.T) {
		dr := newDriver(t, Options{Screen: demoScreen(60, 12), Width: 100, Height: 30})
		dr.actions(400)
		f := dr.d.Frame()
		save(t, "small", f)
		assertRectangular(t, f, 100, 30)
	})

	t.Run("no-viewport", func(t *testing.T) {
		dr := newDriver(t, Options{Width: 80, Height: 24})
		dr.actions(300)
		f := dr.d.Frame()
		save(t, "no-viewport", f)
		assertRectangular(t, f, 80, 24)
	})
}
