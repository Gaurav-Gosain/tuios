package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// TestPrintableTitleDropsDecorative asserts the shared sanitizer strips the
// decorative codepoints an agent writes into a terminal title while leaving
// our own status glyphs, box drawing, arrows and ordinary text untouched.
//
// The junk is a Dingbat sparkle, a Miscellaneous-Symbols diamond, an emoji and
// a symbol carrying the emoji variation selector, plus the codepoints Claude
// Code writes with OSC 0: U+2733 while idle and the U+2802/U+2810 Braille pair
// it alternates while working. The Braille frames used to survive and were the
// tofu box the rail showed.
func TestPrintableTitleDropsDecorative(t *testing.T) {
	for _, tc := range []struct {
		in   string
		drop []rune
		keep []string
	}{
		{"✳ claude ♦ \U0001f680 build\uFE0F", []rune{'✳', '♦', '\U0001f680', '\uFE0F'}, []string{"claude", "build"}},
		{"✳ Claude Code", []rune{'✳'}, []string{"Claude Code"}},
		{"⠂ Claude Code", []rune{'⠂'}, []string{"Claude Code"}},
		{"⠐ Acknowledge request", []rune{'⠐'}, []string{"Acknowledge request"}},
	} {
		got := printableTitle(tc.in)
		for _, bad := range tc.drop {
			if strings.ContainsRune(got, bad) {
				t.Errorf("printableTitle(%q) kept U+%04X: %q", tc.in, bad, got)
			}
		}
		for _, want := range tc.keep {
			if !strings.Contains(got, want) {
				t.Errorf("printableTitle(%q) dropped %q: %q", tc.in, want, got)
			}
		}
	}

	for _, keep := range []string{"● run │ tests → ok", "●▲○■× café 日本語 (v2) ─ ok"} {
		if got := printableTitle(keep); got != keep {
			t.Errorf("printableTitle mangled legitimate glyphs: got %q want %q", got, keep)
		}
	}
}

// decorativeTitle carries the junk an agent tends to inject into a terminal
// title: a Dingbat sparkle, a Miscellaneous-Symbols diamond, an emoji, and a
// symbol carrying the emoji variation selector. None of it should survive as
// chrome.
const decorativeTitle = "✳ claude ♦ \U0001f680 build️"

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
