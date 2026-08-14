package vis

import (
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz"
	"github.com/charmbracelet/x/ansi"
)

// cell is one drawn character with the ground it was drawn on. The frame is
// checked at this resolution because the display's structural claims are about
// cells: a band is solid, a dot is inked, a bar is n cells wide.
type cell struct {
	r  rune
	bg string
}

// cells decodes one rendered row. It tracks only the background, which is the
// attribute the figure/ground claim rests on: a fragment rendered without one
// emits a reset that punches a transparent hole through the band, and that hole
// is invisible in any assertion made on the state behind the frame.
func cells(row string) []cell {
	var out []cell
	bg := "default"
	for len(row) > 0 {
		if !strings.HasPrefix(row, "\x1b[") {
			r, size := decodeRune(row)
			out = append(out, cell{r: r, bg: bg})
			row = row[size:]
			continue
		}
		end := strings.IndexByte(row, 'm')
		if end < 0 {
			break
		}
		for _, p := range splitSGR(row[2:end]) {
			switch {
			case p == "0", p == "49":
				bg = "default"
			case strings.HasPrefix(p, "48;2;"):
				bg = p
			}
		}
		row = row[end+1:]
	}
	return out
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			return r, len(string(r))
		}
	}
	return 0, len(s)
}

// splitSGR folds the semicolon-separated parameters back into whole colour
// specifications, since a truecolour background is five parameters that only
// mean anything together.
func splitSGR(s string) []string {
	fields := strings.Split(s, ";")
	var out []string
	for i := 0; i < len(fields); i++ {
		if fields[i] == "48" && i+4 < len(fields) && fields[i+1] == "2" {
			out = append(out, strings.Join(fields[i:i+5], ";"))
			i += 4
			continue
		}
		if fields[i] == "38" && i+4 < len(fields) && fields[i+1] == "2" {
			i += 4
			continue
		}
		out = append(out, fields[i])
	}
	return out
}

func hexBg(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return "48;2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(b>>8))
}

// The figure/ground claim, asserted on the drawn frame. No cell of the rail
// band may be transparent: a fragment rendered without a background emits a
// reset, and the reset shows whatever the terminal had in that cell before,
// which reads as a hole punched through the instrument. The first capture round
// found exactly that, in a state every assertion on the state behind the frame
// had passed.
//
// The band is allowed more than one fill. It is allowed exactly three: its own
// ground, the title chip, and the key badges in the hint strip. Anything else
// is a fill nobody decided on.
func TestRailBandHasNoHoles(t *testing.T) {
	pal := palette(false)
	allowed := map[string]string{
		hexBg(pal.Panel):  "Panel",
		hexBg(pal.Accent): "the title chip",
		hexBg(pal.Card):   "a key badge",
	}
	for _, size := range [][2]int{{120, 34}, {100, 30}, {160, 44}} {
		dr := newDriver(t, Options{Screen: demoScreen(70, 14), Width: size[0], Height: size[1]})
		dr.actions(300)
		for y, line := range strings.Split(dr.d.Frame(), "\n") {
			cs := cells(line)
			if len(cs) < railWidth {
				t.Fatalf("%dx%d row %d decoded to %d cells", size[0], size[1], y, len(cs))
			}
			for x, c := range cs[len(cs)-railWidth:] {
				if _, ok := allowed[c.bg]; !ok {
					t.Fatalf("%dx%d rail cell (%d,%d) %q sits on %s, which is not the band, the chip or a badge",
						size[0], size[1], x, y, string(c.r), c.bg)
				}
			}
		}
	}
}

// The resting state is what is on screen for almost the whole run, and it has
// to stay quiet: a frame that already carries the alarm ink has nothing left to
// escalate to when a rule actually breaks.
func TestRestingFrameCarriesNoAlarm(t *testing.T) {
	dr := newDriver(t, Options{Screen: demoScreen(70, 14)})
	dr.actions(400)
	pal := palette(false)
	frame := dr.d.Frame()
	if strings.Contains(frame, hexBg(pal.Warn)) {
		t.Error("the resting frame fills a cell with the alarm colour")
	}
	if strings.Contains(frame, "failed") && !strings.Contains(frame, "0 failed") {
		t.Error("the resting frame says something failed")
	}
	for _, word := range []string{"falsified", "shrink"} {
		if strings.Contains(ansi.Strip(frame), word) {
			t.Errorf("the resting frame claims %q before anything did", word)
		}
	}
}

