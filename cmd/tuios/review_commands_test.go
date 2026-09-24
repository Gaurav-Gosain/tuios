package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewCommandRoundTrip drives tuios review, review note, review notes
// and review send against a real daemon whose pane sits in a throwaway
// repository: the diff shows the change with the note under its line, the
// JSON carries the fields a script reads, and send queues one message.
func TestReviewCommandRoundTrip(t *testing.T) {
	repo := testutil.GitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "api.go"), []byte("package api\n\nfunc Do() {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "api")
	t.Chdir(repo)
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "work"}); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "api.go"), []byte("package api\n\nfunc Do() {\n\tretry()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		root := newRootCommand()
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("tuios %s: %v", strings.Join(args, " "), err)
		}
	}
	run("review", "note", "-s", "work", "api.go:4", "why", "retry", "here?")

	var out bytes.Buffer
	if err := runReview(&out, "work", "", reviewOptions{context: 3}, false); err != nil {
		t.Fatalf("review: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"Review of work, pane ",
		"uncommitted changes: 1 file, +1 -0, 1 note",
		"M  api.go  +1 -0",
		"@@ -1,4 +1,5 @@",
		"      4 +     retry()",
		"> note n1 (shell): why retry here?",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("tuios review lacks %q:\n%s", want, text)
		}
	}

	raw := captureStdout(t, func() {
		if err := runReview(&out, "work", "", reviewOptions{context: 3, stat: true}, true); err != nil {
			t.Errorf("review --json: %v", err)
		}
	})
	var res map[string]any
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("review --json did not print JSON: %v\n%s", err, raw)
	}
	for _, key := range []string{"session", "window", "worktree", "base", "base_sha", "tree_sha", "files", "totals", "truncated", "notes", "untrusted"} {
		if _, ok := res[key]; !ok {
			t.Errorf("review --json lacks %q: %v", key, res)
		}
	}

	out.Reset()
	if err := runReviewNote(&out, "work", "", map[string]any{"action": "list"}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "api.go:4") || !strings.Contains(out.String(), "unsent") {
		t.Errorf("review notes = %q", out.String())
	}

	windows, err := c.Call("list-windows", map[string]any{"session": "work"})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Windows []struct {
			ID string `json:"window_id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(windows, &list); err != nil || len(list.Windows) == 0 {
		t.Fatalf("list-windows = %s (%v)", windows, err)
	}
	if _, err := c.Call("set-agent-state", map[string]any{"session": "work", "window": list.Windows[0].ID, "state": "working"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runReviewSend(&out, "work", "", nil, false, false); err != nil {
		t.Fatalf("review send: %v", err)
	}
	if !strings.Contains(out.String(), "1 review note in one message. Queued q1 for the focused pane. It is typed when the agent comes to rest.") {
		t.Errorf("review send = %q", out.String())
	}
}

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
