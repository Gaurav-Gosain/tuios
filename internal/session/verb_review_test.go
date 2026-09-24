package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// reviewFixture is a daemon whose panes start in a throwaway repository, with
// a session work of two windows, a verb connection from outside every pane,
// and the repository. The queue looks at a pane 50 ms after it comes to rest.
func reviewFixture(t *testing.T) (d *Daemon, sp, repo string, sess *Session, c *verbConn, a, b string) {
	t.Helper()
	repo = testutil.GitRepo(t)
	writeIn(t, repo, "api.go", "package api\n\nfunc Do() error {\n\tif err == nil {\n\t\treturn nil\n\t}\n\treturn err\n}\n")
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "commit", "-q", "-m", "api")
	t.Chdir(repo)
	d, sp = startTestDaemon(t)
	d.queue.rest = 50 * time.Millisecond
	sess, a, b = twoWindowSession(t, d, "work")
	return d, sp, repo, sess, dialVerb(t, sp), a, b
}

func notesOf(t *testing.T, res map[string]any) []map[string]any {
	t.Helper()
	raw, _ := res["notes"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, n := range raw {
		out = append(out, n.(map[string]any))
	}
	return out
}

func filesOf(t *testing.T, res map[string]any) map[string]map[string]any {
	t.Helper()
	raw, _ := res["files"].([]any)
	out := map[string]map[string]any{}
	for _, f := range raw {
		m := f.(map[string]any)
		out[m["path"].(string)] = m
	}
	return out
}

// queuedText is the text of the first entry queued for window.
func queuedText(d *Daemon, window string) string {
	d.queue.mu.Lock()
	defer d.queue.mu.Unlock()
	if pq := d.queue.panes[window]; pq != nil && len(pq.entries) > 0 {
		return pq.entries[0].text
	}
	return ""
}

// TestReviewDiffReadsThePanesRepository: the diff of the pane's repository,
// uncommitted and untracked work included, with nothing in the repository
// changed by reading it.
func TestReviewDiffReadsThePanesRepository(t *testing.T) {
	_, _, repo, _, c, a, _ := reviewFixture(t)
	writeIn(t, repo, "api.go", "package api\n\nfunc Do() error {\n\terr := f()\n\tif err == nil {\n\t\treturn nil\n\t}\n\treturn err\n}\n")
	writeIn(t, repo, "notes.txt", "one\ntwo\n")
	status := testutil.Git(t, repo, "status", "--porcelain", "--untracked-files=all")

	res := result(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a}))
	if res["type"] != "review_diff" || res["untrusted"] != true || res["window"] != a || res["session"] != "work" {
		t.Errorf("header = %v", res)
	}
	// No upstream and no recorded base: HEAD, uncommitted only.
	if res["base"] != "HEAD" || res["uncommitted"] != true || res["base_sha"] != testutil.Git(t, repo, "rev-parse", "HEAD") {
		t.Errorf("base = %v %v %v", res["base"], res["uncommitted"], res["base_sha"])
	}
	if res["worktree"] != canonRoot(repo) {
		t.Errorf("worktree = %v, want %s", res["worktree"], canonRoot(repo))
	}
	files := filesOf(t, res)
	if f := files["api.go"]; f == nil || f["status"] != "M" || f["added"] != 1.0 {
		t.Errorf("api.go = %v", f)
	}
	if f := files["notes.txt"]; f == nil || f["status"] != "U" || f["added"] != 2.0 {
		t.Errorf("notes.txt = %v", f)
	}
	if got := testutil.Git(t, repo, "status", "--porcelain", "--untracked-files=all"); got != status {
		t.Errorf("review-diff changed git status:\n%s\nto\n%s", status, got)
	}

	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a, "base": "no-such-branch"}), ErrVerbInvalidParams, "a base that is not a commit")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a, "base": "--output=x"}), ErrVerbInvalidParams, "a base that reads as an option")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a, "paths": []string{"../outside"}}), ErrVerbInvalidParams, "a path that leaves the repository")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a, "against": "work"}), ErrVerbInvalidParams, "against a session that is not a fan")
}

// TestReviewDiffWithNoRepository: a pane outside every repository is
// not_repo, and nothing runs.
func TestReviewDiffWithNoRepository(t *testing.T) {
	t.Chdir(t.TempDir())
	d, sp := startTestDaemon(t)
	_, a, _ := twoWindowSession(t, d, "work")
	c := dialVerb(t, sp)
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a}), ErrVerbNotRepo, "a pane with no repository")
	mustRefuse(t, callP(c, t, "review-note", map[string]any{"action": "list", "session": "work", "window": a}), ErrVerbNotRepo, "notes on a pane with no repository")
}

