package tuie2e

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// macConfig holds the keybindings of the config.toml attached to issue #556,
// written on a Mac: the leader and all 25 of its opt+ keys.
const macConfig = `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
move_and_follow_1 = ['opt+shift+1']
move_and_follow_2 = ['opt+shift+2']
move_and_follow_3 = ['opt+shift+3']
move_and_follow_4 = ['opt+shift+4']
move_and_follow_5 = ['opt+shift+5']
move_and_follow_6 = ['opt+shift+6']
move_and_follow_7 = ['opt+shift+7']
move_and_follow_8 = ['opt+shift+8']
move_and_follow_9 = ['opt+shift+9']
switch_workspace_1 = ['opt+1']
switch_workspace_2 = ['opt+2']
switch_workspace_3 = ['opt+3']
switch_workspace_4 = ['opt+4']
switch_workspace_5 = ['opt+5']
switch_workspace_6 = ['opt+6']
switch_workspace_7 = ['opt+7']
switch_workspace_8 = ['opt+8']
switch_workspace_9 = ['opt+9']

[keybindings.layout]
preselect_down = ['opt+j']
preselect_left = ['opt+h']
preselect_right = ['opt+l']
preselect_up = ['opt+k']

[keybindings.terminal_mode]
terminal_exit_mode = ['opt+esc']
terminal_next_window = ['opt+tab', 'alt+n']
terminal_prev_window = ['opt+shift+tab', 'alt+p']
`

// TestMacConfigWorksOnLinux is issue #556. The reporter's config.toml from a
// Mac binds 25 keys as opt+. On Linux each one was an error, and one error
// threw the whole file away, the leader too. Now tuios reads opt+ as alt+ on
// every platform: the file loads with no config problem, the doctor notes the
// 25 keys for information, and opt+1 (Alt+1 on Linux) switches to workspace 1.
//
// It runs as a daemon session, as the reporter's file asks for
// (startup.daemon = true), so the client's load is the one under test.
//
// Negative control: make ValidateKey reject opt+ off macOS again. The doctor
// then lists 25 keys tuios cannot read, and this fails at that check. With the
// doctor checks skipped, it fails at the log viewer, which lists the 25 keys
// as config problems. The key presses alone cannot tell: a dropped opt+1
// falls back to the default alt+1, which is the same key on Linux.
func TestMacConfigWorksOnLinux(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("opt+ is the native spelling on macOS")
	}
	base := t.TempDir()
	writeConfig(t, base, macConfig)
	killDaemon(t, base)

	out, err := tuiosCLI(t, base, "keybinds", "doctor", "--json")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	var rep struct {
		Leader      string            `json:"leader"`
		KeyProblems []json.RawMessage `json:"key_problems"`
		OptionKeys  []struct {
			Key    string `json:"key"`
			ReadAs string `json:"read_as"`
		} `json:"option_keys_read_as_alt"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("keybinds doctor json: %v\n%s", err, out)
	}
	if len(rep.KeyProblems) != 0 {
		t.Fatalf("the doctor lists %d keys tuios cannot read in the Mac config, want 0:\n%s", len(rep.KeyProblems), out)
	}
	if len(rep.OptionKeys) != 25 {
		t.Fatalf("the doctor notes %d opt+ keys, want 25:\n%s", len(rep.OptionKeys), out)
	}
	if rep.Leader != "ctrl+s" {
		t.Fatalf("the doctor reads the leader as %q, want ctrl+s", rep.Leader)
	}

	const session = "e2e-mac-config"
	term := startIn(t, base, startOpts{args: []string{"new", session}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted with the Mac config: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	// opt+2 then opt+1, as Alt+2 and Alt+1. The session starts on
	// workspace 1, so the first press is what makes the second one count.
	if err := term.SendKeys(tuitest.Alt("2")); err != nil {
		t.Fatalf("send Alt+2: %v", err)
	}
	waitWorkspace(t, base, session, 2)
	if err := term.SendKeys(tuitest.Alt("1")); err != nil {
		t.Fatalf("send Alt+1: %v", err)
	}
	waitWorkspace(t, base, session, 1)

	if err := term.SendKeys(tuitest.Ctrl('s')); err != nil {
		t.Fatalf("send Ctrl+S: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Ctrl+S did not start the prefix chord, so leader_key = 'ctrl+s' was ignored: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mac-config-leader-ctrl-s")

	// The log viewer holds every config problem tuios found at start. The
	// notification that counts them can be replaced by the next one before
	// a frame shows it, so the log is the place to look.
	if err := term.SendKeys("D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "mac-config-log-viewer")
	if strings.Contains(term.Screen().Text(), "Config:") {
		t.Fatalf("tuios logs a config problem for the Mac config:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the log viewer: %v", err)
	}
}

// TestUnreadableKeyKeepsTheRestOfTheFile is the fallback of issue #556. One
// key tuios cannot read used to throw away the whole file: tuios ran on the
// defaults, the leader stayed ctrl+b, and the only error went to a stderr the
// first frame wiped. Now the key is dropped, the rest of the file applies,
// and the TUI says that the config has a problem.
//
// Negative control: make LoadUserConfig return an error again when
// ValidateConfig has errors, and this fails at the "2 config problems" wait.
// The TUI says "1 config problem" instead: tuios could not load the file and
// runs on the defaults. On main at d2f6277b, before this fix, no problem is
// shown at all, and with that wait skipped the test fails at the prefix menu
// wait, because Ctrl+S goes to the pane under the default leader.
func TestUnreadableKeyKeepsTheRestOfTheFile(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
switch_workspace_1 = ['ctrl+nope']

[keybindings.terminal_mode]
terminal_exit_mode = ['hyper+esc']
`)
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-bad-keys"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted with unreadable keys in config.toml: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("2 config problems", uiTimeout); err != nil {
		t.Fatalf("the TUI did not say that two keys in config.toml cannot be read: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "bad-keys-config-problems")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('s')); err != nil {
		t.Fatalf("send Ctrl+S: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Ctrl+S did not start the prefix chord, so leader_key = 'ctrl+s' was ignored: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "bad-keys-leader-ctrl-s")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the prefix menu: %v", err)
	}
}
