package tuie2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestNoNewBareSleeps holds the count of time.Sleep calls per test file to
// sleepAllowance. A test waits on a condition with waitUntil, proves a state
// holds with stayTrue, and pauses with pause only when nothing can be
// observed. The table may only fall: a file over its number has a new sleep, and
// a file under it has not lowered the number it earned.
//
// It reads the source, like the checks in internal/lint, and counts only code,
// so a comment that names the call does not count. wait_test.go holds the
// helpers and is the one file outside the table.
func TestNoNewBareSleeps(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		if f == "wait_test.go" || f == "sleep_allowance_test.go" || f == "sleep_ratchet_test.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for line := range strings.SplitSeq(string(src), "\n") {
			if code, _, _ := strings.Cut(line, "//"); strings.Contains(code, "time.Sleep(") {
				n++
			}
		}
		switch allowed := sleepAllowance[f]; {
		case n > allowed:
			t.Errorf("%s has %d time.Sleep calls, allowed %d: wait on the condition with waitUntil, prove it holds with stayTrue, or pause with a reason", f, n, allowed)
		case n < allowed:
			t.Errorf("%s has %d time.Sleep calls and the table allows %d: lower the number in sleep_allowance_test.go", f, n, allowed)
		}
	}
}
