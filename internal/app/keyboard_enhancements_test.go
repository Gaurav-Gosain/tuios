package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestAllKeysAreAskedForWhileBindingsReadKeys pins issue #202. Alternate-key
// reporting only adds the base-layout key to a key the host already sends as
// an escape code, and a plain letter is sent as text. So the key after the
// leader has to be asked for as an escape code, or a Ukrainian "ш" arrives
// with no US "i" behind it. A pane with the keyboard is left alone: it gets
// text as text.
func TestAllKeysAreAskedForWhileBindingsReadKeys(t *testing.T) {
	cfg := config.DefaultConfig()
	o := NewOS(OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	o.Windows = []*terminal.Window{{ID: "a", Terminal: em, Width: 80, Height: 24, Workspace: o.CurrentWorkspace}}
	o.FocusedWindow = 0

	check := func(what string, want bool) {
		t.Helper()
		e := o.keyboardEnhancements()
		if !e.ReportAlternateKeys {
			t.Errorf("%s: alternate keys are not asked for", what)
		}
		if e.ReportAllKeysAsEscapeCodes != want || e.ReportAssociatedText != want {
			t.Errorf("%s: all keys %v, associated text %v, want both %v",
				what, e.ReportAllKeysAsEscapeCodes, e.ReportAssociatedText, want)
		}
	}

	o.Mode = WindowManagementMode
	check("window mode", true)

	o.Mode = TerminalMode
	check("terminal mode, typing into the pane", false)

	o.PrefixActive = true
	check("terminal mode, prefix pending", true)
	o.PrefixActive = false

	o.TilingPrefixActive = true
	check("terminal mode, window prefix pending", true)
	o.TilingPrefixActive = false

	o.ShowInbox = true
	check("terminal mode, Inbox open", true)
	o.ShowInbox = false

	o.SidebarFocused = true
	check("terminal mode, rail focused", true)
	o.SidebarFocused = false

	check("terminal mode again", false)
}
