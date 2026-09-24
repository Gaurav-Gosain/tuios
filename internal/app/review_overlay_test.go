package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// These tests pin the review overlay: it opens on the first diff and not
// before, a pane with no repository opens nothing, the frame fits 80x24 and
// 120x40, notes sit under their lines, notes and sends carry the person's
// nonce and a key from send-keys never does, the cursor and scroll stay in
// bounds, and the compare view reads, checks and keeps a fan's attempts.

// reviewFake answers the review verbs and records what it was sent.
type reviewFake struct {
	calls   []queueCall
	diff    map[string]any
	diffErr error
	notes   []review.Note
	fan     map[string]any
	fanErr  error
	nextID  int
}

func (f *reviewFake) call(verb string, params map[string]any, _ time.Duration) (json.RawMessage, error) {
	f.calls = append(f.calls, queueCall{verb, params})
	switch verb {
	case "review-diff":
		if f.diffErr != nil {
			return nil, f.diffErr
		}
		out := map[string]any{}
		for k, v := range f.diff {
			out[k] = v
		}
		out["notes"] = f.notes
		if a, ok := params["against"].(string); ok {
			out["against"] = a
		}
		if params["uncommitted"] == true {
			out["uncommitted"] = true
		}
		return json.Marshal(out)
	case "review-note":
		switch params["action"] {
		case "add":
			f.nextID++
			n := review.Note{ID: fmt.Sprintf("n%d", f.nextID), Path: params["path"].(string), Side: params["side"].(string), Text: params["text"].(string), By: "human"}
			if l, ok := params["line"].(int); ok {
				n.Line = l
			}
			if q, ok := params["quote"].(string); ok {
				n.Quote = q
			}
			if h, ok := params["hunk"].(string); ok {
				n.HunkHeader = h
			}
			f.notes = append(f.notes, n)
		case "remove":
			f.notes = slices.DeleteFunc(f.notes, func(n review.Note) bool { return n.ID == params["id"] })
		case "edit":
			for i := range f.notes {
				if f.notes[i].ID == params["id"] {
					f.notes[i].Text = params["text"].(string)
				}
			}
		}
		return json.Marshal(map[string]any{"type": "review_notes", "notes": f.notes})
	case "send-review":
		var ids []string
		for _, n := range f.notes {
			if n.SentAt == 0 {
				ids = append(ids, n.ID)
			}
		}
		return json.Marshal(map[string]any{"type": "review_sent", "notes": len(ids), "ids": ids, "position": 1, "queued": 1, "delivering": false})
	case "compare-fan":
		if f.fanErr != nil || f.fan == nil {
			return nil, &session.VerbCallError{Code: session.ErrVerbInvalidParams, Message: "not part of a fan"}
		}
		return json.Marshal(f.fan)
	case "verify-fan":
		return json.Marshal(map[string]any{"type": "fan_verify_started", "sessions": []string{"api-1", "api-2"}})
	case "keep-fan":
		return json.Marshal(map[string]any{"type": "fan_kept", "kept": params["session"], "removed": []map[string]any{{"session": "api-2", "removed": true}}})
	}
	return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: verb}
}

func (f *reviewFake) last(verb string) (map[string]any, bool) {
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].verb == verb {
			return f.calls[i].params, true
		}
	}
	return nil, false
}

func (f *reviewFake) count(verb string) int {
	n := 0
	for _, c := range f.calls {
		if c.verb == verb {
			n++
		}
	}
	return n
}

// sampleDiff is a diff of two files: one hunk that changes a loop, and a new
// file.
func sampleDiff() map[string]any {
	retry := review.File{Path: "api/retry.go", Status: "M", Added: 3, Removed: 1, Hunks: []review.Hunk{{
		Header: "@@ -40,4 +40,6 @@ func Do(ctx context.Context) error {", OldStart: 40, OldLines: 4, NewStart: 40, NewLines: 6,
		Lines: []review.Line{
			{Op: "context", Old: 40, New: 40, Text: "\tfor attempt := 0; ; attempt++ {"},
			{Op: "delete", Old: 41, Text: "\t\tif err := f(); err == nil {"},
			{Op: "add", New: 41, Text: "\t\terr := f()"},
			{Op: "add", New: 42, Text: "\t\tif err == nil {"},
			{Op: "context", Old: 42, New: 43, Text: "\t\t\treturn nil"},
			{Op: "add", New: 44, Text: "\t\t}"},
			{Op: "context", Old: 43, New: 45, Text: "\t}"},
		},
	}}}
	doc := review.File{Path: "docs/retry.md", Status: "A", Added: 1, Hunks: []review.Hunk{{
		Header: "@@ -0,0 +1 @@", NewStart: 1, NewLines: 1,
		Lines: []review.Line{{Op: "add", New: 1, Text: "# Retry"}},
	}}}
	return map[string]any{
		"type": "review_diff", "session": "api-2", "window": "w-1", "base": "main", "base_sha": "abc",
		"files":  []review.File{retry, doc},
		"totals": review.Totals{Files: 2, Added: 4, Removed: 1},
	}
}

