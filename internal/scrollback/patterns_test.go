package scrollback

import (
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestExtractPathsUsesBothPatterns pins what the path and line:col patterns
// find. They compile on first use, so a pattern that no longer compiled would
// only show up here, not when the package loads.
func TestExtractPathsUsesBothPatterns(t *testing.T) {
	got := ExtractPaths("error at /src/main.go:12:3, see ./docs/a.md:7 and https://example.test/x?y=1 or /src/main.go:12:3 again")
	want := []PathBlock{
		{Raw: "/src/main.go:12:3", Path: "/src/main.go", Line: 12, Col: 3},
		{Raw: "./docs/a.md:7", Path: "./docs/a.md", Line: 7},
		{Raw: "https://example.test/x?y=1", Path: "https://example.test/x?y=1", IsURL: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractPaths:\n got %+v\nwant %+v", got, want)
	}
}

// TestParseBlocksByPromptPatterns drives the regex fallback, which uses the
// other three patterns: a prompt is found, and a listing line that looks like
// a prompt followed by file details is not taken for a command.
func TestParseBlocksByPromptPatterns(t *testing.T) {
	term := vt.NewWithScrollback(60, 12, 100)
	for _, line := range []string{
		"user@host$ ls -l",
		"total 48",
		"-rw-r--r--  1 me  staff  120 Jan 3 notes.txt",
		"$ echo hi",
		"hi",
		"$ notes.txt 29.1 MB",
	} {
		_, _ = term.Write([]byte(line + "\r\n"))
	}
	blocks := ParseBlocks(term)
	var cmds []string
	for _, b := range blocks {
		if b.Method != "regex" {
			t.Fatalf("block %q parsed by %q, want the regex fallback", b.Command, b.Method)
		}
		cmds = append(cmds, b.Command)
	}
	if want := []string{"echo hi", "ls -l"}; !reflect.DeepEqual(cmds, want) {
		t.Fatalf("commands %q, want %q", cmds, want)
	}
}
