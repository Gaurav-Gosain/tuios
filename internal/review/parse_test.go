package review

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseRawNumstat(t *testing.T) {
	raw := ":100644 100644 aaaaaaa bbbbbbb M\x00api/retry.go\x00" +
		":000000 100644 0000000 ccccccc A\x00docs/new.md\x00" +
		":100644 100644 ddddddd eeeeeee R087\x00old/name.go\x00new/name.go\x00" +
		":100644 000000 fffffff 0000000 D\x00gone.txt\x00" +
		":100644 100644 1111111 2222222 M\x00logo.png\x00" +
		":100644 100755 3333333 3333333 T\x00run.sh\x00" +
		":100644 100644 4444444 5555555 C075\x00src.go\x00copy.go\x00" +
		"\n80\t12\tapi/retry.go\x00" +
		"9\t0\tdocs/new.md\x00" +
		"2\t1\t\x00old/name.go\x00new/name.go\x00" +
		"0\t3\tgone.txt\x00" +
		"-\t-\tlogo.png\x00" +
		"0\t0\trun.sh\x00" +
		"4\t0\tcopy.go\x00"
	files, err := ParseRawNumstat(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{Path: "api/retry.go", Status: "M", Added: 80, Removed: 12},
		{Path: "docs/new.md", Status: "A", Added: 9},
		{Path: "new/name.go", OldPath: "old/name.go", Status: "R", Added: 2, Removed: 1},
		{Path: "gone.txt", Status: "D", Removed: 3},
		{Path: "logo.png", Status: "M", Binary: true},
		{Path: "run.sh", Status: "M"},
		{Path: "copy.go", Status: "A", Added: 4},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for i := range want {
		if fmt.Sprintf("%+v", files[i]) != fmt.Sprintf("%+v", want[i]) {
			t.Errorf("file %d = %+v, want %+v", i, files[i], want[i])
		}
	}
}

func TestParseHunkHeader(t *testing.T) {
	cases := []struct {
		in             string
		os, ol, ns, nl int
		bad            bool
	}{
		{in: "@@ -40,7 +40,19 @@ func Do(ctx context.Context", os: 40, ol: 7, ns: 40, nl: 19},
		{in: "@@ -1 +1 @@", os: 1, ol: 1, ns: 1, nl: 1},
		{in: "@@ -0,0 +1,3 @@", os: 0, ol: 0, ns: 1, nl: 3},
		{in: "@@ -5,2 +4,0 @@", os: 5, ol: 2, ns: 4, nl: 0},
		{in: "@@ nonsense @@", bad: true},
		{in: "-40,7 +40,19", bad: true},
		{in: "@@ -a,1 +1,1 @@", bad: true},
	}
	for _, c := range cases {
		os, ol, ns, nl, err := ParseHunkHeader(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q parsed, want an error", c.in)
			}
			continue
		}
		if err != nil || os != c.os || ol != c.ol || ns != c.ns || nl != c.nl {
			t.Errorf("%q = %d %d %d %d (%v)", c.in, os, ol, ns, nl, err)
		}
	}
}

// unifiedFixture is git's unified diff of three files: a modification with a
// blank context line that lost its space, CRLF lines and no newline at the
// end on either side; a binary file; and a new file.
const unifiedFixture = "diff --git a/a.go b/a.go\n" +
	"index 1..2 100644\n" +
	"--- a/a.go\n" +
	"+++ b/a.go\n" +
	"@@ -1,3 +1,4 @@ package a\n" +
	" one\r\n" +
	"\n" +
	"-two\n" +
	"\\ No newline at end of file\n" +
	"+two!\r\n" +
	"+three\n" +
	"\\ No newline at end of file\n" +
	"diff --git a/logo.png b/logo.png\n" +
	"index 3..4 100644\n" +
	"Binary files a/logo.png and b/logo.png differ\n" +
	"diff --git a/--- b/---\n" +
	"new file mode 100644\n" +
	"--- /dev/null\n" +
	"+++ b/---\n" +
	"@@ -0,0 +1,2 @@\n" +
	"+--- not a header\n" +
	"+diff --git inside a hunk\n"

