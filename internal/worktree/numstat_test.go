package worktree

import (
	"testing"
)

func TestParseNumstatCountsFilesLinesAndBinaries(t *testing.T) {
	out := "3\t1\ta.go\n-\t-\tlogo.png\n10\t0\tdocs/{old => new}.md\r\n\n"
	got := ParseNumstat(out)
	want := Numstat{Files: 3, Added: 13, Removed: 1}
	if got != want {
		t.Errorf("ParseNumstat = %+v, want %+v", got, want)
	}
	if got := ParseNumstat(""); got != (Numstat{}) {
		t.Errorf("ParseNumstat of nothing = %+v, want zero", got)
	}
}
