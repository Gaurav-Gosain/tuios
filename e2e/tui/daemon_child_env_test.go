package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The processes the daemon starts outside every pane get the filtered
// environment a pane gets.
//
// The failure modes this is written against:
//   - a daemon hook, a plugin command, an exec line or a task verify command
//     keeps the variables of the agent tool call that started the daemon: its
//     session ids, its credential, NO_COLOR;
//   - after a restart from pane X the daemon holds TUIOS_PANE_ID=X, and every
//     exec line and hook is then placed in pane X and may act as X;
//   - the filter takes away an ordinary variable of the person's.

// childEnvAgent is the environment of an agent's tool call that starts the
// daemon, and childEnvRemoved the names of it no daemon child may keep.
var (
	childEnvAgent = []string{
		"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=outer-session",
		"CLAUDE_CODE_MESSAGING_TOKEN=outer-token", "CODEX_THREAD_ID=outer-thread",
		"TUIOS_PANE_ID=outer-pane", "TUIOS_PANE_TOKEN=outer-pane-token",
		"NO_COLOR=1", "TUIOS_E2E_CALLER_KEPT=kept",
	}
	childEnvRemoved = []string{
		"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_MESSAGING_TOKEN",
		"CODEX_THREAD_ID", "TUIOS_PANE_ID", "TUIOS_PANE_TOKEN", "NO_COLOR",
	}
)

// envLines reads an "env" dump into a map.
func envLines(text string) map[string]string {
	got := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

// checkChildEnv fails for each removed variable the child kept, and when it
// lost the ordinary one.
func checkChildEnv(t *testing.T, who, text string) {
	t.Helper()
	got := envLines(text)
	var kept []string
	for _, name := range childEnvRemoved {
		if v, ok := got[name]; ok {
			kept = append(kept, name+"=["+v+"]")
		}
	}
	if len(kept) > 0 {
		t.Errorf("ASSERTION: the %s kept %s from the agent tool call that started the daemon, want them unset", who, strings.Join(kept, " "))
	}
	if got["TUIOS_E2E_CALLER_KEPT"] != "kept" {
		t.Errorf("ASSERTION: the %s lost TUIOS_E2E_CALLER_KEPT, an ordinary variable", who)
	}
}

// TestDaemonChildrenDoNotInheritTheAgentThatStartedTheDaemon starts the daemon
// from an agent's tool call, then reads the environment of an
// after-new-window hook, a plugin action and an exec line.
func TestDaemonChildrenDoNotInheritTheAgentThatStartedTheDaemon(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "out")
	mustMkdir(out)
	hookFile := filepath.Join(out, "hook-env.txt")
	writeConfig(t, base, "[hooks]\nafter-new-window = '[ \"$TUIOS_WINDOW_NAME\" = trigger ] && env > "+hookFile+"'\n")
	killDaemon(t, base)
	if o, err := tuiosCLIEnv(t, base, childEnvAgent, "new", "childenv", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, o)
	}

	// The hook.
	if o, err := tuiosCLI(t, base, "new-window", "-s", "childenv", "trigger", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	checkChildEnv(t, "hook", waitFileHas(t, hookFile, "the hook never wrote its environment", 20*time.Second, func(s string) bool { return strings.Contains(s, "TUIOS_EVENT=") }))

	// The plugin action.
	plug := filepath.Join(base, "envdump")
	mustMkdir(plug)
	pluginFile := filepath.Join(out, "plugin-env.txt")
	manifest := "api = 1\nid = \"e2e.envdump\"\nname = \"envdump\"\nversion = \"0.1.0\"\n\n[permissions]\ngrants = [\"ui\"]\n\n" +
		"[[process]]\nid = \"dump\"\nrun = \"action\"\ntitle = \"Dump\"\ncommand = [\"sh\", \"dump.sh\"]\n"
	for name, body := range map[string]string{"tuios-plugin.toml": manifest, "dump.sh": "env > " + pluginFile + "\n"} {
		if err := os.WriteFile(filepath.Join(plug, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if o, err := tuiosCLI(t, base, "plugins", "install", plug, "--yes"); err != nil {
		t.Fatalf("plugins install: %v\n%s", err, o)
	}
	if o, err := tuiosCLI(t, base, "plugins", "run", "e2e.envdump", "dump", "--wait"); err != nil {
		t.Fatalf("plugins run: %v\n%s", err, o)
	}
	checkChildEnv(t, "plugin", waitFileHas(t, pluginFile, "the plugin never wrote its environment", 20*time.Second, func(s string) bool { return s != "" }))

	// The exec line.
	execFile := filepath.Join(out, "exec-env.txt")
	req, _ := json.Marshal(map[string]any{"line": "env > " + execFile, "timeout_ms": 20000})
	if o, err := tuiosCLI(t, base, "call", "exec", string(req)); err != nil {
		t.Fatalf("call exec: %v\n%s", err, o)
	}
	checkChildEnv(t, "exec line", waitFileHas(t, execFile, "the exec line never wrote its environment", 20*time.Second, func(s string) bool { return s != "" }))
}

// TestExecAfterARestartFromAPaneIsNotThatPane starts a daemon as a restart
// from pane X does: with TUIOS_PANE_ID=X in its environment, and X restored
// with its id. An exec line must then be placed in no pane, not in X.
func TestExecAfterARestartFromAPaneIsNotThatPane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if o, err := tuiosCLI(t, base, "new", "restart", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, o)
	}
	ids := grantWindowIDs(t, base, "restart")
	if len(ids) == 0 {
		t.Fatal("the session has no window")
	}
	x := ids[0]
	if o, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, o)
	}
	// What restart-server run in pane X starts the new daemon with.
	env := []string{"TUIOS_PANE_ID=" + x, "TUIOS_SESSION=restart"}
	if o, err := tuiosCLIEnv(t, base, env, "new", "again", "--detach"); err != nil {
		t.Fatalf("start the daemon again: %v\n%s", err, o)
	}
	waitTextOf(t, "pane X is restored with its id", 20*time.Second, func() string {
		o, _ := tuiosCLI(t, base, "list-windows", "-s", "restart", "--json")
		return o
	}, func(s string) bool { return strings.Contains(s, x) })

	grantsFile := filepath.Join(base, "grants.json")
	req, _ := json.Marshal(map[string]any{"line": tuiosBin + " pane-grants --json > " + grantsFile, "timeout_ms": 20000})
	if o, err := tuiosCLI(t, base, "call", "exec", string(req)); err != nil {
		t.Fatalf("call exec: %v\n%s", err, o)
	}
	got := waitFileHas(t, grantsFile, "the exec line never wrote its grants", 20*time.Second, func(s string) bool { return strings.Contains(s, "window") })
	var tr transcript
	tr.add("pane X is %s\nexec line pane-grants --json:\n%s", x, got)
	tr.save(t, "exec-after-restart.txt")
	if strings.Contains(got, x) {
		t.Errorf("ASSERTION: after a restart from pane %s, an exec line is placed in that pane:\n%s", x, got)
	}
}

