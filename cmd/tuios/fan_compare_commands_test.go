package main

import (
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestFanVerifyKeepsTheQuotingOfItsArguments: several words after -- reach
// the check as those words, so a pattern holding | is one argument and not a
// pipe, while one word is a shell line and keeps its &&.
func TestFanVerifyKeepsTheQuotingOfItsArguments(t *testing.T) {
	var got []string
	fanVerifyRun = func(target, command string, _ time.Duration, _, _ bool) error {
		got = append(got, target, command)
		return nil
	}
	t.Cleanup(func() { fanVerifyRun = runFanVerify })

	run := func(args ...string) string {
		t.Helper()
		got = nil
		cmd := newFanVerifyCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("fan verify %v: %v", args, err)
		}
		if len(got) != 2 || got[0] != "s" {
			t.Fatalf("fan verify %v sent %v", args, got)
		}
		return got[1]
	}
	if line := run("s", "--", "go", "test", "./..."); line != "go test ./..." {
		t.Errorf("a plain command became %q", line)
	}
	if line := run("s", "--", "make lint && make test"); line != "make lint && make test" {
		t.Errorf("a shell line became %q", line)
	}
	line := run("s", "--", "printf", `%s\n`, "TestA|TestB", "it's", "", "$HOME")
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("sh -c %q: %v", line, err)
	}
	if want := "TestA|TestB\nit's\n\n$HOME\n"; string(out) != want {
		t.Errorf("sh -c %q printed %q, want each word as given %q", line, out, want)
	}
}
