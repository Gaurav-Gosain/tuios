package tuie2e

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// TestNoNewBareSleeps fails when a change adds a time.Sleep to a test file. A
// test waits on a condition with waitUntil, proves a state holds with
// stayTrue, and pauses with pause only when nothing can be observed.
//
// It reads the change, not the tree, so there is no table of counts for
// concurrent pull requests to race on. A file that gains more time.Sleep lines
// than it loses fails, naming the file and the count. The change is the diff
// from the base: HEAD^1 in CI, where a pull request is checked out as the merge
// of its head into the base with two commits of history, and the merge base
// with origin/main elsewhere (TUIOS_E2E_BASE names another ref). Outside CI a
// missing git or ref skips the check; in CI it fails, because a check that
// skips there protects nothing.
func TestNoNewBareSleeps(t *testing.T) {
	ci := os.Getenv("GITHUB_ACTIONS") != ""
	skip := func(format string, args ...any) {
		t.Helper()
		if ci {
			t.Fatalf(format+": the e2e job must check out at least two commits (fetch-depth: 2) or fetch origin/main", args...)
		}
		t.Skipf(format, args...)
	}
	base := os.Getenv("TUIOS_E2E_BASE")
	switch {
	case base != "":
	case ci:
		base = "HEAD^1"
	default:
		mb, err := exec.Command("git", "merge-base", "origin/main", "HEAD").Output()
		if err != nil {
			skip("no merge base with origin/main: %v", err)
		}
		base = strings.TrimSpace(string(mb))
	}
	diff, err := exec.Command("git", "diff", "--no-color", "-U0", base, "--", ".").Output()
	if err != nil {
		skip("git diff %s failed: %v", base, err)
	}
	growth := sleepGrowth(string(diff))
	var files []string
	for f, n := range growth {
		if n > 0 {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		t.Errorf("%s adds %d more time.Sleep than it removes: wait on the condition with waitUntil, prove it holds with stayTrue, or pause with a reason", f, growth[f])
	}
}

// sleepGrowth reads a unified diff and returns, for each *_test.go file other
// than the two that hold the helpers, the time.Sleep( calls added minus the ones removed. Only
// code counts: a comment that names the call does not.
func sleepGrowth(diff string) map[string]int {
	growth := map[string]int{}
	file := ""
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if file == "/dev/null" || !strings.HasSuffix(file, "_test.go") || strings.HasSuffix(file, "wait_test.go") || strings.HasSuffix(file, "sleep_ratchet_test.go") {
				file = ""
			}
		case strings.HasPrefix(line, "--- "):
		case file == "":
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"):
			code, _, _ := strings.Cut(line[1:], "//")
			if strings.Contains(code, "time.Sleep(") {
				if line[0] == '+' {
					growth[file]++
				} else {
					growth[file]--
				}
			}
		}
	}
	return growth
}

// TestSleepGrowthReadsTheDiff holds the reading of a diff: an added sleep is
// growth, a removed one is credit in the same file, a comment is not code, and
// the helper file is exempt.
func TestSleepGrowthReadsTheDiff(t *testing.T) {
	diff := strings.Join([]string{
		"--- a/e2e/tui/a_test.go", "+++ b/e2e/tui/a_test.go", "@@ -1 +1,2 @@",
		"-\ttime.Sleep(time.Second)", "+\twaitUntil(t, \"x\", uiTimeout, probe)", "+\ttime.Sleep(time.Second)", "+\ttime.Sleep(time.Second)",
		"--- a/e2e/tui/b_test.go", "+++ b/e2e/tui/b_test.go", "@@ -1 +1 @@",
		"+\t// time.Sleep(1) is named here only", "-\ttime.Sleep(1)",
		"--- a/e2e/tui/wait_test.go", "+++ b/e2e/tui/wait_test.go", "@@ -1 +1 @@",
		"+\ttime.Sleep(d)",
	}, "\n")
	got := sleepGrowth(diff)
	if got["e2e/tui/a_test.go"] != 1 {
		t.Errorf("a_test.go grew by %d, want 1", got["e2e/tui/a_test.go"])
	}
	if got["e2e/tui/b_test.go"] != -1 {
		t.Errorf("b_test.go grew by %d, want -1", got["e2e/tui/b_test.go"])
	}
	if _, ok := got["e2e/tui/wait_test.go"]; ok {
		t.Error("wait_test.go is counted")
	}
}
