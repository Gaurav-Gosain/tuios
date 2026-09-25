package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// TestSidebarSanitizesTitles checks a title carrying nerd-font private-use
// icons and control characters reaches the rail laundered.
func TestSidebarSanitizesTitles(t *testing.T) {
	if got := printableTitle(" nvim \x1b]0;x\x07"); got != "nvim ]0;x" {
		// The escape byte and the bell go; printable remnants of a title
		// sequence stay (they are the shell's bug to fix, not tofu).
		t.Errorf("printableTitle = %q", got)
	}
	overlay.SetASCII(true)
	t.Cleanup(func() { overlay.SetASCII(false) })
	if got := printableTitle("café ▲"); got != "caf" {
		t.Errorf("ASCII printableTitle = %q, want %q", got, "caf")
	}
}

// sidebarMultiSessionOS builds an OS attached to "main" with agent-flagged
// windows and the sidebar on, plus a synthetic three-session tree the way a
// daemon-backed client would see one. The tree order is the daemon's creation
// order: main, scratch, deploy.
func sidebarMultiSessionOS(t *testing.T, w, h int) (*OS, sessiontree.Tree) {
	t.Helper()
	m := newNarrowOS(t, w, h)
	m.CurrentWorkspace = 1
	m.SessionName = "main"
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "claude", Width: 40, Height: 20, Workspace: 1, AgentState: "working"},
		{ID: "bbbbbbbb2222", CustomName: "tests", Width: 40, Height: 20, Workspace: 1, AgentState: "needs_input"},
		{ID: "cccccccc3333", CustomName: "logs", Width: 40, Height: 20, Workspace: 1},
	}
	m.FocusedWindow = 0
	withSidebar(t, true, "left", config.SidebarDefaultWidth)
	m.Settings = config.Global
	m.SidebarOrder = nil

	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: "main", Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: "claude", AgentState: "working", Focused: true},
			{ID: "bbbbbbbb2222", Title: "tests", AgentState: "needs_input"},
			{ID: "cccccccc3333", Title: "logs"},
		}},
		{Name: "scratch", WindowCount: 2},
		{Name: "deploy", WindowCount: 1},
	})
	return m, tree
}

// TestSidebarASCIIFallback renders every variant in ASCII-only mode and
// asserts nothing outside ASCII leaks through except the agent-state shapes,
// which are deliberately kept everywhere they appear. In the default mode the
// only private-use codepoints allowed are the dock's own pill caps.
func TestSidebarASCIIFallback(t *testing.T) {
	m, tree := sidebarMultiSessionOS(t, 120, 40)

	prevASCII := config.Global.UseASCIIOnly
	config.Global.UseASCIIOnly = true
	m.Settings.UseASCIIOnly = true
	overlay.SetASCII(true)
	t.Cleanup(func() {
		config.Global.UseASCIIOnly = prevASCII
		overlay.SetASCII(prevASCII)
	})

	agentShapes := map[rune]bool{'●': true, '▲': true, '○': true, '■': true, '×': true}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {51, 37}, {40, 20}} {
		m.Width, m.Height = size[0], size[1]
		m.EffectiveWidth, m.EffectiveHeight = size[0], size[1]
		lines, w := m.sidebarPanelLinesForTree(tree)
		if w == 0 {
			t.Fatalf("%dx%d: sidebar hidden unexpectedly", size[0], size[1])
		}
		for i, ln := range lines {
			if lw := lipgloss.Width(ln); lw != w {
				t.Errorf("%dx%d row %d is %d cells, want %d", size[0], size[1], i, lw, w)
			}
			for _, r := range stripSidebarANSI(ln) {
				if r <= 0x7e || agentShapes[r] {
					continue
				}
				t.Errorf("%dx%d row %d leaks non-ASCII rune %q (U+%04X)", size[0], size[1], i, r, r)
			}
		}
	}

	// Default mode: private-use codepoints only where the dock already uses
	// them (the pill caps); anything else is the kind of tofu the rail must
	// never emit.
	config.Global.UseASCIIOnly = false
	overlay.SetASCII(false)
	sanctioned := map[rune]bool{}
	for _, r := range config.DockPillLeftChar + config.DockPillRightChar {
		sanctioned[r] = true
	}
	m.Width, m.Height = 120, 40
	m.EffectiveWidth, m.EffectiveHeight = 120, 40
	lines, _ := m.sidebarPanelLinesForTree(tree)
	for i, ln := range lines {
		for _, r := range stripSidebarANSI(ln) {
			pua := (r >= 0xe000 && r <= 0xf8ff) || r >= 0xf0000
			if pua && !sanctioned[r] {
				t.Errorf("row %d emits unsanctioned private-use rune U+%04X", i, r)
			}
		}
	}
}

// stripSidebarANSI drops SGR escape sequences so rune audits see only what is
// actually printed.
func stripSidebarANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
