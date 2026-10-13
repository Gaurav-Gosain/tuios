package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// railHeaderRow is the row of the rail whose first word is label, or -1.
func railHeaderRow(s tuitest.Screen, label string) int {
	_, rows := s.Size()
	for r := range rows {
		if f := strings.Fields(railRowText(s, r)); len(f) > 0 && f[0] == label {
			return r
		}
	}
	return -1
}

// railRowText is a rail row's text without the focus mark, the edge rule or
// the padding.
func railRowText(s tuitest.Screen, row int) string {
	line := strings.TrimSuffix(strings.TrimRight(railLine(s, row), " "), "│")
	return strings.TrimSpace(strings.TrimPrefix(line, "▎"))
}

// railGitOneLineConfig squeezes the git section to one line for its two rows.
// The custom section's share takes all but one of the lines the rail has, git
// keeps its floor of two, the sessions section takes its one row back from
// the shares, and the rail gives back the line it is then short from the last
// share first, which is git's.
const railGitOneLineConfig = `[appearance.sidebar]
enabled = true
position = "left"
width = 28
sections = "sessions,custom:99,git:1"
[appearance.sidebar.custom]
command = "i=1; while [ $i -le 70 ]; do printf 'FEED-%02d\\n' \"$i\"; i=$((i+1)); done"
`

// railGitOneLine attaches a client with the git section on one line and the
// focused pane in a repository on a branch the test names.
func railGitOneLine(t *testing.T, branch string) *tuitest.Terminal {
	t.Helper()
	term, base := railClient(t, "gitline", railGitOneLineConfig, startOpts{cols: 120, rows: 40})
	dir := filepath.Join(base, "repo")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", dir, "init", "-q"},
		{"-C", dir, "branch", "-M", branch},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railHeaderRow(s, "git") >= 0 }, uiTimeout); err != nil {
		t.Fatalf("the git section never appeared: %v\n%s", err, term.Snapshot())
	}
	return term
}

// TestRailGitOneLineOwnsUpToItsRows: a git section given one line for its
// repository and branch rows spends that line on "+2", as every other section
// does, rather than drawing its header alone and leaving the line out.
//
// How it could pass wrongly, and what stops it: the section given two lines
// after all, so nothing is hidden. The line under the header must not name
// the repository, which is the first row.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Rail git section on one
// line").
func TestRailGitOneLineOwnsUpToItsRows(t *testing.T) {
	term := railGitOneLine(t, "gitline-branch")
	dir := artifactDir(t)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.HasSuffix(railRowText(s, railHeaderRow(s, "git")+1), "+2")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the git section on one line does not end in +2: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "git-one-line")
	s := term.Screen()
	if under := railRowText(s, railHeaderRow(s, "git")+1); strings.Contains(under, "repo") {
		t.Fatalf("the fixture gave git two lines: %q is under its header\n%s", under, term.Snapshot())
	}
	alive(t, term, "after reading the git section")
}
