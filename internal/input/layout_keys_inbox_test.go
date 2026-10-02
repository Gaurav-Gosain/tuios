//go:build !slim

package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func TestBaseLayoutKeyRunsInboxBinding(t *testing.T) {
	o := inboxInputOS(t)
	o.OpenInbox("")
	// esc and q close the Inbox. q is й on the Ukrainian layout.
	o, _ = HandleKeyPress(ukr('й', 'q'), o)
	if o.ShowInbox {
		t.Fatal("й on the Q key did not close the Inbox")
	}
}

func TestBaseLayoutKeyRunsPrefixBinding(t *testing.T) {
	o, _ := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o = leader(o, ukr('ш', 'i'))
	if !o.ShowInbox {
		t.Fatal("leader then ш on the I key did not open the Inbox")
	}
}

// A key that beat the switch to report-all keys arrives as bare text with no
// base-layout key. After the leader it is dropped, not typed into the pane.
func TestLeaderDropsLayoutKeyThatBeatTheFlags(t *testing.T) {
	o, pty := osWithFocusedPane(t, config.DefaultConfig(), app.TerminalMode)
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 5})
	o = leader(o, tea.KeyPressMsg{Code: 'ш', Text: "ш"})
	if len(pty.got) != 0 {
		t.Fatalf("the pane got %q for a ш typed before the host switched", pty.got)
	}
	// The prefix stays pending, so the key can be typed again once the host
	// has switched and reports its base-layout key.
	if !o.PrefixActive {
		t.Fatal("the dropped key ended the prefix")
	}
	o.NoteKeyboardEnhancements(tea.KeyboardEnhancementsMsg{Flags: 29})
	o, _ = HandleKeyPress(ukr('ш', 'i'), o)
	if !o.ShowInbox {
		t.Fatal("ш typed again after the switch did not open the Inbox")
	}
}
