//go:build ghostty

package vt

// Fuzzing the differential harness rather than one emulator.
//
// Every other fuzz target here needs somebody to have written down what the
// right answer is, so it can only find the bugs an invariant was written for.
// Two independent emulators fed the same bytes need nothing written down: a
// disagreement is a bug in one of them, and the campaign finds it without
// anyone having predicted it. That is worth more than the invariants, and this
// is the only build where both implementations exist in one process.
//
// The generator is internal/fuzz/vtgen, the same grammar the single-emulator
// targets use, because random bytes never leave the parser's ground state.
// Comparison happens after every step, not once at the end: an emulator that
// diverges and then converges again is still an emulator that drew the wrong
// frame, and the frame is what a person sees.

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen"
	uv "github.com/charmbracelet/ultraviolet"
)

// diffFuzzCols and diffFuzzRows size the screen for a campaign. Smaller than
// the 80x24 the script targets use: a narrow short screen wraps and scrolls
// constantly, so the same number of steps reaches far more of the scroll and
// reflow paths, and each comparison walks fewer cells.
const (
	diffFuzzCols = 40
	diffFuzzRows = 12
)

// diffPrelude is written to both emulators before any script runs.
//
// Mode 2027 is grapheme clustering. The pure emulator clusters
// unconditionally; the library gates it on the mode, which is off by default,
// so with no prelude the two disagree about the width of the first flag or
// ZWJ sequence the generator draws and every campaign dies at step three
// having tested nothing. Turning it on is a no-op for the pure emulator and
// puts the library in the mode the pure emulator is permanently in, so the
// rest of the grammar becomes reachable. The default-off disagreement is not
// swept away by this: it is pinned by TestGhosttyGraphemeClusteringDefault.
const diffPrelude = "\x1b[?2027h"

// divergence is one disagreement between the backends.
//
// Sig is what makes it *this* disagreement rather than any disagreement, and
// it deliberately excludes step numbers, screen positions and the offending
// text, all of which move as the shrinker cuts. The shrinker uses it as the
// oracle's key: without one, a script carrying three separate bugs reduces to
// whichever survives the cuts, and the reduction printed underneath a report
// is then a repro for something else. That is not hypothetical; it is what
// this harness did before Sig existed.
type divergence struct {
	Sig    string
	Detail string
}

func (d divergence) found() bool { return d.Sig != "" }

func (d divergence) String() string { return d.Detail }

// at prefixes a divergence with where in the run it happened.
func (d divergence) at(format string, args ...any) divergence {
	if !d.found() {
		return d
	}
	d.Detail = fmt.Sprintf(format, args...) + ": " + d.Detail
	return d
}

var agree = divergence{}

func differ(sig, format string, args ...any) divergence {
	return divergence{Sig: sig, Detail: fmt.Sprintf(format, args...)}
}

// cellSig names what is wrong with a pair of cells, finely enough that two
// different bugs never share a signature and coarsely enough that a bug keeps
// its signature while the shrinker cuts around it.
func cellSig(a, b *uv.Cell) string {
	norm := func(c *uv.Cell) (string, int, uv.Style, string) {
		if c == nil {
			return " ", 1, uv.Style{}, ""
		}
		content := c.Content
		if content == "" {
			content = " "
		}
		w := c.Width
		if w == 0 {
			w = 1
		}
		return content, w, c.Style, c.Link.URL
	}
	ac, aw, as, al := norm(a)
	bc, bw, bs, bl := norm(b)

	var parts []string
	if ac != bc {
		// The rune counts separate the cluster bugs from each other: a
		// backend that split a cluster holds fewer runes than one that kept
		// it whole, and a backend that swallowed a mark holds more.
		switch an, bn := len([]rune(ac)), len([]rune(bc)); {
		case an > bn:
			parts = append(parts, "content:pure-holds-more-runes")
		case an < bn:
			parts = append(parts, "content:ghostty-holds-more-runes")
		default:
			parts = append(parts, "content:same-rune-count")
		}
	}
	if aw != bw {
		parts = append(parts, "width")
	}
	if as.Attrs != bs.Attrs {
		parts = append(parts, "attrs")
	}
	if as.Underline != bs.Underline {
		parts = append(parts, "underline")
	}
	if !colorEquivalent(as.Fg, bs.Fg) {
		parts = append(parts, "fg")
	}
	if !colorEquivalent(as.Bg, bs.Bg) {
		parts = append(parts, "bg")
	}
	if !colorEquivalent(as.UnderlineColor, bs.UnderlineColor) {
		parts = append(parts, "ulcolor")
	}
	if al != bl {
		parts = append(parts, "link")
	}
	if len(parts) == 0 {
		return "cell:equivalent"
	}
	return "cell[" + strings.Join(parts, ",") + "]"
}