// reviewOS is a client on a daemon session with a fake daemon behind it.
func reviewOS(t *testing.T) (*OS, *reviewFake) {
	t.Helper()
	forgetSidebarState(t)
	m := inboxOS(t, zeroSettle())
	m.FocusedWindow = 0
	f := &reviewFake{diff: sampleDiff()}
	m.SetInboxVerbCaller(f.call, func() string { return "nonce-1" })
	return m, f
}

// openReviewed opens the review of the focused pane and applies the diff.
func openReviewed(t *testing.T, m *OS) {
	t.Helper()
	cmd, handled := m.ReviewFocusedPane()
	if !handled {
		t.Fatal("ctrl+b v did not handle the key")
	}
	if m.ReviewOpen() {
		t.Fatal("the overlay opened before the diff arrived")
	}
	runMsg(t, m, cmd)
	if !m.ReviewOpen() {
		t.Fatalf("the overlay did not open on the diff: %v", m.Notifications)
	}
}

// reviewFrameText renders the overlay and checks it fills the screen exactly.
func reviewFrameText(t *testing.T, m *OS) []string {
	t.Helper()
	out := m.renderReview()
	lines := strings.Split(ansi.Strip(out), "\n")
	if len(lines) != m.Height {
		t.Fatalf("the frame is %d rows on a %d row screen:\n%s", len(lines), m.Height, strings.Join(lines, "\n"))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != m.Width {
			t.Fatalf("row %d is %d cells on a %d cell screen: %q\n%s", i, w, m.Width, l, strings.Join(lines, "\n"))
		}
	}
	return lines
}

// rowWith is the index of the first line holding text, -1 for none.
func rowWith(lines []string, text string) int {
	for i, l := range lines {
		if strings.Contains(l, text) {
			return i
		}
	}
	return -1
}

// TestReviewOpensOnTheDiffAndFitsTheScreen: ctrl+b v reads the diff of the
// focused pane and opens once it arrives, and the frame fills 80x24 and
// 120x40 exactly with the header, the files and the first file's hunk.
func TestReviewOpensOnTheDiffAndFitsTheScreen(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, f := reviewOS(t)
			m.Width, m.Height = size[0], size[1]
			f.notes = []review.Note{{ID: "n1", Path: "api/retry.go", Side: "new", Line: 42, Quote: "\t\tif err == nil {", Text: "log the attempt number here too", By: "human"}}
			openReviewed(t, m)
			p, _ := f.last("review-diff")
			if p["session"] != "here" || p["window"] != "w-1" {
				t.Errorf("review-diff was asked for %v, want the focused pane of this session", p)
			}
			lines := reviewFrameText(t, m)
			header := lines[1]
			for _, want := range []string{"Review  api-2  w-1", "2 files", "+4 -1", "vs main", "1 note"} {
				if !strings.Contains(header, want) {
					t.Errorf("the header lacks %q: %q", want, header)
				}
			}
			for _, want := range []string{"M api/retry.go", "+3 -1", "A docs/retry.md", "@@ -40,4 +40,6 @@", "err := f()", "c note", "S send 1 note", "esc close"} {
				if rowWith(lines, want) < 0 {
					t.Errorf("the frame lacks %q:\n%s", want, strings.Join(lines, "\n"))
				}
			}
			if size[0] == 120 {
				for _, want := range []string{"uncommitted only", "b base"} {
					if rowWith(lines, want) < 0 {
						t.Errorf("the 120 column footer lacks %q:\n%s", want, lines[len(lines)-2])
					}
				}
			}
		})
	}
}

