package app

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The parts of multi copy mode that live on the OS: the copy sweep over every
// pane, the dock's pill and help, and the prefix menu naming the mode.

func selectedWindowID(id string) *terminal.Window {
	w := selectedWindow()
	w.ID = id
	w.Workspace = 1
	return w
}

// liveWindow is a pane with a real emulator, on workspace 1.
func liveWindow(t *testing.T, id string) *terminal.Window {
	t.Helper()
	ch := make(chan struct{}, 64)
	w := terminal.NewDaemonWindow(id, id, 0, 0, 80, 24, 0, "pty-"+id, ch, config.DefaultScrollbackLines)
	t.Cleanup(func() { w.Close() })
	w.Workspace = 1
	return w
}

// A multi copy yank sweeps the light over every pane it took text from, on
// one clock, and appearance.motion = none still turns it off.
//
// Negative control: making NoteCopyFlashMany keep only the last pane fails the
// first half.
func TestMultiCopyYankSweepsEveryPane(t *testing.T) {
	m := flashOS(t)
	a, b := selectedWindowID("wa"), selectedWindowID("wb")
	m.Windows = append(m.Windows, a, b)
	m.NoteCopyFlashMany([]*terminal.Window{a, b})
	for _, w := range []*terminal.Window{a, b} {
		if m.copyFlashFor(w.ID) == nil {
			t.Errorf("no sweep over pane %s", w.ID)
		}
		if _, ok := m.copyFlashProgress(w.ID); !ok {
			t.Errorf("the sweep over pane %s is not running", w.ID)
		}
	}
	if m.copyFlashFor("wa").At != m.copyFlashFor("wb").At {
		t.Error("the sweeps do not share one clock")
	}
	m.CancelCopyFlash()
	if m.copyFlashFor("wb") != nil {
		t.Error("cancelling the sweep left one running")
	}

	m.Settings.Motion = config.MotionNone
	m.NoteCopyFlashMany([]*terminal.Window{a, b})
	if m.copyFlash != nil {
		t.Error("appearance.motion = none still swept the copy")
	}
}

// One pane's sweep ending leaves the others running.
func TestMultiCopySweepDropsOnePane(t *testing.T) {
	m := flashOS(t)
	a, b := selectedWindowID("wa"), selectedWindowID("wb")
	m.NoteCopyFlashMany([]*terminal.Window{a, b})
	m.dropCopyFlash("wa")
	if m.copyFlashFor("wa") != nil || m.copyFlashFor("wb") == nil {
		t.Error("dropping one pane's sweep did not leave the other")
	}
	m.copyFlash.At = time.Now().Add(-time.Hour)
	if m.CopyFlashActive() {
		t.Error("an expired sweep is still active")
	}
}

func TestMultiCopyDockSaysWhatTheModeIs(t *testing.T) {
	m := flashOS(t)
	a, b, c := selectedWindowID("wa"), selectedWindowID("wb"), selectedWindowID("wc")
	m.Windows = []*terminal.Window{a, b, c}
	m.FocusedWindow = 0
	m.MultiCopy = &MultiCopy{IDs: []string{"wa", "wb", "wc"}, Format: config.MultiCopyFormatMarkdown}

	if label, ok := m.multiCopyPill(); !ok || strings.TrimSpace(label) != "MULTI 3" {
		t.Errorf("pill = %q, want MULTI 3", label)
	}
	m.SetMultiCopyParked(map[string]bool{"wc": true})
	if label, _ := m.multiCopyPill(); strings.TrimSpace(label) != "MULTI 2/3" {
		t.Errorf("pill after a search = %q, want MULTI 2/3", label)
	}

	help := hintsText(m.copyModeHelp(a)[0])
	for _, want := range []string{"y yank all", "Y save to file", "tab format: markdown"} {
		if !strings.Contains(help, want) {
			t.Errorf("multi copy help %q does not have %q", help, want)
		}
	}
	// Plain copy mode's help is unchanged.
	m.MultiCopy = nil
	if help := hintsText(m.copyModeHelp(a)[0]); strings.Contains(help, "yank all") {
		t.Errorf("plain copy mode shows the multi copy help: %q", help)
	}
}

func hintsText(h []overlay.Hint) string {
	var parts []string
	for _, x := range h {
		parts = append(parts, x.Key+" "+x.Label)
	}
	return strings.Join(parts, ", ")
}

// With multifocus on, the prefix menu names the copy-mode key for what it does
// now. That line is how a multifocus user finds the mode.
func TestPrefixMenuNamesMultiCopyMode(t *testing.T) {
	m := flashOS(t)
	a, b := liveWindow(t, "wa"), liveWindow(t, "wb")
	m.Windows = []*terminal.Window{a, b}
	m.FocusedWindow = 0

	describe := func() string {
		for _, bnd := range m.prefixMenuBindings() {
			if bnd.Key == "[" {
				return bnd.Description
			}
		}
		return ""
	}
	if got := describe(); strings.Contains(got, "Multi") {
		t.Errorf("without multifocus the menu says %q", got)
	}
	m.MultifocusSet = map[string]bool{"wa": true, "wb": true}
	if got := describe(); got != "Multi copy mode (2 panes)" {
		t.Errorf("with multifocus the menu says %q, want Multi copy mode (2 panes)", got)
	}
}
