package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain runs resume-wrap itself when the test binary is started as it.
func TestMain(m *testing.M) {
	if os.Getenv("RESUME_WRAP_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// TestCommandNeverGetsTheToken runs a command under resume-wrap with a token
// in the environment. The command runs, sees no token, and its exit code is
// resume-wrap's.
func TestCommandNeverGetsTheToken(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	cmd := exec.Command(os.Args[0], "sh", "-c", "printf 'token=[%s]' \"$OSC7577_TOKEN\"; exit 3")
	cmd.Env = append(os.Environ(), "RESUME_WRAP_TEST_MAIN=1", "OSC7577_TOKEN=dummy-token-replace-me")
	out, err := cmd.CombinedOutput()
	if got := string(out); !strings.Contains(got, "token=[]") {
		t.Errorf("the command saw the token or did not run: %q", got)
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 3 {
		t.Errorf("resume-wrap ended with %v, want exit code 3", err)
	}
}

// TestNoCommandIsAUsageError ends with code 2 and the usage line.
func TestNoCommandIsAUsageError(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "RESUME_WRAP_TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "Usage: resume-wrap") {
		t.Errorf("resume-wrap with no command: %v %q, want exit code 2 and the usage", err, out)
	}
}
