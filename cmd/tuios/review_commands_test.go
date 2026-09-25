package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

func TestParseNoteTarget(t *testing.T) {
	for _, tc := range []struct {
		spec string
		hunk bool
		path string
		line int
		bad  bool
	}{
		{spec: "api/retry.go:42", path: "api/retry.go", line: 42},
		{spec: "c:/x.go:3", path: "c:/x.go", line: 3},
		{spec: "api/retry.go", hunk: true, path: "api/retry.go"},
		{spec: "api/retry.go", bad: true},
		{spec: "api/retry.go:0", bad: true},
		{spec: "api/retry.go:x", bad: true},
		{spec: ":3", bad: true},
	} {
		path, line, err := parseNoteTarget(tc.spec, tc.hunk)
		if tc.bad {
			if err == nil {
				t.Errorf("%q parsed as %q:%d, want an error", tc.spec, path, line)
			}
			continue
		}
		if err != nil || path != tc.path || line != tc.line {
			t.Errorf("%q = %q:%d (%v)", tc.spec, path, line, err)
		}
	}
}

// TestPrintReviewCleansWhatItPrints: text from the repository and from a note
// reaches the terminal with its control characters left out.
func TestPrintReviewCleansWhatItPrints(t *testing.T) {
	res := reviewDiffResult{
		Session: "work", Window: "4be1c09a-0000", Base: "main", BaseSHA: "0123456789abcdef",
		Files: []review.File{{Path: "a\x1b]0;x\x07.go", Status: "M", Added: 1, Hunks: []review.Hunk{{
			Header: "@@ -1 +1,2 @@", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
			Lines: []review.Line{{Op: review.OpContext, Old: 1, New: 1, Text: "x"}, {Op: review.OpAdd, New: 2, Text: "evil\x1b[2J"}},
		}}}},
		Totals: review.Totals{Files: 1, Added: 1},
		Notes:  []review.Note{{ID: "n1", Path: "a\x1b]0;x\x07.go", Side: "new", Line: 2, Text: "see\x1b[31m this", By: "human"}},
	}
	var out bytes.Buffer
	if err := printReview(&out, res, "", false); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Errorf("a control character reached the output: %q", out.String())
	}
	if !strings.Contains(out.String(), "against main (0123456)") || !strings.Contains(out.String(), "> note n1 (human): see[31m this") {
		t.Errorf("output =\n%s", out.String())
	}
}
