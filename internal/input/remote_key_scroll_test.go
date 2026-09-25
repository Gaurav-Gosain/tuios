package input

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A pane scrolled back with the wheel is in an implicit copy mode, and any key
// the person presses ends it: they have stopped reading. tuios send-keys is
// dispatched through the same handler, so an agent typing into the pane ended
// the person's scrolled view too, at a moment decided by another process.
//
// The policy pinned here: a remote key is not the person's. It leaves copy
// mode alone, implicit or explicit, and goes where it would go with no copy
// mode in progress. The person's own key keeps doing what it did.
//
// NEGATIVE CONTROLS, each run by mutating the shipped code and watching the
// named row fail:
//
//   - remoteKeyBypassesCopyMode returning false: every "remote" row in
//     TestARemoteKeyLeavesAScrolledPaneScrolled fails, the terminal-mode ones
//     saying the pane is back at live output and the window-mode one saying
//     the same.
//   - The terminal-mode handler skipping only the exit and not the copy-mode
//     dispatch: the "remote, explicit copy mode" row fails saying nothing
//     reached the guest, because copy mode swallowed the j.

// scrolledPane is a window with history, scrolled back ten lines the way the
// wheel leaves it, in front of a PTY that records what reaches it.
func scrolledPane(t *testing.T, explicit bool) (*terminal.Window, *capturePty) {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	for i := range 60 {
		_, _ = fmt.Fprintf(em, "line %d\r\n", i)
	}
	pty := &capturePty{}
	win := &terminal.Window{ID: "scrolled-0001", Terminal: em, Pty: pty, Width: 82, Height: 26}
	if explicit {
		win.EnterCopyMode()
	} else {
		win.EnterCopyModeImplicit()
	}
	win.CopyMode.ScrollOffset = 10
	win.ScrollbackOffset = 10
	return win, pty
}

func TestARemoteKeyLeavesAScrolledPaneScrolled(t *testing.T) {
	cfg := config.DefaultConfig()
	j := tea.KeyPressMsg{Code: 'j', Text: "j"}
	rows := []struct {
		name         string
		mode         app.Mode
		explicit     bool
		remote       bool
		wantScrolled bool
		wantGuest    string
	}{
		{"remote key, explicit copy mode", app.TerminalMode, true, true, true, "j"},
		{"remote key, window mode", app.WindowManagementMode, false, true, true, ""},
		{"the person's key, window mode", app.WindowManagementMode, false, false, false, ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			win, pty := scrolledPane(t, row.explicit)
			o := &app.OS{
				Settings:             config.Global,
				Mode:                 row.mode,
				FocusedWindow:        0,
				Windows:              []*terminal.Window{win},
				KeybindRegistry:      config.NewKeybindRegistry(cfg),
				ProcessingRemoteKeys: row.remote,
			}
			switch row.mode {
			case app.TerminalMode:
				HandleTerminalModeKey(j, o)
			default:
				HandleWindowManagementModeKey(j, o)
			}
			scrolled := win.InCopyMode() && win.ScrollbackOffset == 10
			if scrolled != row.wantScrolled {
				t.Errorf("scrolled=%t after the key, want %t (copy mode %t, offset %d)",
					scrolled, row.wantScrolled, win.InCopyMode(), win.ScrollbackOffset)
			}
			if row.explicit && !win.CopyModeVisible() {
				t.Errorf("the person's explicit copy mode was ended or demoted")
			}
			if got := string(pty.got); got != row.wantGuest {
				t.Errorf("the guest received %q, want %q", got, row.wantGuest)
			}
		})
	}
}