// A violation has to ink its own dot and no other. Showing the wrong rule red
// sends a maintainer after a bug that is not there.
func TestViolationInksItsOwnDot(t *testing.T) {
	rules := demoRules()
	for _, target := range []string{"panic", "pane-size", "rail-signature"} {
		dr := newDriver(t, Options{Rules: rules})
		dr.actions(50)
		dr.breaks(target)
		s := dr.d.snapshot()

		broken := 0
		for i, b := range s.Broken {
			if !b {
				continue
			}
			broken++
			if rules[i].Name != target {
				t.Errorf("breaking %q inked %q", target, rules[i].Name)
			}
		}
		if broken != 1 {
			t.Errorf("breaking %q inked %d dots, want 1", target, broken)
		}
		text := ansi.Strip(dr.d.Frame())
		if !strings.Contains(text, target) {
			t.Errorf("the frame never names the rule that broke (%q)", target)
		}
		if !strings.Contains(text, "1 failed") {
			t.Errorf("breaking %q did not show a failure count", target)
		}
	}
}

// The matrix draws one dot per registered rule. A dot that stands for nothing,
// or a rule with no dot, is the display disagreeing with the oracle about what
// is being checked.
func TestMatrixDrawsOneDotPerRule(t *testing.T) {
	rules := demoRules()
	dr := newDriver(t, Options{Rules: rules})
	dr.actions(20)
	g := unicodeGlyphs
	dots := strings.Count(ansi.Strip(dr.d.Frame()), g.dot)
	// The separator between figures uses the same glyph, so the count is a
	// floor rather than an equality; what matters is that no rule is missing.
	if dots < len(rules) {
		t.Errorf("drew %d dots for %d registered rules", dots, len(rules))
	}
	fams := families(rules)
	text := ansi.Strip(dr.d.Frame())
	for _, f := range fams {
		if !strings.Contains(text, f.name) {
			t.Errorf("family %q from the registry is not on screen", f.name)
		}
	}
}

// The viewport passes the app's own cells through untouched. A harness that
// restyled the thing it is testing would produce a screenshot of software that
// does not exist.
func TestViewportPassesAppCellsThrough(t *testing.T) {
	const w, h = 60, 10
	dr := newDriver(t, Options{Screen: demoScreen(w, h), Width: 120, Height: 34})
	dr.actions(10)
	want := strings.Split(demoScreen(w, h)(), "\n")
	got := strings.Split(dr.d.Frame(), "\n")
	for i, wl := range want {
		// Row 0 of the frame is the hairline, so the app's rows start at 1 and
		// each is inset by one border column.
		row := ansi.Strip(got[i+1])
		if !strings.Contains(row, wl) {
			t.Fatalf("app row %d did not survive into the frame\n want %q\n  got %q", i, wl, row)
		}
	}
	if !strings.Contains(ansi.Strip(got[0]), "60"+unicodeGlyphs.times+"10") {
		t.Errorf("the frame title does not carry the app's live size: %q", ansi.Strip(got[0]))
	}
}

// The harness frame speaks for the harness, so it goes red when the harness has
// something to say. The app's own colours are never touched.
func TestHarnessBorderFollowsThePhase(t *testing.T) {
	pal := palette(false)
	rest := newDriver(t, Options{Screen: demoScreen(60, 10)})
	rest.actions(20)
	if strings.Contains(strings.Split(rest.d.Frame(), "\n")[0], fgOf(pal.Warn)) {
		t.Error("the harness border is red before anything broke")
	}
	red := newDriver(t, Options{Screen: demoScreen(60, 10)})
	red.actions(20)
	red.breaks("pane-size")
	if !strings.Contains(strings.Split(red.d.Frame(), "\n")[0], fgOf(pal.Warn)) {
		t.Error("the harness border stayed quiet through a falsification")
	}
}

func fgOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return "38;2;" + strconv.Itoa(int(r>>8)) + ";" + strconv.Itoa(int(g>>8)) + ";" + strconv.Itoa(int(b>>8))
}

