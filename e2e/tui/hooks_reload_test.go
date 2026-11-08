package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// listHooksHas reports whether tuios list-hooks --json names command.
func listHooksHas(t *testing.T, base, command string) (bool, string) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-hooks", "--json")
	if err != nil {
		return false, out
	}
	return strings.Contains(out, command), out
}

// TestConfigApplyReloadsHooks is issue 1622. The daemon read [hooks] once,
// at start, so a changed hook needed tuios restart-server. tuios config
// apply, which only the person can run, now loads the table. A file change
// alone does not arm a new hook, because a process in a pane can write the
// file and a hook runs a command as the daemon's child. A file change that
// only removes a hook applies at once.
func TestConfigApplyReloadsHooks(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "")
	if out, err := tuiosCLI(t, base, "new", "hk", "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	mark := filepath.Join(base, "hooked.log")
	hook := "echo hooked-$TUIOS_WINDOW_NAME >> " + mark
	saveConfigLikeAnEditor(t, base, "[hooks]\nafter-new-window = \""+hook+"\"\n")

	// The file change alone arms nothing.
	time.Sleep(2 * time.Second)
	if has, out := listHooksHas(t, base, "hooked-"); has {
		t.Fatalf("ASSERTION: a file change armed a new hook with no config apply\n%s", out)
	}

	out, err := tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("config apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "after-new-window") {
		t.Errorf("ASSERTION: config apply does not say the hook changed:\n%s", out)
	}
	if has, out := listHooksHas(t, base, "hooked-"); !has {
		t.Fatalf("ASSERTION: list-hooks does not show the hook after config apply\n%s", out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", "hk", "first"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(mark)
		if strings.Contains(string(data), "hooked-first") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the hook did not run after config apply (log %q)", data)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// A file change that removes the hook applies without config apply.
	saveConfigLikeAnEditor(t, base, "")
	deadline = time.Now().Add(uiTimeout)
	for {
		has, out := listHooksHas(t, base, "hooked-")
		if !has {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: a file change that removes the hook did not apply\n%s", out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
