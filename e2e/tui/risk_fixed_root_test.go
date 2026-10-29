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

// TestRiskRootIsTheStartFolder raises approvals from panes that try to make
// a write outside their root look inside, and from panes whose writes are
// inside. The home folder is the one the harness gives the daemon.
//
// Marked outside the worktree, each from a pane started in a plain folder:
//
//   - after cd and an OSC 7 to /, to the folder above, or into a git
//     worktree tuios did not make (which the daemon then detects);
//   - from a pane that started in the home folder and has no agent;
//   - a relative write from a pane at /, from a held call whose harness runs
//     in /, and from a pane whose OSC 7 names its start folder while its
//     process runs in /.
//
// Not marked: a relative write from a pane in its start folder; writes in a
// start folder written through a link, or resolved; and from a pane started
// in the home folder that did cd into a project and started an agent there,
// a write in the project, also after the agent moves on.
//
// Each case raises an Edit, which the hook only reports, or a Bash cp, which
// the hook holds. Negative controls are in NEGATIVE_CONTROLS.md.
func TestRiskRootIsTheStartFolder(t *testing.T) {
	// The folders are at a short path with a short name: t.TempDir() carries
	// the test's name and a random suffix, a run the line redacts to "***",
	// and a line that hides part of its path cannot be read as outside. They
	// are under /tmp, since the temp folder on macOS is long by itself.
	up, err := os.MkdirTemp("/tmp", "r")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(up) })
	if resolved, err := filepath.EvalSymlinks(up); err == nil {
		up = resolved
	}
	// The isolation root is short too. The harness gives the daemon and
	// the panes its HOME (xdgKeys), so that is the home folder here.
	base, err := os.MkdirTemp("/tmp", "b")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	killDaemon(t, base)
	home := xdgDir(base, "HOME")
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[agents.approvals]\nenabled = [\"claude-code\"]\nhold_seconds = 60\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	start := filepath.Join(up, "start")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	// A start folder reached through a link, as /home is a link to
	// /var/home on Fedora Silverblue.
	real := filepath.Join(up, "real")
	link := filepath.Join(up, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
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
	inHome := filepath.Join(home, "notes.txt")

	type pane struct {
		session, window, startIn, moveTo, tool, target string
		// announce is the folder the pane's OSC 7 names, when it is not
		// moveTo: the pane says one thing and its process is in another.
		announce string
		// harnessCwd is the cwd the hook payload carries, "" for none.
		harnessCwd string
		// agent says an agent starts in moveTo, through a hook that names
		// it, before the approval. agentMovesTo is where that agent goes
		// after, with a hook that names the new folder.
		agent        bool
		agentMovesTo string
		detect       bool
		// safe says the write stays inside the root and must not be marked.
		safe bool
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
		// A relative path is read from where the call runs: the pane's own
		// process, or the cwd the harness sends with a held call.
		{session: "edit-rel-root", moveTo: "/", tool: "Edit", target: "etc/notes.txt"},
		{session: "bash-rel-root", moveTo: "/", tool: "Bash", target: "etc/notes.txt"},
		{session: "bash-rel-cwd", moveTo: start, tool: "Bash", target: "notes-copy.txt", harnessCwd: "/"},
		{session: "bash-rel-osc7", moveTo: "/", announce: start, tool: "Bash", target: "notes-copy.txt"},
		{session: "edit-rel-in", moveTo: start, tool: "Edit", target: "notes-copy.txt", safe: true},
		{session: "bash-rel-in", moveTo: start, tool: "Bash", target: "notes-copy.txt", harnessCwd: start, safe: true},
		// A start folder through a link: a write in it is inside, written
		// through the link or resolved.
		{session: "edit-link-real", startIn: link, moveTo: link, tool: "Edit", target: filepath.Join(real, "main.go"), safe: true},
		{session: "edit-link-link", startIn: link, moveTo: link, tool: "Edit", target: filepath.Join(link, "main.go"), safe: true},
		{session: "bash-link-real", startIn: link, moveTo: link, tool: "Bash", target: filepath.Join(real, "main.go"), safe: true},
		// tuios started in the home folder, then cd proj && claude: the
		// agent's start folder is the root.
		{session: "agent-in", startIn: home, moveTo: proj, tool: "Edit", target: filepath.Join(proj, "main.go"), agent: true, safe: true},
		{session: "agent-in-bash", startIn: home, moveTo: proj, tool: "Bash", target: filepath.Join(proj, "main.go"), agent: true, safe: true},
		{session: "agent-out", startIn: home, moveTo: proj, tool: "Edit", target: inHome, agent: true},
		{session: "agent-moved", startIn: home, moveTo: proj, tool: "Edit", target: filepath.Join(proj, "main.go"), agent: true, agentMovesTo: "/", safe: true},
		{session: "agent-moved-out", startIn: home, moveTo: proj, tool: "Edit", target: "/etc/notes.txt", agent: true, agentMovesTo: "/"},
	}
	for _, c := range cases {
		if c.startIn == "" {
			c.startIn = start
		}
		if c.announce == "" {
			c.announce = c.moveTo
		}
		// PWD names the folder as the test wrote it, so a link stays a
		// link in the folder tuios records.
		if out, err := tuiosCLIInDir(t, base, c.startIn, []string{"PWD=" + c.startIn}, "new", c.session, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", c.session, err, out)
		}
		c.window = onlyWindow(t, base, c.session)
		typeInPane(t, base, c.session, c.window, "cd "+c.moveTo+" && printf '\\033]7;file://localhost"+c.announce+"\\007'")
	}
	// Precondition: the daemon reads the new folder as each pane's folder.
	for _, c := range cases {
		waitWindowCwd(t, base, c.session, c.window, c.announce)
	}
	// Precondition: the daemon detects the linked worktree as the session's
	// worktree. It looks on a save, which a change of state brings.
	for _, c := range cases {
		if c.detect {
			waitDetectedWorktree(t, base, c.session, linked)
		}
	}
	// The agents start, and some move on.
	for _, c := range cases {
		if !c.agent {
			continue
		}
		fireClaudeHook(t, base, c.session, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "go", "cwd": c.moveTo})
		if c.agentMovesTo != "" {
			typeInPane(t, base, c.session, c.window, "cd "+c.agentMovesTo+" && printf '\\033]7;file://localhost"+c.agentMovesTo+"\\007'")
			waitWindowCwd(t, base, c.session, c.window, c.agentMovesTo)
			fireClaudeHook(t, base, c.session, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "go on", "cwd": c.agentMovesTo})
		}
	}

	for _, c := range cases {
		if c.tool == "Edit" {
			// The hook reports the pane blocked and exits.
			fields := map[string]any{
				"hook_event_name": "PermissionRequest",
				"tool_name":       "Edit",
				"tool_input":      map[string]string{"file_path": c.target, "old_string": "hi", "new_string": "hello"},
			}
			if c.agentMovesTo != "" {
				fields["cwd"] = c.agentMovesTo
			}
			fireClaudeHook(t, base, c.session, fields)
			continue
		}
		// The hook holds the pane's prompt until it is answered.
		startHeldBashHook(t, base, c.session, "cp notes.txt "+c.target, c.harnessCwd)
	}

	for _, c := range cases {
		var it riskItem
		if c.tool == "Edit" {
			it = waitRiskItem(t, base, c.session)
		} else {
			it = waitHeldRiskItem(t, base, c.session)
		}
		t.Logf("%s (at %s, says %s, harness cwd %q): %q risk %v", c.session, c.moveTo, c.announce, c.harnessCwd, it.Summary, it.Risk)
		if got := slices.Contains(it.Risk, "outside the worktree"); got == c.safe {
			t.Errorf("ASSERTION: %s: a write to %s from a pane at %s (harness cwd %q) is marked %v, want outside the worktree %v", c.session, c.target, c.moveTo, c.harnessCwd, it.Risk, !c.safe)
		}
	}
	saveRiskItems(t, base, "risk-root-start-folder-items.json")
}

// fireClaudeHook runs Claude Code's hook for an event in window 0 of
// session, with the payload fields given beside the session id.
func fireClaudeHook(t *testing.T, base, session string, fields map[string]any) {
	t.Helper()
	fields["session_id"] = "e2e-" + session
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tuiosBin, "agent-hook", "claude-code", "--session", session, "--window", "0")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Stdin = bytes.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("agent-hook %v for %s: %v\n%s", fields["hook_event_name"], session, err, out)
	}
}

// startHeldBashHook runs Claude Code's PermissionRequest hook for a Bash call
// in window 0 of session, in the background. The hook holds the prompt; the
// test's cleanup ends it.
func startHeldBashHook(t *testing.T, base, session, command, cwd string) {
	t.Helper()
	body := map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      "e2e-" + session,
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
	}
	if cwd != "" {
		body["cwd"] = cwd
	}
	payload, err := json.Marshal(body)
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
