//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSessionIntegrationEndToEnd installs a session-only integration into a
// home of the test's own, runs the hook it wrote the way the harness would,
// and checks the pane carries the conversation id while its state stays what
// it was. It is the whole path: the installer, the agent-hook binary, the
// list-verbs check and set-agent-session on a real daemon.
func TestSessionIntegrationEndToEnd(t *testing.T) {
	e := newEnv(t)
	home := t.TempDir()
	qwenDir := filepath.Join(home, ".qwen")
	if err := os.MkdirAll(qwenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := `{"general": {"vimMode": true}}`
	if err := os.WriteFile(filepath.Join(qwenDir, "settings.json"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	withHome := func(extra ...string) []string {
		return append(e.environ(), append([]string{"HOME=" + home}, extra...)...)
	}
	run := func(stdin string, env []string, args ...string) string {
		t.Helper()
		cmd := exec.Command(e.bin, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("", withHome(), "integration", "install", "qwen")
	settings, err := os.ReadFile(filepath.Join(qwenDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "agent-hook qwen --integration 1") || !strings.Contains(string(settings), `"vimMode": true`) {
		t.Fatalf("settings.json after install:\n%s", settings)
	}
	var statuses []map[string]any
	if err := json.Unmarshal([]byte(run("", withHome(), "integration", "status", "qwen", "--json")), &statuses); err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0]["installed"] != true || statuses[0]["current"] != true || statuses[0]["reports"] != "session" {
		t.Fatalf("status: %v", statuses)
	}

	e.mustRun("new", "--detach", "work")
	e.waitForSocket(10 * time.Second)
	c := e.dial()
	windows := c.result(1, "list-windows", map[string]any{"session": "work"}, 5*time.Second)
	wins, _ := windows["windows"].([]any)
	if len(wins) == 0 {
		t.Fatalf("no windows: %v", windows)
	}
	win, _ := wins[0].(map[string]any)["window_id"].(string)
	if win == "" {
		t.Fatalf("list-windows gave no window_id: %v", wins[0])
	}

	// What qwen runs on SessionStart, with the pane's environment.
	payload := `{"hook_event_name":"SessionStart","session_id":"qw-e2e","source":"startup"}`
	explain := run(payload, withHome("TUIOS_PANE_ID="+win, "TUIOS_SESSION=work"), "agent-hook", "qwen", "--integration", "1", "--explain")
	if !strings.Contains(explain, `"applied":true`) {
		t.Fatalf("agent-hook --explain: %s", explain)
	}
	got := c.result(2, "get-agent-state", map[string]any{"session": "work", "window": win}, 5*time.Second)
	if got["agent_session_id"] != "qw-e2e" || got["state"] != "none" {
		t.Fatalf("get-agent-state after the hook: %v", got)
	}

	run("", withHome(), "integration", "uninstall", "qwen")
	settings, _ = os.ReadFile(filepath.Join(qwenDir, "settings.json"))
	if strings.Contains(string(settings), "agent-hook") || !strings.Contains(string(settings), `"vimMode": true`) {
		t.Fatalf("settings.json after uninstall:\n%s", settings)
	}

	var doctor map[string]any
	if err := json.Unmarshal([]byte(run("", withHome(), "doctor", "agents", "--json")), &doctor); err != nil {
		t.Fatal(err)
	}
	if without, _ := doctor["without_integration"].([]any); len(without) == 0 {
		t.Fatalf("doctor names no harness without an integration: %v", doctor)
	}
}