// TestReviewDiffOfAWorktreeSessionUsesItsBase: a worktree session is diffed
// against the base it was made from, commits and uncommitted work together,
// and against diffs two attempts of a fan with each other.
func TestReviewDiffOfAWorktreeSessionUsesItsBase(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/retry", 2, "")
	one := worktreePath(t, d, names[0])
	writeIn(t, one, "README", "hello\ncommitted\n")
	testutil.Git(t, one, "commit", "-q", "-am", "change")
	writeIn(t, one, "new.txt", "a\n")

	res := result(t, callP(c, t, "review-diff", map[string]any{"session": names[0]}))
	if res["base"] != "main" || res["uncommitted"] != false {
		t.Errorf("base = %v uncommitted %v, want the fan's base", res["base"], res["uncommitted"])
	}
	files := filesOf(t, res)
	if len(files) != 2 || files["README"] == nil || files["new.txt"] == nil {
		t.Errorf("files = %v", files)
	}

	vs := result(t, callP(c, t, "review-diff", map[string]any{"session": names[1], "against": names[0]}))
	if vs["against"] != names[0] || vs["base"] != "" {
		t.Errorf("against = %v base %v", vs["against"], vs["base"])
	}
	// What the second attempt has that the first does not: neither change.
	vf := filesOf(t, vs)
	if f := vf["new.txt"]; f == nil || f["status"] != "D" {
		t.Errorf("against: new.txt = %v", f)
	}
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": names[1], "against": names[1]}), ErrVerbInvalidParams, "against itself")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"session": names[1], "against": names[0], "base": "main"}), ErrVerbInvalidParams, "against with a base")

	// A note on the worktree goes when the worktree is removed.
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": names[0], "path": "README", "line": 2, "text": "why?"}))
	if n, _ := d.reviewNotes.list(canonRoot(one), d.manager.GetSession(names[0]).GetState().Windows[0].ID); len(n) != 1 {
		t.Fatalf("the note was not kept: %v", n)
	}
	result(t, callP(c, t, "remove-worktree", map[string]any{"session": names[0], "force": true, "keep_session": true}))
	if d.reviewNotes.entries.Load() != 0 {
		t.Errorf("the notes outlived their worktree: %d panes hold some", d.reviewNotes.entries.Load())
	}
}

// TestReviewNotesFollowTheirLine: a note is found again when lines are added
// above it, and is outdated when its line is gone. A note with no quote gets
// the line's text from the file.
func TestReviewNotesFollowTheirLine(t *testing.T) {
	_, _, repo, _, c, a, _ := reviewFixture(t)
	add := result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": a, "path": "api.go", "line": 4, "text": "log the attempt"}))
	notes := notesOf(t, add)
	if len(notes) != 1 || notes[0]["quote"] != "if err == nil {" || notes[0]["by"] != queueByShell || notes[0]["side"] != "new" || add["id"] != notes[0]["id"] {
		t.Fatalf("added note = %v", notes)
	}
	hunk := result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": a, "path": "api.go", "hunk": "@@ -3,2 +3,3 @@ func Do", "text": "wrap it"}))
	if n := notesOf(t, hunk); len(n) != 2 {
		t.Fatalf("notes = %v", n)
	}

	writeIn(t, repo, "api.go", "package api\n\n// Do does it.\n// Twice.\nfunc Do() error {\n\tif err == nil {\n\t\treturn nil\n\t}\n\treturn err\n}\n")
	res := result(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a}))
	var line, hunkNote map[string]any
	for _, n := range notesOf(t, res) {
		if n["hunk_header"] != nil {
			hunkNote = n
		} else {
			line = n
		}
	}
	if line["line"] != 6.0 || line["outdated"] != nil {
		t.Errorf("the note did not follow its line down two: %v", line)
	}
	// The hunk it was on is gone, and the one there now holds its line.
	if h, _ := hunkNote["hunk_header"].(string); !strings.HasPrefix(h, "@@ -1,5 +1,7 @@") || hunkNote["line"] != 1.0 || hunkNote["outdated"] != nil {
		t.Errorf("hunk note = %v, want it on the hunk that holds line 3 now", hunkNote)
	}

	writeIn(t, repo, "api.go", "package api\n")
	res = result(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": a}))
	for _, n := range notesOf(t, res) {
		if n["hunk_header"] == nil && n["outdated"] != true {
			t.Errorf("a note whose line is gone is not outdated: %v", n)
		}
	}
}

