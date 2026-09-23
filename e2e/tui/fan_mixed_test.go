package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFanMixesAgentsFromTheCallersPathAndStartAgentWaits drives P12 through the
// binary a person runs. The daemon starts with a PATH that has the fake claude
// and nothing else. fan is then run from a shell whose PATH also has mytool, a
// program no manifest knows, with its own prompt per session and a variable
// passed with --env. claude reaches its prompt by its title and gets its
// prompt; mytool shows nothing, so its prompt waits until it reports idle.
// Then start-agent opens a named claude beside them and returns once it is
// ready.
//
// Negative control: with PATH not sent by the CLI, the daemon refuses mytool
// as not installed and the fan call fails.
func TestFanMixesAgentsFromTheCallersPathAndStartAgentWaits(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}

	toolBin := filepath.Join(t.TempDir(), "tool-bin")
	if err := os.MkdirAll(toolBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"ARGS: $*\"\necho \"MARK: $FAN_MARK\"\nwhile IFS= read -r line; do echo \"GOT: $line\"; done\n"
	if err := os.WriteFile(filepath.Join(toolBin, "mytool"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	callerPath := "PATH=" + toolBin + string(os.PathListSeparator) + os.Getenv("PATH")

	out, err := tuiosCLIEnv(t, base, []string{callerPath, "FAN_MARK=from-the-shell"}, "fan",
		"--agent", "claude,mytool --flag", "--env", "FAN_MARK", "--repo", repo, "--name", "mix",
		"--prompt", "Claude task.", "--prompt", "Tool task.")
	if err != nil {
		t.Fatalf("fan with two agents: %v: %s", err, out)
	}
	if !strings.Contains(out, "Started 2 agents on mix") || !strings.Contains(out, "mytool --flag") {
		t.Fatalf("fan output does not name both agents:\n%s", out)
	}

	waitPane := func(session, want string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			pane, _ := tuiosCLI(t, base, "capture-pane", "-s", session)
			if strings.Contains(pane, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never showed %q:\n%s", session, want, pane)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	waitPane("repo-mix", "GOT: Claude task.")
	waitPane("repo-mix-2", "ARGS: --flag")
	waitPane("repo-mix-2", "MARK: from-the-shell")

	// mytool showed no state, so its prompt still waits.
	for _, r := range worktreeRows(t, base, "--group", "mix") {
		if r["session"] == "repo-mix-2" && r["prompt_status"] == "sent" {
			t.Fatalf("the prompt was typed at a program that showed nothing: %v", r)
		}
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "repo-mix-2", "idle"); err != nil {
		t.Fatalf("report idle for mytool: %v: %s", err, out)
	}
	waitPane("repo-mix-2", "GOT: Tool task.")

	out, err = tuiosCLI(t, base, "start-agent", "-s", "plain", "claude", "--name", "reviewer", "--ready-timeout", "30000")
	if err != nil || !strings.Contains(out, "reviewer") || !strings.Contains(out, "is ready") {
		t.Fatalf("start-agent did not report a ready reviewer: %v\n%s", err, out)
	}
	listed, err := tuiosCLI(t, base, "list-agents", "--select", "name:reviewer")
	if err != nil || !strings.Contains(listed, "reviewer") {
		t.Fatalf("the started agent is not addressable by its name: %v\n%s", err, listed)
	}
}
