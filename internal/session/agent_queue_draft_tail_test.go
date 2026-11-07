package session

import (
	"fmt"
	"slices"
	"testing"

	"tuios.dev/vt"
)

// tailShownWrite writes s to the pane's terminal under its lock.
func tailShownWrite(p *PTY, s string) {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	_, _ = fmt.Fprint(p.terminal, s)
}

// TestTailShownAllocatesOnce checks the rows tailShown reads for a draft (the
// faint cells blanked in shown and kept in tail, a box row whole, a blank row
// skipped) and holds the read to a fixed number of allocations.
//
// Measured on go1.27.3 with two strings.Builders reset per row: 408
// allocations at n=10 on this screen.
func TestTailShownAllocatesOnce(t *testing.T) {
	p := &PTY{terminal: vt.NewEmulator(120, 40)}
	tailShownWrite(p, "\x1b[36;1Hdone reading")
	// Row 37 stays blank.
	tailShownWrite(p, "\x1b[38;1H╭──❯ ask ──╮")
	tailShownWrite(p, "\x1b[39;1H│ say \x1b[2mtry this\x1b[22m now")
	// A row that ends in a faint run, the usual placeholder: shown cuts the
	// blanks the faint cells leave at its end.
	tailShownWrite(p, "\x1b[40;1Hx ❯ \x1b[2mTry this\x1b[22m")

	wantTail := []string{"done reading", "╭──❯ ask ──╮", "│ say try this now", "x ❯ Try this"}
	wantShown := []string{"done reading", "╭──❯ ask ──╮", "│ say          now", "x ❯"}
	tail, shown := p.tailShown(10)
	if !slices.Equal(tail, wantTail) || !slices.Equal(shown, wantShown) {
		t.Fatalf("tailShown(10) = %q, %q, want %q, %q", tail, shown, wantTail, wantShown)
	}

	got := testing.AllocsPerRun(50, func() { _, _ = p.tailShown(10) })
	if got > 10 {
		t.Errorf("tailShown(10) on a 120x40 screen allocates %v times, want at most 10", got)
	}

	// A first call's rows stay as they were after the screen changes.
	tailShownWrite(p, "\x1b[36;1HDONE READING")
	tailShownWrite(p, "\x1b[39;1H│ say \x1b[2mTRY THIS\x1b[22m now")
	tail2, shown2 := p.tailShown(10)
	if !slices.Equal(tail, wantTail) || !slices.Equal(shown, wantShown) {
		t.Errorf("rows of the first call after the screen changed = %q, %q, want %q, %q",
			tail, shown, wantTail, wantShown)
	}
	wantTail2 := []string{"DONE READING", "╭──❯ ask ──╮", "│ say TRY THIS now", wantTail[3]}
	wantShown2 := []string{"DONE READING", wantShown[1], wantShown[2], wantShown[3]}
	if !slices.Equal(tail2, wantTail2) || !slices.Equal(shown2, wantShown2) {
		t.Errorf("tailShown(10) after the screen changed = %q, %q, want %q, %q", tail2, shown2, wantTail2, wantShown2)
	}

	blank := &PTY{terminal: vt.NewEmulator(120, 40)}
	if tail, shown := blank.tailShown(10); tail != nil || shown != nil {
		t.Errorf("tailShown(10) on a blank screen = %#v, %#v, want nil, nil", tail, shown)
	}
}