// TestReviewWithNoRepositoryOpensNothing: a pane with no repository under it
// opens nothing, and the dock says so.
func TestReviewWithNoRepositoryOpensNothing(t *testing.T) {
	m, f := reviewOS(t)
	f.diffErr = &session.VerbCallError{Code: session.ErrVerbNotRepo, Message: "no git repository is under window w-1"}
	cmd, handled := m.ReviewFocusedPane()
	if !handled || cmd == nil {
		t.Fatal("ctrl+b v did not ask for the diff")
	}
	runMsg(t, m, cmd)
	if m.ReviewOpen() || m.review.pending {
		t.Fatal("the overlay opened on a pane with no repository")
	}
	if len(m.Notifications) == 0 || m.Notifications[len(m.Notifications)-1].Message != "No git repository under this pane" {
		t.Errorf("the dock says %v", m.Notifications)
	}
	if f.count("compare-fan") != 0 {
		t.Error("a refused review still asked about the fan")
	}
}

// TestReviewNotesSitUnderTheirLines: a note on a line is drawn right under
// that line, a note on a hunk under the hunk's last line, and a note whose
// line is gone first, marked outdated with its quote.
func TestReviewNotesSitUnderTheirLines(t *testing.T) {
	m, f := reviewOS(t)
	f.notes = []review.Note{
		{ID: "n1", Path: "api/retry.go", Side: "new", Line: 42, Text: "log the attempt", By: "human"},
		{ID: "n2", Path: "api/retry.go", Side: "new", Line: 40, HunkHeader: "@@ -40,4 +40,6 @@ func Do(ctx context.Context) error {", Text: "wrap with context", By: "w-9", SentAt: time.Now().Add(-3 * time.Minute).UnixNano()},
		{ID: "n3", Path: "api/retry.go", Side: "new", Line: 7, Quote: "old line", Text: "gone now", By: "human", Outdated: true},
		{ID: "n4", Path: "README", Side: "new", Line: 1, Text: "on a file no longer changed", By: "shell"},
	}
	openReviewed(t, m)
	lines := reviewFrameText(t, m)
	line := rowWith(lines, "if err == nil {")
	note := rowWith(lines, "note: log the attempt")
	if line < 0 || note != line+1 {
		t.Errorf("the line note is on row %d, its line on row %d:\n%s", note, line, strings.Join(lines, "\n"))
	}
	hunk := rowWith(lines, "note (hunk) from pane w-9: wrap with context")
	if hunk < 0 || !strings.Contains(lines[hunk], "sent 3m") {
		t.Errorf("the hunk note is not drawn as sent by a pane:\n%s", strings.Join(lines, "\n"))
	}
	closing := rowWith(lines, " 43  45 ")
	if closing < 0 || hunk != closing+1 {
		t.Errorf("the hunk note is on row %d, the hunk's last line on row %d:\n%s", hunk, closing, strings.Join(lines, "\n"))
	}
	outdated := rowWith(lines, "outdated note, line 7 \"old line\": gone now")
	hunkHeader := rowWith(lines, "@@ -40,4")
	if outdated < 0 || outdated > hunkHeader {
		t.Errorf("the outdated note is on row %d, the hunk on row %d:\n%s", outdated, hunkHeader, strings.Join(lines, "\n"))
	}
	if rowWith(lines, "- README") < 0 {
		t.Errorf("a file with notes and no changes is not listed:\n%s", strings.Join(lines, "\n"))
	}
	m.ReviewFile(1)
	m.ReviewFile(1)
	lines = reviewFrameText(t, m)
	if rowWith(lines, "note, line 1 from a script: on a file no longer changed") < 0 {
		t.Errorf("the note on an unchanged file is not shown:\n%s", strings.Join(lines, "\n"))
	}
}

