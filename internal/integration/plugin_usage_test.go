package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestOpenCodePluginFeedsUsage runs the rendered opencode plugin under node,
// with a stand-in tuios that records each run, and feeds it opencode's
// message.updated and session.idle events. It checks the plugin runs
// agent-statusline with the model and the sum of its assistant messages'
// cost, only when either changed, once more at the end of the turn, and
// never for a subagent's session.
func TestOpenCodePluginFeedsUsage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in tuios is a shell script")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	if err := os.Mkdir(calls, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "tuios")
	script := "#!/bin/sh\nf=$(mktemp \"" + calls + "/call.XXXXXX\")\n{ printf '%s\\n' \"$*\"; cat; } > \"$f\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tg := mustTarget(t, OpenCode)
	plugin := filepath.Join(dir, "plugin.mjs")
	if err := os.WriteFile(plugin, tg.format.(ownedFile).render(tg, fake), 0o644); err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(dir, "drive.mjs")
	events := []map[string]any{
		// A user message is not a cost.
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "m0", "sessionID": "ses_1", "role": "user"}}},
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "m1", "sessionID": "ses_1", "role": "assistant", "parentID": "m0", "modelID": "claude-sonnet-4-6", "providerID": "anthropic", "cost": 0.1}}},
		// The same message streaming, its cost unchanged to the cent: no run.
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "m1", "sessionID": "ses_1", "role": "assistant", "parentID": "m0", "modelID": "claude-sonnet-4-6", "cost": 0.101}}},
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "m2", "sessionID": "ses_1", "role": "assistant", "parentID": "m0", "modelID": "claude-sonnet-4-6", "cost": 0.25}}},
		// A subagent's session is dropped.
		{"type": "session.created", "properties": map[string]any{"info": map[string]any{"id": "ses_child", "parentID": "ses_1"}}},
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "c1", "sessionID": "ses_child", "role": "assistant", "modelID": "haiku", "cost": 5}}},
		// A cost of the wrong type is left out.
		{"type": "message.updated", "properties": map[string]any{"info": map[string]any{"id": "m3", "sessionID": "ses_1", "role": "assistant", "modelID": "claude-sonnet-4-6", "cost": "lots"}}},
		{"type": "session.idle", "properties": map[string]any{"sessionID": "ses_1"}},
	}
	evJSON, _ := json.Marshal(events)
	code := `import { TuiosAgentState } from ` + jsString(plugin) + `;
const hooks = await TuiosAgentState({});
for (const event of ` + string(evJSON) + `) {
  await hooks.event({ event });
}
`
	if err := os.WriteFile(driver, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, driver)
	cmd.Env = append(os.Environ(), "TUIOS_ENV=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}

	// The runs are not waited for: collect them as they land.
	var got []string
	deadline := time.Now().Add(10 * time.Second)
	for {
		got = readCalls(t, calls, "agent-statusline")
		if len(got) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	got = readCalls(t, calls, "agent-statusline")
	want := []string{
		"agent-statusline opencode --integration 3\n" + `{"session_id":"ses_1","modelID":"claude-sonnet-4-6","cost":0.1}`,
		"agent-statusline opencode --integration 3\n" + `{"session_id":"ses_1","modelID":"claude-sonnet-4-6","cost":0.351}`,
		"agent-statusline opencode --integration 3 --turn-end\n" + `{"session_id":"ses_1","modelID":"claude-sonnet-4-6","cost":0.351}`,
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n\n") != strings.Join(want, "\n\n") {
		t.Errorf("runs:\n%s\n\nwant:\n%s", strings.Join(got, "\n\n"), strings.Join(want, "\n\n"))
	}
}

// readCalls reads the recorded runs whose arguments start with prefix.
func readCalls(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || !strings.HasPrefix(string(data), prefix) {
			continue
		}
		out = append(out, strings.TrimSpace(string(data)))
	}
	return out
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
