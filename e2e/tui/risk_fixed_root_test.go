package tuie2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestRiskRootIsTheStartFolder starts agent panes in a plain folder. Each pane
// then moves: it does cd to / and prints an OSC 7 that names /, or does the
// same for the folder above its start folder, or does cd into a git worktree
// that tuios did not make, which the daemon then detects as the session's
// worktree. Two more panes start in the home folder and stay there. Each pane
// raises an approval for a write outside the folder it
// started in: an Edit, which the hook only reports, or a Bash cp, which the
// hook holds. Every item is marked outside the worktree. The risk rules
// measure from the folder the pane started in, which nothing the pane does
// moves.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the whole fix removed, no
// item is marked. With only the session side removed (paneRoot and the
// lifecycle root back on the working folder and the detected worktree), the
// panes at / are still marked, since the rules read / as no root. The held
// cp from the folder above and both writes in the detected worktree are not.
// With only the home check removed from paneRoot, the panes in the home
// folder are not marked.
func TestRiskRootIsTheStartFolder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// The folders are at a short path with a short name: t.TempDir() carries
	// the test's name and a random suffix, a run the line redacts to "***",
	// and a line that hides part of its path cannot be read as outside.
	up, err := os.MkdirTemp("", "r")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(up) })
	if resolved, err := filepath.EvalSymlinks(up); err == nil {
		up = resolved
	}
	start := filepath.Join(up, "start")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	// Outside the start folder, inside the folder above it, and not under
	// any system folder, so only the outside-the-worktree rule can mark it.
	outsidePath := filepath.Join(up, "elsewhere", "notes.txt")
	// A linked worktree of a repository, at a short path, that tuios did not
	// make: the daemon detects it from the pane's folder.
	repo := testutil.GitRepo(t)
	linked := filepath.Join(up, "wt")
	testutil.Git(t, repo, "worktree", "add", "-q", "-b", "elsewhere", linked)
	inLinked := filepath.Join(linked, "notes.txt")

	// A pane that starts in the home folder has no root at all: a write
	// anywhere in the account is outside. The file is never written.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	inHome := filepath.Join(home, ".tuios-e2e-risk-probe")

	type pane struct {
		session, window, startIn, moveTo, tool, target string
		detect                                         bool
	}
	cases := []*pane{
		{session: "edit-root", moveTo: "/", tool: "Edit", target: outsidePath},
		{session: "bash-root", moveTo: "/", tool: "Bash", target: outsidePath},
		{session: "edit-up", moveTo: up, tool: "Edit", target: outsidePath},
		{session: "bash-up", moveTo: up, tool: "Bash", target: outsidePath},
		{session: "edit-wt", moveTo: linked, tool: "Edit", target: inLinked, detect: true},
		{session: "bash-wt", moveTo: linked, tool: "Bash", target: inLinked, detect: true},
		{session: "edit-home", startIn: home, moveTo: home, tool: "Edit", target: inHome},
		{session: "bash-home", startIn: home, moveTo: home, tool: "Bash", target: inHome},
	}
	for _, c := range cases {
		if c.startIn == "" {
			c.startIn = start
		}
		if out, err := tuiosCLIIn(t, base, c.startIn, "new", c.session, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", c.session, err, out)
		}
		c.window = onlyWindow(t, base, c.session)
		typeInPane(t, base, c.session, c.window, "cd "+c.moveTo+" && printf '\\033]7;file://localhost"+c.moveTo+"\\007'")
	}
	// Precondition: the daemon reads the new folder as each pane's folder.
	for _, c := range cases {
		waitWindowCwd(t, base, c.session, c.window, c.moveTo)
	}
	// Precondition: the daemon detects the linked worktree as the session's
	// worktree. It looks on a save, which a change of state brings.
	for _, c := range cases {
		if c.detect {
			waitDetectedWorktree(t, base, c.session, linked)
		}
	}

	for _, c := range cases {
		if c.tool == "Edit" {
			// The hook reports the pane blocked and exits.
			fireEditHook(t, base, c.session, c.target)
			continue
		}
		// The hook holds the pane's prompt until it is answered.
		startHeldBashHook(t, base, c.session, "cp notes.txt "+c.target)
	}

	for _, c := range cases {
		var it riskItem
		if c.tool == "Edit" {
			it = waitRiskItem(t, base, c.session)
		} else {
			it = waitHeldRiskItem(t, base, c.session)
		}
		t.Logf("%s (at %s): %q risk %v", c.session, c.moveTo, it.Summary, it.Risk)
		if !slices.Contains(it.Risk, "outside the worktree") {
			t.Errorf("ASSERTION: %s: a write to %s from a pane at %s is marked %v, want outside the worktree", c.session, c.target, c.moveTo, it.Risk)
		}
	}
	saveRiskItems(t, base, "risk-root-start-folder-items.json")
}