// TestReviewNoteAndSendAsThePerson: c on a line opens the note line under it,
// enter adds the note with the line, its side, its text and the attach
// nonce, and S sends the unsent notes with the nonce; the dock says they are
// queued.
func TestReviewNoteAndSendAsThePerson(t *testing.T) {
	m, f := reviewOS(t)
	openReviewed(t, m)
	// The cursor starts on the hunk header; the third row is the removed
	// line, the fourth the first added one.
	m.ReviewMove(3)
	m.ReviewNote(false)
	if !m.ReviewEditing() {
		t.Fatal("c did not open the note line")
	}
	m.ReviewEditorType("keep the error")
	lines := reviewFrameText(t, m)
	editor := rowWith(lines, "note: keep the error_")
	added := rowWith(lines, "+         err := f()")
	if editor < 0 || editor != added+1 {
		t.Errorf("the note line is on row %d, its line on row %d:\n%s", editor, added, strings.Join(lines, "\n"))
	}
	runMsg(t, m, m.ReviewEditorSubmit())
	p, ok := f.last("review-note")
	if !ok {
		t.Fatal("no note was added")
	}
	if p["action"] != "add" || p["path"] != "api/retry.go" || p["line"] != 41 || p["side"] != "new" || p["quote"] != "\t\terr := f()" || p["text"] != "keep the error" || p["human_nonce"] != "nonce-1" {
		t.Errorf("review-note add was sent %v", p)
	}
	if len(m.review.notes) != 1 {
		t.Fatalf("the overlay holds %d notes after the add", len(m.review.notes))
	}
	// A note on the whole hunk, on the old side of a removed line.
	m.ReviewNote(true)
	m.ReviewEditorType("wrap it")
	runMsg(t, m, m.ReviewEditorSubmit())
	p, _ = f.last("review-note")
	if p["hunk"] != "@@ -40,4 +40,6 @@ func Do(ctx context.Context) error {" || p["line"] != nil {
		t.Errorf("C sent %v", p)
	}

	runMsg(t, m, m.ReviewSend())
	p, ok = f.last("send-review")
	if !ok || p["human_nonce"] != "nonce-1" || p["session"] != "api-2" || p["window"] != "w-1" {
		t.Fatalf("send-review was sent %v", p)
	}
	msg := m.Notifications[len(m.Notifications)-1].Message
	if !strings.Contains(msg, "2 notes queued, sent when w-1 is at rest") {
		t.Errorf("the dock says %q", msg)
	}
	// The overlay covers the dock, so it says the same above its keys.
	if frame := reviewFrameText(t, m); !strings.Contains(frame[len(frame)-3], "2 notes queued, sent when w-1 is at rest") {
		t.Errorf("the overlay does not show the dock's message:\n%s", strings.Join(frame, "\n"))
	}
	if m.review.unsentNotes() != 0 {
		t.Error("the sent notes still count as unsent")
	}
	if cmd := m.ReviewSend(); cmd != nil {
		t.Error("S with nothing unsent still sends")
	}
}

// TestReviewEditAndResolve: e edits the note under the cursor and x removes
// it, both as the person.
func TestReviewEditAndResolve(t *testing.T) {
	m, f := reviewOS(t)
	f.notes = []review.Note{{ID: "n1", Path: "api/retry.go", Side: "new", Line: 42, Text: "first", By: "human"}}
	openReviewed(t, m)
	rows := m.reviewRows(m.reviewRowsWidth())
	for i, r := range rows {
		if r.kind == reviewRowNote {
			m.review.cursor = i
			break
		}
	}
	m.ReviewEditNote()
	if !m.ReviewEditing() || m.review.editor.draft != "first" {
		t.Fatal("e did not open the note's text")
	}
	m.ReviewEditorType(" and more")
	runMsg(t, m, m.ReviewEditorSubmit())
	p, _ := f.last("review-note")
	if p["action"] != "edit" || p["id"] != "n1" || p["text"] != "first and more" || p["human_nonce"] != "nonce-1" {
		t.Errorf("e sent %v", p)
	}
	runMsg(t, m, m.ReviewResolveNote())
	p, _ = f.last("review-note")
	if p["action"] != "remove" || p["id"] != "n1" || p["human_nonce"] != "nonce-1" {
		t.Errorf("x sent %v", p)
	}
	if len(m.review.notes) != 0 {
		t.Error("the resolved note is still shown")
	}
}