// TestReviewNotesWhoMayChangeThem: a pane writes notes as itself and changes
// only its own, a caller outside every pane changes any but the person's, and
// only the attached client's nonce writes a note as the person.
func TestReviewNotesWhoMayChangeThem(t *testing.T) {
	d, sp, _, _, c, a, b := reviewFixture(t)
	tui := attachTUI(t, sp, "work")
	person := result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "the person's", "human_nonce": tui.HumanNonce()}))
	personID := person["id"].(string)
	if n := notesOf(t, person); n[0]["by"] != queueByHuman {
		t.Errorf("a note with the nonce is by %v, want human", n[0]["by"])
	}
	mustRefuse(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "x", "human_nonce": "0123456789abcdef0123456789abcdef"}), ErrVerbNotHuman, "a nonce no client holds")
	// A pane that runs here for another machine has no grants here and must
	// not pass for the person's shell.
	raw := []byte(`{"action":"add","session":"work","window":"` + b + `","path":"api.go","line":4,"text":"x"}`)
	if _, verr := d.verbReviewNote(&connState{paneOnly: true}, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("review-note from a forwarded pane = %v, want forbidden", verr)
	}

	// Pane a, with write. A note is typed into its pane when it is sent, so a
	// pane writes notes only on panes it could type into: not on b while b
	// holds admin.
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": a, "grants": []string{"read", "write"}}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	wantForbidden(t, "a pane writing a note on a pane that holds more", callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "the pane's"}))
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read"}}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	mine := result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "the pane's"}))
	mineID := mine["id"].(string)
	for _, n := range notesOf(t, mine) {
		if n["id"] == mineID && n["by"] != a {
			t.Errorf("the pane's note is by %v, want %s", n["by"], a)
		}
	}
	wantForbidden(t, "a pane removing the person's note", callP(pane, t, "review-note", map[string]any{"action": "remove", "session": "work", "window": b, "id": personID}))
	wantForbidden(t, "a pane editing the person's note", callP(pane, t, "review-note", map[string]any{"action": "edit", "session": "work", "window": b, "id": personID, "text": "mine now"}))
	result(t, callP(pane, t, "review-note", map[string]any{"action": "edit", "session": "work", "window": b, "id": mineID, "text": "edited"}))
	cleared := result(t, callP(pane, t, "review-note", map[string]any{"action": "clear", "session": "work", "window": b}))
	if cleared["removed"] != 1.0 || cleared["kept"] != 1.0 || len(notesOf(t, cleared)) != 1 {
		t.Errorf("a pane's clear = %v, want its own note gone and the person's kept", cleared)
	}

	// Outside every pane: any note but the person's.
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	wantForbidden(t, "a shell removing the person's note", callP(c, t, "review-note", map[string]any{"action": "remove", "session": "work", "window": b, "id": personID}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "remove", "session": "work", "window": b, "id": personID, "human_nonce": tui.HumanNonce()}))
	mustRefuse(t, callP(c, t, "review-note", map[string]any{"action": "remove", "session": "work", "window": b, "id": personID}), ErrVerbInvalidParams, "a note that is gone")

	for _, bad := range []map[string]any{
		{"action": "add", "session": "work", "window": b, "path": "/etc/passwd", "line": 1, "text": "x"},
		{"action": "add", "session": "work", "window": b, "path": "../x", "line": 1, "text": "x"},
		{"action": "add", "session": "work", "window": b, "path": "api.go", "text": "x"},
		{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 1, "text": " \x1b "},
		{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 1, "text": strings.Repeat("x", review.TextMax+1)},
		{"action": "add", "session": "work", "window": b, "path": "api.go", "hunk": "not a header", "text": "x"},
		{"action": "edit", "session": "work", "window": b, "text": "x"},
		{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 1, "side": "left", "text": "x"},
	} {
		mustRefuse(t, callP(c, t, "review-note", bad), ErrVerbInvalidParams, "review-note "+jsonParams(bad))
	}
}