// The tape is a ring of actions and nothing else. Its cell count is the number
// of actions it has room for, and its glyphs replay the classes in order, so a
// reader counting cells is counting real work.
func TestTapeReplaysTheRing(t *testing.T) {
	for _, n := range []int{1, 40, tapeCap - 1, tapeCap, tapeCap + 137} {
		dr := newDriver(t, Options{Width: 88, Height: 30})
		dr.actions(n)
		s := dr.d.snapshot()
		if s.Actions != n {
			t.Fatalf("%d actions counted %d", n, s.Actions)
		}
		if want := n % tapeCap; s.Head != want {
			t.Errorf("after %d actions the head is at %d, want %d", n, s.Head, want)
		}
		written := 0
		for _, v := range s.Tape {
			if v != 0 {
				written++
			}
		}
		if want := min(n, tapeCap); written != want {
			t.Errorf("after %d actions the ring holds %d cells, want %d", n, written, want)
		}
	}
}

// The violating action keeps its cell in the stream, and the stream freezes,
// because generation actually stopped.
func TestTapeInksTheViolatingCell(t *testing.T) {
	dr := newDriver(t, Options{Width: 88, Height: 30})
	dr.actions(120)
	dr.breaks("pane-size")
	s := dr.d.snapshot()
	if s.ViolationCell < 0 {
		t.Fatal("no tape cell was marked for the action that broke it")
	}
	if want := (s.Head - 1 + tapeCap) % tapeCap; s.ViolationCell != want {
		t.Errorf("the violating cell is %d, want the newest cell %d", s.ViolationCell, want)
	}
	if !strings.Contains(dr.d.Frame(), hexBg(palette(false).Warn)) {
		t.Error("the violating cell is not inked in the drawn frame")
	}
}

// Every bar in the funnel is its candidate's real length against the sequence
// minimisation started from. The scale never moves, because the collapse to a
// sliver is the thing worth watching.
func TestFunnelBarsAreProportional(t *testing.T) {
	dr := newDriver(t, Options{Width: 120, Height: 34})
	dr.actions(400)
	dr.breaks("pane-size")
	dr.shrinks()
	s := dr.d.snapshot()

	const figure, cells = 6, 40
	for _, a := range s.Runs {
		bar := barCells(funnelRow(a, s, palette(false), figure, cells, false))
		want := float64(a.Size) / float64(s.InitialLen) * cells
		if diff := float64(bar) - want; diff > 1 || diff < -1 {
			t.Errorf("a candidate of %d in %d drew %d of %d cells, want about %.1f",
				a.Size, s.InitialLen, bar, cells, want)
		}
	}
	if s.RunsSeen != len(s.Runs)+s.Elided {
		t.Errorf("the funnel shows %d rows and elides %d, which does not account for %d candidates",
			len(s.Runs), s.Elided, s.RunsSeen)
	}
	// The header's figure is the shortest sequence that still failed.
	best := s.InitialLen
	for _, a := range s.Runs {
		if a.Accepted && a.Size < best {
			best = a.Size
		}
	}
	if s.BestLen > best {
		t.Errorf("the header claims %d as the shortest failing sequence, but %d was accepted", s.BestLen, best)
	}
}

// barCells counts the block cells a funnel row drew, partials included as the
// fraction they stand for.
func barCells(row string) int {
	n := 0
	for _, r := range ansi.Strip(row) {
		if r == '█' {
			n++
		}
		for _, e := range unicodeGlyphs.eighths[1:] {
			if string(r) == e {
				n++
			}
		}
	}
	return n
}

// The end card is a claim somebody will paste into an issue, so every figure on
// it comes straight off the Result.
func TestCardFiguresComeFromTheResult(t *testing.T) {
	dr := newDriver(t, Options{Command: "tuios-fuzz --seed 000000000000002a --replay"})
	dr.actions(200)
	dr.breaks("layout-overlap")
	dr.shrinks()
	res := fuzz.Result{
		Seed: 42, Failed: true, Step: 1,
		Violations: []fuzz.Violation{{Rule: "layout-overlap", Detail: "panes A and B both claim (10,4)"}},
		Actions:    []fuzz.Action{{Kind: fuzz.NewPane}, {Kind: fuzz.Resize, A: 61, B: 20}},
		Executed:   4182, Replays: 173,
	}
	dr.d.Done(res)
	text := ansi.Strip(dr.d.Frame())

	for _, want := range []string{
		"000000000000002a",         // the seed, as the replay command spells it
		"173 replays",              // the engine's own replay counter
		"layout-overlap",           // the rule the Result names
		"panes A and B both claim", // the detail verbatim
		"new-pane",                 // the minimal script, action by action
		"resize 61 20",             //
		"tuios-fuzz --seed 000000000000002a --replay", // the command that reproduces it
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the end card does not carry %q", want)
		}
	}
	if strings.Contains(text, "run held") {
		t.Error("a falsified run showed the passing verdict")
	}
}