// TestReviewKeysFromSendKeysDoNotActAsThePerson: a note send-keys typed is not
// saved, and S, x and the keep confirmation refuse a key that did not come
// from the keyboard, since each acts as the person.
func TestReviewKeysFromSendKeysDoNotActAsThePerson(t *testing.T) {
	m, f := reviewOS(t)
	f.notes = []review.Note{{ID: "n1", Path: "api/retry.go", Side: "new", Line: 42, Text: "first", By: "human"}}
	f.fan = map[string]any{"group": "try/retry", "repo": "api", "base": "main", "rows": []map[string]any{{"session": "api-1"}, {"session": "api-2"}}}
	openReviewed(t, m)

	m.ProcessingRemoteKeys = true
	m.ReviewMove(3)
	m.ReviewNote(false)
	m.ReviewEditorType("from an agent")
	if cmd := m.ReviewEditorSubmit(); cmd != nil {
		t.Error("a note send-keys typed was saved")
	}
	if cmd := m.ReviewSend(); cmd != nil {
		t.Error("S from send-keys sent the notes")
	}
	rows := m.reviewRows(m.reviewRowsWidth())
	for i, r := range rows {
		if r.kind == reviewRowNote {
			m.review.cursor = i
		}
	}
	if cmd := m.ReviewResolveNote(); cmd != nil {
		t.Error("x from send-keys removed a note")
	}
	runMsg(t, m, m.ReviewCompare())
	m.ReviewCompareKeep()
	if cmd := m.ReviewCompareConfirm(true); cmd != nil {
		t.Error("y from send-keys kept an attempt")
	}
	m.ReviewCompareVerifyPrompt()
	m.ReviewEditorType("true")
	if cmd := m.ReviewEditorSubmit(); cmd != nil {
		t.Error("a verify command from send-keys was run")
	}
	for _, verb := range []string{"review-note", "send-review", "keep-fan", "verify-fan"} {
		if f.count(verb) != 0 {
			t.Errorf("a key from send-keys reached %s", verb)
		}
	}

	// The same keys from the keyboard do reach the daemon: a draft send-keys
	// touched stays refused even then.
	m.ProcessingRemoteKeys = false
	m.ReviewCompareBack()
	m.ReviewNote(false)
	m.ProcessingRemoteKeys = true
	m.ReviewEditorType("x")
	m.ProcessingRemoteKeys = false
	if cmd := m.ReviewEditorSubmit(); cmd != nil {
		t.Error("a draft send-keys touched was saved once the keyboard pressed enter")
	}
}

// TestReviewCursorAndScrollStayInBounds: the cursor stops at both ends, the
// end of a long file scrolls into view, and the next file starts at its top.
func TestReviewCursorAndScrollStayInBounds(t *testing.T) {
	m, f := reviewOS(t)
	m.Width, m.Height = 80, 24
	var lines []review.Line
	for i := 1; i <= 200; i++ {
		lines = append(lines, review.Line{Op: "add", New: i, Text: fmt.Sprintf("line %d", i)})
	}
	f.diff["files"] = []review.File{{Path: "big.go", Status: "A", Added: 200, Hunks: []review.Hunk{{Header: "@@ -0,0 +1,200 @@", NewStart: 1, NewLines: 200, Lines: lines}}}, {Path: "small.go", Status: "M", Added: 1, Hunks: []review.Hunk{{Header: "@@ -1 +1 @@", OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1, Lines: []review.Line{{Op: "add", New: 1, Text: "tail"}}}}}}
	openReviewed(t, m)
	m.ReviewMove(-5)
	if m.review.cursor != 0 {
		t.Errorf("the cursor went above the first row to %d", m.review.cursor)
	}
	m.ReviewEdge(true)
	if m.review.cursor != 200 {
		t.Errorf("G put the cursor on row %d, want 200", m.review.cursor)
	}
	text := reviewFrameText(t, m)
	if rowWith(text, "line 200") < 0 {
		t.Errorf("the last line is not on screen after G:\n%s", strings.Join(text, "\n"))
	}
	m.ReviewMove(10)
	if m.review.cursor != 200 {
		t.Errorf("the cursor went past the last row to %d", m.review.cursor)
	}
	m.ReviewHunk(1)
	if m.review.file != 1 || m.review.cursor != 0 {
		t.Errorf("] past the last hunk is on file %d row %d, want the next file's hunk", m.review.file, m.review.cursor)
	}
	text = reviewFrameText(t, m)
	if rowWith(text, "tail") < 0 || rowWith(text, "line 200") >= 0 {
		t.Errorf("the next file does not start at its top:\n%s", strings.Join(text, "\n"))
	}
	m.ReviewHunk(-1)
	if m.review.file != 0 || m.review.cursor != 0 {
		t.Errorf("[ back is on file %d row %d, want the first file's hunk", m.review.file, m.review.cursor)
	}
}

// TestReviewTruncatedAndBinaryFiles: a file past a limit and a binary file
// show a placeholder with their counts, not an empty column.
func TestReviewTruncatedAndBinaryFiles(t *testing.T) {
	m, f := reviewOS(t)
	f.diff["files"] = []review.File{{Path: "huge.txt", Status: "M", Added: 6000, Removed: 2, Truncated: true}, {Path: "logo.png", Status: "A", Binary: true}}
	openReviewed(t, m)
	text := reviewFrameText(t, m)
	if rowWith(text, "Too large to show here: +6000 -2 lines") < 0 {
		t.Errorf("the truncated file has no placeholder:\n%s", strings.Join(text, "\n"))
	}
	m.ReviewFile(1)
	text = reviewFrameText(t, m)
	if rowWith(text, "Binary file, binary. No text is shown.") < 0 {
		t.Errorf("the binary file has no placeholder:\n%s", strings.Join(text, "\n"))
	}
	m.ReviewNote(false)
	if m.ReviewEditing() {
		t.Error("c opened a note on a placeholder")
	}
}