// diffScreens reports the first cell the two emulators disagree about.
func diffScreens(pure, gh Terminal) divergence {
	w, h := pure.Width(), pure.Height()
	if gw, ghh := gh.Width(), gh.Height(); gw != w || ghh != h {
		return differ("size", "size pure=%dx%d ghostty=%dx%d", w, h, gw, ghh)
	}
	for y := range h {
		for x := range w {
			pc, gc := pure.CellAt(x, y), gh.CellAt(x, y)
			if !cellsEquivalent(pc, gc) {
				return differ(cellSig(pc, gc), "cell (%d,%d)\n  pure    %s\n  ghostty %s",
					x, y, ghDiffCellText(pc), ghDiffCellText(gc))
			}
		}
	}
	return agree
}

// diffCursor covers position and visibility. The cursor is not a cell, so a
// screen comparison never sees it, and a pane whose cursor sits one column
// off is a pane that types in the wrong place.
func diffCursor(pure, gh Terminal) divergence {
	pp, gp := pure.CursorPosition(), gh.CursorPosition()
	switch {
	case pp.X != gp.X && pp.Y != gp.Y:
		return differ("cursor:both-axes", "cursor pure=%v ghostty=%v", pp, gp)
	case pp.X != gp.X:
		return differ("cursor:column", "cursor pure=%v ghostty=%v", pp, gp)
	case pp.Y != gp.Y:
		return differ("cursor:row", "cursor pure=%v ghostty=%v", pp, gp)
	}
	if ph, ghh := pure.IsCursorHidden(), gh.IsCursorHidden(); ph != ghh {
		return differ("cursor:hidden", "cursor hidden pure=%v ghostty=%v", ph, ghh)
	}
	return agree
}

// diffScreenState covers the flags the app reads to decide what to draw at
// all: which screen is live, and how big the scroll region is.
func diffScreenState(pure, gh Terminal) divergence {
	if pa, ga := pure.IsAltScreen(), gh.IsAltScreen(); pa != ga {
		return differ("altscreen", "alt screen pure=%v ghostty=%v", pa, ga)
	}
	if pr, gr := pure.ScrollRegion(), gh.ScrollRegion(); pr != gr {
		return differ("scrollregion", "scroll region pure=%v ghostty=%v", pr, gr)
	}
	return agree
}

// diffModes compares the modes the app actually branches on. The full mode
// map is not compared: an implementation is free to remember a mode it does
// not implement, and only the ones with a reader here change what is drawn.
func diffModes(pure, gh Terminal) divergence {
	if pm, gm := pure.HasMouseMode(), gh.HasMouseMode(); pm != gm {
		return differ("mode:mouse", "mouse mode pure=%v ghostty=%v", pm, gm)
	}
	if pb, gb := pure.BracketedPasteEnabled(), gh.BracketedPasteEnabled(); pb != gb {
		return differ("mode:bracketed-paste", "bracketed paste pure=%v ghostty=%v", pb, gb)
	}
	if pa, ga := pure.ApplicationCursorKeys(), gh.ApplicationCursorKeys(); pa != ga {
		return differ("mode:appcursor", "application cursor keys pure=%v ghostty=%v", pa, ga)
	}
	return agree
}

// diffScrollback compares the history the two have accumulated. Only the tail
// is walked: an early divergence shows up in the tail too once more lines
// push past it, and walking thousands of lines per step would cost more than
// the whole campaign is worth.
func diffScrollback(pure, gh Terminal, tail int) divergence {
	pl, gl := pure.ScrollbackLen(), gh.ScrollbackLen()
	if pl != gl {
		return differ("scrollback:len", "scrollback len pure=%d ghostty=%d", pl, gl)
	}
	from := max(pl-tail, 0)
	for i := from; i < pl; i++ {
		p, g := lineToString(pure.ScrollbackLine(i)), lineToString(gh.ScrollbackLine(i))
		if p != g {
			return differ("scrollback:line", "scrollback line %d\n  pure    %q\n  ghostty %q", i, p, g)
		}
	}
	return agree
}

// diffRender compares what each side would send to the host. The two frames
// are re-parsed rather than compared as bytes because the same colour has
// several legal encodings and only the emulator knows which one the guest
// used; what has to match is the picture, not the spelling.
func diffRender(pure, gh Terminal) divergence {
	pr, gr := pure.Render(), gh.Render()
	if pr == gr {
		return agree
	}
	w, h := pure.Width(), pure.Height()
	if w <= 0 || h <= 0 {
		return agree
	}
	pe, ge := NewEmulator(w, h), NewEmulator(w, h)
	_, _ = pe.Write([]byte(pr))
	_, _ = ge.Write([]byte(gr))
	for y := range h {
		for x := range w {
			pc, gc := pe.CellAt(x, y), ge.CellAt(x, y)
			if !cellsEquivalent(pc, gc) {
				return differ("render:"+cellSig(pc, gc),
					"rendered frame cell (%d,%d)\n  pure    %s\n  ghostty %s",
					x, y, ghDiffCellText(pc), ghDiffCellText(gc))
			}
		}
	}
	return agree
}

// diffCheap is what runs after every single step. Walking the grid and the
// cursor is a few thousand comparisons; re-parsing two rendered frames is two
// more emulators, so that is saved for the checkpoints.
func diffCheap(pure, gh Terminal) divergence {
	for _, d := range []divergence{
		diffScreens(pure, gh),
		diffCursor(pure, gh),
		diffScreenState(pure, gh),
		diffModes(pure, gh),
	} {
		if d.found() {
			return d
		}
	}
	return agree
}