// TestSendReviewQueuesOneMessage: the unsent notes go to the agent as one
// message through the delivery queue, typed when it rests, and are then
// marked sent; the header names the sender, and says the person only with the
// nonce.
func TestSendReviewQueuesOneMessage(t *testing.T) {
	d, sp, repo, sess, c, _, b := reviewFixture(t)
	writeIn(t, repo, "api.go", "package api\n\n// Do does it.\nfunc Do() error {\n\tif err == nil {\n\t\treturn nil\n\t}\n\treturn err\n}\n")
	diff := result(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": b}))
	header := filesOf(t, diff)["api.go"]["hunks"].([]any)[0].(map[string]any)["header"].(string)
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "log the attempt number here too"}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "hunk": header, "text": "wrap with context"}))
	result(t, callP(c, t, "review-diff", map[string]any{"session": "work", "window": b}))

	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b}), ErrVerbInvalidParams, "a pane with no agent")
	setAgentState(t, c, "work", b, "working", "", "")
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "now": true}), ErrVerbNotReady, "now into a working agent")
	setAgentState(t, c, "work", b, "needs_input", "approval", "run it?")
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "now": true}), ErrVerbAgentBlocked, "now into an agent on a prompt")
	setAgentState(t, c, "work", b, "working", "", "")

	res := result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b}))
	if res["type"] != "review_sent" || res["notes"] != 2.0 || res["queued_id"] == "" || res["position"] != 1.0 || res["delivering"] != false {
		t.Fatalf("send-review = %v", res)
	}
	text := queuedText(d, b)
	for _, want := range []string{
		"Review notes on your changes (vs HEAD), from a script:",
		"1. api.go:1-6 (hunk \"@@ -1,5 +1,6 @@\")\n   wrap with context\n",
		"2. api.go:5, on \"if err == nil {\"\n   log the attempt number here too\n",
		"Address each note, then say which you changed.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the queued message lacks %q:\n%s", want, text)
		}
	}
	for _, n := range notesOf(t, result(t, callP(c, t, "review-note", map[string]any{"action": "list", "session": "work", "window": b}))) {
		if n["sent_at"] == nil {
			t.Errorf("a sent note has no sent_at: %v", n)
		}
	}
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b}), ErrVerbNoNotes, "nothing unsent")

	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the notes are typed at rest", 5*time.Second, func() bool { return paneShows(t, d, sess, b, "Review notes on your changes") })

	// Sent again by id, by the person: the header says so.
	tui := attachTUI(t, sp, "work")
	notes := notesOf(t, result(t, callP(c, t, "review-note", map[string]any{"action": "list", "session": "work", "window": b})))
	setAgentState(t, c, "work", b, "working", "", "")
	eventually(t, "the typed entry leaves the queue", 5*time.Second, func() bool { return d.queue.count(b) == 0 })
	result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "ids": []string{notes[1]["id"].(string)}, "human_nonce": tui.HumanNonce()}))
	if text := queuedText(d, b); !strings.HasPrefix(text, "Review notes on your changes (vs HEAD), from the person:\n\n1. api.go:5,") || strings.Contains(text, "2. ") {
		t.Errorf("the person's message =\n%s", text)
	}
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "ids": []string{"n999"}}), ErrVerbInvalidParams, "an id that names no note")
}

// TestSendReviewFromAPaneSaysSo: a message a pane sends names the pane, and
// the pane's copy of the person's nonce is refused.
func TestSendReviewFromAPaneSaysSo(t *testing.T) {
	d, sp, _, _, c, a, b := reviewFixture(t)
	tui := attachTUI(t, sp, "work")
	result(t, callP(c, t, "set-window", map[string]any{"session": "work", "window": a, "name": "lead"}))
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": a, "grants": []string{"read", "write"}}))
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read"}}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "check this"}))
	setAgentState(t, c, "work", b, "working", "", "")

	raw := []byte(`{"session":"work","window":"` + b + `","human_nonce":"` + tui.HumanNonce() + `"}`)
	if _, verr := d.verbSendReview(&connState{paneOnly: true}, raw); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("send-review from a forwarded pane = %v, want forbidden", verr)
	}
	if d.queue.count(b) != 0 {
		t.Fatal("a refused send queued something")
	}
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "send-review", map[string]any{"session": "work", "window": b}))
	if text := queuedText(d, b); !strings.HasPrefix(text, "Review notes on your changes, from pane lead:") {
		t.Errorf("a pane's message =\n%s", text)
	}
}

