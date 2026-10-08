package tuie2e

import (
	"runtime"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestMacKeysInConfigKeepTheRestOfTheFile is issue #556. A config.toml copied
// from a Mac binds keys as opt+1. On Linux tuios cannot read an opt+ key, and
// one such key threw the whole file away: tuios ran on the defaults, the
// leader stayed ctrl+b, and the only error went to a stderr the first frame
// wiped. Now the unreadable key is dropped, the rest of the file applies, and
// the TUI says that the config has a problem.
//
// It runs as a daemon session, as the reporter's file asks for
// (startup.daemon = true), so the client's load is the one under test.
//
// Negative control: make LoadUserConfig return an error again when
// ValidateConfig has errors, and this fails at the "2 config problems" wait.
// The TUI says "1 config problem" instead: tuios could not load the file and
// runs on the defaults. On main at d2f6277b, before this fix, no problem is
// shown at all, and with that wait skipped the test fails at the prefix menu
// wait, because Ctrl+S goes to the pane under the default leader.
func TestMacKeysInConfigKeepTheRestOfTheFile(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("opt+ keys are valid on macOS")
	}
	base := t.TempDir()
	writeConfig(t, base, `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
switch_workspace_1 = ['opt+1']

[keybindings.terminal_mode]
terminal_exit_mode = ['opt+esc']
`)
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-mac-keys"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted with opt+ keys in config.toml: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("2 config problems", uiTimeout); err != nil {
		t.Fatalf("the TUI did not say that two keys in config.toml cannot be read: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mac-keys-config-problems")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('s')); err != nil {
		t.Fatalf("send Ctrl+S: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Ctrl+S did not start the prefix chord, so leader_key = 'ctrl+s' was ignored: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mac-keys-leader-ctrl-s")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the prefix menu: %v", err)
	}
}