func TestReadUnified(t *testing.T) {
	files := []File{{Path: "a.go"}, {Path: "logo.png", Binary: true}, {Path: "---"}}
	truncated, stopped, err := readUnified(strings.NewReader(unifiedFixture), files, DefaultLimits)
	if err != nil || truncated || stopped {
		t.Fatalf("truncated %v stopped %v err %v", truncated, stopped, err)
	}
	if len(files[0].Hunks) != 1 {
		t.Fatalf("a.go hunks = %+v", files[0].Hunks)
	}
	h := files[0].Hunks[0]
	if h.Header != "@@ -1,3 +1,4 @@ package a" || h.OldStart != 1 || h.NewLines != 4 {
		t.Errorf("hunk = %+v", h)
	}
	want := []Line{
		{Op: OpContext, Old: 1, New: 1, Text: "one"},
		{Op: OpContext, Old: 2, New: 2, Text: ""},
		{Op: OpDelete, Old: 3, Text: "two", NoNewline: true},
		{Op: OpAdd, New: 3, Text: "two!"},
		{Op: OpAdd, New: 4, Text: "three", NoNewline: true},
	}
	if fmt.Sprintf("%+v", h.Lines) != fmt.Sprintf("%+v", want) {
		t.Errorf("lines =\n%+v\nwant\n%+v", h.Lines, want)
	}
	if files[1].Hunks != nil || !files[1].Binary {
		t.Errorf("binary file = %+v", files[1])
	}
	third := files[2].Hunks
	if len(third) != 1 || len(third[0].Lines) != 2 || third[0].Lines[0].Text != "--- not a header" || third[0].Lines[1].Text != "diff --git inside a hunk" {
		t.Errorf("a hunk's lines that look like headers were not read as lines: %+v", third)
	}
}

func TestReadUnifiedLimits(t *testing.T) {
	big := func(name string, n int) string {
		var b strings.Builder
		fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", name, name, name, n)
		for i := range n {
			fmt.Fprintf(&b, "+line %d\n", i)
		}
		return b.String()
	}

	t.Run("lines per file", func(t *testing.T) {
		files := []File{{Path: "huge"}, {Path: "small"}}
		truncated, _, err := readUnified(strings.NewReader(big("huge", 5001)+big("small", 3)), files, DefaultLimits)
		if err != nil || !truncated {
			t.Fatalf("truncated %v err %v", truncated, err)
		}
		if !files[0].Truncated || files[0].Hunks != nil {
			t.Errorf("a 5001-line file = truncated %v with %d hunks", files[0].Truncated, len(files[0].Hunks))
		}
		if files[1].Truncated || len(files[1].Hunks) != 1 || len(files[1].Hunks[0].Lines) != 3 {
			t.Errorf("the file after it = %+v", files[1])
		}
	})
	t.Run("exactly at the line limit", func(t *testing.T) {
		files := []File{{Path: "full"}}
		truncated, _, _ := readUnified(strings.NewReader(big("full", 5000)), files, DefaultLimits)
		if truncated || files[0].Truncated || len(files[0].Hunks[0].Lines) != 5000 {
			t.Errorf("a 5000-line file was truncated")
		}
	})
	t.Run("files", func(t *testing.T) {
		files := []File{{Path: "a"}, {Path: "b"}, {Path: "c"}}
		truncated, stopped, _ := readUnified(strings.NewReader(big("a", 1)+big("b", 1)+big("c", 1)), files, Limits{Files: 2, Bytes: 1 << 20, LinesPerFile: 10})
		if !truncated || !stopped || files[0].Truncated || files[1].Truncated || !files[2].Truncated {
			t.Errorf("truncated %v stopped %v files %+v", truncated, stopped, files)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		files := []File{{Path: "a"}, {Path: "b"}, {Path: "c"}}
		in := big("a", 10) + big("b", 200) + big("c", 1)
		truncated, stopped, _ := readUnified(strings.NewReader(in), files, Limits{Files: 10, Bytes: 600, LinesPerFile: 1000})
		if !truncated || !stopped || files[0].Truncated || !files[1].Truncated || !files[2].Truncated {
			t.Errorf("truncated %v stopped %v files %v %v %v", truncated, stopped, files[0].Truncated, files[1].Truncated, files[2].Truncated)
		}
		if files[1].Hunks != nil {
			t.Errorf("the file past the byte limit kept hunks")
		}
	})
}
