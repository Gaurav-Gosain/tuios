package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPanesDoNotInheritTheAgentThatStartedTheDaemon starts the daemon from a
// command with an agent's tool call environment, the way `tuios new --detach`
// run by Codex or Claude Code does, and reads what a pane's shell was given.
//
// A command that finds no daemon starts one, and the daemon keeps that
// command's environment for its life. Every pane is built from the daemon's
// environment, so when an agent's tool call started it, every pane of every
// session got the call's NO_COLOR, CODEX_THREAD_ID, CODEX_CI, CLAUDECODE,
// CLAUDE_CODE_SESSION_ID and the rest. Programs in every pane drew without
// colour. Worse, a Codex started in a pane carried the outer Codex's
// CODEX_THREAD_ID, and tuios's Codex hook drops the events of a Codex whose
// thread differs from the inherited one as a nested Codex session, so the
// pane never reported its state.
//
// The daemon's environment still reaches the panes. Only the variables that
// describe the one command that started it are cut. CODEX_HOME and
// CLAUDE_CONFIG_DIR name the person's folders, not a session, and stay.
//
// How this could pass wrongly, written down first:
//   - A daemon started earlier with another environment could answer. The
//     test has its own base, and the first command it runs starts the daemon
//     with the agent's environment.
//   - The suite's own environment may already hold agent variables, when it
//     is run from Codex or Claude Code. The test sets every value it asserts,
//     and the values it sets come after the inherited ones and win.
//   - The pane could lose every variable, or the daemon could have been
//     started without the caller's environment at all, and the removed ones
//     would read unset for that reason. The same command also passes
//     CODEX_HOME, CLAUDE_CONFIG_DIR and an ordinary variable, and the pane
//     must have the test's value of each.
//   - An empty value could be read as removed. Each variable is printed with
//     ${VAR-unset}, so an empty one prints [] and a missing one [unset].
//   - The line read could be the shell's echo of the typed command. The shell
//     prints through a format, so only its output has a VAR line without %s.
//   - The hook could report idle for a reason other than the missing
//     CODEX_THREAD_ID, or skip for a reason other than it. The hook runs with
//     --explain, and its decision is read: the run in the pane as it is must
//     send, and the same run with CODEX_THREAD_ID set in the pane, a Codex
//     really nested in another, must still be skipped as a nested session.
//   - The pane's PATH could find another tuios, whose hook dials another
//     daemon. The hook is run by the absolute path of the binary under test.
//
// The pane's text and both hook decisions are saved under artifactDir.
func TestPanesDoNotInheritTheAgentThatStartedTheDaemon(t *testing.T) {
	const session = "e2e-started-by"
	base := t.TempDir()
	killDaemon(t, base)
	// The folders are under a short directory in /tmp because a pane is 80
	// columns wide, and a line holding a path under t.TempDir wraps, so it
	// would not read back as one line.
	home, err := os.MkdirTemp("/tmp", "tuios-env-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	codexHome := filepath.Join(home, "codex")
	claudeDir := filepath.Join(home, "claude")
	agentEnv := []string{
		"TERM=dumb", "COLORTERM=",
		"NO_COLOR=1", "CLICOLOR=0", "FORCE_COLOR=0",
		"CODEX_THREAD_ID=outer-thread", "CODEX_CI=1", "CODEX_SHELL=1",
		"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=outer-session",
		"CODEX_HOME=" + codexHome, "CLAUDE_CONFIG_DIR=" + claudeDir,
		"TUIOS_E2E_CALLER_KEPT=kept",
	}
	removed := []string{
		"NO_COLOR", "CLICOLOR", "FORCE_COLOR",
		"CODEX_THREAD_ID", "CODEX_CI", "CODEX_SHELL",
		"CLAUDECODE", "CLAUDE_CODE_SESSION_ID",
	}
	kept := map[string]string{
		"CODEX_HOME":            codexHome,
		"CLAUDE_CONFIG_DIR":     claudeDir,
		"TUIOS_E2E_CALLER_KEPT": "kept",
	}
	if out, err := tuiosCLIEnv(t, base, agentEnv, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	names := append(append([]string{}, removed...), "CODEX_HOME", "CLAUDE_CONFIG_DIR", "TUIOS_E2E_CALLER_KEPT")
	cmd := `printf 'VAR %s=[%s] END\n'`
	for _, name := range names {
		cmd += ` ` + name + ` "${` + name + `-unset}"`
	}
	if out, err := tuiosCLIEnv(t, base, agentEnv, "send-keys", "-s", session, "-l", cmd+"\r"); err != nil {
		t.Fatalf("ask the shell for its environment: %v\n%s", err, out)
	}

	got := map[string]string{}
	var pane string
	deadline := time.Now().Add(shellTimeout)
	for len(got) < len(names) {
		pane, _ = tuiosCLIEnv(t, base, agentEnv, "capture-pane", "-s", session)
		for _, line := range strings.Split(pane, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "VAR ") || !strings.HasSuffix(line, "] END") || strings.Contains(line, "%s") {
				continue
			}
			name, value, ok := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(line, "VAR "), "] END"), "=[")
			if ok {
				got[name] = value
			}
		}
		if len(got) < len(names) {
			if time.Now().After(deadline) {
				t.Fatalf("the shell printed %d of its %d variables:\n%s", len(got), len(names), pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.WriteFile(filepath.Join(artifactDir(t), "pane.txt"), []byte(pane), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, want := range kept {
		if got[name] != want {
			t.Errorf("the pane has %s=[%s], want [%s]: the daemon's environment must still reach "+
				"its panes, or the removed variables below read unset for the wrong reason\n%s",
				name, got[name], want, pane)
		}
	}
	var leaked []string
	for _, name := range removed {
		if got[name] != "unset" {
			leaked = append(leaked, name+"=["+got[name]+"]")
		}
	}
	if len(leaked) > 0 {
		t.Errorf("the pane inherited %s from the agent tool call that started the daemon, "+
			"want them unset\n%s", strings.Join(leaked, " "), pane)
	}

	// The Codex hook, run in the pane the way a Codex started there runs it.
	// Its decision goes to a file, which is moved into place only when the
	// hook has finished, so a half-written one is never read.
	payload := `{"hook_event_name":"SessionStart","session_id":"inner-thread"}`
	hook := func(file, prefix string) string {
		t.Helper()
		path := filepath.Join(base, file)
		line := `printf '%s\n' '` + payload + `' | ` + prefix + `'` + tuiosBin + `' agent-hook codex --explain 2>'` +
			path + `.tmp'; mv '` + path + `.tmp' '` + path + `'`
		if out, err := tuiosCLIEnv(t, base, agentEnv, "send-keys", "-s", session, "-l", line+"\r"); err != nil {
			t.Fatalf("run the Codex hook in the pane: %v\n%s", err, out)
		}
		deadline := time.Now().Add(shellTimeout)
		for {
			if b, err := os.ReadFile(path); err == nil {
				_ = os.WriteFile(filepath.Join(artifactDir(t), file), b, 0o644)
				return string(b)
			}
			if time.Now().After(deadline) {
				pane, _ := tuiosCLIEnv(t, base, agentEnv, "capture-pane", "-s", session)
				t.Fatalf("the Codex hook in the pane never finished:\n%s", pane)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	type decision struct {
		Skip   string `json:"skip"`
		Report *struct {
			State string `json:"state"`
		} `json:"report"`
	}
	readDecision := func(explained string) decision {
		t.Helper()
		var d decision
		if err := json.Unmarshal([]byte(strings.TrimSpace(explained)), &d); err != nil {
			t.Fatalf("the hook's --explain output is not one JSON decision: %v\n%s", err, explained)
		}
		return d
	}

	plain := hook("hook-plain.json", "")
	if d := readDecision(plain); d.Skip != "" || d.Report == nil || d.Report.State != "idle" {
		t.Errorf("a Codex started in the pane was not reported, want an idle report: "+
			"skipped as %q (a nested Codex session means the pane inherited CODEX_THREAD_ID)\n%s",
			d.Skip, plain)
	}
	var state string
	deadline = time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosCLIEnv(t, base, agentEnv, "get-agent-state", "-s", session, "--json")
		var st struct {
			State string `json:"state"`
		}
		if json.Unmarshal([]byte(out), &st) == nil {
			state = st.State
		}
		if state == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("the pane's agent state stayed %q after a Codex in it started, want idle: "+
				"the hook dropped the event as a nested Codex session\nget-agent-state: %s\nhook: %s",
				state, out, plain)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The positive half of the gate: a Codex really started inside another
	// Codex has CODEX_THREAD_ID in its own environment, and its events are
	// still dropped.
	nested := hook("hook-nested.json", "CODEX_THREAD_ID=outer-in-pane ")
	if d := readDecision(nested); d.Skip != "nested Codex session" {
		t.Errorf("a Codex nested in another in the pane was not skipped as a nested Codex session, "+
			"so the gate the pane's hook is checked against is not working\n%s", nested)
	}
}
