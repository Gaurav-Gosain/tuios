package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// decorativeTitle carries the junk an agent tends to inject into a terminal
// title: a Dingbat sparkle, a Miscellaneous-Symbols diamond, an emoji, and a
// symbol carrying the emoji variation selector. None of it should survive as
// chrome.
const decorativeTitle = "✳ claude ♦ \U0001f680 build️"

// TestPrintableTitleDropsDecorative asserts the shared sanitizer strips
// decorative symbol/emoji codepoints while leaving our own status glyphs and
// legitimate box-drawing/arrow characters untouched.
func TestPrintableTitleDropsDecorative(t *testing.T) {
	got := printableTitle(decorativeTitle)
	for _, bad := range []rune{'✳', '♦', '\U0001f680', '️'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("printableTitle kept decorative U+%04X: %q", bad, got)
		}
	}
	if !strings.Contains(got, "claude") || !strings.Contains(got, "build") {
		t.Errorf("printableTitle dropped legitimate text: %q", got)
	}

	// Our state glyphs, box-drawing, and arrows must pass through verbatim.
	keep := "● run │ tests → ok"
	if got := printableTitle(keep); got != keep {
		t.Errorf("printableTitle mangled legitimate glyphs: got %q want %q", got, keep)
	}
}

// TestPrintableTitleDropsSpinnerFrames pins the codepoints Claude Code actually
// writes with OSC 0: U+2733 while idle and the U+2802/U+2810 Braille pair it
// alternates while working. The Braille frames used to survive and were the
// tofu box the rail showed. Our own status glyphs and ordinary text stay.
func TestPrintableTitleDropsSpinnerFrames(t *testing.T) {
	for _, in := range []string{"✳ Claude Code", "⠂ Claude Code", "⠐ Acknowledge request"} {
		got := printableTitle(in)
		for _, bad := range []rune{'✳', '⠂', '⠐'} {
			if strings.ContainsRune(got, bad) {
				t.Errorf("printableTitle kept U+%04X from %q: %q", bad, in, got)
			}
		}
	}

	keep := "●▲○■× café 日本語 (v2) ─ ok"
	if got := printableTitle(keep); got != keep {
		t.Errorf("printableTitle mangled status glyphs or text: got %q want %q", got, keep)
	}
}

// TestTitleBadgeStripsDecorative checks the window title badge source launders
// a decorative terminal title while keeping the agent-state glyph it prepends.
func TestTitleBadgeStripsDecorative(t *testing.T) {
	win := &terminal.Window{ID: "w1", CustomName: decorativeTitle, AgentState: "working"}
	got := getWindowTitle(win, 1, false, "", 80)
	if strings.ContainsAny(got, "✳♦️") || strings.ContainsRune(got, '\U0001f680') {
		t.Errorf("title badge kept decorative junk: %q", got)
	}
	if !strings.Contains(got, "●") {
		t.Errorf("title badge dropped the state glyph: %q", got)
	}
	if !strings.Contains(got, "claude") {
		t.Errorf("title badge dropped the real name: %q", got)
	}
}

// TestPaletteRowStripsDecorative checks a palette row launders a decorative
// entry name.
func TestPaletteRowStripsDecorative(t *testing.T) {
	item := CommandPaletteItem{Name: decorativeTitle, Category: "Session", AgentState: "working"}
	got := paletteRow(item, false, theme.UI(), 60)
	if strings.ContainsAny(got, "✳♦️") || strings.ContainsRune(got, '\U0001f680') {
		t.Errorf("palette row kept decorative junk: %q", got)
	}
	if !strings.Contains(got, "claude") {
		t.Errorf("palette row dropped the real name: %q", got)
	}
}

// spinnerTitle is the title Claude Code actually sets while it works: a Braille
// spinner frame in front of its name, which tofus in any font that stops at
// Latin.
const spinnerTitle = "⠂ Claude Code"

// TestContextMenuTitleStripsDecorative checks the pane menu's header launders
// the window title. Every other surface was routed through the sanitizer and
// this one was missed, so the tofu box came back the moment a pane was
// right-clicked.
func TestContextMenuTitleStripsDecorative(t *testing.T) {
	m := ctxMenuOS(t, 80, 24)
	m.Windows[0].CustomName = spinnerTitle

	title, _ := m.paneMenu(0)
	m.ContextMenu = &ContextMenu{Title: title, Items: []ContextMenuItem{{Label: "Close pane"}}}
	out, _ := m.renderContextMenu()
	if strings.ContainsRune(out, '⠂') {
		t.Errorf("the pane menu drew the spinner frame: %q", out)
	}
	if !strings.Contains(out, "Claude Code") {
		t.Errorf("the pane menu dropped the real name: %q", out)
	}
}

// TestOverlayTitlesStripDecorative walks the remaining surfaces that echo a
// window title or session name, so a sanitizer gap cannot come back one overlay
// at a time.
func TestOverlayTitlesStripDecorative(t *testing.T) {
	// Checking for the real name too: a surface that drew nothing at all would
	// otherwise pass for having drawn nothing dirty.
	laundered := func(t *testing.T, what, out string) {
		t.Helper()
		if strings.ContainsAny(out, "✳♦️⠂") || strings.ContainsRune(out, '\U0001f680') {
			t.Errorf("%s drew decorative junk:\n%s", what, out)
		}
		if !strings.Contains(out, "claude") {
			t.Errorf("%s dropped the real name:\n%s", what, out)
		}
	}

	t.Run("session switcher", func(t *testing.T) {
		m := daemonRailOS(t, 80, 24)
		m.SessionSwitcherItems = []sessiontree.Node{{ID: "s1", Title: decorativeTitle}}
		out, _, _ := m.renderSessionSwitcher()
		laundered(t, "the session switcher", out)
	})

	t.Run("quit menu", func(t *testing.T) {
		m := ctxMenuOS(t, 80, 24)
		m.IsDaemonSession, m.SessionName = true, decorativeTitle
		out, _, _ := m.renderQuitMenu()
		laundered(t, "the quit menu", out)
	})

	t.Run("aggregate view", func(t *testing.T) {
		m := ctxMenuOS(t, 80, 24)
		m.Windows[0].CustomName = decorativeTitle
		out, _, _ := m.renderAggregateView()
		laundered(t, "the aggregate view", out)
	})
}

// TestSidebarStripsDecorative checks decorative junk in a session or window
// title never reaches a rendered rail row.
func TestSidebarStripsDecorative(t *testing.T) {
	m, _ := sidebarMultiSessionOS(t, 120, 40)
	tree := sessiontree.Build([]sessiontree.SessionInput{
		{Name: decorativeTitle, Attached: true, IsCurrent: true, Windows: []sessiontree.WindowInput{
			{ID: "aaaaaaaa1111", Title: decorativeTitle, AgentState: "working", Focused: true},
		}},
	})

	lines, _ := m.sidebarPanelLinesForTree(tree)
	joined := strings.Join(lines, "\n")
	if strings.ContainsAny(joined, "✳♦️") || strings.ContainsRune(joined, '\U0001f680') {
		t.Errorf("sidebar rendered decorative junk:\n%s", joined)
	}
	if !strings.Contains(joined, "claude") {
		t.Errorf("sidebar dropped the real name:\n%s", joined)
	}
}
