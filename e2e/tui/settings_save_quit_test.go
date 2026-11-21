package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestASettingSurvivesAQuitRightAfterIt: set-config applies the value on the
// client and saves it to config.toml in a command after that. A client that
// quit while the command was out lost the write, and the next start read the
// old value. The client now writes what is still waiting before it exits.
//
// The knob settings_save_hold_ms holds each write on its way to the file for
// three seconds, so the client always quits while the write is out. The
// write on the way out does not wait for the knob.
//
// How this could pass wrongly, written down first:
//   - The set could fail, so nothing is held. set-config must exit 0, and it
//     answers only after the client applied the value.
//   - The held write could land after all. The client is gone before the
//     hold ends, and the test reads the file before the hold would end.
//
// Negative control: without the write on the way out, config.toml has no
// border_style.
func TestASettingSurvivesAQuitRightAfterIt(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create home: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, env: []string{knob("settings_save_hold_ms=3000")}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "set-config", "appearance.border_style", "double"); err != nil {
		t.Fatalf("set-config appearance.border_style double: %v\n%s", err, out)
	}
	if err := term.Close(); err != nil {
		t.Logf("close: %v", err)
	}

	file := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "config.toml")
	data, _ := os.ReadFile(file)
	if !strings.Contains(string(data), `border_style = "double"`) {
		t.Fatalf("ASSERTION: config.toml has no border_style = \"double\" after the client quit: the write was lost\n%s", data)
	}
}
