package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startHintFor(t *testing.T, base, sess, config string, afterStart func()) string {
	t.Helper()
	killDaemon(t, base)
	if config != "" {
		writeConfig(t, base, config)
	}
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if afterStart != nil {
		afterStart()
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookEventScript), 0o700); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(dir, "params.json")
	out := filepath.Join(dir, "result.txt")
	raw, _ := json.Marshal(map[string]any{
		"harness": "claude-code",
		"payload": `{"hook_event_name":"SessionStart","session_id":"conv-hint","source":"startup"}`,
	})
	if err := os.WriteFile(in, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	line := "TUIOS_E2E_CLI=" + tuiosBin + " sh " + script + " " + in + " " + out + "\n"
	if o, err := tuiosCLI(t, base, "send-text", "-s", sess, "-w", "0", line); err != nil {
		t.Fatalf("type the call into the pane: %v\n%s", err, o)
	}
	var res []byte
	waitUntil(t, "the call from the pane finishes", uiTimeout, func() (bool, string) {
		var err error
		if res, err = os.ReadFile(out); err != nil {
			return false, err.Error()
		}
		return true, ""
	})
	text := string(res)
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if !strings.Contains(text, "exit=0") || i < 0 || j < i {
		t.Fatalf("the call failed:\n%s", text)
	}
	var reply struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(text[i:j+1]), &reply); err != nil {
		t.Fatalf("read the call's answer: %v\n%s", err, text)
	}
	for _, keep := range []string{"You run in tuios pane", "Agents here:", "Never type into a pane on needs_input", "tuios --skill"} {
		if !strings.Contains(reply.Stdout, keep) {
			t.Fatalf("the hint lacks %q, so it may be empty for another reason:\n%s", keep, reply.Stdout)
		}
	}
	return reply.Stdout
}

const (
	nameLine   = "Name your pane after the task"
	noNameLine = "Do not rename your pane or this session"
)

func TestSessionStartHintFollowsNameHint(t *testing.T) {
	var artifact strings.Builder
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(artifactDir(t), "agent-name-hint.txt"), []byte(artifact.String()), 0o600)
	})

	def := startHintFor(t, t.TempDir(), "e2e-nh-default", "", nil)
	fmt.Fprintf(&artifact, "== default ==\n%s\n", def)
	if !strings.Contains(def, nameLine) || strings.Contains(def, noNameLine) {
		t.Fatalf("the default hint does not tell the agent to name its pane:\n%s", def)
	}

	offBase := t.TempDir()
	off := startHintFor(t, offBase, "e2e-nh-off", "[agents]\nname_hint = \" OFF \"\n", nil)
	fmt.Fprintf(&artifact, "== config.toml off ==\n%s\n", off)
	if strings.Contains(off, nameLine) || !strings.Contains(off, noNameLine) {
		t.Fatalf("with name_hint off the hint still names the pane, or lacks the word not to:\n%s", off)
	}

	over := startHintFor(t, offBase, "e2e-nh-over", "[agents]\nname_hint = \"off\"\n", func() {
		if out, err := tuiosCLI(t, offBase, "set-config", "--no-save", "-s", "e2e-nh-over", "agents.name_hint", "on"); err != nil {
			t.Fatalf("set the override: %v\n%s", err, out)
		}
	})
	fmt.Fprintf(&artifact, "== config.toml off, session override on ==\n%s\n", over)
	if !strings.Contains(over, nameLine) || strings.Contains(over, noNameLine) {
		t.Fatalf("a session override of on did not bring the naming line back:\n%s", over)
	}
}
