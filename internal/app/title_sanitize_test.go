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

	// Our state glyphs (all below U+2600), box-drawing, and arrows must pass
	// through verbatim.
	keep := "● run │ tests → ok"
	if got := printableTitle(keep); got != keep {
		t.Errorf("printableTitle mangled legitimate glyphs: got %q want %q", got, keep)
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
