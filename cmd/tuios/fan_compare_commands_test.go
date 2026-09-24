package main

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func intp(n int) *int { return &n }

// TestRenderFanCompareReadsLikeTheListing: one aligned line per attempt with
// its check, and the two commands that come next.
func TestRenderFanCompareReadsLikeTheListing(t *testing.T) {
	now := time.Unix(10_000, 0)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	res := fanCompareResult{
		Group: "fan/retry", Repo: "api", Base: "origin/main",
		Rows: []fanCompareRow{
			{Session: "api-fan-retry", Agent: "claude", State: "done", Files: intp(4), Added: intp(120), Removed: intp(31),
				Verify: &session.FanVerify{Command: "go test ./...", State: session.VerifyPassed, FinishedAt: ago(14 * time.Minute)}},
			{Session: "api-fan-retry-2", Harness: "codex", State: "working", Files: intp(2), Added: intp(40), Removed: intp(3)},
			{Session: "api-fan-retry-3", Agent: "claude", State: "errored", Files: intp(0), Added: intp(0), Removed: intp(0)},
		},
	}
	exit := 1
	res.Rows[2].LastCommand = &struct {
		Cmdline string `json:"cmdline"`
		Exit    *int   `json:"exit"`
		At      int64  `json:"at"`
	}{Cmdline: "make lint", Exit: &exit}

	out := renderFanCompare(res, "", now)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want a header, three rows and the hint:\n%s", len(lines), out)
	}
	if lines[0] != "fan/retry in api, 3 attempts against origin/main" {
		t.Errorf("header = %q", lines[0])
	}
	for i, want := range []string{"go test ./... passed 14m ago", "no check yet", "last command exited 1 (make lint)"} {
		if !strings.HasSuffix(lines[i+1], want) {
			t.Errorf("row %d = %q, want it to end with %q", i+1, lines[i+1], want)
		}
	}
	if !strings.Contains(lines[2], "codex") {
		t.Errorf("a row with no agent name does not fall back to the harness: %q", lines[2])
	}
	// The count columns line up on the right.
	end := func(line, s string) int { return strings.Index(line, s) + len(s) }
	col := end(lines[1], "+120 -31")
	if end(lines[2], "+40 -3") != col || end(lines[3], "+0 -0") != col || end(lines[1], "4 files") != end(lines[2], "2 files") {
		t.Errorf("the line counts do not line up:\n%s", out)
	}
	if !strings.Contains(lines[4], "tuios fan keep <session>") || !strings.Contains(lines[4], "tuios fan diff A B") {
		t.Errorf("hint = %q", lines[4])
	}
}

func TestFanVerifyOutcomeSaysWhatHappened(t *testing.T) {
	cases := map[string]*session.FanVerify{
		"passed in 1.5s":  {State: session.VerifyPassed, StartedAt: 1, FinishedAt: 1 + int64(1500*time.Millisecond)},
		"failed, exit 2":  {State: session.VerifyFailed, Exit: intp(2)},
		"failed: timed":   {State: session.VerifyFailed, Note: "timed out after 5m0s"},
		"no check record": nil,
	}
	for want, v := range cases {
		if got := fanVerifyOutcome(v); !strings.HasPrefix(got, want) {
			t.Errorf("fanVerifyOutcome(%+v) = %q, want it to start %q", v, got, want)
		}
	}
}

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