func TestPassCardSaysWhatHeld(t *testing.T) {
	rules := demoRules()
	dr := newDriver(t, Options{Rules: rules})
	dr.actions(200)
	dr.d.Done(fuzz.Result{Seed: 42, Executed: 4182, Replays: 1})
	text := ansi.Strip(dr.d.Frame())
	if !strings.Contains(text, itoa(len(rules))+" invariants held") {
		t.Errorf("the pass card does not say how many invariants held: %q", firstLines(text, 12))
	}
	if strings.Contains(text, "minimal reproduction") {
		t.Error("a passing run offered a reproduction")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[:min(n, len(lines))], "\n")
}

// The tape glyph for an action has to mean something, which it only does if
// every Kind belongs to exactly one class. A Kind added to the alphabet and
// left out would be drawn under whichever letter the fallback happened to be.
func TestClassesPartitionTheAlphabet(t *testing.T) {
	classes := DefaultClasses()
	for k := fuzz.Kind(0); k < 200; k++ {
		name := k.String()
		if strings.HasPrefix(name, "kind") {
			// Past the end of the alphabet.
			continue
		}
		hits := 0
		for _, c := range classes {
			if c.Holds(k) {
				hits++
			}
		}
		if hits != 1 {
			t.Errorf("action %q belongs to %d classes, want exactly 1", name, hits)
		}
	}
	seen := map[string]bool{}
	for _, c := range classes {
		if seen[c.Letter] {
			t.Errorf("two classes draw the same tape glyph %q", c.Letter)
		}
		seen[c.Letter] = true
		if len(c.Letter) != 1 || c.Letter[0] > 127 {
			t.Errorf("class %q draws %q, which is not one ASCII cell", c.Name, c.Letter)
		}
	}
}

// A fuzzer-generated payload is hostile by construction: a Guest action's text
// is an escape sequence aimed at a terminal. Echoing one unlaundered would let
// the system under test drive the instrument that is measuring it.
func TestGeneratedPayloadsCannotDriveTheTerminal(t *testing.T) {
	dr := newDriver(t, Options{Width: 100, Height: 30})
	dr.d.Step(0, fuzz.Action{Kind: fuzz.Guest, S: "\x1b[2J\x1b[?1049h\x1b]0;pwned\x07"}, nil)
	dr.d.Step(1, fuzz.Action{Kind: fuzz.Rename, S: "\x1b[31mred"}, nil)
	frame := dr.d.Frame()
	// The display's own escapes are colour and cursor control; the payload's
	// were a screen clear, an alt-screen switch and a title set.
	for _, forbidden := range []string{"\x1b[2J", "\x1b[?1049h", "\x1b]0;"} {
		if strings.Contains(frame, forbidden) {
			t.Errorf("a generated payload put %q into the frame", forbidden)
		}
	}
	if !strings.Contains(ansi.Strip(frame), "guest") {
		t.Error("the ledger dropped the action instead of laundering it")
	}
}

// Below the floor the display declines rather than drawing a mangled
// instrument, because a squeezed instrument is one that can be misread.
func TestDeclinesBelowTheFloor(t *testing.T) {
	if Fits(MinWidth-1, MinHeight) || Fits(MinWidth, MinHeight-1) {
		t.Error("the display accepted a terminal below its own floor")
	}
	if !Fits(MinWidth, MinHeight) {
		t.Error("the display declined its own floor")
	}
}

// Narrow enough and the app viewport goes first. The instruments alone are
// still the whole truth of the run.
func TestNarrowFrameKeepsTheInstruments(t *testing.T) {
	dr := newDriver(t, Options{Screen: demoScreen(80, 20), Width: 60, Height: 24})
	dr.actions(100)
	f := dr.d.Frame()
	assertRectangular(t, f, 60, 24)
	text := ansi.Strip(f)
	for _, want := range []string{"invariants", "checks", "seed"} {
		if !strings.Contains(text, want) {
			t.Errorf("the narrow frame dropped %q", want)
		}
	}
}