// TestReviewNotesSurviveARestart: the store is saved and loaded; a pane that
// did not come back, and a worktree whose directory is gone, lose their notes.
func TestReviewNotesSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "review", "notes.json")
	root := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}

	var s reviewNoteStore
	s.load(path, func(string) bool { return true })
	kept, _ := s.add(root, "w-live", review.Note{Path: "a.go", Side: "new", Line: 3, Text: "keep"})
	s.add(root, "w-dead", review.Note{Path: "a.go", Side: "new", Line: 3, Text: "pane gone"})
	s.add(gone, "w-live", review.Note{Path: "a.go", Side: "new", Line: 3, Text: "dir gone"})
	s.setBase(root, "w-live", "origin/main")
	s.saveNowAndFreeze()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the saved file = %v (%v), want mode 0600", info, err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	var back reviewNoteStore
	back.load(path, func(w string) bool { return w == "w-live" })
	notes, base := back.list(root, "w-live")
	if len(notes) != 1 || notes[0].ID != kept.ID || notes[0].Text != "keep" || base != "origin/main" {
		t.Errorf("loaded = %v base %q", notes, base)
	}
	if n, _ := back.list(root, "w-dead"); len(n) != 0 {
		t.Errorf("a pane that did not come back kept %v", n)
	}
	if n, _ := back.list(gone, "w-live"); len(n) != 0 {
		t.Errorf("a worktree that is gone kept %v", n)
	}
	next, _ := back.add(root, "w-live", review.Note{Path: "b.go", Side: "new", Line: 1, Text: "new"})
	if next.ID == kept.ID || next.ID == "n1" || next.ID == "n2" || next.ID == "n3" {
		t.Errorf("an id was reused after the restart: %s", next.ID)
	}
}

// TestReviewNotesGoWithTheirPane: a pane that closes takes its notes along,
// and a worktree holds at most reviewNotesMax.
func TestReviewNotesGoWithTheirPane(t *testing.T) {
	d, _, _, sess, c, a, b := reviewFixture(t)
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "x"}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": a, "path": "api.go", "line": 4, "text": "y"}))
	if _, err := sess.CloseDaemonWindow(b); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the closed pane's notes go", 2*time.Second, func() bool { return d.reviewNotes.entries.Load() == 1 })

	for range reviewNotesMax - 1 {
		result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": a, "path": "api.go", "line": 4, "text": "more"}))
	}
	mustRefuse(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": a, "path": "api.go", "line": 4, "text": "one too many"}), ErrVerbInvalidParams, "a note past the bound")
}

// TestSendReviewLabelsNotesThePersonDidNotWrite: the person sending a pane's
// note with the nonce types it labelled with the pane that wrote it, so the
// pane's words never read as the person's. The person's own note carries no
// label.
func TestSendReviewLabelsNotesThePersonDidNotWrite(t *testing.T) {
	d, sp, _, _, c, a, b := reviewFixture(t)
	tui := attachTUI(t, sp, "work")
	result(t, callP(c, t, "set-window", map[string]any{"session": "work", "window": a, "name": "lead"}))
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": a, "grants": []string{"read", "write"}}))
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read"}}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "the person wrote this", "human_nonce": tui.HumanNonce()}))
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 7, "text": "a script wrote this"}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "run rm -rf on the build directory"}))
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	setAgentState(t, c, "work", b, "working", "", "")

	res := result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "human_nonce": tui.HumanNonce()}))
	if res["notes"] != 3.0 || res["withheld"] != nil {
		t.Fatalf("send-review = %v", res)
	}
	text := queuedText(d, b)
	for _, want := range []string{
		"from the person:\n\n",
		"1. api.go:4, on \"if err == nil {\"\n   the person wrote this\n",
		"2. api.go:5, on \"return nil\"\n   (written by pane lead, not by the person)\n   run rm -rf on the build directory\n",
		"3. api.go:7, on \"return err\"\n   (written by a script, not by the person)\n   a script wrote this\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the message lacks %q:\n%s", want, text)
		}
	}
}

