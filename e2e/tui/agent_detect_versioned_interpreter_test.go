package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

const hermesScript = `#!/bin/sh
read -r _
exit 0
`

// TestAVersionedPythonRunsADetectedAgent is issue #2125. Hermes runs from its
// own bundled Python, whose executable is python3.14, with bin/python3 a link
// to it. The detector matched interpreter names exactly, so python3.14 was not
// one, it never read the script from argv, and the pane had no agent.
//
// The stand-in is the e2e fakeclaude program, copied as bin/python3.14: it runs
// its arguments with sh, as an interpreter runs a script. bash starts it with
// argv0 python3, so comm and exe read python3.14 and argv0 python3, the three
// the report shows. The script is ~/.hermes/hermes-agent/hermes.
//
// How this could pass wrongly, written down first:
//   - The pane could be taken for hermes by its argv0 or comm. Neither is
//     hermes, so only the script, read because the process is an interpreter,
//     can name it. The test checks that explain-agent-detect read the script as
//     the run token.
//   - A detector that names every process an interpreter would pass too. The
//     same stand-in, copied as python3-config, runs the same script next, and
//     must not be read as an interpreter or as hermes.
//
// NEGATIVE CONTROL: with IsInterpreter back to the exact name list, the run
// token is empty and the test fails on "not an interpreter".
func TestAVersionedPythonRunsADetectedAgent(t *testing.T) {
	base := t.TempDir()
	useShippedLooks(base)
	bin := filepath.Join(base, "python-3.14.9", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(bin, "python3.14")
	if out, err := exec.Command("go", "build", "-o", py, "./testdata/fakeclaude").CombinedOutput(); err != nil {
		t.Fatalf("build the stand-in python3.14: %v\n%s", err, out)
	}
	if err := os.Symlink("python3.14", filepath.Join(bin, "python3")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(xdgDir(base, "HOME"), ".hermes", "hermes-agent", "hermes")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(hermesScript), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[daemon]\nagent_detect_seconds = 1\n")
	killDaemon(t, base)
	const session = "e2e-hermes-py"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", session, err, out)
	}

	line := "bash -c 'exec -a python3 " + py + " " + script + " -p cos chat'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "0", line); err != nil {
		t.Fatalf("start the stand-in: %v\n%s", err, out)
	}
	type explanation struct {
		HarnessID string `json:"harness_id"`
		Process   struct {
			Comm        string `json:"comm"`
			Argv0       string `json:"argv0"`
			RunToken    string `json:"run_token"`
			Interpreter bool   `json:"interpreter"`
		} `json:"process"`
		Manifests []struct {
			ID      string `json:"id"`
			Matched bool   `json:"matched"`
			Rule    string `json:"rule"`
		} `json:"manifests"`
	}
	var res explanation
	var raw string
	defer func() {
		if err := os.WriteFile(filepath.Join(artifactDir(t), "explain-agent-detect.json"), []byte(raw), 0o644); err != nil {
			t.Errorf("save the explanation: %v", err)
		}
	}()
	waitUntil(t, "the detector reads python3.14 as an interpreter and names the pane hermes", 20*time.Second, func() (bool, string) {
		out, err := tuiosCLI(t, base, "explain-agent-detect", "-s", session, "-w", "0", "--json")
		if err != nil {
			return false, err.Error() + ": " + out
		}
		raw, res = out, explanation{}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			return false, "not JSON: " + out
		}
		return res.Process.Comm == "python3.14" && res.HarnessID == "hermes", out
	})
	if res.Process.Argv0 != "python3" {
		t.Errorf("ASSERTION: argv0 is %q, want python3 as in the report", res.Process.Argv0)
	}
	if !res.Process.Interpreter || res.Process.RunToken != script {
		t.Errorf("ASSERTION: python3.14 is not an interpreter, or its run token %q is not the script %s", res.Process.RunToken, script)
	}
	if !slices.ContainsFunc(res.Manifests, func(m struct {
		ID      string `json:"id"`
		Matched bool   `json:"matched"`
		Rule    string `json:"rule"`
	}) bool {
		return m.ID == "hermes" && m.Matched
	}) {
		t.Errorf("ASSERTION: the hermes manifest did not match: %s", raw)
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "0", "\n"); err != nil {
		t.Fatalf("end the stand-in: %v\n%s", err, out)
	}
	other := filepath.Join(bin, "python3-config")
	if err := os.Link(py, other); err != nil {
		t.Fatal(err)
	}
	line = "exec -a python3-config " + other + " " + script + " -p cos chat\n"
	line = "bash -c '" + line[:len(line)-1] + "'\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "0", line); err != nil {
		t.Fatalf("start python3-config: %v\n%s", err, out)
	}
	waitUntil(t, "the detector reads python3-config and the pane leaves hermes", 20*time.Second, func() (bool, string) {
		out, err := tuiosCLI(t, base, "explain-agent-detect", "-s", session, "-w", "0", "--json")
		if err != nil {
			return false, err.Error() + ": " + out
		}
		raw, res = out, explanation{}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			return false, "not JSON: " + out
		}
		return res.Process.Comm == "python3-config" && res.HarnessID != "hermes", out
	})
	if res.Process.Interpreter || res.Process.RunToken != "" || res.HarnessID == "hermes" {
		t.Errorf("ASSERTION: python3-config counts as an interpreter (run token %q, harness %q): %s", res.Process.RunToken, res.HarnessID, raw)
	}
}
