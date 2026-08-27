package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// boxTopLeft is the first cell of the box a session draws around a pane, with
// the styling stripped. It is read off the rendered string rather than off the
// setting, because the setting is only interesting where it reaches the screen.
func boxTopLeft(t *testing.T, m *OS, win *terminal.Window) string {
	t.Helper()
	box := m.renderWindowBox(win, 0, true, lipgloss.Color("62"))
	row := stripANSIForTrace(strings.SplitN(box, "\n", 2)[0])
	r := []rune(row)
	if len(r) == 0 {
		t.Fatal("the pane drew an empty first row")
	}
	return string(r[0])
}

// TestOneClientsSettingsStopAtThatClient is the whole point of Settings being a
// field rather than a wall of package variables.
//
// `tuios ssh` and tuios-web each run ONE process with one goroutine per
// connection. Before this, the settings panel wrote package globals on every
// keypress, so a person who picked a different border style picked it for every
// other client attached to that server, on sessions they had never heard of.
//
// Two OS values here are two connections. One of them drives the settings
// funnel every keypress in the panel goes through, and the other must come out
// of it drawing exactly what it drew before.
//
// Negative control, confirmed red: point setConfigFromRegistry back at
// &config.Global and make Settings.GetBorderForStyle read config.Global's
// BorderStyle - the old design for this one setting. The frame assertion fails
// with "the other session's pane is drawing Bob's border: corner \"╔\", was
// \"╭\"", which is the bug as it was reported.
func TestOneClientsSettingsStopAtThatClient(t *testing.T) {
	alice := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})
	bob := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})
	t.Cleanup(func() { alice.Cleanup(); bob.Cleanup() })

	aliceWin := newTestWindow(t, "alice-pane-001", 40, 12)
	bobWin := newTestWindow(t, "bob-pane-0001", 40, 12)
	alice.Windows = []*terminal.Window{aliceWin}
	bob.Windows = []*terminal.Window{bobWin}

	before := boxTopLeft(t, alice, aliceWin)

	// Bob opens the settings panel and picks another border, another zen mode
	// and another rail. SetConfig is the funnel: the panel, the command
	// palette and set-option all arrive here.
	for _, kv := range [][2]string{
		{"appearance.border_style", "double"},
		{"appearance.zen_mode", config.ZenModeAlways},
		{"appearance.sidebar.enabled", "true"},
		{"appearance.scrollback_lines", "512"},
	} {
		if err := bob.SetConfig(kv[0], kv[1]); err != nil {
			t.Fatalf("SetConfig(%q, %q) = %v", kv[0], kv[1], err)
		}
	}

	// Bob got what Bob asked for.
	if got := bob.Settings.BorderStyle; got != "double" {
		t.Fatalf("the client that made the change reads border style %q, want double", got)
	}

	// Alice did not, in any of the four.
	if got := alice.Settings.BorderStyle; got != "rounded" {
		t.Errorf("border style crossed sessions: Alice reads %q, want rounded", got)
	}
	if got := alice.Settings.ZenMode; got != config.ZenModeDisabled {
		t.Errorf("zen mode crossed sessions: Alice reads %q, want disabled", got)
	}
	if alice.Settings.SidebarEnabled {
		t.Error("the rail crossed sessions: Alice's sidebar was turned on by Bob")
	}
	if got := alice.Settings.ScrollbackLines; got != config.DefaultScrollbackLines {
		t.Errorf("scrollback crossed sessions: Alice reads %d, want %d", got, config.DefaultScrollbackLines)
	}

	// And on the frame, which is where a user would see it. Alice's pane draws
	// the corner it drew before Bob touched anything, and Bob's draws his.
	if got := boxTopLeft(t, alice, aliceWin); got != before {
		t.Errorf("the other session's pane is drawing Bob's border: corner %q, was %q", got, before)
	}
	if got := boxTopLeft(t, bob, bobWin); got == before {
		t.Errorf("Bob's own pane still draws %q, so the change never reached his frame", got)
	}
}

// TestTheProcessSeedIsNotWrittenByASession is the other half: a served session
// editing its settings must leave the seed every later connection copies
// exactly where startup left it. Without this, the second client to connect
// inherits the first client's taste.
//
// Negative control, confirmed red: point setConfigFromRegistry back at
// &config.Global. Three assertions fail, the last naming the symptom - the
// next connection opens on the previous client's zen mode.
func TestTheProcessSeedIsNotWrittenByASession(t *testing.T) {
	seed := config.Global
	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})
	t.Cleanup(m.Cleanup)

	// zen_mode has no hand-written setter, so it takes the registry funnel:
	// the one call every row of the settings panel arrives through.
	if err := m.SetConfig("appearance.zen_mode", config.ZenModeAlways); err != nil {
		t.Fatalf("SetConfig = %v", err)
	}
	if m.Settings.ZenMode != config.ZenModeAlways {
		t.Errorf("the session did not take its own change: zen mode %q", m.Settings.ZenMode)
	}
	if config.Global.ZenMode != seed.ZenMode {
		t.Errorf("a session's settings page rewrote the process seed: %q, want %q",
			config.Global.ZenMode, seed.ZenMode)
	}

	later := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})
	t.Cleanup(later.Cleanup)
	if later.Settings.ZenMode != seed.ZenMode {
		t.Errorf("the next connection inherited the previous client's zen mode: %q",
			later.Settings.ZenMode)
	}
}
