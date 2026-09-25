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

// These tests pin the review overlay's boundaries: a key from send-keys never
// acts as the person, file text is laundered, the compare view runs one
// refresh chain and reads nothing while hidden, and an older daemon hides the
// keys it cannot serve.

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

// runningFan is a fan of two attempts with a check running in the second.
func runningFan() map[string]any {
	return map[string]any{"group": "g", "rows": []map[string]any{
		{"session": "api-1"},
		{"session": "api-2", "verify": session.FanVerify{Command: "go test ./...", State: session.VerifyRunning, StartedAt: time.Now().UnixNano()}},
	}}
}

// openCompare shows the compare view and applies its first read, returning
// the command the answer left, which is the first tick of a refresh chain
// while a check runs.
func openCompare(t *testing.T, m *OS) (ReviewCompareMsg, uint64) {
	t.Helper()
	cmd := m.ReviewCompare()
	if cmd == nil {
		t.Fatal("w read nothing")
	}
	msg := cmd().(ReviewCompareMsg)
	return msg, m.review.compare.tickGen
}

// TestReviewCompareRunsOneRefreshChain: w, esc and w again while a check runs
// leave one refresh chain, not two. The tick and the answer of the chain the
// first w started end where they are; only the second chain reads.
func TestReviewCompareRunsOneRefreshChain(t *testing.T) {
	m, f := reviewOS(t)
	f.fan = runningFan()
	openReviewed(t, m)

	first, gen1 := openCompare(t, m)
	if m.applyReviewCompare(first) == nil {
		t.Fatal("the first w started no refresh while a check runs")
	}
	if cmd := m.ReviewCompareBack(); cmd != nil {
		m.Update(cmd())
	}
	second, gen2 := openCompare(t, m)
	if gen1 == gen2 {
		t.Error("the second w did not start a new chain")
	}
	if m.applyReviewCompare(second) == nil {
		t.Fatal("the second w started no refresh while a check runs")
	}
	// An answer of the first chain arriving late starts nothing more.
	if m.applyReviewCompare(first) != nil {
		t.Error("a late answer to the first w started a second chain")
	}
	reads := 0
	for _, gen := range []uint64{gen1, gen2} {
		if m.applyReviewTick(ReviewTickMsg{Gen: m.review.gen, TickGen: gen}) != nil {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("a second each, %d chains read the rows, want 1", reads)
	}

	// A keep starts a new chain the same way.
	m.ReviewCompareMove(-1)
	m.ReviewCompareKeep()
	m.Update(m.ReviewCompareConfirm(true)())
	if m.review.compare.tickGen == gen2 {
		t.Error("the read after a keep did not start a new chain")
	}
}

// TestReviewKeysOnAnOlderDaemon: a daemon whose list-verbs, probed as the
// Inbox watch starts, leaves review-diff out gets no review keys: ctrl+b v, v
// in the Inbox and v on a rail row do what an unbound key does, and the
// prefix menu, the help and the palette leave the review out. A review that
// comes back unknown_verb does the same from then on.
func TestReviewKeysOnAnOlderDaemon(t *testing.T) {
	offered := func(m *OS) (menu, help, palette bool) {
		for _, b := range m.prefixMenuBindings() {
			if b.Description == "Review changes" {
				menu = true
			}
		}
		for _, c := range m.HelpCategories() {
			for _, b := range c.Bindings {
				if isReviewHelpBinding(b) {
					help = true
				}
			}
		}
		m.PaletteItems = nil
		for _, it := range m.allPaletteItems() {
			if it.Name == paletteReviewName {
				palette = true
			}
		}
		return menu, help, palette
	}
	for _, how := range []string{"probe", "unknown_verb"} {
		t.Run(how, func(t *testing.T) {
			m, f := reviewOS(t)
			m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
			m.Windows[0].AgentState = "working"
			done := item("1", session.AttentionFinished, "work", "w-7", "done", time.Now().UnixNano())
			m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{done}})
			if menu, help, palette := offered(m); !menu || !help || !palette {
				t.Fatalf("a newer daemon is not offered the review: menu %v, help %v, palette %v", menu, help, palette)
			}
			switch how {
			case "probe":
				m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{done}, NoReview: true})
			case "unknown_verb":
				f.diffErr = &session.VerbCallError{Code: session.ErrVerbUnknownVerb, Message: "unknown verb"}
				cmd, _ := m.ReviewFocusedPane()
				runMsg(t, m, cmd)
				if m.ReviewOpen() {
					t.Fatal("a refused review opened")
				}
			}
			if menu, help, palette := offered(m); menu || help || palette {
				t.Errorf("an older daemon is offered the review: menu %v, help %v, palette %v", menu, help, palette)
			}
			calls := len(f.calls)
			if _, handled := m.ReviewFocusedPane(); handled {
				t.Error("ctrl+b v is handled on a daemon that cannot review")
			}
			m.OpenInbox("")
			if _, handled := m.InboxReview(); handled {
				t.Error("v in the Inbox is handled on a daemon that cannot review")
			}
			if _, handled := m.SidebarAgentReview("here", "w-2"); handled {
				t.Error("v on a rail row is handled on a daemon that cannot review")
			}
			if len(f.calls) != calls {
				t.Errorf("the review keys asked an older daemon %v", f.calls[calls:])
			}
			// A newer daemon after a restart is probed again.
			m.applyInboxSnapshot(InboxSnapshotMsg{Items: []session.AttentionItem{done}})
			if _, handled := m.ReviewFocusedPane(); !handled {
				t.Error("ctrl+b v is not handled once the daemon can review again")
			}
		})
	}
}

// TestProbeReviewDiff: the probe asks list-verbs for review-diff by name.
func TestProbeReviewDiff(t *testing.T) {
	asked := ""
	call := func(verb string, params map[string]any) ([]byte, error) {
		asked, _ = params["verb"].(string)
		return json.Marshal(map[string]any{"verbs": []map[string]string{{"verb": "review-diff"}}})
	}
	if s, k := probeVerb(call, "review-diff"); !s || !k || asked != "review-diff" {
		t.Errorf("the probe asked for %q and read supported=%v known=%v", asked, s, k)
	}
	unknown := func(string, map[string]any) ([]byte, error) {
		return nil, &session.VerbCallError{Code: session.ErrVerbUnknownVerb}
	}
	if s, k := probeVerb(unknown, "review-diff"); s || !k {
		t.Errorf("unknown_verb read as supported=%v known=%v", s, k)
	}
}