// TestATaskVerifyDoesNotInheritTheAgentThatStartedTheDaemon starts the daemon
// from an agent's tool call, then has the board's verify command write its
// environment. The command runs in the agent's worktree as a child of the
// daemon, so it gets what the hook, the plugin and the exec line above get.
//
// How it could pass wrongly: a verify that never ran writes no file and
// leaves the card in verify, so the test waits for the card to reach review
// with a passed record before it reads the file. A verify run with an empty
// environment would drop the ordinary variable, which checkChildEnv wants
// kept.
//
// Negative control (NEGATIVE_CONTROLS.md): with cmd.Env cut from runVerify,
// the verify command keeps the agent and pane variables.
func TestATaskVerifyDoesNotInheritTheAgentThatStartedTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	t.Cleanup(func() { dumpDaemonLogOnFail(t, base) })
	const sess = "childenv-verify"
	if o, err := tuiosCLIEnv(t, base, childEnvAgent, "new", sess, "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, o)
	}
	waitForSessionInfo(t, base, sess)

	repo := filepath.Join(base, "demo")
	lifeRepo(t, repo)
	a := strings.TrimSpace(mustCLI(t, base, "new-window", "-s", sess, "agent-a", "--no-focus", "--print-id", "--cwd", repo))
	mustCLI(t, base, "set-agent-state", "-s", sess, "-w", a, "working", "--harness", "claude-code", "-m", "on it")
	verifyFile := filepath.Join(base, "verify-env.txt")
	mustCLI(t, base, "task", "dispatch", "verify", "-s", sess, "env > "+verifyFile)
	mustCLI(t, base, "task", "add", "-s", sess, "-p", "do it", "the work") // t1
	mustCLI(t, base, "task", "claim", "t1", "-s", sess, "-w", a)
	mustCLI(t, base, "task", "stage", "t1", "verify", "-s", sess)
	cards, raw := waitLife(t, base, sess, 20*time.Second, "t1 did not reach review after its verify",
		func(c map[string]lifeCard) bool { return c["t1"].Stage == "review" })
	if c := cards["t1"]; c.Verify == nil || c.Verify.State != "passed" {
		t.Fatalf("t1 in review has verify %+v, want passed:\n%s", c.Verify, raw)
	}
	got, err := os.ReadFile(verifyFile)
	if err != nil {
		t.Fatalf("the verify command passed and wrote no environment: %v", err)
	}
	checkChildEnv(t, "verify command", string(got))
}
