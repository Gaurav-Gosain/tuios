package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLaunchClaude is a program called claude for the launch test. "claude
// auth status --json" answers signed in when the marker file exists, signed
// out (with a failing exit) when it does not, and hangs when the hang file
// exists. Any other call writes its argv, one word a line, to the argv file,
// reports the pane idle and waits.
const fakeLaunchClaude = `#!/bin/sh
if [ "$1" = auth ]; then
  if [ -f "__HANG__" ]; then sleep 30; fi
  if [ -f "__MARK__" ]; then
    printf '{\n  "loggedIn": true,\n  "email": "person@example.com"\n}\n'
    exit 0
  fi
  printf '{\n  "loggedIn": false\n}\n'
  exit 1
fi
printf '%s\n' "$@" > "__ARGV__"
"__TUIOS__" set-agent-state idle --harness claude-code -m ready
exec sleep 600
`

// TestStartAgentLaunchChoiceAndReadiness starts Claude Code with a model,
// an effort and a permission mode, and asks the daemon whether claude can
// start: installed, signed in, signed out, not installed, and a check that
// hangs.
//
// What it holds:
//   - start-agent --model opus --effort high --permission-mode plan starts
//     the program with --model opus --effort high --permission-mode plan,
//     the flags the claude-code manifest's [launch] table names;
//   - an effort the harness does not list, a model that is a flag, and a
//     model for a program no manifest knows are each refused before a pane
//     opens, and the refusal names the param;
//   - agent-readiness says installed and signed_in yes, no or unknown, and
//     never carries the check's output (the email in it);
//   - a hanging check answers unknown within its 2 second bound.
//
// How this could pass wrongly, written down first:
//   - The argv file could be left from an earlier start. Each start writes
//     a file of its own.
//   - A refused start could still open a pane. The pane count of the session
//     is read before and after.
//   - The readiness answer is kept for a minute, so a second ask could read
//     the first answer. Each case is a PATH of its own, and the cache is by
//     program path.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: with withChoice adding
// no flags, the argv check fails.
func TestStartAgentLaunchChoiceAndReadiness(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	dir := t.TempDir()
	// bin makes a PATH folder holding the fake claude. signed and hang make
	// its auth check answer signed in or hang.
	bin := func(name string, signed, hang bool) (path, argv string) {
		t.Helper()
		folder := filepath.Join(dir, name)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		mark, hangFile := filepath.Join(folder, "signed-in"), filepath.Join(folder, "hang")
		argv = filepath.Join(folder, "argv")
		script := strings.NewReplacer("__MARK__", mark, "__HANG__", hangFile, "__ARGV__", argv, "__TUIOS__", tuiosBin).Replace(fakeLaunchClaude)
		if err := os.WriteFile(filepath.Join(folder, "claude"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		for file, on := range map[string]bool{mark: signed, hangFile: hang} {
			if on {
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return folder, argv
	}
	sysPath := "/usr/bin:/bin"

	readiness := func(path string) (installed bool, signedIn string, raw string) {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"harnesses": []string{"claude-code"}, "env": map[string]string{"PATH": path}})
		out, err := tuiosCLI(t, base, "call", "agent-readiness", string(params))
		if err != nil {
			t.Fatalf("agent-readiness: %v\n%s", err, out)
		}
		var res struct {
			Agents []struct {
				Harness, Command string
				SignedIn         string `json:"signed_in"`
				Installed        bool
			}
		}
		start := strings.Index(out, "{")
		if start < 0 || json.Unmarshal([]byte(out[start:]), &res) != nil || len(res.Agents) != 1 {
			t.Fatalf("ASSERTION: agent-readiness gave no one answer for claude-code:\n%s", out)
		}
		return res.Agents[0].Installed, res.Agents[0].SignedIn, out
	}

	signedPath, argvFile := bin("signed", true, false)
	for _, c := range []struct {
		name, path string
		installed  bool
		signedIn   string
		within     time.Duration
	}{
		{"signed in", signedPath + ":" + sysPath, true, "yes", 10 * time.Second},
		{"signed out", must(bin("out", false, false)) + ":" + sysPath, true, "no", 10 * time.Second},
		{"not installed", filepath.Join(dir, "empty") + ":" + sysPath, false, "", 10 * time.Second},
		{"hangs", must(bin("hang", true, true)) + ":" + sysPath, true, "unknown", 6 * time.Second},
	} {
		began := time.Now()
		installed, signed, raw := readiness(c.path)
		took := time.Since(began)
		if installed != c.installed || signed != c.signedIn {
			t.Errorf("ASSERTION: %s: installed %v signed_in %q, want %v %q\n%s", c.name, installed, signed, c.installed, c.signedIn, raw)
		}
		if strings.Contains(raw, "example.com") || strings.Contains(raw, "loggedIn") {
			t.Errorf("ASSERTION: %s: the answer carries the check's output:\n%s", c.name, raw)
		}
		if took > c.within {
			t.Errorf("ASSERTION: %s: the answer took %v, over %v", c.name, took, c.within)
		}
		t.Logf("%s: installed=%v signed_in=%q in %v", c.name, installed, signed, took.Round(time.Millisecond))
	}

	env := []string{"PATH=" + signedPath + ":" + sysPath}
	panes := func() int {
		t.Helper()
		out, err := tuiosCLI(t, base, "list-windows", "-s", "work")
		if err != nil {
			t.Fatalf("list-windows: %v\n%s", err, out)
		}
		return strings.Count(out, "\n")
	}
	before := panes()
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"an effort claude does not list", []string{"claude", "--effort", "ultra"}, "effort \"ultra\" is not one Claude Code takes: low, medium, high, xhigh, max"},
		{"a model that is a flag", []string{"claude", "--model=--dangerously-skip-permissions"}, "model \"--dangerously-skip-permissions\": one word, not starting with -"},
		{"a model for a program no manifest knows", []string{"/bin/sleep 60", "--model", "opus"}, "takes no model option"},
	} {
		out, err := tuiosCLIEnv(t, base, env, append([]string{"start-agent", "-s", "work"}, c.args...)...)
		if err == nil || !strings.Contains(out, c.want) {
			t.Errorf("ASSERTION: %s: want a refusal saying %q, got err=%v\n%s", c.name, c.want, err, out)
		}
	}
	if got := panes(); got != before {
		t.Errorf("ASSERTION: a refused start opened a pane: %d windows, then %d", before, got)
	}

	out, err := tuiosCLIEnv(t, base, env, "start-agent", "-s", "work", "claude", "--name", "planner",
		"--model", "opus", "--effort", "high", "--permission-mode", "plan", "--ready-timeout", "20000", "--", "--verbose")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	var argv []byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if argv, err = os.ReadFile(argvFile); err == nil && len(argv) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	want := "--model\nopus\n--effort\nhigh\n--permission-mode\nplan\n--verbose\n"
	if string(argv) != want {
		t.Errorf("ASSERTION: the agent started with argv %q, want %q\n%s", argv, want, out)
	}
	art := filepath.Join(artifactDir(t), "start-agent-argv.txt")
	if err := os.WriteFile(art, []byte("start-agent claude --model opus --effort high --permission-mode plan -- --verbose\nargv after the program:\n"+string(argv)+"\nanswer:\n"+out), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("artifact %s", art)
}

// must is the first of two results.
func must(path, _ string) string { return path }
