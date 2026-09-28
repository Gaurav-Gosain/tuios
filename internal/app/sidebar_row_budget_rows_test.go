package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Rows drawn on the row budget, at the widths a person can set.

// TestADownMachineSaysSoOnANarrowRail: with the budget, a narrow rail kept a
// down machine's name and dropped the whole figure, so "workstation" with
// three messages queued read "▾ workstation ───": no mail, and nothing but
// the ink to say the machine was down. Every width from 15 to 40 columns now
// draws the mail count, and a down machine with no mail draws a mark, while
// an up machine draws neither.
func TestADownMachineSaysSoOnANarrowRail(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	pal := theme.UI()
	row := func(status federation.Status, queued, cw int) string {
		node := sessiontree.Node{Kind: sessiontree.KindHost, Host: "workstation", Title: "workstation", HostStatus: string(status), HostQueued: queued}
		return ansi.Strip(m.sidebarHostRow(node, cw, pal, "", sidebarRowState{}, false))
	}
	mark := hostDownMark()
	for rail := 15; rail <= 40; rail++ {
		cw := rail - 1
		queued := row(federation.StatusUnreachable, 3, cw)
		if !strings.Contains(queued, "3 queued") {
			t.Errorf("rail %d: a down machine with mail lost its count: %q", rail, queued)
		}
		// The mail outranks the name here, so the narrowest rails read
		// "w… 3 queued", as they did before the budget.
		if !strings.Contains(queued, "▾ w") {
			t.Errorf("rail %d: the header names no machine: %q", rail, queued)
		}
		down := row(federation.StatusUnreachable, 0, cw)
		if !strings.Contains(down, "offline") && !strings.Contains(down, mark) {
			t.Errorf("rail %d: a down machine reads like an up one: %q", rail, down)
		}
		// The positive half: the mark means down, so an up machine and one
		// on its way up never wear it.
		if up := row(federation.StatusUp, 0, cw); strings.Contains(up, mark) {
			t.Errorf("rail %d: an up machine wears the down mark: %q", rail, up)
		}
		if on := row(federation.StatusConnecting, 0, cw); strings.Contains(on, mark) {
			t.Errorf("rail %d: a machine that is connecting wears the down mark: %q", rail, on)
		}
		for _, r := range []string{queued, down} {
			if w := lipgloss.Width(r); w > cw {
				t.Errorf("rail %d: the header is %d cells, wider than %d: %q", rail, w, cw, r)
			}
		}
	}
	// A rail with room for everything says all of it.
	if got := row(federation.StatusUnreachable, 3, 39); !strings.Contains(got, "3 queued, offline") {
		t.Errorf("a wide rail should say the mail and why: %q", got)
	}
}

// TestAgentQueuedFigureIsPickedInTheBudget: the queued figure used to be
// picked against the name's keep alone, so a row with the state after the
// name drew "deploy-the-api-… 2 queued" and dropped "· working" where
// "2q · working" fit. The longest form that keeps every other token wins now.
func TestAgentQueuedFigureIsPickedInTheBudget(t *testing.T) {
	after := []sidebarAgentToken{{Name: "state", Text: "working"}}
	queued := []string{"2 queued", "2q"}
	const nameW = 22 // "deploy-the-api-gateway"
	for _, tc := range []struct {
		avail   int
		figure  string
		working bool
	}{
		{40, "2 queued", true},
		{29, "2 queued", true}, // both fit exactly beside the name's keep
		{28, "2q", true},       // the long form would cost the state
		{23, "2q", true},       // the short form and the state fit exactly
		// Neither form keeps the state, so the longest one kept at all.
		{22, "2 queued", false},
		{19, "2 queued", false},
		{13, "2q", false},
		{12, "", false}, // no form fits beside the name's keep
	} {
		var s railScratch
		figure, keep, _ := sidebarAgentFit(&s, nil, after, nameW, "", queued, "", sidebarAgentSep(), tc.avail)
		if !keep[1] {
			figure = ""
		}
		if figure != tc.figure || keep[0] != tc.working {
			t.Errorf("avail %d drew %q with the state kept = %v, want %q and %v",
				tc.avail, figure, keep[0], tc.figure, tc.working)
		}
	}
}

// TestNoteLineNeverEndsInABareEllipsis: a sentence that opens on a wide
// character cuts to "…" alone in two cells, and the line read "claude · …".
// The sentence is left off instead. One more cell draws it again, which is
// the positive half.
func TestNoteLineNeverEndsInABareEllipsis(t *testing.T) {
	m, _ := sectionsTestOS(t, 120, 30)
	pal := theme.UI()
	note := func(msg string, avail int) string {
		tokens := []sidebarAgentToken{{Name: "harness", Text: "claude"}, {Name: "message", Text: msg}}
		return ansi.Strip(m.sidebarAgentNoteText(tokens, lipgloss.NewStyle(), avail, pal))
	}
	sep := sidebarAgentSep()
	// "claude · " is 9 cells, so 11 leaves the message 2.
	if got := note("日本語のメモ", 11); got != "claude" {
		t.Errorf("a wide sentence cut to its ellipsis: got %q, want %q", got, "claude")
	}
	if got, want := note("日本語のメモ", 12), "claude"+sep+"日"+overlay.Ellipsis(); got != want {
		t.Errorf("a wide sentence with room for a character: got %q, want %q", got, want)
	}
	if got, want := note("approve Bash", 11), "claude"+sep+"a"+overlay.Ellipsis(); got != want {
		t.Errorf("a narrow sentence in two cells: got %q, want %q", got, want)
	}
}
