package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// attachedTree is sessionColorTree with the attachment moved, which is the one
// thing the focus gutter is supposed to follow.
func attachedTree(attached string) sessiontree.Tree {
	in := []sessiontree.SessionInput{
		{Name: "main", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "nvim", Workspace: 1},
		}},
		{Name: "api", CurrentWorkspace: 1, Windows: []sessiontree.WindowInput{
			{ID: "dddddddd4444", Title: "server", Workspace: 1},
		}},
	}
	for i := range in {
		if in[i].Name != attached {
			continue
		}
		in[i].Attached, in[i].IsCurrent = true, true
		in[i].Windows[0].Focused = true
	}
	return sessiontree.Build(in)
}

// TestFocusedPaneGutterIsItsSessionsColour is the coherence the rail was missing:
// the attached session's row and the focused pane's row two lines below it wear
// the same mark in the same hue, because they are two halves of one answer to
// "where am I". A magenta session over a blue pane read as a mismatch.
func TestFocusedPaneGutterIsItsSessionsColour(t *testing.T) {
	m, tree := sessionColorOS(t, 120, 40)
	rows := railStyled(t, m, tree)

	ink := fgParams(m.sessionTint("main", theme.TerminalBg()))
	session := styledRow(t, rows, "main")
	pane := styledRow(t, rows, "nvim")
	for _, c := range []struct {
		what, row string
	}{{"session", session}, {"focused pane", pane}} {
		if got := gutterCell(c.row); got != "▎" {
			t.Errorf("the %s row has no gutter mark, column 0 is %q: %q", c.what, got, c.row)
		}
		if !strings.Contains(c.row, ink) {
			t.Errorf("the %s row's gutter is not the session's colour: %q", c.what, c.row)
		}
	}

	// The colour is the session's, so moving the session's colour moves both
	// marks together rather than only the one the user set it on.
	m.SessionAccent = "bright cyan"
	m.sidebarCache.invalidate()
	rows = railStyled(t, m, tree)
	want, _ := ParseAccent("bright cyan")
	moved := fgParams(theme.Readable(want.RGB(), theme.TerminalBg()))
	for _, name := range []string{"main", "nvim"} {
		row := styledRow(t, rows, name)
		if !strings.Contains(row, moved) {
			t.Errorf("the %q row did not follow the session's new colour: %q", name, row)
		}
	}
}

// TestFocusedPaneGutterFollowsTheAttachedSession: attaching elsewhere is what
// repaints the mark, since the colour belongs to the session and not to the row.
func TestFocusedPaneGutterFollowsTheAttachedSession(t *testing.T) {
	m, _ := sessionColorOS(t, 120, 40)

	for _, name := range []string{"main", "api"} {
		m.SessionName = name
		m.sidebarCache.invalidate()
		rows := railStyled(t, m, attachedTree(name))

		pane := "nvim"
		if name == "api" {
			pane = "server"
		}
		row := styledRow(t, rows, pane)
		if !strings.Contains(row, fgParams(m.sessionTint(name, theme.TerminalBg()))) {
			t.Errorf("attached to %q, the focused pane's gutter is not that session's colour: %q", name, row)
		}
		other := "api"
		if name == "api" {
			other = "main"
		}
		if strings.Contains(row, fgParams(m.sessionTint(other, theme.TerminalBg()))) {
			t.Errorf("attached to %q, the focused pane's gutter still carries %q's colour: %q", name, other, row)
		}
	}
}

// TestSeverityStillOutranksIdentityInTheTerminalsSection holds the ladder the
// session colours were added under: an alarm owns the gutter, and identity is
// what gives way. Only the focus mark took a hue, so the rungs above it are
// unmoved.
func TestSeverityStillOutranksIdentityInTheTerminalsSection(t *testing.T) {
	m, _ := attentionOS(t, 120, 40)
	m.FocusedWindow = 0
	pal := theme.UI()

	row := treeRow(t, m, "server") // needs_input, not focused
	if !strings.Contains(row, fgParams(pal.Warning)) {
		t.Errorf("the alarm lost the gutter to identity: %q", row)
	}
	if tint := m.sessionTint("main", theme.TerminalBg()); tint != nil && strings.Contains(row, fgParams(tint)) {
		t.Errorf("an unfocused pane wanting a human took the session's colour: %q", row)
	}
}

// TestPinnedPaneAccentOutranksTheSessionColour: an accent the user put on one
// pane is the more specific thing they asked for, so it still wins the gutter of
// the pane they are sitting on.
func TestPinnedPaneAccentOutranksTheSessionColour(t *testing.T) {
	m, tree := sessionColorOS(t, 120, 40)
	pinned := SlotAccent(3)
	m.SetWindowAccent("aaaaaaaa1111", pinned)
	m.sidebarCache.invalidate()

	row := styledRow(t, railStyled(t, m, tree), "nvim")
	if !strings.Contains(row, fgParams(pinned.Color())) {
		t.Errorf("the pinned pane accent lost the gutter to the session's colour: %q", row)
	}
}

// TestSessionColoursOffRestoreTheAccentFocusGutter: the config key is the way
// back, so with it off every focus mark on the rail is the rail accent it was
// before, at both widths.
func TestSessionColoursOffRestoreTheAccentFocusGutter(t *testing.T) {
	withSessionColors(t, false)
	pal := theme.UI()

	m, tree := sessionColorOS(t, 120, 40)
	row := styledRow(t, railStyled(t, m, tree), "nvim")
	if !strings.Contains(row, fgParams(pal.Accent)) {
		t.Errorf("with session colours off the focused pane's gutter is not the rail accent: %q", row)
	}

	ms, ts := stripOS(t, 120, 20)
	lines := railStyled(t, ms, ts)
	if !strings.Contains(strings.Join(lines, "\n"), fgParams(pal.Accent)) {
		t.Error("with session colours off the strip's spine lost the rail accent")
	}
}

// TestStripSpineMarksTheAttachedSessionInItsColour: the strip is the rail at
// another width and not another object, so the one bar it draws is the same hue
// the expanded rail would draw it in.
func TestStripSpineMarksTheAttachedSessionInItsColour(t *testing.T) {
	m, tree := stripOS(t, 120, 20)
	pal := theme.UI()

	lines := railStyled(t, m, tree)
	// The spine sits on Panel, which is the ground the colour has to clear.
	want := fgParams(m.sessionTint("main", pal.Panel))
	if !strings.Contains(strings.Join(lines, "\n"), want) {
		t.Errorf("the strip's attached-session bar is not the session's colour:\n%s",
			strings.Join(railPlain(t, m, tree), "\n"))
	}
}