// TestReviewUncommittedAndBase: u asks for the uncommitted changes only and
// back, and b asks for another base.
func TestReviewUncommittedAndBase(t *testing.T) {
	m, f := reviewOS(t)
	openReviewed(t, m)
	runMsg(t, m, m.ReviewToggleUncommitted())
	p, _ := f.last("review-diff")
	if p["uncommitted"] != true {
		t.Errorf("u sent %v", p)
	}
	if !strings.Contains(reviewFrameText(t, m)[1], "uncommitted only") {
		t.Error("the header does not say uncommitted only")
	}
	m.ReviewBasePrompt()
	if !m.ReviewEditing() || m.review.editor.draft != "" {
		t.Fatalf("the base line opened with %+v", m.review.editor)
	}
	m.ReviewEditorType("origin/main")
	runMsg(t, m, m.ReviewEditorSubmit())
	p, _ = f.last("review-diff")
	if p["base"] != "origin/main" || p["uncommitted"] != nil {
		t.Errorf("b sent %v", p)
	}
}

// TestReviewCompareView: w shows the fan's attempts with what each changed
// and its check, V runs the last command again in every attempt and reads
// the rows while it runs, and K keeps one after a confirmation that names the
// others.
func TestReviewCompareView(t *testing.T) {
	exit1 := 1
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m, f := reviewOS(t)
			m.Width, m.Height = size[0], size[1]
			now := time.Now()
			f.fan = map[string]any{"group": "fan/retry", "repo": "api", "base": "origin/main", "rows": []map[string]any{
				{"session": "api-1", "agent": "claude", "state": "done", "files": 4, "added": 120, "removed": 31,
					"verify": session.FanVerify{Command: "go test ./...", State: "passed", StartedAt: now.Add(-14 * time.Minute).UnixNano(), FinishedAt: now.Add(-14 * time.Minute).UnixNano()}},
				{"session": "api-2", "agent": "codex", "state": "working", "files": 2, "added": 40, "removed": 3},
				{"session": "api-3", "agent": "claude", "state": "errored", "files": 0, "added": 0, "removed": 0,
					"last_command": map[string]any{"cmdline": "make lint", "exit": exit1, "at": now.Add(-20 * time.Minute).UnixNano()}},
			}}
			openReviewed(t, m)
			if p, _ := f.last("compare-fan"); p["changes"] != false {
				t.Errorf("the fan probe counted changes: %v", p)
			}
			if !strings.Contains(reviewFrameText(t, m)[len(reviewFrameText(t, m))-2], "w compare") {
				t.Error("the footer does not offer w for a pane in a fan")
			}
			runMsg(t, m, m.ReviewCompare())
			if !m.ReviewCompareShown() {
				t.Fatal("w did not show the compare view")
			}
			if p, _ := f.last("compare-fan"); p["changes"] != true {
				t.Errorf("the compare view did not count changes: %v", p)
			}
			lines := reviewFrameText(t, m)
			if !strings.Contains(lines[0], "Compare  fan/retry in api, 3 attempts vs origin/main") {
				t.Errorf("the title is %q", lines[0])
			}
			wide := size[0] >= 120
			checks := map[string]string{"api-1": "go test ./... passed", "api-2": "not run", "api-3": "last command exited 1: make lint"}
			if !wide {
				checks = map[string]string{"api-1": "passed", "api-2": "-", "api-3": "exit 1"}
			}
			for name, check := range checks {
				row := rowWith(lines, name)
				if row < 0 || !strings.Contains(lines[row], check) {
					t.Errorf("the row of %s does not say %q:\n%s", name, check, strings.Join(lines, "\n"))
				}
			}
			if row := rowWith(lines, "api-1"); !strings.Contains(lines[row], "+120 -31") || !strings.Contains(lines[row], "14m") {
				t.Errorf("the first row lacks its counts or age: %q", lines[row])
			}
			if c := m.review.compare; c.cursor != 1 {
				t.Errorf("the cursor starts on row %d, want the reviewed attempt's", c.cursor)
			}

			m.ReviewCompareVerifyPrompt()
			if m.review.editor == nil || m.review.editor.draft != "go test ./..." {
				t.Fatalf("V did not fill in the last command: %+v", m.review.editor)
			}
			cmd := m.ReviewEditorSubmit()
			vmsg := cmd()
			tick := m.applyReviewVerify(vmsg.(ReviewVerifyMsg))
			if p, _ := f.last("verify-fan"); p["command"] != "go test ./..." || p["session"] != "api-2" {
				t.Errorf("verify-fan was sent %v", p)
			}
			if tick == nil {
				t.Fatal("a running check schedules no read of the rows")
			}
			lines = reviewFrameText(t, m)
			if row := rowWith(lines, "api-2"); !strings.Contains(lines[row], "running") {
				t.Errorf("the checked row does not say running: %q", lines[row])
			}
			// The daemon says the check ended: the next read finds nothing
			// running, and nothing more is scheduled.
			before := f.count("compare-fan")
			next := m.applyReviewTick(ReviewTickMsg{Gen: m.review.gen, TickGen: m.review.compare.tickGen})
			if next == nil {
				t.Fatal("the tick did not read the rows")
			}
			if again := m.applyReviewCompare(next().(ReviewCompareMsg)); again != nil {
				t.Error("rows with no check running schedule another read")
			}
			if f.count("compare-fan") != before+1 {
				t.Error("the tick did not read the rows once")
			}
			if row := rowWith(reviewFrameText(t, m), "api-2"); row < 0 {
				t.Error("the rows went away")
			} else if p, _ := f.last("compare-fan"); p["changes"] != false {
				t.Errorf("the tick counted changes: %v", p)
			}
			if row := rowWith(reviewFrameText(t, m), "api-1"); !strings.Contains(reviewFrameText(t, m)[row], "+120 -31") {
				t.Error("a read without counts dropped the counts")
			}

			m.ReviewCompareKeep()
			lines = reviewFrameText(t, m)
			if rowWith(lines, "Keep api-2 and remove api-1, api-3?") < 0 {
				t.Errorf("the confirmation does not name what goes:\n%s", strings.Join(lines, "\n"))
			}
			if cmd := m.ReviewCompareConfirm(false); cmd != nil || f.count("keep-fan") != 0 {
				t.Error("n kept an attempt")
			}
			m.ReviewCompareKeep()
			cmd = m.ReviewCompareConfirm(true)
			m.Update(cmd())
			if p, _ := f.last("keep-fan"); p["session"] != "api-2" {
				t.Errorf("keep-fan was sent %v", p)
			}
			if msg := m.Notifications[len(m.Notifications)-1].Message; !strings.Contains(msg, "Kept api-2. Removed api-2") && !strings.Contains(msg, "Kept api-2.") {
				t.Errorf("the dock says %q", msg)
			}
		})
	}
}