// diffFull adds the expensive comparisons, run at checkpoints and at the end.
func diffFull(pure, gh Terminal) divergence {
	if d := diffCheap(pure, gh); d.found() {
		return d
	}
	if d := diffScrollback(pure, gh, 32); d.found() {
		return d
	}
	return diffRender(pure, gh)
}

// diffCheckpoint is how often the expensive comparison runs during a replay.
const diffCheckpoint = 16

// newDiffFuzzPair builds both emulators without a *testing.T, so the shrinker
// can replay a candidate hundreds of times without registering cleanups.
func newDiffFuzzPair(w, h int) (*Emulator, *GhosttyTerminal) {
	return NewEmulator(w, h), NewGhosttyTerminal(w, h)
}

// diffWrite feeds the same bytes to both and reports a write error as a
// divergence, so a backend that starts refusing input is a finding rather
// than a silent skip.
func diffWrite(pure *Emulator, gh *GhosttyTerminal, b string) divergence {
	if _, err := pure.WriteString(b); err != nil {
		return differ("write:pure", "pure write: %v", err)
	}
	if _, err := gh.Write([]byte(b)); err != nil {
		return differ("write:ghostty", "ghostty write: %v", err)
	}
	return agree
}

// diffReplay runs a script into both emulators a step at a time and returns
// the first divergence. A panic on either side is a finding rather than a
// dead test binary, so the shrinker survives it and reduces it.
func diffReplay(s vtgen.Script) (found divergence) {
	pure, gh := newDiffFuzzPair(diffFuzzCols, diffFuzzRows)
	defer func() {
		if r := recover(); r != nil {
			found = differ("panic", "panic: %v", r)
		}
		_ = gh.Close()
	}()

	if d := diffWrite(pure, gh, diffPrelude); d.found() {
		return d.at("prelude")
	}

	for i, seq := range s {
		if seq.Kind == "resize" {
			pure.Resize(seq.Cols, seq.Rows)
			gh.Resize(seq.Cols, seq.Rows)
		} else if d := diffWrite(pure, gh, seq.Bytes); d.found() {
			return d.at("step %d", i+1)
		}
		d := diffCheap(pure, gh)
		if !d.found() && (i+1)%diffCheckpoint == 0 {
			d = diffFull(pure, gh)
		}
		if d.found() {
			return d.at("step %d (%s)", i+1, seq.Desc)
		}
	}
	return diffFull(pure, gh).at("after %d steps", len(s))
}

// diffReplaySplit is the same comparison with the bytes arriving the way a
// PTY reader hands them over. Two parsers holding half a sequence each is
// where their state machines are most likely to differ, and it is the one
// arrival pattern no structured test produces.
func diffReplaySplit(s vtgen.Script, seed uint64) (found divergence) {
	pure, gh := newDiffFuzzPair(diffFuzzCols, diffFuzzRows)
	defer func() {
		if r := recover(); r != nil {
			found = differ("panic", "panic: %v", r)
		}
		_ = gh.Close()
	}()

	if d := diffWrite(pure, gh, diffPrelude); d.found() {
		return d.at("prelude")
	}

	writes := s.SplitWrites(seed)
	for i, w := range writes {
		if d := diffWrite(pure, gh, w); d.found() {
			return d.at("write %d", i+1)
		}
		d := diffCheap(pure, gh)
		if !d.found() && (i+1)%diffCheckpoint == 0 {
			d = diffFull(pure, gh)
		}
		if d.found() {
			return d.at("write %d of %d (%q)", i+1, len(writes), w)
		}
	}
	return diffFull(pure, gh).at("after %d writes", len(writes))
}

// diffFilter drops the steps whose divergence is already understood, so a
// campaign spends its budget on new ground instead of rediscovering the
// pinned tests. Every filter here costs coverage, so each one names the test
// that owns it; when that test starts failing because the two agree, the
// filter goes too.
func diffFilter(s vtgen.Script) vtgen.Script {
	out := make(vtgen.Script, 0, len(s))
	for _, seq := range s {
		// SGR 21 is double underline to ghostty and nothing to the pure
		// emulator. Pinned by TestGhosttyKnownDivergences/sgr21-double-underline.
		if seq.Kind == "sgr" && sgrHasParam(seq.Bytes, "21") {
			continue
		}
		// A raw C1 introducer is a control to the pure emulator and invalid
		// UTF-8 to the library, so every one of these diverges on the first
		// byte and the rest of the step is printed as text. Pinned by
		// TestGhosttyEightBitControlsInUTF8.
		if hasC1Introducer(seq.Bytes) {
			continue
		}
		// The prelude owns mode 2027; a script toggling it would undo the
		// setup mid-run and reintroduce the clustering divergence pinned by
		// TestGhosttyGraphemeClusteringDefault.
		if seq.Kind == "mode" && strings.Contains(seq.Bytes, "2027") {
			continue
		}
		out = append(out, seq)
	}
	return out
}

