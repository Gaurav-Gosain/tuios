package tuie2e

import (
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

// riskItem is one Inbox item as list-attention --json prints it.
type riskItem struct {
	Session string   `json:"session"`
	Kind    string   `json:"kind"`
	Summary string   `json:"summary"`
	Risk    []string `json:"risk"`
}

// fireEditHook runs Claude Code's PermissionRequest hook for an Edit of path
// in window 0 of session. An Edit is never held (its body does not fit the
// line), so the hook reports needs_input and returns.
func fireEditHook(t *testing.T, base, session, path string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "e2e-" + session,
		"tool_name":       "Edit",
		"tool_input":      map[string]string{"file_path": path, "old_string": "hi", "new_string": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", "0")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = strings.NewReader(string(payload))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook for %s: %v\n%s", session, err, out)
	}
}

// waitRiskItem polls list-attention until session has an approval item.
func waitRiskItem(t *testing.T, base, session string) riskItem {
	t.Helper()
	var out string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		out, _ = tuiosCLI(t, base, "list-attention", "--json")
		var list struct {
			Items []riskItem `json:"items"`
		}
		if json.Unmarshal([]byte(out), &list) == nil {
			for _, it := range list.Items {
				if it.Session == session && it.Kind == "approval" {
					return it
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no approval item for %s:\n%s", session, out)
	return riskItem{}
}

// shownPrefix is the part of an Inbox line's path that the line shows whole:
// up to the "***" the redaction puts in place of a key-like run, or up to the
// "..." the clip adds.
func shownPrefix(summary string) string {
	p := strings.TrimPrefix(summary, "approve Edit: ")
	if i := strings.Index(p, "***"); i >= 0 {
		p = p[:i]
	}
	return strings.TrimSuffix(p, "...")
}

// TestRiskInWorktree is the Edit of a file inside a worktree tuios made, from
// a pane whose root is that worktree: the worktree session's own pane, and a
// pane of another session that starts in the worktree, as start-agent --cwd
// does. The Inbox line is clipped, and the clip cuts the path before it
// reaches the file. The outside-the-worktree rule read the cut path as a
// whole one and marked the call.
//
// The negative side runs in the same daemon: an Edit outside the worktree,
// short and whole, and long and cut after it has already left the worktree,
// is still marked.
//
// The name is short on purpose: t.TempDir() carries it, and with its random
// suffix a name of 22 characters or more is a run the redaction replaces,
// which TestRiskInWorktreeRedacted covers.
func TestRiskInWorktree(t *testing.T) {
	base, root, panes := riskWorktree(t)
	for _, name := range []string{"out-short", "out-long", "out-key"} {
		if out, err := tuiosCLIIn(t, base, root, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	fireEditHook(t, base, "out-short", "/etc/hosts")
	// Outside, and long enough to be cut after it has left the worktree.
	fireEditHook(t, base, "out-long", "/etc/"+strings.Repeat("x", 120)+".conf")

	// Short, and with a name the line redacts: the line shows no clip, and
	// the stars still hide part of the call.
	fireEditHook(t, base, "out-key", "/etc/"+strings.Repeat("a1", 20)+".conf")

	checkInsideMarks(t, base, root, panes, false)

	short := waitRiskItem(t, base, "out-short")
	if !slices.Equal(short.Risk, []string{"outside the worktree"}) {
		t.Errorf("an Edit of /etc/hosts is marked %v, want only outside the worktree", short.Risk)
	}
	long := waitRiskItem(t, base, "out-long")
	t.Logf("out-long: %q risk %v", long.Summary, long.Risk)
	if !strings.Contains(long.Summary, ": /etc/x") || !strings.HasSuffix(long.Summary, "...") {
		t.Fatalf("precondition: the long outside line %q is not cut after it left the worktree", long.Summary)
	}
	if !slices.Contains(long.Risk, "outside the worktree") || !slices.Contains(long.Risk, "cut short") {
		t.Errorf("a cut Edit outside the worktree is marked %v, want outside the worktree and cut short", long.Risk)
	}
	key := waitRiskItem(t, base, "out-key")
	t.Logf("out-key: %q risk %v", key.Summary, key.Risk)
	if key.Summary != "approve Edit: /etc/***.conf" {
		t.Fatalf("precondition: the redacted line is %q", key.Summary)
	}
	if !slices.Contains(key.Risk, "cut short") {
		t.Errorf("a redacted line is marked %v, want cut short", key.Risk)
	}
	saveRiskItems(t, base, "risk-in-worktree-items.json")
}

// TestRiskInWorktreeRedacted is TestRiskInWorktree with a run of letters and
// digits in the worktree's path, which the line redacts to "***" before the
// clip: here the test's own directory, named after this test. A worktree
// directory named for a branch such as fix/issue-1234-handle-long-names is
// one too. The rule read the path with the stars as a whole one.
func TestRiskInWorktreeRedacted(t *testing.T) {
	base, root, panes := riskWorktree(t)
	checkInsideMarks(t, base, root, panes, true)
	saveRiskItems(t, base, "risk-in-worktree-redacted-items.json")
}

// riskWorktree starts a daemon with approvals on for claude-code, makes a
// worktree on feat/kinder-greeting where tuios puts it by default (under
// XDG_DATA_HOME), opens a session in it, and reports an Edit of
// demo/greet.py there from two panes. It returns the isolation root, the
// worktree and the panes' sessions.
func riskWorktree(t *testing.T) (base, root string, panes []string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	repo := testutil.GitRepo(t)
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "home", "--detach"); err != nil {
		t.Fatalf("create the home session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/kinder-greeting", "--repo", repo, "--detach"); err != nil {
		t.Fatalf("worktree new: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "worktree", "ls", "--json")
	if err != nil {
		t.Fatalf("worktree ls: %v\n%s", err, out)
	}
	var wts []struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &wts); err != nil || len(wts) != 1 {
		t.Fatalf("worktree ls did not list one worktree: %v\n%s", err, out)
	}
	root = wts[0].Path
	if !strings.HasPrefix(root, xdgDir(base, "XDG_DATA_HOME")) {
		t.Fatalf("the worktree %s is not under XDG_DATA_HOME", root)
	}
	if out, err := tuiosCLIIn(t, base, root, "new", "demo", "--detach"); err != nil {
		t.Fatalf("create demo: %v\n%s", err, out)
	}
	panes = []string{wts[0].Session, "demo"}
	for _, session := range panes {
		fireEditHook(t, base, session, filepath.Join(root, "demo", "greet.py"))
	}
	return base, root, panes
}

// checkInsideMarks asserts that no pane's Edit inside root is marked outside
// the worktree, and that each is marked cut short, since its line does not
// show the whole path. redacted says the line must hide part of the path
// behind "***".
func checkInsideMarks(t *testing.T, base, root string, panes []string, redacted bool) {
	t.Helper()
	for _, session := range panes {
		it := waitRiskItem(t, base, session)
		t.Logf("%s: %q risk %v", session, it.Summary, it.Risk)
		// The precondition: the line does not show the whole path, and what it
		// shows stops inside the worktree's own path.
		shown := shownPrefix(it.Summary)
		if shown == strings.TrimPrefix(it.Summary, "approve Edit: ") || !strings.HasPrefix(root, shown) {
			t.Fatalf("precondition: %s's line %q does not stop inside %s", session, it.Summary, root)
		}
		if strings.Contains(it.Summary, "***") != redacted {
			t.Fatalf("precondition: %s's line %q, want redacted %v", session, it.Summary, redacted)
		}
		if slices.Contains(it.Risk, "outside the worktree") {
			t.Errorf("ASSERTION: %s marks an Edit inside its worktree outside the worktree: %v", session, it.Risk)
		}
		if !slices.Contains(it.Risk, "cut short") {
			t.Errorf("%s lost the cut short mark: %v", session, it.Risk)
		}
	}
}

// saveRiskItems keeps every Inbox item as the daemon marked it, the test's
// artifact, when TUIOS_E2E_FRAMES names a directory.
func saveRiskItems(t *testing.T, base, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	out, _ := tuiosCLI(t, base, "list-attention", "--json")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(out), 0o644); err != nil {
		t.Logf("could not save the items: %v", err)
	}
}