// startHeldBashHook runs Claude Code's PermissionRequest hook for a Bash call
// in window 0 of session, in the background. The hook holds the prompt; the
// test's cleanup ends it.
func startHeldBashHook(t *testing.T, base, session, command string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "e2e-" + session,
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	hook := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", "0")
	hook.Dir = workDirIn(t, base)
	hook.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		hook.Env = append(hook.Env, key+"="+xdgDir(base, key))
	}
	hook.Stdin = bytes.NewReader(payload)
	if err := hook.Start(); err != nil {
		t.Fatalf("start the hook for %s: %v", session, err)
	}
	finished := make(chan struct{})
	go func() {
		_ = hook.Wait()
		close(finished)
	}()
	t.Cleanup(func() {
		_ = hook.Process.Kill()
		<-finished
	})
}

// onlyWindow is the id of a session's one window.
func onlyWindow(t *testing.T, base, session string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows gave no single window: %v\n%s", err, out)
	}
	return listing.Windows[0].WindowID
}

// waitWindowCwd waits until list-windows reports want as the window's folder.
func waitWindowCwd(t *testing.T, base, session, window, want string) {
	t.Helper()
	var out string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "list-windows", "-s", session, "--json")
		var listing struct {
			Windows []struct {
				WindowID string `json:"window_id"`
				Cwd      string `json:"cwd"`
			} `json:"windows"`
		}
		if json.Unmarshal([]byte(out), &listing) == nil {
			for _, w := range listing.Windows {
				if w.WindowID == window && w.Cwd == want {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("precondition: %s never reported %s as its folder:\n%s", session, want, out)
}

// waitHeldRiskItem waits until session has an approval item the daemon holds
// (it carries a request id), so its risk is the hold's.
func waitHeldRiskItem(t *testing.T, base, session string) riskItem {
	t.Helper()
	var out string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "list-attention", "--json")
		var list struct {
			Items []struct {
				riskItem
				RequestID string `json:"request_id"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			for _, it := range list.Items {
				if it.Session == session && it.Kind == "approval" && it.RequestID != "" && strings.Contains(it.Summary, "cp ") {
					return it.riskItem
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no held approval for %s:\n%s", session, out)
	return riskItem{}
}

// waitDetectedWorktree waits until worktree ls reports path as the session's
// worktree, not one tuios made. A save is when the daemon looks, and only a
// change of state brings one soon, so each try marks the pane idle.
func waitDetectedWorktree(t *testing.T, base, session, path string) {
	t.Helper()
	var out string
	deadline := time.Now().Add(3 * uiTimeout)
	for time.Now().Before(deadline) {
		_, _ = tuiosCLI(t, base, "set-agent-state", "-s", session, "idle")
		out, _ = tuiosCLI(t, base, "worktree", "ls", "--json")
		var rows []struct {
			Session string `json:"session"`
			Path    string `json:"path"`
			Managed bool   `json:"managed"`
		}
		if json.Unmarshal([]byte(out), &rows) == nil {
			for _, r := range rows {
				if r.Session == session && r.Path == path && !r.Managed {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("precondition: %s never detected %s as its worktree:\n%s", session, path, out)
}