// hasC1Introducer reports whether a sequence uses a raw eight-bit control
// byte anywhere, including the C1 string terminator.
func hasC1Introducer(b string) bool {
	return strings.ContainsAny(b, "\x84\x85\x8e\x8f\x90\x9b\x9c\x9d\x9e\x9f")
}

// sgrHasParam reports whether an SGR sequence sets a given parameter. It reads
// the parameter list rather than the whole string so "21" does not match the
// 21 inside a colour component.
func sgrHasParam(b, want string) bool {
	i := strings.IndexByte(b, '[')
	if i < 0 || !strings.HasSuffix(b, "m") {
		return false
	}
	for _, p := range strings.Split(b[i+1:len(b)-1], ";") {
		if p == want {
			return true
		}
	}
	return false
}

// shrinkTo reduces a script to the smallest one that still fails the same
// way, where "the same way" is the signature and not merely "fails".
func shrinkTo(s vtgen.Script, want string, replay func(vtgen.Script) divergence) vtgen.Script {
	return vtgen.Shrink(s, func(c vtgen.Script) bool { return replay(c).Sig == want })
}

// diffFuzzGate keeps the differential targets opt-in.
//
// The two backends currently disagree about at least eight distinct things
// (each pinned below), and several of them are common enough that most
// generated streams hit one. A target that fails on nearly every input is
// not a gate, it is noise, and the alternative - an allowlist of the 43
// signatures a census turns up - covers so much of the space that it would
// pass a genuinely new bug. So these run only when asked for, and the
// durable regression value lives in the pinned tests instead: those assert
// what each backend does today, and fire when either one changes.
//
// Once the pinned divergences are resolved these should become ungated, and
// the sweep should become the gate.
func diffFuzzGate(f *testing.F) {
	f.Helper()
	if os.Getenv("TUIOS_DIFF_FUZZ") == "" {
		f.Skip("set TUIOS_DIFF_FUZZ=1 to run the differential fuzz targets; " +
			"they report the open divergences pinned in this file")
	}
}

// FuzzGhosttyDifferentialScript is the coverage-guided differential target: a
// corpus entry decodes to a script, so the mutator steers which sequences the
// two emulators are asked to agree about.
func FuzzGhosttyDifferentialScript(f *testing.F) {
	diffFuzzGate(f)
	for _, seed := range [][]byte{
		{},
		{0x01},
		{0xff, 0xff, 0xff, 0xff},
		[]byte("tuios"),
		[]byte("the quick brown fox jumps over the lazy dog"),
		[]byte("\x00\x11\x22\x33\x44\x55\x66\x77\x88\x99\xaa\xbb"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		script := diffFilter(vtgen.FromBytes(data).Script(80))
		if d := diffReplay(script); d.found() {
			small := shrinkTo(script, d.Sig, diffReplay)
			t.Fatalf("[%s] %s\n\nreduced from %d steps to %d:\n%s",
				d.Sig, d, len(script), len(small), small)
		}
	})
}

// FuzzGhosttyDifferentialSplit is the same two emulators reading at PTY
// boundaries instead of at sequence boundaries.
func FuzzGhosttyDifferentialSplit(f *testing.F) {
	diffFuzzGate(f)
	for _, seed := range [][]byte{
		{},
		{0x7f},
		[]byte("split me"),
		[]byte("\xe4\xb8\x96\xe4\xb8\x96\xe4\xb8\x96"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		script := diffFilter(vtgen.FromBytes(data).Script(80))
		seed := uint64(len(data))*1099511628211 + 1
		replay := func(s vtgen.Script) divergence { return diffReplaySplit(s, seed) }
		if d := replay(script); d.found() {
			small := shrinkTo(script, d.Sig, replay)
			t.Fatalf("[%s] %s\n\nreduced from %d steps to %d:\n%s",
				d.Sig, d, len(script), len(small), small)
		}
	})
}

// envInt reads an integer knob, treating anything unparseable as a mistake
// rather than as unset, so a typo in a shell export widens nothing silently.
func envInt(t *testing.T, name string) int {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s=%q is not a number", name, v)
	}
	return n
}

// TestGhosttyDifferentialSweep is the deterministic half: fixed seeds rather
// than a mutating corpus, so a failure names the seed and reproduces exactly.
//
// It is opt-in for the reason diffFuzzGate gives: with the divergences
// pinned below still open, every seed finds one. TUIOS_DIFF_SWEEP_SEEDS
// switches it on and sets how many seeds to run.
func TestGhosttyDifferentialSweep(t *testing.T) {
	seeds := envInt(t, "TUIOS_DIFF_SWEEP_SEEDS")
	if seeds <= 0 {
		t.Skip("set TUIOS_DIFF_SWEEP_SEEDS=<seeds> to sweep for divergences")
	}
	steps := 100
	if testing.Short() {
		steps = 40
	}

	for seed := range uint64(seeds) {
		script := diffFilter(vtgen.New(seed).Script(steps))
		if d := diffReplay(script); d.found() {
			small := shrinkTo(script, d.Sig, diffReplay)
			t.Errorf("seed %d [%s]: %s\n\nreduced from %d steps to %d:\n%s",
				seed, d.Sig, d, len(script), len(small), small)
			return
		}
		replay := func(s vtgen.Script) divergence { return diffReplaySplit(s, seed) }
		if d := replay(script); d.found() {
			small := shrinkTo(script, d.Sig, replay)
			t.Errorf("seed %d [%s], split into reader-sized writes: %s\n\nreduced from %d steps to %d:\n%s",
				seed, d.Sig, d, len(script), len(small), small)
			return
		}
	}
}