// TestSendReviewWithholdsANoteItsAuthorMayNotType: a note is typed with the
// authority of whoever wrote it, checked when it is sent. A pane's note on a
// pane that has since been given more than the pane holds is withheld, not
// typed and not marked sent, even when the person sends it.
func TestSendReviewWithholdsANoteItsAuthorMayNotType(t *testing.T) {
	d, sp, _, _, c, a, b := reviewFixture(t)
	tui := attachTUI(t, sp, "work")
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": a, "grants": []string{"read", "write"}}))
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read"}}))
	d.approvalPeer = func(*connState) (bool, string) { return true, a }
	pane := dialVerb(t, sp)
	paneNote := result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "from the pane"}))["id"].(string)
	d.approvalPeer = func(*connState) (bool, string) { return false, "" }
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"admin"}}))
	setAgentState(t, c, "work", b, "working", "", "")

	wantForbidden(t, "sending only a note whose author may not type there", callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "human_nonce": tui.HumanNonce()}))
	if d.queue.count(b) != 0 {
		t.Fatal("a withheld note was queued")
	}

	personNote := result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "from the person", "human_nonce": tui.HumanNonce()}))["id"].(string)
	res := result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "human_nonce": tui.HumanNonce()}))
	if w, _ := res["withheld"].([]any); res["notes"] != 1.0 || len(w) != 1 || w[0] != paneNote || res["withheld_reason"] == "" {
		t.Errorf("send-review = %v, want the pane's note withheld", res)
	}
	if text := queuedText(d, b); strings.Contains(text, "from the pane") || !strings.Contains(text, "from the person\n") {
		t.Errorf("the message =\n%s", text)
	}
	for _, n := range notesOf(t, result(t, callP(c, t, "review-note", map[string]any{"action": "list", "session": "work", "window": b}))) {
		if sent := n["sent_at"] != nil; sent != (n["id"] == personNote) {
			t.Errorf("note %v sent %v", n["id"], sent)
		}
	}
}

// TestCanonRootOfARemovedDirectory: a worktree whose directory is gone still
// names the key its notes were stored under, through the nearest ancestor
// that is there, so removing it drops them when a symbolic link is on the
// path (as /var is on macOS).
func TestCanonRootOfARemovedDirectory(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	wt := filepath.Join(real, "trees", "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(link, "trees", "wt")
	key := canonRoot(viaLink)
	if key == filepath.Clean(viaLink) {
		t.Fatalf("canonRoot(%s) = %s, the link was not resolved", viaLink, key)
	}
	var s reviewNoteStore
	s.add(key, "w", review.Note{Path: "a.go", Side: review.SideNew, Line: 1, Text: "x"})

	if err := os.RemoveAll(filepath.Join(real, "trees")); err != nil {
		t.Fatal(err)
	}
	if got := canonRoot(viaLink); got != key {
		t.Errorf("after removal canonRoot = %s, want %s", got, key)
	}
	s.dropRoot(canonRoot(viaLink))
	if s.entries.Load() != 0 {
		t.Error("dropRoot missed the notes of a removed directory reached through a link")
	}
	if got := canonRoot("/"); got != "/" {
		t.Errorf("canonRoot(/) = %s", got)
	}
}

// TestReviewAuthorEncodingsAgree: review.Compose labels notes by comparing
// their author with the sender in the queue's encoding, so the two must
// spell the person, a shell and a link the same way.
func TestReviewAuthorEncodingsAgree(t *testing.T) {
	if review.ByHuman != queueByHuman || review.ByShell != queueByShell || review.ByLinkPrefix != queueByLinkPrefix {
		t.Errorf("review %q %q %q, queue %q %q %q", review.ByHuman, review.ByShell, review.ByLinkPrefix, queueByHuman, queueByShell, queueByLinkPrefix)
	}
}

// TestRemoveWorktreeDropsNotesWhenTheDirectoryIsGone: remove-worktree on a
// worktree whose directory was already deleted still drops its notes, though
// the path it records can no longer be resolved.
func TestRemoveWorktreeDropsNotesWhenTheDirectoryIsGone(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	names := fakeFan(t, d, c, repo, "fan/gone", 1, "")
	one := worktreePath(t, d, names[0])
	result(t, callP(c, t, "review-note", map[string]any{"action": "add", "session": names[0], "path": "README", "line": 1, "text": "why?"}))
	if d.reviewNotes.entries.Load() != 1 {
		t.Fatalf("the note was not kept")
	}
	if err := os.RemoveAll(one); err != nil {
		t.Fatal(err)
	}
	res := result(t, callP(c, t, "remove-worktree", map[string]any{"session": names[0], "keep_session": true}))
	if res["gone"] != true {
		t.Fatalf("remove-worktree = %v, want the directory reported gone", res)
	}
	if d.reviewNotes.entries.Load() != 0 {
		t.Errorf("the notes outlived their removed worktree: %d panes hold some", d.reviewNotes.entries.Load())
	}
}
