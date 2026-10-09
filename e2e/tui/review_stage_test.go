package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewStageByHunkAndLine drives review-diff's index modes and
// review-stage the way tuios-gpui does: through a real gui-bridge, whose verb
// proxy adds the person's nonce, against a real daemon, on a pane in a scratch
// repository.
//
// One file has two hunks of unstaged change. The test stages the first hunk
// whole, then one added line of the second, and checks git diff --cached
// byte for byte after each. It unstages that line again from the staged
// diff, undoes the unstage, and redoes it with the undo object the undo
// returned. An undo after a manual git add is refused with index_changed and
// changes nothing. An untracked file is listed as U. One of its lines is
// staged as a new file, and unstaging that hunk takes the file out of the
// index. Then it is staged whole and unstaged by its undo. The working files keep their bytes from start to end.
//
// Every verb sent and answered is in the bridge's log, and the cached diff
// after each step is in transcript.txt, both in the artifact directory.
//
// Negative control (NEGATIVE_CONTROLS.md): with SelectionPatch keeping every
// unselected added line, the stage of one line stages both, and the cached
// diff check after it fails.
func TestReviewStageByHunkAndLine(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)

	var log strings.Builder
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(log.String()), 0o644); err != nil {
			t.Logf("save the transcript: %v", err)
		}
	})

	lines := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"}
	file := filepath.Join(repo, "list.txt")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", "list.txt")
	testutil.Git(t, repo, "commit", "-q", "-m", "list")

	// Hunk 1 changes line 2. Hunk 2 adds two lines after line 10.
	changed := slices.Clone(lines)
	changed[1] = "TWO"
	changed = slices.Insert(changed, 10, "new a", "new b")
	work := strings.Join(changed, "\n") + "\n"
	if err := os.WriteFile(file, []byte(work), 0o644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(repo, "notes.txt")
	if err := os.WriteFile(untracked, []byte("a note\nanother\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workBytes := func() {
		t.Helper()
		if got := readFile(t, file); got != work {
			t.Fatalf("list.txt changed in the working tree:\n%s", got)
		}
		if got := readFile(t, untracked); got != "a note\nanother\n" {
			t.Fatalf("notes.txt changed in the working tree: %q", got)
		}
	}

	if out, err := tuiosCLIIn(t, base, repo, "new", "rs", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	b := startBridgeWith(t, base, bridgeOpts{args: []string{"--session", "rs"}, cols: 120, rows: 40, keepDaemon: true, name: "rs"})

	type line struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	}
	type hunk struct {
		Header string `json:"header"`
		Lines  []line `json:"lines"`
	}
	type file1 struct {
		Path   string `json:"path"`
		Status string `json:"status"`
		Hunks  []hunk `json:"hunks"`
	}
	type undoObj = map[string]any
	diff := func(mode string) map[string]file1 {
		t.Helper()
		res := b.verb("review-diff", map[string]any{"session": "rs", "index": mode})
		if !res.OK {
			t.Fatalf("review-diff index %s: %s %s", mode, res.Code, res.Error)
		}
		var d struct {
			Base  string  `json:"base"`
			Index string  `json:"index"`
			Files []file1 `json:"files"`
		}
		if err := json.Unmarshal(res.Result, &d); err != nil {
			t.Fatal(err)
		}
		wantBase := map[string]string{"unstaged": "index", "staged": "HEAD"}[mode]
		if d.Base != wantBase || d.Index != mode {
			t.Fatalf("review-diff index %s: base %q index %q, want %q %q", mode, d.Base, d.Index, wantBase, mode)
		}
		out := map[string]file1{}
		for _, f := range d.Files {
			out[f.Path] = f
		}
		fmt.Fprintf(&log, "## review-diff index %s\n%s\n\n", mode, res.Result)
		return out
	}
	stage := func(params map[string]any) undoObj {
		t.Helper()
		params["session"] = "rs"
		res := b.verb("review-stage", params)
		if !res.OK {
			t.Fatalf("review-stage %v: %s %s", params, res.Code, res.Error)
		}
		var r struct {
			Action string  `json:"action"`
			Undo   undoObj `json:"undo"`
		}
		if err := json.Unmarshal(res.Result, &r); err != nil || r.Action != params["action"] || r.Undo == nil {
			t.Fatalf("review-stage result %s (%v)", res.Result, err)
		}
		fmt.Fprintf(&log, "## review-stage %v\n%s\n\n", params["action"], res.Result)
		return r.Undo
	}
	// cached is git diff --cached of list.txt without its index line, whose
	// blob names say nothing a reader can check by eye.
	cached := func(step, want string) {
		t.Helper()
		raw := testutil.Git(t, repo, "diff", "--cached", "--no-color", "--", "list.txt")
		var kept []string
		for l := range strings.SplitSeq(raw, "\n") {
			if !strings.HasPrefix(l, "index ") {
				kept = append(kept, l)
			}
		}
		got := strings.Join(kept, "\n") + "\n" // testutil.Git trims the last newline
		fmt.Fprintf(&log, "## git diff --cached after %s\n%s\n", step, got)
		if got != want {
			t.Fatalf("after %s, git diff --cached =\n%s\nwant\n%s", step, got, want)
		}
		workBytes()
	}
	lineIndex := func(h hunk, op, text string) int {
		t.Helper()
		for i, l := range h.Lines {
			if l.Op == op && l.Text == text {
				return i
			}
		}
		t.Fatalf("hunk %s has no %s line %q", h.Header, op, text)
		return -1
	}

	// The unstaged diff: two hunks of list.txt, and notes.txt untracked.
	un := diff("unstaged")
	f := un["list.txt"]
	if f.Status != "M" || len(f.Hunks) != 2 {
		t.Fatalf("unstaged list.txt = %+v, want M with two hunks", f)
	}
	if un["notes.txt"].Status != "U" {
		t.Fatalf("unstaged notes.txt = %+v, want U", un["notes.txt"])
	}

	head := "diff --git a/list.txt b/list.txt\n--- a/list.txt\n+++ b/list.txt\n"
	hunk1 := "@@ -1,5 +1,5 @@\n one\n-two\n+TWO\n three\n four\n five\n"

	// Stage hunk 1 whole.
	stage(map[string]any{"action": "stage", "path": "list.txt", "hunk": f.Hunks[0].Header})
	cached("staging hunk 1", head+hunk1)

	// The unstaged diff now holds hunk 2 only. Stage its second added line.
	f = diff("unstaged")["list.txt"]
	if len(f.Hunks) != 1 {
		t.Fatalf("after staging hunk 1, unstaged list.txt = %+v, want one hunk", f)
	}
	stage(map[string]any{"action": "stage", "path": "list.txt", "hunk": f.Hunks[0].Header, "lines": []int{lineIndex(f.Hunks[0], "add", "new b")}})
	bothStaged := head + "@@ -1,5 +1,5 @@\n one\n-two\n+TWO\n three\n four\n five\n@@ -8,5 +8,6 @@ seven\n eight\n nine\n ten\n+new b\n eleven\n twelve\n"
	cached("staging the line new b", bothStaged)
	if f = diff("unstaged")["list.txt"]; len(f.Hunks) != 1 || lineIndex(f.Hunks[0], "add", "new a") < 0 {
		t.Fatalf("after staging new b, unstaged list.txt = %+v", f)
	}

	// Unstage that line from the staged diff.
	st := diff("staged")["list.txt"]
	if len(st.Hunks) != 2 {
		t.Fatalf("staged list.txt = %+v, want two hunks", st)
	}
	unstageUndo := stage(map[string]any{"action": "unstage", "path": "list.txt", "hunk": st.Hunks[1].Header, "lines": []int{lineIndex(st.Hunks[1], "add", "new b")}})
	cached("unstaging the line new b", head+hunk1)

	// Undo the unstage, then redo it with the undo object the undo returned.
	redo := stage(map[string]any{"action": "undo", "undo": unstageUndo})
	cached("undoing the unstage", bothStaged)
	again := stage(map[string]any{"action": "undo", "undo": redo})
	cached("redoing the unstage", head+hunk1)

	// After a manual git add the index is not as the redo left it, so its
	// undo is refused, and changes nothing.
	testutil.Git(t, repo, "add", "list.txt")
	all := head + "@@ -1,5 +1,5 @@\n one\n-two\n+TWO\n three\n four\n five\n@@ -8,5 +8,7 @@ seven\n eight\n nine\n ten\n+new a\n+new b\n eleven\n twelve\n"
	cached("git add", all)
	res := b.verb("review-stage", map[string]any{"session": "rs", "action": "undo", "undo": again})
	fmt.Fprintf(&log, "## review-stage undo after git add\n%s %s\n\n", res.Code, res.Error)
	if res.OK || res.Code != "index_changed" {
		t.Fatalf("undo after git add = ok %v code %q (%s), want index_changed", res.OK, res.Code, res.Error)
	}
	cached("the refused undo", all)

	// The untracked file: one line staged as a new file, then that hunk
	// unstaged whole, which removes the file from the index again.
	nf := diff("unstaged")["notes.txt"]
	if nf.Status != "U" || len(nf.Hunks) != 1 {
		t.Fatalf("unstaged notes.txt = %+v, want U with one hunk", nf)
	}
	stage(map[string]any{"action": "stage", "path": "notes.txt", "hunk": nf.Hunks[0].Header, "lines": []int{lineIndex(nf.Hunks[0], "add", "a note")}})
	if got := testutil.Git(t, repo, "show", ":notes.txt"); got != "a note" {
		t.Fatalf("after staging one line of notes.txt, the index holds %q", got)
	}
	nf = diff("staged")["notes.txt"]
	if nf.Status != "A" || len(nf.Hunks) != 1 {
		t.Fatalf("staged notes.txt = %+v, want A with one hunk", nf)
	}
	stage(map[string]any{"action": "unstage", "path": "notes.txt", "hunk": nf.Hunks[0].Header})
	if got := testutil.Git(t, repo, "ls-files", "--", "notes.txt"); got != "" {
		t.Fatalf("after unstaging all of notes.txt, it is in the index: %q", got)
	}

	// Staged whole, then unstaged by its undo.
	addNotes := stage(map[string]any{"action": "stage", "path": "notes.txt"})
	if got := testutil.Git(t, repo, "diff", "--cached", "--name-status", "--", "notes.txt"); got != "A\tnotes.txt" {
		t.Fatalf("after staging notes.txt, git diff --cached --name-status = %q", got)
	}
	if entries, _ := addNotes["entries"].([]any); len(entries) != 1 || entries[0].(map[string]any)["absent"] != true {
		t.Fatalf("the undo of staging notes.txt = %v, want notes.txt absent before", addNotes)
	}
	stage(map[string]any{"action": "undo", "undo": addNotes})
	if got := testutil.Git(t, repo, "ls-files", "--", "notes.txt"); got != "" {
		t.Fatalf("after undoing the stage, notes.txt is in the index: %q", got)
	}
	if un = diff("unstaged"); un["notes.txt"].Status != "U" {
		t.Fatalf("after the undo, unstaged notes.txt = %+v, want U", un["notes.txt"])
	}
	workBytes()
}