// TestGhosttyDifferentialCensus is the diagnostic the sweep cannot be: it
// keeps going past the first failure and reports every distinct signature it
// found, with one reduced repro each.
//
// A sweep that stops at the first divergence tells you there is at least one
// bug. Triage needs the set, because fixing the first only reveals the
// second, and because a signature seen once in a thousand seeds is a
// different kind of problem from one seen in every seed. It is gated behind
// TUIOS_DIFF_CENSUS because reducing every distinct finding costs minutes,
// which is too much for a test that runs on every build.
func TestGhosttyDifferentialCensus(t *testing.T) {
	seeds := envInt(t, "TUIOS_DIFF_CENSUS")
	if seeds <= 0 {
		t.Skip("set TUIOS_DIFF_CENSUS=<seeds> to take a census of divergences")
	}
	steps := 120
	if n := envInt(t, "TUIOS_DIFF_CENSUS_STEPS"); n > 0 {
		steps = n
	}

	type finding struct {
		count  int
		seed   uint64
		mode   string
		detail string
		repro  vtgen.Script
	}
	found := map[string]*finding{}

	record := func(seed uint64, mode string, script vtgen.Script, d divergence, replay func(vtgen.Script) divergence) {
		if f, ok := found[d.Sig]; ok {
			f.count++
			return
		}
		found[d.Sig] = &finding{
			count: 1, seed: seed, mode: mode, detail: d.Detail,
			repro: shrinkTo(script, d.Sig, replay),
		}
	}

	for seed := range uint64(seeds) {
		script := diffFilter(vtgen.New(seed).Script(steps))
		if d := diffReplay(script); d.found() {
			record(seed, "whole sequences", script, d, diffReplay)
		}
		replay := func(s vtgen.Script) divergence { return diffReplaySplit(s, seed) }
		if d := replay(script); d.found() {
			record(seed, "split writes", script, d, replay)
		}
	}

	if len(found) == 0 {
		t.Logf("%d seeds of %d steps: the two backends agreed everywhere", seeds, steps)
		return
	}

	sigs := make([]string, 0, len(found))
	for sig := range found {
		sigs = append(sigs, sig)
	}
	sort.Slice(sigs, func(i, j int) bool { return found[sigs[i]].count > found[sigs[j]].count })

	var b strings.Builder
	fmt.Fprintf(&b, "%d seeds of %d steps produced %d distinct divergences:\n", seeds, steps, len(found))
	for _, sig := range sigs {
		f := found[sig]
		fmt.Fprintf(&b, "\n=== %s (%d/%d seeds, first at seed %d, %s)\n%s\nrepro (%d steps):\n%s\n",
			sig, f.count, seeds, f.seed, f.mode, f.detail, len(f.repro), f.repro)
	}
	t.Error(b.String())
}

// TestGhosttyEightBitControlsInUTF8 pins the first divergence the
// differential campaign found, and the reason diffFilter drops these steps.
//
// The pure emulator honours a raw C1 byte as its control: 0x9b introduces a
// CSI, 0x9d an OSC, 0x90 a DCS, 0x85 is NEL. The library treats the same byte
// as what it is in a UTF-8 stream, an unpaired continuation byte, and emits
// U+FFFD, after which the rest of the sequence lands on screen as text.
//
// The library follows kitty, foot and alacritty here, and it matters beyond
// conformance: 0x9b is an ordinary byte in binary data, so on the pure
// emulator `cat` of a binary file can move the cursor, set a scroll region or
// switch screens, where on the library backend it only prints replacement
// characters. Whichever behaviour is wanted, the two backends must not differ
// about it, because a pane rehydrated across a backend change would replay
// the same bytes to a different screen.
func TestGhosttyEightBitControlsInUTF8(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"CSI", "\x9b31mX"},
		{"CUP", "\x9b2;3HX"},
		{"OSC", "\x9d0;title\x9cX"},
		{"DCS", "\x900;1\x9cX"},
		{"NEL", "A\x85B"},
		{"IND", "A\x84B"},
		{"SS2", "\x8eqX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newDiffPair(t, 40, 12)
			p.write(t, []byte(tc.in))
			if !diffScreens(p.pure, p.gh).found() && !diffCursor(p.pure, p.gh).found() {
				t.Fatalf("the two backends now agree on %q as an eight-bit control; "+
					"delete this entry and the hasC1Introducer filter in diffFilter", tc.in)
			}
		})
	}
}

