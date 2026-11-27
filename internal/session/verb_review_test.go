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

// queuedText is the text of the first entry queued for window.
func queuedText(d *Daemon, window string) string {
	d.queue.mu.Lock()
	defer d.queue.mu.Unlock()
	if pq := d.queue.panes[window]; pq != nil && len(pq.entries) > 0 {
		return pq.entries[0].text
	}
	return ""
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
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	pane := dialVerb(t, sp)
	wantForbidden(t, "a pane writing a note on a pane that holds more", callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "the pane's"}))
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
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
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
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
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "send-review", map[string]any{"session": "work", "window": b}))
	if text := queuedText(d, b); !strings.HasPrefix(text, "Review notes on your changes, from pane lead:") {
		t.Errorf("a pane's message =\n%s", text)
	}
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
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	pane := dialVerb(t, sp)
	result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "run rm -rf on the build directory"}))
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
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
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	pane := dialVerb(t, sp)
	paneNote := result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 5, "text": "from the pane"}))["id"].(string)
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
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

// TestSendReviewSendsEachNoteToItsReceiver: a note's to names who it goes
// to. send-review types each agent's notes into its own pane, one message
// each, and never sends a note kept for the person. A receiver must be an
// agent pane of the session in the same worktree, and a pane may name only a
// receiver it could type into, when it adds the note and when it sends it.
func TestSendReviewSendsEachNoteToItsReceiver(t *testing.T) {
	d, sp, _, _, c, a, b := reviewFixture(t)
	note := func(to, text string, line int) map[string]any {
		p := map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": line, "text": text}
		if to != "" {
			p["to"] = to
		}
		return callP(c, t, "review-note", p)
	}
	setAgentState(t, c, "work", b, "working", "", "")
	mustRefuse(t, note(a, "x", 4), ErrVerbInvalidParams, "a receiver that runs no agent")
	mustRefuse(t, note("nowhere", "x", 4), ErrVerbInvalidParams, "a receiver that is no pane")
	setAgentState(t, c, "work", a, "working", "", "")

	result(t, note("", "for b", 4))
	toA := result(t, note(a, "for a", 5))
	kept := result(t, note(review.NoteToPerson, "for me", 7))
	toAID, keptID := toA["id"].(string), kept["id"].(string)
	for _, n := range notesOf(t, kept) {
		want := map[string]any{"for b": nil, "for a": a, "for me": review.NoteToPerson}[n["text"].(string)]
		if n["to"] != want {
			t.Errorf("note %q has to %v, want %v", n["text"], n["to"], want)
		}
	}

	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "ids": []string{keptID}}), ErrVerbInvalidParams, "sending a note kept for the person")
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "to": review.NoteToPerson}), ErrVerbInvalidParams, "sending to the person")

	res := result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b}))
	if msgs, _ := res["messages"].([]any); res["notes"] != 2.0 || len(msgs) != 2 {
		t.Fatalf("send-review = %v, want 2 notes in 2 messages", res)
	}
	if text := queuedText(d, b); !strings.Contains(text, "for b") || strings.Contains(text, "for a") || strings.Contains(text, "for me") {
		t.Errorf("b's message =\n%s", text)
	}
	if text := queuedText(d, a); !strings.Contains(text, "for a") || strings.Contains(text, "for b") || strings.Contains(text, "for me") {
		t.Errorf("a's message =\n%s", text)
	}
	for _, n := range notesOf(t, result(t, callP(c, t, "review-note", map[string]any{"action": "list", "session": "work", "window": b}))) {
		if sent := n["sent_at"] != nil; sent == (n["id"] == keptID) {
			t.Errorf("note %q sent %v", n["text"], sent)
		}
	}
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b}), ErrVerbNoNotes, "a send with only a kept note left")

	// edit with to gives the note to another receiver, unsent again. to
	// names one receiver for a send.
	edited := result(t, callP(c, t, "review-note", map[string]any{"action": "edit", "session": "work", "window": b, "id": keptID, "text": "for a now", "to": a}))
	for _, n := range notesOf(t, edited) {
		if n["id"] == keptID && (n["to"] != a || n["sent_at"] != nil) {
			t.Errorf("the edited note = %v, want to %s and unsent", n, a)
		}
	}
	mustRefuse(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "to": b}), ErrVerbNoNotes, "a send to b with only a's note unsent")
	if res := result(t, callP(c, t, "send-review", map[string]any{"session": "work", "window": b, "to": a, "ids": []string{toAID, keptID}})); res["notes"] != 2.0 {
		t.Errorf("send-review to a = %v, want 2 notes", res)
	}

	// Pane b, with write, may not give a note to a while a holds admin, nor
	// send one there.
	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": b, "grants": []string{"read", "write"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, b })
	pane := dialVerb(t, sp)
	wantForbidden(t, "a pane giving a note to a pane that holds more", callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "x", "to": a}))
	result(t, callP(pane, t, "review-note", map[string]any{"action": "add", "session": "work", "window": b, "path": "api.go", "line": 4, "text": "pane to b"}))
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	result(t, note(a, "shell to a", 5))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, b })
	res = result(t, callP(pane, t, "send-review", map[string]any{"session": "work", "window": b}))
	if w, _ := res["withheld"].([]any); res["notes"] != 1.0 || len(w) != 1 {
		t.Errorf("a pane's send = %v, want the note to a withheld and its own sent", res)
	}
}

// TestReviewDiffOfAFolder: review-diff with dir reads the repository that
// holds a folder of this machine, with no pane and no notes. A folder in no
// repository is not_repo, a pane without admin is refused, and dir takes no
// window.
func TestReviewDiffOfAFolder(t *testing.T) {
	d, sp, repo, _, c, a, _ := reviewFixture(t)
	writeIn(t, repo, "api.go", "package api\n\nfunc Do() error { return nil }\n")
	res := result(t, callP(c, t, "review-diff", map[string]any{"dir": filepath.Join(repo, "."), "uncommitted": true}))
	files, _ := res["files"].([]any)
	if len(files) != 1 || res["window"] != "" || len(notesOf(t, res)) != 0 {
		t.Errorf("review-diff of the folder = %v, want api.go, no pane and no notes", res)
	}
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"dir": t.TempDir()}), ErrVerbNotRepo, "a folder in no repository")
	mustRefuse(t, callP(c, t, "review-diff", map[string]any{"dir": repo, "window": a}), ErrVerbInvalidParams, "dir with a window")

	result(t, callP(c, t, "set-pane-grants", map[string]any{"session": "work", "window": a, "grants": []string{"read", "write"}}))
	d.setApprovalPeer(func(*connState) (bool, string) { return true, a })
	wantForbidden(t, "a pane without admin naming a folder", callP(dialVerb(t, sp), t, "review-diff", map[string]any{"session": "work", "dir": repo}))
	d.setApprovalPeer(func(*connState) (bool, string) { return false, "" })
	if _, verr := d.verbReviewDiff(&connState{viaLink: true}, []byte(`{"dir":"`+repo+`"}`)); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("review-diff with dir over a link = %v, want forbidden", verr)
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
