package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

func envLines(text string) map[string]string {
	got := map[string]string{}
	for line := range strings.SplitSeq(text, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			got[k] = v
		}
	}
	return got
}

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

	if o, err := tuiosCLI(t, base, "new-window", "-s", "childenv", "trigger", "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, o)
	}
	checkChildEnv(t, "hook", waitFileHas(t, hookFile, "the hook never wrote its environment", 20*time.Second, func(s string) bool { return strings.Contains(s, "TUIOS_EVENT=") }))

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
