package lint

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var testDirPart = regexp.MustCompile(`(^|/)Test[A-Z][^/]*/`)

// TestNoE2EFramesInGit keeps the frames of an e2e run out of the tree. A run
// with TUIOS_E2E_FRAMES set to a relative path writes them under e2e/tui, one
// folder per test, and 70 of them (2.7 MB) once went in with a commit as
// e2e/tui/1/. Rejected: a tracked file under e2e/tui in a folder named after a
// test. The .gitignore line for the same folders keeps git add from staging
// them.
func TestNoE2EFramesInGit(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files", "--", "e2e/tui").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	if bad := framePaths(string(out)); len(bad) > 0 {
		t.Errorf("git tracks %d e2e frame files; remove them with git rm -r and keep TUIOS_E2E_FRAMES outside the checkout:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
}

// TestFramePathsCatchesATestFolder feeds the check a frame path and paths it
// must allow.
func TestFramePathsCatchesATestFolder(t *testing.T) {
	got := framePaths("e2e/tui/1/TestDropPreviewFillWashesTheZone/1-before.txt\n" +
		"e2e/tui/frames/TestX/a.png\n" +
		"e2e/tui/testdata/a.txt\n" +
		"e2e/tui/drop_test.go\n" +
		"e2e/tui/TestdataNotes.md\n")
	want := []string{"e2e/tui/1/TestDropPreviewFillWashesTheZone/1-before.txt", "e2e/tui/frames/TestX/a.png"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("framePaths gave %q, want %q", got, want)
	}
}

func framePaths(lsFiles string) []string {
	var bad []string
	for p := range strings.SplitSeq(strings.TrimSpace(lsFiles), "\n") {
		if testDirPart.MatchString(strings.TrimPrefix(p, "e2e/tui/")) {
			bad = append(bad, p)
		}
	}
	return bad
}
