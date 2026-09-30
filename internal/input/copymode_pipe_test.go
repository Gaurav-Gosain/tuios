package input

import (
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Copy-pipe keys through the real copy-mode entry point: the command gets
// the selection, and copy mode stays or ends by the entry's cancel.

func pipeCopyOS(t *testing.T, multi bool) (*app.OS, []*terminal.Window) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the copy command runs with sh -c")
	}
	o, w := multiCopyOS(t)
	if !multi {
		o.MultifocusSet = nil
	}
	o.UserConfig = config.DefaultConfig()
	o.UserConfig.Keybindings.CopyPipe = []config.CopyPipeBinding{
		{Key: "p", Command: "tr '\\n' ' '", Description: "flatten"},
		{Key: "P", Command: "tr '\\n' ' '", Cancel: true},
	}
	o.EnterCopyModeFocused()
	return o, w
}

// pipeDone runs a key's command and returns how the pipe ended.
func pipeDone(t *testing.T, o *app.OS, key string) app.CopyPipeDoneMsg {
	t.Helper()
	cmd := mcPress(o, key)
	if cmd == nil {
		t.Fatalf("%s returned no command", key)
	}
	msg, ok := cmd().(app.CopyPipeDoneMsg)
	if !ok {
		t.Fatalf("%s did not pipe: %T", key, msg)
	}
	return msg
}

func TestCopyPipeKeyStaysInCopyMode(t *testing.T) {
	o, w := pipeCopyOS(t, false)
	typeSearch(o, "boot")
	mcPress(o, "V", "j")
	x, y := w[0].CopyMode.CursorX, w[0].CopyMode.CursorY
	msg := pipeDone(t, o, "p")
	if msg.Output != "boot ok LLDP neighbor: swp1 rack-sw-01" || msg.Label != "flatten" {
		t.Fatalf("pipe = %+v", msg)
	}
	cm := w[0].CopyMode
	if !w[0].InCopyMode() || w[0].HasSelection() || cm.CursorX != x || cm.CursorY != y {
		t.Fatalf("after p: copy mode %v, selection %v, cursor %d,%d want %d,%d",
			w[0].InCopyMode(), w[0].HasSelection(), cm.CursorX, cm.CursorY, x, y)
	}
}

func TestCopyPipeAndCancelLeavesCopyMode(t *testing.T) {
	o, w := pipeCopyOS(t, false)
	typeSearch(o, "boot")
	mcPress(o, "V", "j")
	if msg := pipeDone(t, o, "P"); msg.Output != "boot ok LLDP neighbor: swp1 rack-sw-01" {
		t.Fatalf("pipe = %+v", msg)
	}
	if w[0].InCopyMode() {
		t.Fatal("P did not leave copy mode")
	}
}

// With no selection, the key says what to do and runs nothing.
func TestCopyPipeKeyNeedsASelection(t *testing.T) {
	o, w := pipeCopyOS(t, false)
	if cmd := mcPress(o, "p"); cmd != nil {
		t.Fatal("p with no selection returned a command")
	}
	if !w[0].InCopyMode() {
		t.Fatal("p with no selection left copy mode")
	}
	if n := len(o.Notifications); n == 0 || o.Notifications[n-1].Message != "No text is selected. Press v or V to select, then press p." {
		t.Fatalf("notifications = %+v", o.Notifications)
	}
}

// With copy_command set, the plain y pipes too, and stays in copy mode.
func TestPlainYankUsesTheCopyCommand(t *testing.T) {
	o, w := pipeCopyOS(t, false)
	o.Settings.CopyCommand = "tr a-z A-Z"
	typeSearch(o, "boot")
	mcPress(o, "V")
	if msg := pipeDone(t, o, "y"); msg.Output != "BOOT OK" {
		t.Fatalf("pipe = %+v", msg)
	}
	if !w[0].InCopyMode() || w[0].HasSelection() {
		t.Fatal("y did not keep copy mode with the selection cleared")
	}
}

// In multi copy mode the command gets every selection once, in the format y
// copies, and cancel ends the mode in every pane.
func TestMultiCopyPipeKey(t *testing.T) {
	o, w := pipeCopyOS(t, true)
	typeSearch(o, "lldp")
	mcPress(o, "V")
	msg := pipeDone(t, o, "P")
	want := "LLDP neighbor: swp1 rack-sw-01 LLDP neighbor: swp2 rack-sw-01 "
	if msg.Output != want {
		t.Fatalf("pipe = %q, want %q", msg.Output, want)
	}
	if o.MultiCopy != nil {
		t.Fatal("P did not end multi copy mode")
	}
	for _, p := range w {
		if p.InCopyMode() {
			t.Errorf("pane %s is still in copy mode", p.ID)
		}
	}
}
