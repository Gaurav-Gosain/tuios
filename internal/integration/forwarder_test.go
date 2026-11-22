package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runSh runs one installed command the way a harness does: through sh, with
// PATH set to dirs and path as stdin.
func runSh(t *testing.T, cmd, path, stdin string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the guard is sh")
	}
	c := exec.Command("/bin/sh", "-c", cmd)
	c.Env = []string{"HOME=" + t.TempDir(), "PATH=" + path}
	c.Stdin = strings.NewReader(stdin)
	out, err := c.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("sh: %v", err)
	}
	return string(out), code
}

// fakeTuios writes a program named name into dir that prints what it ran.
func fakeTuios(t *testing.T, dir, name, print string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+print+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestHookCommandIsHarmlessWithoutTheProgram is the bug in the issue: a
// settings file that travels to a host without tuios made every hook exit 127.
// The positive half runs the same command with the program on PATH, so a
// command that never ran anything cannot pass both.
func TestHookCommandIsHarmlessWithoutTheProgram(t *testing.T) {
	run := Runner{Program: "tuios"}
	cmd := run.command(ClaudeCode, 3)
	payload := `{"hook_event_name":"Stop","session_id":"x","cwd":"/tmp"}`

	out, code := runSh(t, cmd, "/usr/bin:/bin", payload)
	if code != 0 || out != "" {
		t.Fatalf("without tuios: rc=%d out=%q, want rc=0 and no output", code, out)
	}

	dir := t.TempDir()
	fakeTuios(t, dir, "tuios", "RAN")
	out, code = runSh(t, cmd, dir+":/usr/bin:/bin", payload)
	if code != 0 || !strings.Contains(out, "RAN") {
		t.Fatalf("with tuios: rc=%d out=%q, want the hook to run", code, out)
	}
}

// TestStatusLineKeepsTheChainWithoutTheProgram: the person's own status line
// is the line's $1, and losing tuios must not take it down with them.
func TestStatusLineKeepsTheChainWithoutTheProgram(t *testing.T) {
	run := Runner{Program: "tuios"}
	cmd := run.statusLineCommand("echo CHAINED")

	out, code := runSh(t, cmd, "/usr/bin:/bin", `{"model":{"display_name":"Opus"}}`)
	if code != 0 || strings.TrimSpace(out) != "CHAINED" {
		t.Fatalf("without tuios: rc=%d out=%q, want the chained command alone", code, out)
	}

	dir := t.TempDir()
	fakeTuios(t, dir, "tuios", "RAN")
	out, code = runSh(t, cmd, dir+":/usr/bin:/bin", `{"model":{"display_name":"Opus"}}`)
	if code != 0 || !strings.Contains(out, "RAN") {
		t.Fatalf("with tuios: rc=%d out=%q, want the wrapper to reach tuios", code, out)
	}
}
