package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const pillLeft = ""

var removedKeyEventsPanelSignatures = []string{"Key events", "code="}

func showkeysRow(s tuitest.Screen) string {
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		line := s.Line(r)
		if !strings.Contains(line, pillLeft) {
			continue
		}
		if dockStatus.MatchString(line) {
			continue
		}
		return line
	}
	return ""
}

func assertNoRemovedPanel(t *testing.T, term *tuitest.Terminal, when string) {
	t.Helper()
	text := term.Screen().Text()
	for _, sig := range removedKeyEventsPanelSignatures {
		if strings.Contains(text, sig) {
			t.Fatalf("removed key-events panel signature %q is on screen %s\n%s",
				sig, when, term.Snapshot())
		}
	}
}

// TestShowKeysOverlayConsolidated proves that --show-keys drives the single good
// bottom-right keycast on the real binary: the pressed key lands in the keycast
// row, the removed top-left diagnostic panel is nowhere on screen, and the
// overlay is independent of the command palette (which stays closed throughout).
// It captures keys in both window-management and terminal mode.
func TestShowKeysOverlayConsolidated(t *testing.T) {
	term, _ := start(t, startOpts{args: []string{"--show-keys"}})
	waitBoot(t, term)

	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "n")
	}, uiTimeout); err != nil {
		t.Fatalf("keycast never showed the 'n' keypress in window mode: %v\n%s", err, term.Snapshot())
	}
	assertNoRemovedPanel(t, term, "in window-management mode")

	if strings.Contains(term.Screen().Text(), paletteTitle) {
		t.Fatalf("command palette unexpectedly open; overlay must show without it\n%s", term.Snapshot())
	}

	enterTerminalMode(t, term)
	if err := term.SendKeys("Z"); err != nil {
		t.Fatalf("send Z: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "Z")
	}, uiTimeout); err != nil {
		t.Fatalf("keycast never showed the 'Z' keypress in terminal mode: %v\n%s", err, term.Snapshot())
	}
	assertNoRemovedPanel(t, term, "in terminal mode")
	alive(t, term, "after showkeys overlay checks")
}

// TestShowKeysOverlayEnabledByConfig proves the [debug] show_key_events config
// alone, with no --show-keys flag, turns on the same good bottom-right keycast
// and still shows no top-left diagnostic panel.
func TestShowKeysOverlayEnabledByConfig(t *testing.T) {
	base := t.TempDir()
	cfgDir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"),
		[]byte("[debug]\nshow_key_events = true\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	term := startIn(t, base, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(showkeysRow(s), "n")
	}, uiTimeout); err != nil {
		t.Fatalf("config-enabled keycast never showed the 'n' keypress: %v\n%s", err, term.Snapshot())
	}
	assertNoRemovedPanel(t, term, "with config-enabled overlay")
	alive(t, term, "after config-enabled overlay check")
}
