package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStartAgentFallsBackToTheDaemonsPath: a caller started by an app, such
// as the Rex server, sends a bare PATH with none of the person's folders on
// it. start-agent then finds the agent on the daemon's PATH, the one a new
// pane gets, runs it with that PATH, and says so; an agent on neither PATH is
// refused with a message that names both; an agent on the caller's PATH runs
// from there as before.
//
// How it could pass wrongly, written down first:
//   - The agent could be found on the test's own PATH: the caller sends
//     PATH=/usr/bin:/bin, and the agent's folder is only on the daemon's PATH
//     or only on the caller's, by case.
//   - The agent could start and still get the caller's bare PATH, so its own
//     tools are missing: the stand-in writes the PATH it got, and the test
//     reads it.
//   - The result could claim a PATH it did not use: path_from is checked
//     against the case.
//
// NEGATIVE CONTROL: without the fallback in resolveAgentLaunch, the daemon
// case fails with "is not on the PATH the caller sent".
func TestStartAgentFallsBackToTheDaemonsPath(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	daemonDir := filepath.Join(base, "daemon only")
	callerDir := filepath.Join(base, "caller only")
	seen := filepath.Join(base, "seen")
	for _, d := range []string{daemonDir, callerDir, seen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A stand-in no manifest knows, which writes the PATH it got and stays.
	standIn := func(dir, name string) {
		script := "#!/bin/sh\nprintf '%s' \"$PATH\" > '" + filepath.Join(seen, name) + "'\nexec sleep 600\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil { //nolint:gosec // a pane runs it
			t.Fatal(err)
		}
	}
	standIn(daemonDir, "daemonagent")
	standIn(callerDir, "calleragent")

	daemonPath := daemonDir + string(os.PathListSeparator) + os.Getenv("PATH")
	if out, err := tuiosCLIEnv(t, base, []string{"PATH=" + daemonPath}, "new", "work", "--detach"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	// A bare PATH as Rex gives one, without a real agent CLI (crush is in
	// /usr/bin on Arch), which agentPathGuard refuses.
	bare := agentFreePath("/usr/bin", "/bin")

	start := func(path, agent string) (map[string]any, string) {
		t.Helper()
		out, _ := tuiosCLIEnv(t, base, []string{"PATH=" + path}, "start-agent", agent, "-s", "work", "--ready-timeout", "300", "--json")
		saveText(t, "start-"+agent, out)
		i := strings.Index(out, "{")
		if i < 0 {
			return nil, out
		}
		var res map[string]any
		if err := json.NewDecoder(strings.NewReader(out[i:])).Decode(&res); err != nil {
			return nil, out
		}
		return res, out
	}

	t.Run("daemon", func(t *testing.T) {
		res, out := start(bare, "daemonagent")
		if res == nil || res["window_id"] == nil {
			t.Fatalf("ASSERTION: an agent only on the daemon's PATH did not start for a caller with PATH %s:\n%s", bare, out)
		}
		if res["path_from"] != "daemon" {
			t.Errorf("ASSERTION: path_from is %v, want daemon", res["path_from"])
		}
		got := waitForFileText(t, filepath.Join(seen, "daemonagent"), func(s string) bool { return s != "" }, "the agent never wrote its PATH")
		if !strings.Contains(":"+got+":", ":"+daemonDir+":") || got == bare {
			t.Errorf("ASSERTION: the agent runs with PATH %q, want the daemon's, which holds %s", got, daemonDir)
		}
	})

	t.Run("caller", func(t *testing.T) {
		res, out := start(bare+string(os.PathListSeparator)+callerDir, "calleragent")
		if res == nil || res["window_id"] == nil {
			t.Fatalf("ASSERTION: an agent on the caller's PATH did not start:\n%s", out)
		}
		if res["path_from"] != "caller" {
			t.Errorf("ASSERTION: path_from is %v, want caller", res["path_from"])
		}
		got := waitForFileText(t, filepath.Join(seen, "calleragent"), func(s string) bool { return s != "" }, "the agent never wrote its PATH")
		if !strings.Contains(":"+got+":", ":"+callerDir+":") {
			t.Errorf("ASSERTION: the agent runs with PATH %q, want the caller's, which holds %s", got, callerDir)
		}
	})

	t.Run("neither", func(t *testing.T) {
		res, out := start(bare, "nowhereagent")
		if res != nil && res["window_id"] != nil {
			t.Fatalf("ASSERTION: an agent on no PATH started:\n%s", out)
		}
		if !strings.Contains(out, "the PATH the caller sent, nor on the daemon's PATH") {
			t.Errorf("ASSERTION: the refusal does not name both PATHs:\n%s", out)
		}
	})
}

// TestStartAgentNoWaitAnswersBeforeReady: start-agent --no-wait answers once
// the agent's process runs, without waiting for it to be ready, so a caller
// with a short time limit of its own, as a Rex action has, gets an answer
// instead of a timeout. A program that exits at once is still reported as a
// failure, and --prompt is typed once the agent is ready, exactly once.
//
// How it could pass wrongly, written down first:
//   - --no-wait could answer quickly because the agent was ready at once: the
//     stand-in never reports a state until the test sets it.
//   - The prompt could be typed before the agent is ready, into whatever it
//     shows first: the stand-in writes every line it reads, and the test
//     checks the file stays absent until the agent reads idle.
//   - The prompt could be typed twice: the file must hold it once, and keep
//     holding it once.
//   - A failed start could read as started: the stand-in that exits at once
//     must make the command exit non-zero.
//
// NEGATIVE CONTROL: without no_wait in the start-agent verb, the first case
// waits out --ready-timeout and reads not ready.
func TestStartAgentNoWaitAnswersBeforeReady(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	bin := filepath.Join(base, "bin")
	seen := filepath.Join(base, "seen")
	for _, d := range []string{bin, seen} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prompted := filepath.Join(seen, "prompted")
	scripts := map[string]string{
		"slowagent": "#!/bin/sh\nwhile read -r line; do printf '%s\\n' \"$line\" >> '" + prompted + "'; done\n",
		"quitagent": "#!/bin/sh\necho 'quitagent: no such flag' >&2\nexit 3\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil { //nolint:gosec // a pane runs it
			t.Fatal(err)
		}
	}
	path := "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")
	mustCLI(t, base, "new", "work", "--detach")

	// The wait for ready would be 60 s: the answer has to come well before.
	out, err := tuiosCLIEnv(t, base, []string{path}, "start-agent", "slowagent", "-s", "work", "--no-wait", "--ready-timeout", "60000", "--prompt", "hello from no-wait", "--json")
	saveText(t, "no-wait", out)
	if err != nil {
		t.Fatalf("ASSERTION: start-agent --no-wait failed for an agent that started: %v\n%s", err, out)
	}
	var res map[string]any
	if i := strings.Index(out, "{"); i < 0 || json.NewDecoder(strings.NewReader(out[i:])).Decode(&res) != nil {
		t.Fatalf("no JSON result:\n%s", out)
	}
	if res["outcome"] != "started" || res["prompt_status"] != "pending" {
		t.Errorf("ASSERTION: outcome %v and prompt_status %v, want started and pending", res["outcome"], res["prompt_status"])
	}
	win, _ := res["window_id"].(string)
	if win == "" {
		t.Fatalf("no window_id in the result:\n%s", out)
	}
	stayTrue(t, "the prompt waits for the agent to be ready", 2*time.Second, func() (bool, string) {
		b, err := os.ReadFile(prompted)
		return err != nil, string(b)
	})
	mustCLI(t, base, "set-agent-state", "idle", "-s", "work", "-w", win)
	got := waitForFileText(t, prompted, func(s string) bool { return strings.Contains(s, "hello from no-wait") }, "the prompt was never typed after the agent read idle")
	stayTrue(t, "the prompt is typed once", 3*time.Second, func() (bool, string) {
		b, _ := os.ReadFile(prompted)
		return strings.Count(string(b), "hello from no-wait") == 1, string(b)
	})
	saveText(t, "prompted", got)

	out, err = tuiosCLIEnv(t, base, []string{path}, "start-agent", "quitagent", "-s", "work", "--no-wait")
	saveText(t, "no-wait-quit", out)
	if err == nil || !strings.Contains(out, "exited at once with status 3") {
		t.Errorf("ASSERTION: start-agent --no-wait did not fail with the agent's exit status for an agent that exited at once (err %v):\n%s", err, out)
	}
}