// TestGhosttyGraphemeClusteringDefault pins the second divergence, and the
// reason diffReplay writes diffPrelude.
//
// With mode 2027 off, which is the default, the library measures each
// codepoint on its own: a flag is two clusters of two columns, a ZWJ family
// is three. The pure emulator always clusters, so it calls all of them one
// cluster of two columns. Turning 2027 on makes them agree exactly, which is
// what the prelude does.
//
// The default is the case that ships. The host terminal tuios re-emits into
// has its own answer, and when the host disagrees with the pane's model the
// line shears sideways from the emoji onward.
func TestGhosttyGraphemeClusteringDefault(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"regional-indicator-pair", "\U0001F1FA\U0001F1F8"},
		{"zwj-family", "\U0001F468\u200d\U0001F469\u200d\U0001F467"},
		{"emoji-presentation-selector", "\u2764\ufe0f"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Default: the two disagree.
			p := newDiffPair(t, 40, 12)
			p.write(t, []byte(tc.in))
			if !diffScreens(p.pure, p.gh).found() && !diffCursor(p.pure, p.gh).found() {
				t.Fatalf("the two backends now agree on %q with mode 2027 off; "+
					"delete this entry and drop diffPrelude", tc.in)
			}

			// With 2027 on they must agree, which is what the campaign relies on.
			q := newDiffPair(t, 40, 12)
			q.write(t, []byte(diffPrelude+tc.in))
			if d := diffScreens(q.pure, q.gh); d.found() {
				t.Errorf("with mode 2027 set, %q still diverges: %s", tc.in, d)
			}
			if d := diffCursor(q.pure, q.gh); d.found() {
				t.Errorf("with mode 2027 set, %q still diverges: %s", tc.in, d)
			}
		})
	}
}

// The pinned divergences.
//
// Each entry below is a root cause the differential census reduced to a
// minimal input, with the behaviour of BOTH backends asserted rather than
// merely "they differ". Asserting both is what makes these regression tests
// instead of bug reports: if either backend changes in either direction the
// test fires and somebody has to decide whether that was intended.
//
// Every entry says which side is right where that is knowable. Where it is a
// defensible design choice rather than a conformance question, it says that
// instead of pretending.

// diffProbe drives both backends through the grapheme-clustering prelude and
// the input, and reports what each ended up with, so a pinned case can state
// both sides in one line.
type diffProbe struct {
	pureCell, ghCell     string
	pureCursor, ghCursor uv.Position
	pure, gh             Terminal
}

func probeBoth(t *testing.T, in string, x, y int) diffProbe {
	t.Helper()
	p := newDiffPair(t, 40, 12)
	p.write(t, []byte(diffPrelude+in))
	cell := func(term Terminal) string {
		c := term.CellAt(x, y)
		if c == nil || c.Content == "" {
			return " "
		}
		return c.Content
	}
	return diffProbe{
		pureCell: cell(p.pure), ghCell: cell(p.gh),
		pureCursor: p.pure.CursorPosition(), ghCursor: p.gh.CursorPosition(),
		pure: p.pure, gh: p.gh,
	}
}

// TestGhosttyDivergence_SurplusCSIParameters is the largest class the census
// found: 85 of 150 seeds hit it.
//
// A CSI carrying more parameters than its command defines is executed by the
// pure emulator, which acts on the parameters it knows and drops the rest,
// and ignored outright by the library. CUF, CUD, CUP, ICH, ECH and SU all
// behave this way, so it is one rule and not six bugs.
//
// The pure emulator is the xterm-compatible side here: xterm and kitty both
// run the command and ignore surplus parameters. The consequence of the
// divergence is concrete - a guest emitting a slightly malformed cursor move
// moves the cursor on one backend and not the other, so the same byte stream
// rehydrates into two different screens depending on which backend the daemon
// was built with.
func TestGhosttyDivergence_SurplusCSIParameters(t *testing.T) {
	cases := []struct {
		name, in           string
		pureCol, pureRow   int
		ghostCol, ghostRow int
	}{
		{"CUF one parameter is obeyed by both", "\x1b[5C", 5, 0, 5, 0},
		{"CUF with a surplus parameter", "\x1b[5;9C", 5, 0, 0, 0},
		{"CUD with a surplus parameter", "\x1b[3;9B", 0, 3, 0, 0},
		{"CUP takes two, so both obey", "\x1b[3;9H", 8, 2, 8, 2},
		{"CUP with a third parameter", "\x1b[3;9;7H", 8, 2, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := probeBoth(t, tc.in, 0, 0)
			if got := p.pureCursor; got.X != tc.pureCol || got.Y != tc.pureRow {
				t.Errorf("pure cursor = %v, want (%d,%d)", got, tc.pureCol, tc.pureRow)
			}
			if got := p.ghCursor; got.X != tc.ghostCol || got.Y != tc.ghostRow {
				t.Errorf("ghostty cursor = %v, want (%d,%d)", got, tc.ghostCol, tc.ghostRow)
			}
		})
	}
}