// TestReviewCompareEnterAndDiff: enter reviews an attempt and esc comes back
// to the compare view; d with two marks diffs them, with no notes offered.
func TestReviewCompareEnterAndDiff(t *testing.T) {
	m, f := reviewOS(t)
	f.fan = map[string]any{"group": "g", "rows": []map[string]any{{"session": "api-1"}, {"session": "api-2"}}}
	openReviewed(t, m)
	runMsg(t, m, m.ReviewCompare())
	m.ReviewCompareMove(-1)
	runMsg(t, m, m.ReviewCompareOpen())
	if p, _ := f.last("review-diff"); p["session"] != "api-1" {
		t.Errorf("enter reviewed %v", p)
	}
	if m.ReviewCompareShown() {
		t.Fatal("enter left the compare view up")
	}
	m.ReviewClose()
	if !m.ReviewCompareShown() {
		t.Fatal("esc from an attempt's review did not go back to the compare view")
	}
	m.ReviewCompareMark()
	m.ReviewCompareMove(1)
	m.ReviewCompareMark()
	runMsg(t, m, m.ReviewCompareDiff())
	p, _ := f.last("review-diff")
	if p["session"] != "api-1" || p["against"] != "api-2" {
		t.Errorf("d sent %v", p)
	}
	lines := reviewFrameText(t, m)
	if !strings.Contains(lines[1], "against api-2") || strings.Contains(lines[1], "note") {
		t.Errorf("the header of a diff between attempts is %q", lines[1])
	}
	m.ReviewMove(2)
	m.ReviewNote(false)
	if m.ReviewEditing() {
		t.Error("c opened a note on a diff between attempts")
	}
	m.ReviewClose()
	runMsg(t, m, m.ReviewCompareBack())
	if p, _ := f.last("review-diff"); p["session"] != "here" || p["against"] != nil {
		t.Errorf("esc from the compare view went back to %v", p)
	}
	if m.ReviewCompareShown() || !m.ReviewOpen() {
		t.Error("esc from the compare view did not return to the review")
	}
	m.ReviewClose()
	if m.ReviewOpen() {
		t.Error("esc did not close the review")
	}
}

