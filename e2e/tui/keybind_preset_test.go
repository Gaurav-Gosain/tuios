package tuie2e

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTmuxPresetDrivesWorkspaces is the tmux preset on a real daemon: with
// keybindings.preset = "tmux", ctrl+b c opens a workspace, n and p step
// through them, a digit goes to one, and l goes back to the last. A p typed
// right after ctrl+b p reaches the shell instead of switching again, because
// the preset does not repeat p. ctrl+b ctrl+t m opens the minimize menu the
// preset moved m out of.
//
// Negative control: the same config on a build without presets. ctrl+b c
// makes a window on workspace 1, and the first wait for workspace 2 fails.
func TestTmuxPresetDrivesWorkspaces(t *testing.T) {
	term, base := panesSession(t, "tmuxkeys", "\n[keybindings]\npreset = \"tmux\"\n")
	waitForSettledGeometryIn(t, base, "tmuxkeys", 1)

	press := func(keys ...any) {
		t.Helper()
		if err := term.SendKeys(append([]any{tuitest.Ctrl('b')}, keys...)...); err != nil {
			t.Fatalf("press ctrl+b %v: %v", keys, err)
		}
	}
	// p and n step through workspaces that hold a pane, so each new
	// workspace's pane is waited for before the next key.
	paneOn := func(ws int) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for time.Now().Before(deadline) {
			for _, p := range listPanes(t, base, "tmuxkeys") {
				if p.Workspace == ws {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("no pane opened on workspace %d", ws)
	}
	press("c")
	waitWorkspace(t, base, "tmuxkeys", 2)
	paneOn(2)
	press("c")
	waitWorkspace(t, base, "tmuxkeys", 3)
	paneOn(3)
	press("p")
	waitWorkspace(t, base, "tmuxkeys", 2)
	press("n")
	waitWorkspace(t, base, "tmuxkeys", 3)
	press("1")
	waitWorkspace(t, base, "tmuxkeys", 1)
	press("l")
	waitWorkspace(t, base, "tmuxkeys", 3)

	// ctrl+b p then p at once: one switch, and the second p is typed.
	enterTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "p", "p"); err != nil {
		t.Fatalf("press ctrl+b p p: %v", err)
	}
	waitWorkspace(t, base, "tmuxkeys", 2)
	time.Sleep(700 * time.Millisecond)
	if info := sessionInfoOf(t, base, "tmuxkeys"); info.CurrentWorkspace != 2 {
		t.Errorf("ctrl+b p p switched twice: on workspace %d, want 2", info.CurrentWorkspace)
	}

	press(tuitest.Ctrl('t'), "m")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Minimize")
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+b ctrl+t m did not open the minimize menu: %v\n%s", err, term.Snapshot())
	}
}