// TestGhosttyDivergence_ControlCodePointsPrinted is the second largest class,
// 76 of 150 seeds, and the one with a side that is plainly wrong.
//
// DEL and the C1 code points are controls. The pure emulator discards them.
// The library backend puts them on the screen as visible cells and advances
// the cursor over them, so a stream containing a stray 0x7f gains a character
// that was never sent. ECMA-48 has DEL ignored, and xterm, kitty and foot all
// ignore it, so the library side of this is a defect rather than a choice.
//
// It reaches tuios directly: readline and anything that filters terminal
// output emit DEL, and a pane on the library backend would show a glyph for
// each one and be one column further right than the guest believes.
func TestGhosttyDivergence_ControlCodePointsPrinted(t *testing.T) {
	// The control is counted by how far the cursor moved: a backend that
	// discards it advances only over the real characters, one that prints it
	// advances one column further. Reading a fixed cell index cannot say this,
	// because the two backends put the same text in different columns.
	cases := []struct {
		name, in          string
		pureCol, ghostCol int
		printedAt         int
	}{
		{"DEL alone", "\x7f", 0, 1, 0},
		{"DEL after text", "abc\x7f", 3, 4, 3},
		{"U+0084 as text", "AB", 2, 3, 1},
		{"U+0085 as text", "AB", 2, 3, 1},
		{"U+009B as text", "AB", 2, 3, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := probeBoth(t, tc.in, tc.printedAt, 0)
			if got := p.pureCursor.X; got != tc.pureCol {
				t.Errorf("pure cursor column = %d, want %d: the pure emulator "+
					"used to discard the control rather than print it", got, tc.pureCol)
			}
			if got := p.ghCursor.X; got != tc.ghostCol {
				t.Fatalf("ghostty cursor column = %d, want %d; if the library "+
					"stopped printing controls, delete this entry", got, tc.ghostCol)
			}
			if p.ghCell == " " {
				t.Errorf("ghostty left (%d,0) blank, so it no longer prints the control",
					tc.printedAt)
			}
		})
	}

	// NUL is the control both already agree to discard, so it guards the
	// probe itself: if this starts failing the harness is broken, not a
	// backend.
	t.Run("NUL is discarded by both", func(t *testing.T) {
		p := probeBoth(t, "abc\x00", 3, 0)
		if p.pureCell != " " || p.ghCell != " " {
			t.Errorf("NUL: pure=%q ghostty=%q, want both blank", p.pureCell, p.ghCell)
		}
	})
}

// TestGhosttyDivergence_SelectiveErase records a gap the pure emulator has
// always had, which the second backend has now turned into a divergence.
//
// TestConform_SelectiveErase documents the pure emulator not implementing
// DECSCA, DECSED or DECSEL, on the grounds that tmux does not either. The
// library does implement them. So a guest that sends CSI ? 2 J gets its
// screen cleared on one backend and nothing at all on the other, which is the
// case that comment anticipated when it said a future implementation would
// have something to aim at.
func TestGhosttyDivergence_SelectiveErase(t *testing.T) {
	cases := []struct {
		name, in string
		// at is a column holding an UNPROTECTED character, which selective
		// erase must clear. In the mixed case the first two columns are
		// protected and the library is right to keep them, so pointing the
		// probe at column 0 there would test nothing.
		at int
	}{
		{"DECSED erases nothing on the pure emulator", "ABCD\x1b[H\x1b[?2J", 0},
		{"DECSEL erases nothing on the pure emulator", "abcdef\x1b[H\x1b[?0K", 0},
		{"DECSCA does not protect on the pure emulator", "\x1b[1\"qAB\x1b[2\"qCD\x1b[H\x1b[?2J", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := probeBoth(t, tc.in, tc.at, 0)
			if p.pureCell == " " {
				t.Fatalf("the pure emulator now acts on selective erase for %q; "+
					"TestConform_SelectiveErase needs updating and so does this entry", tc.in)
			}
			if p.ghCell != " " {
				t.Errorf("ghostty no longer erases for %q (cell %q); update this entry", tc.in, p.ghCell)
			}
		})
	}
}

