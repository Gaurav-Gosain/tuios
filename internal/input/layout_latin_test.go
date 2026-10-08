package input

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// A chord is matched as the key produced before the key at its US position.
// Dvorak's Ctrl+X sits where US has Ctrl+B: it is the editor's C-x, not the
// leader. German Ctrl+Z is ctrl+z, not ctrl+y.
func TestLatinChordMatchesTheKeyProduced(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'x', BaseCode: 'b', Mod: tea.ModCtrl}, o)
	if o.PrefixActive {
		t.Fatal("Dvorak Ctrl+X started the prefix")
	}
	if got := string(pty.got); got != "\x18" {
		t.Fatalf("the pane got %q for Dvorak Ctrl+X, want %q", got, "\x18")
	}

	keys := bindingKeys(tea.KeyPressMsg{Code: 'z', BaseCode: 'y', Mod: tea.ModCtrl}, 'y')
	if len(keys) == 0 || keys[0] != "ctrl+z" || slices.Contains(keys, "ctrl+y") {
		t.Fatalf("German Ctrl+Z is spelled %v, want ctrl+z and never ctrl+y", keys)
	}
}

// A chord on a non-Latin key still reaches the binding at its US position,
// after the key produced has had its chance.
func TestNonLatinChordFallsBackToBaseLayoutKey(t *testing.T) {
	ctrlEs := tea.KeyPressMsg{Code: 'с', BaseCode: 'c', Mod: tea.ModCtrl}
	keys := bindingKeys(ctrlEs, ctrlEs.BaseCode)
	if len(keys) < 2 || keys[0] != "ctrl+с" || keys[len(keys)-1] != "ctrl+c" {
		t.Fatalf("Ctrl+с is spelled %v, want ctrl+с first and ctrl+c last", keys)
	}
	got := lookupAction(nil, ctrlEs, func(k string) string {
		if k == "ctrl+c" {
			return "bound"
		}
		return ""
	})
	if got != "bound" {
		t.Fatal("Ctrl+с did not reach a ctrl+c binding")
	}

	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'и', BaseCode: 'b', Mod: tea.ModCtrl}, o)
	if !o.PrefixActive {
		t.Fatalf("Ukrainian Ctrl+и (the B key) did not start the prefix (pane got %q)", pty.got)
	}
}

// Latin letters keep their labels: an unbound one does nothing, wherever it
// sits. German ß is on the US "-" key, French é on "2", Turkish ı on "i".
func TestUnboundLatinLetterDoesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyPressMsg
	}{
		{"german sharp s", tea.KeyPressMsg{Code: 'ß', BaseCode: '-', Text: "ß"}},
		{"french e acute", tea.KeyPressMsg{Code: 'é', BaseCode: '2', Text: "é"}},
		{"turkish dotless i", tea.KeyPressMsg{Code: 'ı', BaseCode: 'i', Text: "ı"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := twoPaneWM(t)
			if got := lookupAction(nil, tc.msg, o.KeybindRegistry.GetAction); got != "" {
				t.Fatalf("%s resolves to %q in window mode", tc.msg.Text, got)
			}
			if got := lookupAction(nil, tc.msg, o.KeybindRegistry.GetPrefixAction); got != "" {
				t.Fatalf("%s resolves to %q after the leader", tc.msg.Text, got)
			}
			before := len(o.Windows)
			o, _ = HandleKeyPress(tc.msg, o)
			if o.Mode != app.WindowManagementMode || o.FocusedWindow != 0 || len(o.Windows) != before {
				t.Fatalf("%s changed the layout or mode", tc.msg.Text)
			}
			o = leader(o, tc.msg)
			if o.ShowInbox {
				t.Fatalf("leader then %s opened the Inbox", tc.msg.Text)
			}
		})
	}
}
