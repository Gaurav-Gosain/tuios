//go:build ghostty

package vt

import (
	"strings"
	"testing"
)

func promptRow(w int) string { return strings.Repeat("p", w) }

func shellRepaint(w int) string {
	return "\r\r\x1b[A\x1b[K" + promptRow(w) + "\r\n> " + "\x1b[J\r\x1b[2C"
}

func seedShellSession(tm Terminal, w, h int) {
	var b strings.Builder
	for range h + 5 {
		b.WriteString("row\r\n")
	}
	b.WriteString(promptRow(w) + "\r\n> ")
	_, _ = tm.Write([]byte(b.String()))
}

// TestGhosttyReflowIsReversible is the property that keeps a resize from
// accumulating on its own: shrinking and growing back re-presents the same
// logical lines, so nothing may be left behind in history. If this starts
// failing the emulator itself has begun to ratchet, which is a different and
// worse bug than the one below.
func TestGhosttyReflowIsReversible(t *testing.T) {
	const w, h = 31, 10
	gh := NewGhosttyTerminal(w, h)
	defer func() { _ = gh.Close() }()
	seedShellSession(gh, w, h)

	want := gh.ScrollbackLen()
	wantGrid := gh.Render()
	for i := range 8 {
		gh.Resize(w-(i%2), h)
	}
	gh.Resize(w, h)
	if got := gh.ScrollbackLen(); got != want {
		t.Errorf("scrollback after 8 shrink/grow cycles = %d, want %d", got, want)
	}
	if got := gh.Render(); got != wantGrid {
		t.Errorf("grid not restored after 8 shrink/grow cycles")
	}
}

// TestGhosttyResizeSplitsAFullWidthLine records the mechanism. A line the
// guest filled to the last column and ended with CR LF is a hard line: the
// cursor sat in the pending-wrap state until CR cleared it, so neither
// backend marks it wrapped. Narrowing still has to put its last column
// somewhere. Ghostty rewraps it onto a second row and keeps every character;
// the pure emulator has no reflow and drops the characters that no longer
// fit. Ghostty is right (losing a character on resize is data loss, and
// every reflowing terminal, ghostty, kitty and wezterm, rewraps here), but the
// extra row is what a shell's fixed-step repaint then trips over.
func TestGhosttyResizeSplitsAFullWidthLine(t *testing.T) {
	const w, h = 31, 8
	p := newDiffPair(t, w, h)
	line := strings.Repeat("ab", w/2) + "Z"[:w%2]
	p.write(t, []byte("top\r\n"+line[:w]+"\r\n> "))

	p.pure.Resize(w-1, h)
	p.gh.Resize(w-1, h)

	if got := rowString(p.gh, 2); got != line[w-1:w] {
		t.Errorf("ghostty row 2 = %q, want the wrapped last column %q", got, line[w-1:w])
	}
	if got := rowString(p.pure, 2); got != line[w-1:w] {
		t.Errorf("pure row 2 = %q, want the wrapped last column %q", got, line[w-1:w])
	}
}

// TestGhosttyResizeCostsALinePerShrinkWhenAShellRepaints is the reported bug,
// reduced to bytes. It is a characterisation test, not an endorsement: it
// asserts the divergence that exists today so that the day it changes,
// somebody looks. Neither emulator is at fault. Ghostty's reflow is correct
// and reversible (see above); the pure emulator simply never reflows, which
// is why it happens to agree with the shell. The line is lost because the
// shell steps up a fixed number of rows to repaint a prompt that reflow has
// made one row taller.
//
// Ghostty's own answer to this is the OSC 133 redraw extension: on resize it
// blanks the prompt area so the shell can repaint it cleanly. libghostty's C
// API turns that off at construction ("embedders don't necessarily install
// Ghostty's shell integration"), which is why tuios does not get it and a
// ghostty window does. Enabling it clears the stale rows but not the extra
// one, so it is not on its own a fix.
func TestGhosttyResizeCostsALinePerShrinkWhenAShellRepaints(t *testing.T) {
	const w, h = 31, 10
	p := newDiffPair(t, w, h)
	seedShellSession(p.pure, w, h)
	seedShellSession(p.gh, w, h)

	pureStart, ghStart := p.pure.ScrollbackLen(), p.gh.ScrollbackLen()

	const shrinks = 4
	for i := 1; i <= shrinks; i++ {
		nw := w - i
		p.pure.Resize(nw, h)
		p.gh.Resize(nw, h)
		p.write(t, []byte(shellRepaint(nw)))
	}

	if got := p.pure.ScrollbackLen() - pureStart; got != shrinks {
		t.Errorf("pure emulator lost %d lines over %d shrinks, want %d like ghostty", got, shrinks, shrinks)
	}
	if got := p.gh.ScrollbackLen() - ghStart; got != shrinks {
		t.Errorf("ghostty lost %d lines over %d shrinks, want %d (one per shrink); "+
			"if this is now 0 the interaction is fixed and this test should assert that instead",
			got, shrinks, shrinks)
	}
}

// TestGhosttyResizeCostsNothingWhenThePromptFits is the control that names the
// trigger: the same shell repaint, with a prompt that stops short of the last
// column, costs neither backend anything. This is why a wide pane never shows
// the bug and a narrow one shows it on every resize.
func TestGhosttyResizeCostsNothingWhenThePromptFits(t *testing.T) {
	const w, h = 31, 10
	p := newDiffPair(t, w, h)
	short := func(width int) string { return strings.Repeat("p", width-4) }
	seed := func(tm Terminal) {
		var b strings.Builder
		for range h + 5 {
			b.WriteString("row\r\n")
		}
		b.WriteString(short(w) + "\r\n> ")
		_, _ = tm.Write([]byte(b.String()))
	}
	seed(p.pure)
	seed(p.gh)
	pureStart, ghStart := p.pure.ScrollbackLen(), p.gh.ScrollbackLen()

	for i := 1; i <= 4; i++ {
		nw := w - i
		p.pure.Resize(nw, h)
		p.gh.Resize(nw, h)
		p.write(t, []byte("\r\r\x1b[A\x1b[K"+short(nw)+"\r\n> \x1b[J\r\x1b[2C"))
	}
	if got := p.pure.ScrollbackLen() - pureStart; got != 0 {
		t.Errorf("pure emulator lost %d lines with a prompt that fits, want 0", got)
	}
	if got := p.gh.ScrollbackLen() - ghStart; got != 0 {
		t.Errorf("ghostty lost %d lines with a prompt that fits, want 0", got)
	}
}

func rowString(tm Terminal, y int) string {
	var b strings.Builder
	for x := range tm.Width() {
		if c := tm.CellAt(x, y); c != nil && c.Content != "" {
			b.WriteString(c.Content)
		}
	}
	return strings.TrimRight(b.String(), " ")
}