// TestGhosttyDivergence_BackgroundColourErase pins which operations carry the
// current background into the cells they blank.
//
// The pure emulator applies background-colour erase to EL, IL, DL and SU; the
// library applies it to ED and to nothing else. xterm with bce set fills all
// of them, so the pure emulator is the compatible side. What a user sees on
// the library backend is a coloured application - anything with a themed
// background - growing default-coloured bands wherever it inserts, deletes or
// scrolls lines.
func TestGhosttyDivergence_BackgroundColourErase(t *testing.T) {
	// bgAt reports whether a blanked cell carries a background colour.
	bgAt := func(term Terminal, x, y int) bool {
		c := term.CellAt(x, y)
		return c != nil && c.Style.Bg != nil
	}

	cases := []struct {
		name, in string
		x, y     int
		wantPure bool
		wantGh   bool
	}{
		// ED is the odd one out on BOTH sides: neither carries the
		// background into a cleared screen, though xterm with bce does.
		// It stays here as the case that guards the probe, and as a note
		// that the pure emulator is inconsistent with itself - it applies
		// bce to the four operations below but not to this one.
		{"ED carries the background on neither", "\x1b[41m\x1b[2J", 0, 0, false, false},
		{"EL carries it only on the pure emulator", "ABCD\x1b[H\x1b[41m\x1b[K", 0, 0, true, false},
		{"IL carries it only on the pure emulator", "\x1b[41m\x1b[3L", 0, 0, true, false},
		{"DL carries it only on the pure emulator", "A\r\nB\r\nC\x1b[H\x1b[41m\x1b[2M", 0, 10, true, false},
		{"SU carries it only on the pure emulator", "A\r\nB\x1b[41m\x1b[2S", 0, 10, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newDiffPair(t, 40, 12)
			p.write(t, []byte(diffPrelude+tc.in))
			if got := bgAt(p.pure, tc.x, tc.y); got != tc.wantPure {
				t.Errorf("pure background at (%d,%d) = %v, want %v", tc.x, tc.y, got, tc.wantPure)
			}
			if got := bgAt(p.gh, tc.x, tc.y); got != tc.wantGh {
				t.Errorf("ghostty background at (%d,%d) = %v, want %v", tc.x, tc.y, got, tc.wantGh)
			}
		})
	}
}

// TestGhosttyDivergence_LeftRightMarginReset pins what happens to the
// left and right margins when DECLRMM is switched back off.
//
// Resetting mode 69 disables left and right margin support, and the margins
// go back to the full width with it. The pure emulator does that. The library
// keeps the margins it was given, so a pane that ever enabled DECLRMM keeps
// dead columns on its left for the rest of its life. The pure emulator is the
// side that matches the DEC documentation.
func TestGhosttyDivergence_LeftRightMarginReset(t *testing.T) {
	p := newDiffPair(t, 40, 12)
	p.write(t, []byte(diffPrelude+"\x1b[?69h\x1b[5;20s\x1b[?69l"))

	full := uv.Rect(0, 0, 40, 12)
	if got := p.pure.ScrollRegion(); got != full {
		t.Errorf("pure scroll region = %v, want the full screen %v", got, full)
	}
	if got := p.gh.ScrollRegion(); got == full {
		t.Fatalf("ghostty now clears the margins on DECLRMM reset; delete this entry")
	} else {
		t.Logf("ghostty keeps %v after DECLRMM reset, where the full screen is %v", got, full)
	}
}

// TestGhosttyDivergence_OrphanCombiningMark pins what a combining mark with
// no base character does.
//
// The pure emulator gives it a cell of its own with zero width. The library
// discards it. A zero-width cell is the shape that breaks a renderer, and the
// invariants in vtgen_fuzz_test.go already treat a cell claiming the wrong
// number of columns as a bug, so the library is the safer side here.
func TestGhosttyDivergence_OrphanCombiningMark(t *testing.T) {
	p := newDiffPair(t, 40, 12)
	p.write(t, []byte(diffPrelude+"́"))

	pc, gc := p.pure.CellAt(0, 0), p.gh.CellAt(0, 0)
	if pc == nil || pc.Content != "́" {
		t.Fatalf("the pure emulator no longer stores an orphan mark as a cell (got %s); "+
			"delete this entry", ghDiffCellText(pc))
	}
	if pc.Width != 0 {
		t.Errorf("pure orphan-mark cell width = %d, want 0", pc.Width)
	}
	if gc != nil && gc.Content == "́" {
		t.Errorf("ghostty now stores the orphan mark too; delete this entry")
	}
}

// TestGhosttyDivergence_UnderlineStyleOutOfRange pins what an underline
// substyle nobody defines does.
//
// SGR 4:0 through 4:5 are the defined styles. For 4:7 the library clamps to a
// single underline, which is what kitty does; the pure emulator sets an
// attribute bit instead and leaves the underline style at none. 4:5 is in
// range and both agree on it, which keeps this entry honest about being
// specifically an out-of-range question.
func TestGhosttyDivergence_UnderlineStyleOutOfRange(t *testing.T) {
	t.Run("4:5 is in range and both agree", func(t *testing.T) {
		p := newDiffPair(t, 40, 12)
		p.write(t, []byte(diffPrelude+"\x1b[4:5mX"))
		if d := diffScreens(p.pure, p.gh); d.found() {
			t.Errorf("4:5 diverged: %s", d)
		}
	})

	t.Run("4:7 is out of range and they differ", func(t *testing.T) {
		p := newDiffPair(t, 40, 12)
		p.write(t, []byte(diffPrelude+"\x1b[4:7mX"))
		pc, gc := p.pure.CellAt(0, 0), p.gh.CellAt(0, 0)
		if pc == nil || gc == nil {
			t.Fatal("no cell written")
		}
		if pc.Style.Underline == gc.Style.Underline && pc.Style.Attrs == gc.Style.Attrs {
			t.Fatalf("the two backends now agree on SGR 4:7; delete this entry")
		}
		if gc.Style.Underline == 0 {
			t.Errorf("ghostty underline for 4:7 = 0, expected it to clamp to a real style")
		}
	})
}