// TestScreenPresetLeaderTwiceAndSend: under the screen preset ctrl+a c opens
// a workspace, ctrl+a ctrl+a goes back to the last one, and ctrl+a a sends a
// literal ctrl+a to the pane, which cat -v prints as ^A.
//
// Negative control: with leader_twice = "send" in the config, ctrl+a ctrl+a
// sends ^A instead and the session stays on workspace 2.
func TestScreenPresetLeaderTwiceAndSend(t *testing.T) {
	term, base := panesSession(t, "screenkeys", "\n[keybindings]\npreset = \"screen\"\n")
	waitForSettledGeometryIn(t, base, "screenkeys", 1)
	if err := term.SendKeys(tuitest.Ctrl('a'), "c"); err != nil {
		t.Fatalf("press ctrl+a c: %v", err)
	}
	waitWorkspace(t, base, "screenkeys", 2)
	if err := term.SendKeys(tuitest.Ctrl('a'), tuitest.Ctrl('a')); err != nil {
		t.Fatalf("press ctrl+a ctrl+a: %v", err)
	}
	waitWorkspace(t, base, "screenkeys", 1)

	enterTerminalMode(t, term)
	if err := term.SendKeys("cat -v", tuitest.Enter); err != nil {
		t.Fatalf("start cat -v: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('a'), "a", tuitest.Enter); err != nil {
		t.Fatalf("press ctrl+a a: %v", err)
	}
	if err := term.WaitForText("^A", uiTimeout); err != nil {
		t.Fatalf("ctrl+a a did not reach the pane as ^A: %v\n%s", err, term.Snapshot())
	}
}

// TestZellijPresetOpensModes: under the zellij preset ctrl+g t opens the
// workspace menu (a zellij tab is a workspace) and ctrl+g p the window menu
// (a zellij pane is a window), and the zellij keys in them work: p r splits
// right, t n opens a workspace, t h goes to the one before. keybinds list
// names those keys as they are pressed.
//
// Negative control: without the preset's [window_prefix], p r renames the
// window (the tuios key) and the wait for two windows fails. With
// Bindings() spelling chords from Scopes(leader), the list says ctrl+g t r
// for the split and the list check fails.
func TestZellijPresetOpensModes(t *testing.T) {
	term, base := panesSession(t, "zellijkeys", "\n[keybindings]\npreset = \"zellij\"\n")
	waitForSettledGeometryIn(t, base, "zellijkeys", 1)
	for _, tc := range []struct{ key, title string }{{"t", "Switch to workspace"}, {"p", "Toggle tiling mode"}} {
		if err := term.SendKeys(tuitest.Ctrl('g'), tc.key); err != nil {
			t.Fatalf("press ctrl+g %s: %v", tc.key, err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), tc.title)
		}, uiTimeout); err != nil {
			t.Fatalf("ctrl+g %s did not open the menu with %q: %v\n%s", tc.key, tc.title, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Esc); err != nil {
			t.Fatalf("press esc: %v", err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Pane mode: p r splits right. Tab mode: t n opens a workspace and t h
	// goes back to the one before it.
	if err := term.SendKeys(tuitest.Ctrl('g'), "p", "r"); err != nil {
		t.Fatalf("press ctrl+g p r: %v", err)
	}
	waitForSettledGeometryIn(t, base, "zellijkeys", 2)
	if err := term.SendKeys(tuitest.Ctrl('g'), "t", "n"); err != nil {
		t.Fatalf("press ctrl+g t n: %v", err)
	}
	waitWorkspace(t, base, "zellijkeys", 2)
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if slices.ContainsFunc(slices.Collect(maps.Values(listPanes(t, base, "zellijkeys"))), func(p paneRow) bool { return p.Workspace == 2 }) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.SendKeys(tuitest.Ctrl('g'), "t", "h"); err != nil {
		t.Fatalf("press ctrl+g t h: %v", err)
	}
	waitWorkspace(t, base, "zellijkeys", 1)

	// keybinds list spells each menu key with the key that opens the menu.
	out, err := tuiosCLI(t, base, "keybinds", "list", "--json")
	if err != nil {
		t.Fatalf("keybinds list --json: %v\n%s", err, out)
	}
	type listRow struct {
		Action string   `json:"action"`
		Keys   []string `json:"keys"`
	}
	var rows []listRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("keybinds list --json is not JSON: %v\n%s", err, out)
	}
	for action, want := range map[string]string{"split_vertical": "ctrl+g p r", "new_workspace": "ctrl+g t n"} {
		if !slices.ContainsFunc(rows, func(r listRow) bool {
			return r.Action == action && slices.Contains(r.Keys, want)
		}) {
			t.Errorf("ASSERTION: keybinds list does not show %s on %s", action, want)
		}
	}
}

// TestWelcomeSuggestsThePresetAndWaits: a tmux.conf in the home folder that
// sets the prefix to C-a makes the welcome screen offer "use tmux keys with
// ctrl+a". The offer changes nothing by itself: config.toml has no preset
// until the person opens the picker and presses Enter. Then it holds the
// tmux preset and the sniffed leader, and the screen says so.
//
// Negative control: a build without the picker shows no offer, and the
// first wait fails.
func TestWelcomeSuggestsThePresetAndWaits(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "HOME")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g prefix C-a\nbind | split-window -h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	if err := term.WaitForText("use tmux keys with ctrl+a", bootTimeout); err != nil {
		t.Fatalf("the welcome screen did not offer the tmux keys: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(time.Second)
	if data, _ := os.ReadFile(configPathIn(base)); strings.Contains(string(data), "preset") {
		t.Fatalf("the offer wrote a preset before anyone chose it:\n%s", data)
	}

	if err := term.SendKeys("U"); err != nil {
		t.Fatalf("press U: %v", err)
	}
	if err := term.WaitForText("Keybind preset", uiTimeout); err != nil {
		t.Fatalf("U did not open the picker: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("press enter: %v", err)
	}
	if err := term.WaitForText("The tmux keys are in use", uiTimeout); err != nil {
		t.Fatalf("the picker did not say the keys changed: %v\n%s", err, term.Snapshot())
	}
	data, err := os.ReadFile(configPathIn(base))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `preset = "tmux"`) || !strings.Contains(string(data), `leader_key = "ctrl+a"`) {
		t.Errorf("config.toml after Enter does not hold the tmux preset with ctrl+a:\n%s", data)
	}
}
