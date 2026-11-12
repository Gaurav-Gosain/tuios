package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