// TestReviewNotInAFan: a pane outside every fan offers no w, and w says why.
func TestReviewNotInAFan(t *testing.T) {
	m, _ := reviewOS(t)
	openReviewed(t, m)
	lines := reviewFrameText(t, m)
	if strings.Contains(lines[len(lines)-2], "compare") {
		t.Error("the footer offers compare for a pane in no fan")
	}
	if cmd := m.ReviewCompare(); cmd != nil || m.ReviewCompareShown() {
		t.Error("w opened a compare view for a pane in no fan")
	}
}

// TestReviewEntryPoints: v on an Inbox item and on a rail agent row review
// that item's pane; an item on another machine is refused, and v with
// nothing selected does what an unbound key does.
func TestReviewEntryPoints(t *testing.T) {
	m, f := reviewOS(t)
	if _, handled := m.InboxReview(); handled {
		t.Error("v with nothing selected says it did something")
	}
	done := item("1", session.AttentionFinished, "work", "w-7", "done", time.Now().UnixNano())
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{done}})
	m.OpenInbox("")
	cmd, handled := m.InboxReview()
	if !handled || cmd == nil {
		t.Fatal("v on an Inbox item did nothing")
	}
	runMsg(t, m, cmd)
	if p, _ := f.last("review-diff"); p["session"] != "work" || p["window"] != "w-7" {
		t.Errorf("v on the Inbox reviewed %v", p)
	}
	if !m.ShowInbox || !m.ReviewOpen() {
		t.Error("the review did not open over the Inbox")
	}
	m.ReviewClose()
	if !m.ShowInbox {
		t.Error("closing the review closed the Inbox under it")
	}

	remote := done
	remote.Host = "box"
	m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{remote}})
	if cmd, handled := m.InboxReview(); !handled || cmd != nil {
		t.Error("v on an item on another machine asked this daemon")
	}

	cmd, handled = m.SidebarAgentReview("here", "w-2")
	if !handled || cmd == nil {
		t.Fatal("v on a rail row did nothing")
	}
	runMsg(t, m, cmd)
	if p, _ := f.last("review-diff"); p["window"] != "w-2" {
		t.Errorf("v on the rail reviewed %v", p)
	}
}

// TestReviewWithoutADaemon: outside a daemon session ctrl+b v says it needs
// one and asks nothing.
func TestReviewWithoutADaemon(t *testing.T) {
	m, f := reviewOS(t)
	m.IsDaemonSession = false
	cmd, handled := m.ReviewFocusedPane()
	if !handled || cmd != nil || len(f.calls) != 0 {
		t.Errorf("ctrl+b v outside a daemon: handled %v, cmd %v, calls %v", handled, cmd != nil, f.calls)
	}
}

// TestReviewPaletteEntryWaitsForAnAgent: the palette lists the review only
// once an agent has been seen; the key works either way.
func TestReviewPaletteEntryWaitsForAnAgent(t *testing.T) {
	m, _ := reviewOS(t)
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	has := func() bool {
		m.PaletteItems = nil
		for _, it := range m.allPaletteItems() {
			if strings.Contains(it.Name, "Review changes") {
				return true
			}
		}
		return false
	}
	if has() {
		t.Error("the palette lists the review before any agent was seen")
	}
	if _, handled := m.ReviewFocusedPane(); !handled {
		t.Error("ctrl+b v is not handled before an agent was seen")
	}
	m.Windows[0].AgentState = "working"
	if !has() {
		t.Error("the palette does not list the review once an agent is seen")
	}
}

// TestReviewTextIsLaundered: a control sequence in a file's text is not drawn
// as one.
func TestReviewTextIsLaundered(t *testing.T) {
	m, f := reviewOS(t)
	f.diff["files"] = []review.File{{Path: "evil.txt", Status: "A", Added: 1, Hunks: []review.Hunk{{Header: "@@ -0,0 +1 @@", NewStart: 1, NewLines: 1, Lines: []review.Line{{Op: "add", New: 1, Text: "a\x1b[2Jb\x07c"}}}}}}
	openReviewed(t, m)
	out := m.renderReview()
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x07") {
		t.Error("a control sequence in the diff reached the frame")
	}
	if rowWith(reviewFrameText(t, m), "a[2Jbc") < 0 {
		t.Error("the laundered line is not drawn")
	}
}
