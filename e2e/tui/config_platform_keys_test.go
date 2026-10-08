package tuie2e

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

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
// the action whose only key it was gets its default key back, and the TUI says
// that the config has a problem.
//
// Negative controls:
//   - Make LoadUserConfig return an error again when ValidateConfig has
//     errors. The TUI says "1 config problem", the load failure, and this
//     fails at the "2 config problems" wait. On main at d2f6277b no problem is
//     shown at all, and with that wait skipped it fails at the prefix menu.
//   - Leave the action empty in DropUnreadableKeys instead of giving it its
//     default back. Alt+2 then does nothing, and this fails at the workspace
//     wait.
func TestUnreadableKeyKeepsTheRestOfTheFile(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
switch_workspace_2 = ['ctrl+nope']

[keybindings.terminal_mode]
terminal_exit_mode = ['hyper+esc']
`)
	killDaemon(t, base)

	const session = "e2e-bad-keys"
	term := startIn(t, base, startOpts{args: []string{"new", session}})
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

	// switch_workspace_2 had only the bad key, so it is back on its default.
	if err := term.SendKeys(tuitest.Alt("2")); err != nil {
		t.Fatalf("send Alt+2: %v", err)
	}
	waitWorkspace(t, base, session, 2)

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

// TestDoctorMatchesTheAppWithBadKeys is the review repro of #559. config.toml
// sets an unreadable leader and gives alt+1 to switch_workspace_2, and a
// config.d file gives switch_workspace_1 an unreadable key. The running app
// uses the default leader ctrl+b, and alt+1 keeps running switch_workspace_2:
// the fallback default of switch_workspace_1 yields to it. keybinds doctor
// and explain have to say the same, and the log viewer has to name the file
// of each bad key.
//
// Negative controls:
//   - keybinds doctor builds its report from the file as written again. The
//     doctor then reports the leader as hyper+a, and this fails there.
//   - DropUnreadableKeys gives switch_workspace_1 its default alt+1 without
//     the yield. explain then lists switch_workspace_1 for alt+1, and this
//     fails there. With the doctor checks skipped, it fails at the workspace
//     wait, because Alt+1 runs switch_workspace_1 in the app too.
//   - DropUnreadableKeys names config.toml for every key. The doctor then
//     names config.toml for hyper+1, and this fails there. With the doctor
//     checks skipped, it fails at the log viewer.
func TestDoctorMatchesTheAppWithBadKeys(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `[keybindings]
leader_key = 'hyper+a'

[keybindings.workspaces]
switch_workspace_2 = ['alt+1']
`)
	writeConfigPart(t, base, "config.d/keys.toml", `[keybindings.workspaces]
switch_workspace_1 = ['hyper+1']
`)
	killDaemon(t, base)

	out, err := tuiosCLI(t, base, "keybinds", "doctor", "--json")
	if err != nil {
		t.Fatalf("keybinds doctor: %v\n%s", err, out)
	}
	var rep struct {
		Leader      string `json:"leader"`
		KeyProblems []struct {
			Action  string `json:"action"`
			Key     string `json:"key"`
			File    string `json:"file"`
			Outcome string `json:"outcome"`
		} `json:"key_problems"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("keybinds doctor json: %v\n%s", err, out)
	}
	if rep.Leader != "ctrl+b" {
		t.Fatalf("the doctor says the leader is %q, but the app uses ctrl+b:\n%s", rep.Leader, out)
	}
	files := map[string]string{}
	for _, p := range rep.KeyProblems {
		files[p.Key] = p.File
	}
	if files["hyper+a"] != "config.toml" || files["hyper+1"] != "config.d/keys.toml" {
		t.Fatalf("the doctor does not name the file of each bad key, got %v:\n%s", files, out)
	}

	out, err = tuiosCLI(t, base, "keybinds", "explain", "alt+1", "--json")
	if err != nil {
		t.Fatalf("keybinds explain: %v\n%s", err, out)
	}
	var fate struct {
		Acts []struct {
			Scope    string `json:"scope"`
			Action   string `json:"action"`
			Shadowed bool   `json:"shadowed"`
		} `json:"acts"`
	}
	if err := json.Unmarshal([]byte(out), &fate); err != nil {
		t.Fatalf("keybinds explain json: %v\n%s", err, out)
	}
	var window []string
	for _, a := range fate.Acts {
		if a.Scope == "window" && !a.Shadowed {
			window = append(window, a.Action)
		}
	}
	if len(window) != 1 || window[0] != "switch_workspace_2" {
		t.Fatalf("explain says alt+1 runs %v in window mode, want switch_workspace_2:\n%s", window, out)
	}

	const session = "e2e-doctor-match"
	term := startIn(t, base, startOpts{args: []string{"new", session}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Alt("1")); err != nil {
		t.Fatalf("send Alt+1: %v", err)
	}
	waitWorkspace(t, base, session, 2)

	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatalf("send Ctrl+B: %v", err)
	}
	if err := term.WaitForText("Toggle tiling", uiTimeout); err != nil {
		t.Fatalf("Ctrl+B did not start the prefix chord, so the app does not use the default leader: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("D", "l"); err != nil {
		t.Fatalf("open the log viewer: %v", err)
	}
	if err := term.WaitForText("copy errors", uiTimeout); err != nil {
		t.Fatalf("the log viewer did not open: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "doctor-match-log-viewer")
	text := term.Screen().Text()
	if !strings.Contains(text, "config.d/keys.toml: [workspaces] hyper+1") {
		t.Fatalf("the log viewer does not name config.d/keys.toml for hyper+1:\n%s", term.Snapshot())
	}
	if !strings.Contains(text, "config.toml: [keybindings] leader_key") {
		t.Fatalf("the log viewer does not name config.toml for the leader:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the log viewer: %v", err)
	}
}

// altF7 and altF8 are Alt+F7 and Alt+F8 as a terminal sends them. No default
// binds either, so a press of one shows that the config bound it.
const (
	altF7 = "\x1b[18;3~"
	altF8 = "\x1b[19;3~"
)

// TestReloadWithABadKeyAppliesTheRest saves a bad key into a running config,
// then takes it out again. The reload with the bad key applies the rest of the
// file and names the key. The reload after it, with the key gone, applies too.
//
// Negative control: make the reload reject a config with an unreadable key
// again (validateLayered returns an error when ValidateConfig has errors).
// The TUI then says "Config not reloaded", and this fails at the wait for the
// warning that names ctrl+nope.
func TestReloadWithABadKeyAppliesTheRest(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings]\nleader_key = 'ctrl+s'\n")
	killDaemon(t, base)

	const session = "e2e-reload-bad-key"
	term := startIn(t, base, startOpts{args: []string{"new", session}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the first frame never settled: %v\n%s", err, term.Snapshot())
	}

	saveConfigLikeAnEditor(t, base, `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
switch_workspace_2 = ['alt+f7']
switch_workspace_4 = ['ctrl+nope']
`)
	if err := term.WaitForText("ctrl+nope", configWatchTimeout); err != nil {
		t.Fatalf("the reload with a bad key did not name the key: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "reload-bad-key-warning")
	if err := term.SendKeys(altF7); err != nil {
		t.Fatalf("send Alt+F7: %v", err)
	}
	waitWorkspace(t, base, session, 2)

	saveConfigLikeAnEditor(t, base, `[keybindings]
leader_key = 'ctrl+s'

[keybindings.workspaces]
switch_workspace_1 = ['alt+f8']
`)
	// The workspace keys of the second save are the only sign that it
	// applied. Alt+F8 is bound by nothing else.
	deadline := time.Now().Add(configWatchTimeout)
	for {
		if err := term.SendKeys(altF8); err != nil {
			t.Fatalf("send Alt+F8: %v", err)
		}
		if currentWorkspace(t, base, session) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the reload after the bad key was taken out never applied Alt+F8:\n%s", term.Snapshot())
		}
		time.Sleep(300 * time.Millisecond)
	}
	alive(t, term, "after a reload with a bad key")
}

// TestConfigApplyListsTheKeysItCannotRead runs tuios config apply on a config
// with a bad key in a config.d file. The daemon applies the rest, and the
// command lists the key with its file and what tuios does instead.
//
// Negative control: cut the "dropped_keys" field from the apply-config reply.
// The command then lists no key, and this fails at the output check.
func TestConfigApplyListsTheKeysItCannotRead(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings]\nleader_key = 'ctrl+s'\n")
	killDaemon(t, base)

	term := startIn(t, base, startOpts{args: []string{"new", "e2e-apply-bad-key"}})
	if err := term.WaitForText(welcomeHint, bootTimeout); err != nil {
		t.Fatalf("tuios never booted: %v\n%s", err, term.Snapshot())
	}

	writeConfigPart(t, base, "config.d/keys.toml", "[keybindings.workspaces]\nswitch_workspace_3 = ['hyper+3']\n")
	out, err := tuiosCLI(t, base, "config", "apply")
	if err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	for _, want := range []string{
		"The daemon applied config.toml.",
		"tuios cannot read 1 key:",
		"config.d/keys.toml: [workspaces] hyper+3: invalid modifier: hyper.",
		"switch_workspace_3 uses its default key, alt+3.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("tuios config apply does not say %q:\n%s", want, out)
		}
	}
	alive(t, term, "after config apply with a bad key")
}
