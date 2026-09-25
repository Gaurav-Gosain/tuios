package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// Reviewing what an agent changed: the full-screen diff of a pane's worktree
// against its base, the notes the person leaves on it, and the comparison of
// a fan's attempts. It reads the review-diff and compare-fan verbs, runs no
// git until it is opened, and draws nothing until then.
//
// It is reached from ctrl+b v on the focused pane, v on an Inbox row and v on
// a rail agent row. The key is always bound, since calling it is an explicit
// act; the prefix menu, help and the palette list it only once an agent has
// been seen.
//
// The overlay opens once the first diff arrives. A pane with no repository
// under it opens nothing and the dock says so.
//
// Who acts: the diff is read with this client's own connection. A note added,
// edited or resolved here, and the notes sent with S, carry this client's
// attach nonce, so the daemon records them as the person's and the message
// says "from the person". A key that did not come from the keyboard (send-keys,
// a tape) never does any of that: it may move around the diff, but a note it
// typed is not saved, and S, x, V, K and the keep confirmation refuse it,
// because each of them acts as the person or with this client's authority.
// The rest is the daemon's: review-note holds a note's author to its reach,
// send-review goes through the delivery queue, verify-fan opens its windows
// with no grants and keep-fan refuses a sibling with uncommitted work.

// reviewDiffTimeout bounds one review-diff call. The daemon gives up on git
// after 10 seconds.
const reviewDiffTimeout = 15 * time.Second

// reviewVerbTimeout bounds a note, send or verify call.
const reviewVerbTimeout = 5 * time.Second

// reviewFanTimeout bounds compare-fan with counts and keep-fan, which run git
// in every attempt.
const reviewFanTimeout = 30 * time.Second

// reviewTickEvery is how often the compare view reads the attempts again
// while a check runs in one of them. Nothing ticks otherwise.
const reviewTickEvery = time.Second

// reviewRemoteRefusal is what the dock says when a key that did not come from
// the keyboard tries to act as the person in the review.
const reviewRemoteRefusal = "A key from send-keys does not change a review: only keys from your keyboard write, send or keep as you"

// reviewQuery is what one diff asks for.
type reviewQuery struct {
	Session     string
	Window      string
	Base        string
	Uncommitted bool
	// Against names another attempt of the same fan, diffed with Session.
	Against string
}

// reviewDiffResult is review-diff's answer, as the overlay reads it.
type reviewDiffResult struct {
	Session     string        `json:"session"`
	Window      string        `json:"window"`
	RepoRoot    string        `json:"repo_root"`
	Worktree    string        `json:"worktree"`
	Base        string        `json:"base"`
	BaseSHA     string        `json:"base_sha"`
	Uncommitted bool          `json:"uncommitted"`
	Files       []review.File `json:"files"`
	Totals      review.Totals `json:"totals"`
	Truncated   bool          `json:"truncated"`
	Notes       []review.Note `json:"notes"`
	Against     string        `json:"against"`
}

// reviewLastCommand is the newest command a shell in an attempt finished.
type reviewLastCommand struct {
	Cmdline string `json:"cmdline"`
	Exit    *int   `json:"exit,omitempty"`
	At      int64  `json:"at"`
}

// reviewFanRow is one attempt of compare-fan's answer.
type reviewFanRow struct {
	Session      string             `json:"session"`
	Branch       string             `json:"branch"`
	Agent        string             `json:"agent"`
	Harness      string             `json:"harness"`
	State        string             `json:"state"`
	Files        *int               `json:"files,omitempty"`
	Added        *int               `json:"added,omitempty"`
	Removed      *int               `json:"removed,omitempty"`
	Dirty        *bool              `json:"dirty,omitempty"`
	PromptStatus string             `json:"prompt_status"`
	Verify       *session.FanVerify `json:"verify,omitempty"`
	LastCommand  *reviewLastCommand `json:"last_command,omitempty"`
	Gone         bool               `json:"gone,omitempty"`
	Note         string             `json:"note,omitempty"`
}

// reviewFanResult is compare-fan's answer.
type reviewFanResult struct {
	Group string         `json:"group"`
	Repo  string         `json:"repo"`
	Base  string         `json:"base"`
	Rows  []reviewFanRow `json:"rows"`
}

// reviewFileEntry is one row of the file list: a changed file, or a file that
// is no longer changed but still carries notes.
type reviewFileEntry struct {
	file *review.File
	path string
}

// reviewEditorKind is what the one-line editor at the foot or under a line
// is typing.
type reviewEditorKind int

const (
	reviewEditNote reviewEditorKind = iota
	reviewEditBase
	reviewEditVerify
)

// reviewEditor is the one line being typed: a note, a base or a verify
// command. enter saves it and esc drops it.
type reviewEditor struct {
	kind  reviewEditorKind
	draft string
	// automated is set when a key that did not come from the keyboard
	// touched the draft. Such a draft is never saved.
	automated bool
	// noteID is the note being edited, empty for a new one.
	noteID string
	// The anchor of a new note.
	path, side, quote, hunk string
	line                    int
	// hunkIdx and lineIdx place the editor row under its line: the hunk,
	// and the line within it, or -1 for a note on the whole hunk.
	hunkIdx, lineIdx int
}

// reviewCompare is the compare view of a fan's attempts.
type reviewCompare struct {
	shown   bool
	loading bool
	group   string
	repo    string
	base    string
	rows    []reviewFanRow
	cursor  int
	// marks are the attempts m marked, oldest first, at most two.
	marks []string
	// from is the review the view was opened from, which esc goes back to,
	// and fromWho the name it showed.
	from    reviewQuery
	fromWho string
	// confirmKeep is the attempt K asks about, empty when nothing is asked.
	confirmKeep string
	// tickGen tells the current refresh chain from one a newer check
	// started.
	tickGen uint64
}

// reviewState is the review overlay.
type reviewState struct {
	// gen tells an answer to the current review from one to a review since
	// closed or replaced.
	gen uint64
	// pending is set while the first diff of a review is being read. The
	// overlay is not shown yet.
	pending bool
	open    bool
	// loading is set while a diff is read again inside an open overlay.
	loading bool
	who     string
	query   reviewQuery
	diff    *reviewDiffResult
	notes   []review.Note
	files   []reviewFileEntry
	file    int
	// listFocus is set while the keys move through the file list.
	listFocus  bool
	cursor     int
	scroll     int
	listScroll int
	editor     *reviewEditor
	// fan is the pane's fan, nil when it is not in one.
	fan     *reviewFanResult
	compare *reviewCompare
	// backToCompare says esc returns to the compare view, for a review
	// opened from it.
	backToCompare bool
	// rowsWidth is the width the diff rows were last laid out at, so the
	// keys move over the rows the person sees.
	rowsWidth int
	// pageRows is how many diff rows the last frame showed.
	pageRows int
	// loadErr is why the diff shown is missing, for a review opened from
	// the compare view whose diff could not be read.
	loadErr string
	// openedAt is when this review was asked for.
	openedAt time.Time
	// split asks for the two sides next to each other, where the diff
	// column is wide enough. It outlives the review, so the next one opens
	// the way the last was left.
	split bool
	// xOff is how far the code is scrolled sideways, in cells, and xPath
	// the file it was scrolled in: another file starts at the left edge.
	xOff  int
	xPath string
	// look is the overlay's colours, and hunks what it keeps of each hunk
	// drawn. Both are made while drawing, so a closed review costs nothing.
	look  *reviewLook
	hunks map[*review.File][]*reviewHunkLook
	// statusID is the dock message the review itself raised last. The
	// overlay covers the dock, so it shows that one on its status line. It
	// shows no other: a pane closing or a client resizing is the dock's news,
	// and it waits there for the overlay to close like it does under every
	// other full-screen surface.
	statusID string
}

// reviewNotify raises a dock message for the review and has the overlay show
// it on its status line while it is up. See reviewState.statusID.
func (m *OS) reviewNotify(message, kind string, duration time.Duration) {
	m.ShowNotification(message, kind, duration)
	if k := len(m.Notifications); k > 0 && m.Notifications[k-1].Message == message {
		m.review.statusID = m.Notifications[k-1].ID
	}
}

// clearDiff forgets the diff shown, for a review of another attempt: the
// overlay says it is reading rather than show the last attempt's changes
// under the next one's name.
func (r *reviewState) clearDiff() {
	r.diff, r.notes, r.files, r.editor, r.loadErr = nil, nil, nil, nil, ""
	r.hunks, r.xOff = nil, 0
	r.file, r.cursor, r.scroll, r.listScroll, r.listFocus = 0, 0, 0, 0, false
}

// ReviewDiffMsg is a review-diff answer.
type ReviewDiffMsg struct {
	Gen   uint64
	Query reviewQuery
	Diff  *reviewDiffResult
	// Fan is the pane's fan, read when the review opened; nil when it is in
	// none or it was not asked.
	Fan      *reviewFanResult
	ProbeFan bool
	Err      error
}

// ReviewNotesMsg is a review-note answer.
type ReviewNotesMsg struct {
	Gen    uint64
	Action string
	Notes  []review.Note
	Err    error
}

// ReviewSentMsg is a send-review answer.
type ReviewSentMsg struct {
	Gen            uint64
	Who            string
	Notes          int
	IDs            []string
	Delivering     bool
	Position       int
	Withheld       []string
	WithheldReason string
	Err            error
}

// ReviewCompareMsg is a compare-fan answer for the compare view.
type ReviewCompareMsg struct {
	Gen     uint64
	TickGen uint64
	Changes bool
	Fan     *reviewFanResult
	Err     error
}

// ReviewVerifyMsg is a verify-fan answer.
type ReviewVerifyMsg struct {
	Gen      uint64
	Command  string
	Sessions []string
	Err      error
}

// ReviewKeptMsg is a keep-fan answer.
type ReviewKeptMsg struct {
	Gen     uint64
	Kept    string
	Removed []reviewKeepRow
	Err     error
}

// reviewKeepRow is one sibling keep-fan removed or left.
type reviewKeepRow struct {
	Session string `json:"session"`
	Removed bool   `json:"removed"`
	Note    string `json:"note"`
	Code    string `json:"code"`
}

// ReviewTickMsg reads the attempts again while a check runs.
type ReviewTickMsg struct {
	Gen, TickGen uint64
}

// ReviewOpen reports whether the review overlay is on screen. It owns the
// keyboard while it is.
func (m *OS) ReviewOpen() bool { return m.review.open }

// ReviewEditing reports whether the review's one-line editor is open, where
// every printable key is text.
func (m *OS) ReviewEditing() bool { return m.review.open && m.review.editor != nil }

// ReviewCompareShown reports whether the compare view is showing.
func (m *OS) ReviewCompareShown() bool {
	return m.review.open && m.review.compare != nil && m.review.compare.shown
}

// ReviewConfirming reports whether the compare view is asking to keep an
// attempt, where the next key answers.
func (m *OS) ReviewConfirming() bool {
	return m.ReviewCompareShown() && m.review.compare.confirmKeep != ""
}

// reviewSupported reports whether the daemon can review a pane's changes, as
// far as this client knows: false only once its list-verbs left review-diff
// out, or a review came back unknown_verb. The review keys then do what an
// unbound key does, and the prefix menu, the help and the palette leave them
// out.
func (m *OS) reviewSupported() bool {
	return !m.Inbox.noReview
}

// ReviewFocusedPane opens the review of the focused pane (ctrl+b v). handled
// is false on a daemon without review-diff, so in terminal mode the key
// reaches the focused pane as it did before it was bound.
func (m *OS) ReviewFocusedPane() (tea.Cmd, bool) {
	if !m.reviewSupported() {
		return nil, false
	}
	w := m.GetFocusedWindow()
	if w == nil {
		m.reviewNotify("No pane to review", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	who := printableTitle(m.railTitleShown(w))
	if who == "" {
		who = shortWindowLabel(w.ID)
	}
	return m.openReview(m.SessionName, w.ID, who), true
}

// InboxReview opens the review of the selected Inbox item's pane. handled is
// false with nothing selected, or on a daemon without review-diff, where v
// does what an unbound key does.
func (m *OS) InboxReview() (tea.Cmd, bool) {
	if !m.reviewSupported() {
		return nil, false
	}
	it, ok := m.inboxSelected()
	if !ok {
		return nil, false
	}
	if it.Host != "" {
		m.reviewNotify(inboxWho(it)+" is on "+printableTitle(it.Host)+". Attach there to review it", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	if it.Window == "" {
		m.reviewNotify("That item has no pane to review", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	return m.openReview(it.Session, it.Window, inboxWho(it)), true
}

// SidebarAgentReview opens the review of the pane of a rail agent row.
// handled is false on a daemon without review-diff, and the rail's own
// binding for the key then runs.
func (m *OS) SidebarAgentReview(sessionID, windowID string) (tea.Cmd, bool) {
	if !m.reviewSupported() {
		return nil, false
	}
	_, _, label, ok := m.railPane(sessionID, windowID)
	if !ok {
		m.reviewNotify("That pane is on another machine. Attach there to review it", "info", m.Settings.NotificationDuration)
		return nil, true
	}
	who := printableTitle(label)
	if who == "" {
		who = shortWindowLabel(windowID)
	}
	if sessionID == "" {
		sessionID = m.SessionName
	}
	return m.openReview(sessionID, windowID, who), true
}

// openReview starts reading a pane's diff. The overlay opens when it arrives.
func (m *OS) openReview(sessionName, windowID, who string) tea.Cmd {
	if !m.IsDaemonSession {
		m.reviewNotify("Review needs the daemon. Start a daemon session with: tuios new", "info", m.Settings.NotificationDuration)
		return nil
	}
	if m.AttachedHost != "" {
		m.reviewNotify("This session runs on "+printableTitle(m.AttachedHost)+", and its repository is there. Review it on that machine", "info", m.Settings.NotificationDuration)
		return nil
	}
	r := &m.review
	if r.pending {
		return nil
	}
	*r = reviewState{gen: r.gen + 1, pending: true, who: who, openedAt: time.Now(), split: r.split}
	r.query = reviewQuery{Session: sessionName, Window: windowID}
	return m.reviewLoadCmd(r.query, true)
}

// CloseReview closes the overlay and forgets what it read.
func (m *OS) CloseReview() {
	m.review = reviewState{gen: m.review.gen + 1, split: m.review.split}
}

// reviewLoadCmd reads a diff, and with probeFan also whether the pane is in a
// fan, without counting anything in it.
func (m *OS) reviewLoadCmd(q reviewQuery, probeFan bool) tea.Cmd {
	call := m.inboxCaller()
	gen := m.review.gen
	params := map[string]any{}
	if q.Session != "" {
		params["session"] = q.Session
	}
	if q.Window != "" {
		params["window"] = q.Window
	}
	if q.Against != "" {
		params["against"] = q.Against
	} else {
		if q.Base != "" {
			params["base"] = q.Base
		}
		if q.Uncommitted {
			params["uncommitted"] = true
		}
	}
	return func() tea.Msg {
		msg := ReviewDiffMsg{Gen: gen, Query: q, ProbeFan: probeFan}
		raw, err := call("review-diff", params, reviewDiffTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res reviewDiffResult
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.Diff = &res
		if probeFan && q.Against == "" {
			// A pane outside every fan answers with a refusal, which says
			// only that there is nothing to compare.
			if raw, err := call("compare-fan", map[string]any{"session": res.Session, "changes": false}, reviewVerbTimeout); err == nil {
				var fan reviewFanResult
				if json.Unmarshal(raw, &fan) == nil && fan.Group != "" {
					msg.Fan = &fan
				}
			}
		}
		return msg
	}
}

// reload reads the current diff again.
func (m *OS) reviewReload() tea.Cmd {
	r := &m.review
	r.loading = true
	return m.reviewLoadCmd(r.query, false)
}

// applyReviewDiff shows a diff that arrived.
func (m *OS) applyReviewDiff(msg ReviewDiffMsg) {
	r := &m.review
	if msg.Gen != r.gen || (!r.pending && !r.open) {
		return
	}
	first := r.pending
	r.loading = false
	if msg.Err != nil {
		r.pending = false
		text := reviewErrorText(msg.Err)
		var callErr *session.VerbCallError
		if errors.As(msg.Err, &callErr) && callErr.Code == session.ErrVerbUnknownVerb {
			// An older daemon than the probe found, or one the probe could
			// not ask: the review keys stop being offered from here on.
			m.Inbox.noReview = true
		}
		if first {
			m.review = reviewState{gen: r.gen, split: r.split}
		} else if r.diff == nil {
			r.loadErr = text
		}
		m.reviewNotify(text, "error", m.Settings.NotificationDuration*2)
		return
	}
	prevPath := ""
	if !first && r.query == msg.Query {
		if e := r.currentFile(); e != nil {
			prevPath = e.path
		}
	}
	samePlace := prevPath != ""
	r.pending, r.open, r.loadErr = false, true, ""
	r.diff = msg.Diff
	r.hunks = nil
	r.notes = msg.Diff.Notes
	r.query = msg.Query
	if r.query.Window == "" {
		r.query.Window = msg.Diff.Window
	}
	if msg.ProbeFan {
		r.fan = msg.Fan
	}
	r.editor = nil
	r.rebuildFiles()
	if samePlace {
		r.file = max(slices.IndexFunc(r.files, func(e reviewFileEntry) bool { return e.path == prevPath }), 0)
	} else {
		r.file, r.cursor, r.scroll, r.listScroll, r.listFocus = 0, 0, 0, 0, false
	}
}

// reviewErrorText says what went wrong with a review call, in the words the
// person needs.
func reviewErrorText(err error) string {
	var callErr *session.VerbCallError
	if errors.As(err, &callErr) {
		switch callErr.Code {
		case session.ErrVerbUnknownVerb:
			return "This daemon cannot review changes. Restart it with a newer tuios: tuios kill-server"
		case session.ErrVerbNotRepo:
			if strings.Contains(callErr.Message, "no git repository") {
				return "No git repository under this pane"
			}
			return "Nothing to review here: " + callErr.Message
		case session.ErrVerbNoNotes:
			return "No unsent notes to send"
		case session.ErrVerbQueueFull:
			return "The agent already has as many messages queued as [agents.queue] max allows. Nothing was sent"
		}
		return callErr.Message
	}
	return err.Error()
}

// rebuildFiles lists the changed files, then any file that carries notes but
// is no longer changed, so its notes can still be read and resolved.
func (r *reviewState) rebuildFiles() {
	r.files = r.files[:0]
	if r.diff == nil {
		return
	}
	for i := range r.diff.Files {
		f := &r.diff.Files[i]
		r.files = append(r.files, reviewFileEntry{file: f, path: f.Path})
	}
	if r.query.Against != "" {
		return
	}
	for _, n := range r.notes {
		if !slices.ContainsFunc(r.files, func(e reviewFileEntry) bool { return e.path == n.Path }) {
			r.files = append(r.files, reviewFileEntry{path: n.Path})
		}
	}
	if r.file >= len(r.files) {
		r.file = max(len(r.files)-1, 0)
	}
}

// currentFile is the file the diff column shows, nil for none.
func (r *reviewState) currentFile() *reviewFileEntry {
	if r.file < 0 || r.file >= len(r.files) {
		return nil
	}
	return &r.files[r.file]
}

// unsentNotes counts the notes S would send.
func (r *reviewState) unsentNotes() int {
	n := 0
	for _, note := range r.notes {
		if note.SentAt == 0 {
			n++
		}
	}
	return n
}

// notesOn is the index of every note on path, in r.notes.
func (r *reviewState) notesOn(path string) []int {
	var out []int
	if r.query.Against != "" {
		return nil
	}
	for i, n := range r.notes {
		if n.Path == path {
			out = append(out, i)
		}
	}
	return out
}

// ReviewClose closes the review, or steps back to the compare view the review
// was opened from.
func (m *OS) ReviewClose() tea.Cmd {
	r := &m.review
	if r.editor != nil {
		r.editor = nil
		return nil
	}
	if r.backToCompare && r.compare != nil {
		r.backToCompare = false
		r.compare.shown = true
		return m.reviewCompareResume()
	}
	m.CloseReview()
	return nil
}

// ReviewReload reads the diff again.
func (m *OS) ReviewReload() tea.Cmd {
	if m.review.loading {
		return nil
	}
	return m.reviewReload()
}

// ReviewToggleFocus moves the keys between the file list and the diff.
func (m *OS) ReviewToggleFocus() {
	m.review.listFocus = !m.review.listFocus
}

// ReviewMove moves the cursor by delta: through the files with the list
// focused, through the diff's rows otherwise.
func (m *OS) ReviewMove(delta int) {
	r := &m.review
	if r.listFocus {
		if len(r.files) == 0 {
			return
		}
		next := min(max(r.file+delta, 0), len(r.files)-1)
		if next != r.file {
			r.file, r.cursor, r.scroll = next, 0, 0
		}
		return
	}
	rows := m.reviewRows(m.reviewRowsWidth())
	if len(rows) == 0 {
		return
	}
	r.cursor = reviewStep(rows, r.cursor, delta)
}

// reviewStep is the row delta steps from cur. A note wrapped onto several
// rows is one step, and the cursor sits on its first row.
func reviewStep(rows []reviewRow, cur, delta int) int {
	cur = reviewNoteStart(rows, min(max(cur, 0), len(rows)-1))
	step := 1
	if delta < 0 {
		step, delta = -1, -delta
	}
	for ; delta > 0; delta-- {
		next := cur + step
		for next >= 0 && next < len(rows) && rows[next].kind == reviewRowNote && !rows[next].first {
			next += step
		}
		if next < 0 || next >= len(rows) {
			break
		}
		cur = next
	}
	return cur
}

// reviewNoteStart is the first row of the note row i is part of, or i when
// it is not a note's later row.
func reviewNoteStart(rows []reviewRow, i int) int {
	for i > 0 && i < len(rows) && rows[i].kind == reviewRowNote && !rows[i].first {
		i--
	}
	return i
}

// ReviewPage moves the diff cursor by a page, down for dir > 0.
func (m *OS) ReviewPage(dir int) {
	page := max(m.review.pageRows-1, 1)
	m.ReviewMove(dir * page)
}

// ReviewEdge moves the cursor to the first row, or the last for end.
func (m *OS) ReviewEdge(end bool) {
	if end {
		m.ReviewMove(1 << 20)
	} else {
		m.ReviewMove(-1 << 20)
	}
}

// ReviewEnter jumps to the file under the list's cursor.
func (m *OS) ReviewEnter() {
	r := &m.review
	if r.listFocus {
		r.listFocus = false
		r.cursor, r.scroll = 0, 0
	}
}

// ReviewFile moves to the next file, or the previous for dir < 0.
func (m *OS) ReviewFile(dir int) {
	r := &m.review
	if len(r.files) == 0 {
		return
	}
	next := r.file + dir
	if next < 0 || next >= len(r.files) {
		return
	}
	r.file, r.cursor, r.scroll = next, 0, 0
}

// ReviewHunk moves to the next hunk, or the previous for dir < 0, going on
// into the next or previous file at either end.
func (m *OS) ReviewHunk(dir int) {
	r := &m.review
	r.listFocus = false
	rows := m.reviewRows(m.reviewRowsWidth())
	if dir > 0 {
		for i := r.cursor + 1; i < len(rows); i++ {
			if rows[i].kind == reviewRowHunk {
				r.cursor = i
				return
			}
		}
		if r.file+1 < len(r.files) {
			r.file, r.cursor, r.scroll = r.file+1, 0, 0
			for i, row := range m.reviewRows(m.reviewRowsWidth()) {
				if row.kind == reviewRowHunk {
					r.cursor = i
					break
				}
			}
		}
		return
	}
	for i := min(r.cursor, len(rows)) - 1; i >= 0; i-- {
		if rows[i].kind == reviewRowHunk {
			r.cursor = i
			return
		}
	}
	if r.file > 0 {
		r.file, r.scroll = r.file-1, 0
		prev := m.reviewRows(m.reviewRowsWidth())
		r.cursor = 0
		for i := len(prev) - 1; i >= 0; i-- {
			if prev[i].kind == reviewRowHunk {
				r.cursor = i
				break
			}
		}
	}
}

// ReviewWheel scrolls the diff by delta rows, moving the cursor with it.
func (m *OS) ReviewWheel(delta int) {
	if m.ReviewCompareShown() {
		m.ReviewCompareMove(delta)
		return
	}
	r := &m.review
	r.listFocus = false
	m.ReviewMove(delta)
}

// reviewRowUnderCursor is the diff row under the cursor.
func (m *OS) reviewRowUnderCursor() (reviewRow, bool) {
	r := &m.review
	if r.listFocus {
		return reviewRow{}, false
	}
	rows := m.reviewRows(m.reviewRowsWidth())
	if r.cursor < 0 || r.cursor >= len(rows) {
		return reviewRow{}, false
	}
	return rows[r.cursor], true
}

// ReviewNote opens the note editor on the line under the cursor, or with
// hunk on the whole hunk it is in.
func (m *OS) ReviewNote(hunk bool) {
	r := &m.review
	if r.query.Against != "" {
		m.reviewNotify("Notes go on a review against the base. esc goes back to it", "info", m.Settings.NotificationDuration)
		return
	}
	e := r.currentFile()
	row, ok := m.reviewRowUnderCursor()
	if e == nil || e.file == nil || !ok || row.hunk < 0 {
		m.reviewNotify("Move to a line of the diff to leave a note on it", "info", m.Settings.NotificationDuration)
		return
	}
	h := e.file.Hunks[row.hunk]
	ed := &reviewEditor{kind: reviewEditNote, path: e.path, hunkIdx: row.hunk, lineIdx: -1, automated: m.ProcessingRemoteKeys}
	if hunk || row.kind == reviewRowHunk {
		ed.hunk = h.Header
		ed.side = review.SideNew
		if h.NewLines == 0 && h.OldLines > 0 {
			ed.side = review.SideOld
		}
	} else {
		if row.kind != reviewRowLine {
			m.reviewNotify("Move to a line of the diff to leave a note on it", "info", m.Settings.NotificationDuration)
			return
		}
		ln := h.Lines[row.line]
		ed.lineIdx = row.line
		ed.quote = ln.Text
		if ln.Op == review.OpDelete {
			ed.side, ed.line = review.SideOld, ln.Old
		} else {
			ed.side, ed.line = review.SideNew, ln.New
		}
	}
	r.editor = ed
}

// reviewNoteUnderCursor is the note the cursor is on.
func (m *OS) reviewNoteUnderCursor() (review.Note, bool) {
	row, ok := m.reviewRowUnderCursor()
	if !ok || row.kind != reviewRowNote || row.note < 0 || row.note >= len(m.review.notes) {
		return review.Note{}, false
	}
	return m.review.notes[row.note], true
}

// ReviewEditNote opens the editor on the note under the cursor.
func (m *OS) ReviewEditNote() {
	n, ok := m.reviewNoteUnderCursor()
	if !ok {
		m.reviewNotify("Move to a note to edit it", "info", m.Settings.NotificationDuration)
		return
	}
	m.review.editor = &reviewEditor{kind: reviewEditNote, noteID: n.ID, draft: n.Text, path: n.Path, automated: m.ProcessingRemoteKeys, hunkIdx: -1, lineIdx: -1}
}

// ReviewResolveNote removes the note under the cursor.
func (m *OS) ReviewResolveNote() tea.Cmd {
	n, ok := m.reviewNoteUnderCursor()
	if !ok {
		m.reviewNotify("Move to a note to resolve it", "info", m.Settings.NotificationDuration)
		return nil
	}
	if m.ProcessingRemoteKeys {
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return nil
	}
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	return m.reviewNoteCmd("remove", map[string]any{"id": n.ID, "human_nonce": nonce})
}

// reviewNonce is this client's attach nonce, or a refusal in the dock.
func (m *OS) reviewNonce() (string, bool) {
	nonce := m.inboxNonce()
	if nonce == "" {
		m.reviewNotify("This daemon issued no attach nonce, so it cannot tell you from an agent. Update the daemon", "error", m.Settings.NotificationDuration*2)
		return "", false
	}
	return nonce, true
}

// reviewNoteCmd sends one review-note action on the reviewed pane.
func (m *OS) reviewNoteCmd(action string, params map[string]any) tea.Cmd {
	r := &m.review
	call := m.inboxCaller()
	gen := r.gen
	params["action"] = action
	params["session"] = r.diff.Session
	params["window"] = r.diff.Window
	return func() tea.Msg {
		msg := ReviewNotesMsg{Gen: gen, Action: action}
		raw, err := call("review-note", params, reviewVerbTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Notes []review.Note `json:"notes"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.Notes = res.Notes
		return msg
	}
}

// applyReviewNotes takes the notes a review-note call answered with.
func (m *OS) applyReviewNotes(msg ReviewNotesMsg) {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return
	}
	if msg.Err != nil {
		m.reviewNotify("The note was not saved: "+reviewErrorText(msg.Err), "error", m.Settings.NotificationDuration*2)
		return
	}
	r.notes = msg.Notes
	path := ""
	if e := r.currentFile(); e != nil {
		path = e.path
	}
	r.rebuildFiles()
	if i := slices.IndexFunc(r.files, func(e reviewFileEntry) bool { return e.path == path }); i >= 0 {
		r.file = i
	}
	if msg.Action == "remove" {
		m.reviewNotify("Note resolved", "info", m.Settings.NotificationDuration)
	}
}

// ReviewSend sends every unsent note to the pane's agent as one message,
// queued until it is at rest.
func (m *OS) ReviewSend() tea.Cmd {
	r := &m.review
	if r.query.Against != "" || r.diff == nil {
		return nil
	}
	if m.ProcessingRemoteKeys {
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return nil
	}
	if r.unsentNotes() == 0 {
		m.reviewNotify("No unsent notes to send. c leaves one on the line under the cursor", "info", m.Settings.NotificationDuration)
		return nil
	}
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	call := m.inboxCaller()
	gen, who := r.gen, r.who
	params := map[string]any{"session": r.diff.Session, "window": r.diff.Window, "human_nonce": nonce}
	return func() tea.Msg {
		msg := ReviewSentMsg{Gen: gen, Who: who}
		raw, err := call("send-review", params, reviewVerbTimeout)
		if err != nil {
			msg.Err = err
			return msg
		}
		var res struct {
			Notes          int      `json:"notes"`
			IDs            []string `json:"ids"`
			Position       int      `json:"position"`
			Delivering     bool     `json:"delivering"`
			Withheld       []string `json:"withheld"`
			WithheldReason string   `json:"withheld_reason"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			msg.Err = err
			return msg
		}
		msg.Notes, msg.IDs, msg.Position, msg.Delivering = res.Notes, res.IDs, res.Position, res.Delivering
		msg.Withheld, msg.WithheldReason = res.Withheld, res.WithheldReason
		return msg
	}
}

// applyReviewSent says what happened to the notes sent.
func (m *OS) applyReviewSent(msg ReviewSentMsg) {
	r := &m.review
	if msg.Err != nil {
		m.reviewNotify("The notes were not sent: "+reviewErrorText(msg.Err), "error", m.Settings.NotificationDuration*2)
		return
	}
	if msg.Gen == r.gen {
		now := time.Now().UnixNano()
		for i := range r.notes {
			if slices.Contains(msg.IDs, r.notes[i].ID) {
				r.notes[i].SentAt = now
			}
		}
	}
	notes := reviewCount(msg.Notes, "note")
	text := notes + " sent to " + msg.Who
	if !msg.Delivering {
		text = notes + " queued, sent when " + msg.Who + " is at rest"
	}
	if len(msg.Withheld) > 0 {
		text += ". " + reviewCount(len(msg.Withheld), "note") + " withheld: " + msg.WithheldReason
	}
	m.reviewNotify(text, "info", m.Settings.NotificationDuration)
}

// reviewCount says n things: "1 note", "2 notes".
func reviewCount(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// ReviewToggleUncommitted switches between the diff since the base and the
// uncommitted changes only.
func (m *OS) ReviewToggleUncommitted() tea.Cmd {
	r := &m.review
	if r.query.Against != "" {
		return nil
	}
	r.query.Uncommitted = !r.query.Uncommitted
	return m.reviewReload()
}

// ReviewBasePrompt opens the base line, filled with the base now shown.
func (m *OS) ReviewBasePrompt() {
	r := &m.review
	if r.query.Against != "" {
		return
	}
	base := r.query.Base
	if base == "" && r.diff != nil && !r.diff.Uncommitted {
		base = r.diff.Base
	}
	r.editor = &reviewEditor{kind: reviewEditBase, draft: base, automated: m.ProcessingRemoteKeys, hunkIdx: -1, lineIdx: -1}
}

// ReviewEditorType appends typed text to the open editor.
func (m *OS) ReviewEditorType(text string) {
	ed := m.review.editor
	if ed == nil || text == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		ed.automated = true
	}
	limit := review.TextMax
	if ed.kind != reviewEditNote {
		limit = 4096
	}
	for _, ch := range text {
		if len(ed.draft)+len(string(ch)) > limit || !printableRune(ch, false) {
			continue
		}
		ed.draft += string(ch)
	}
}

// ReviewEditorBackspace removes the last rune of the editor's text.
func (m *OS) ReviewEditorBackspace() {
	ed := m.review.editor
	if ed == nil || ed.draft == "" {
		return
	}
	if m.ProcessingRemoteKeys {
		ed.automated = true
	}
	rs := []rune(ed.draft)
	ed.draft = string(rs[:len(rs)-1])
}

// ReviewEditorCancel drops what the editor holds.
func (m *OS) ReviewEditorCancel() {
	m.review.editor = nil
}

// ReviewEditorSubmit saves what the editor holds: a note, a base to diff
// from, or a command to verify every attempt with.
func (m *OS) ReviewEditorSubmit() tea.Cmd {
	r := &m.review
	ed := r.editor
	if ed == nil {
		return nil
	}
	text := strings.TrimSpace(ed.draft)
	switch ed.kind {
	case reviewEditBase:
		r.editor = nil
		r.query.Base = text
		if text != "" {
			r.query.Uncommitted = false
		}
		return m.reviewReload()
	case reviewEditVerify:
		if text == "" {
			return nil
		}
		if ed.automated || m.ProcessingRemoteKeys {
			r.editor = nil
			m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
			return nil
		}
		r.editor = nil
		return m.reviewVerifyCmd(text)
	}
	if text == "" {
		return nil
	}
	if ed.automated || m.ProcessingRemoteKeys {
		r.editor = nil
		m.reviewNotify("A note that send-keys typed is not saved: "+reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return nil
	}
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	r.editor = nil
	if ed.noteID != "" {
		return m.reviewNoteCmd("edit", map[string]any{"id": ed.noteID, "text": text, "human_nonce": nonce})
	}
	params := map[string]any{"path": ed.path, "side": ed.side, "text": text, "human_nonce": nonce}
	if ed.hunk != "" {
		params["hunk"] = ed.hunk
	} else {
		params["line"] = ed.line
		params["quote"] = ed.quote
	}
	return m.reviewNoteCmd("add", params)
}

// ReviewToggleSplit switches the diff between one column and the two sides
// next to each other. The cursor stays on the line it was on. On a column
// too narrow for two sides the choice is kept for when it is wide enough,
// and the dock says so.
func (m *OS) ReviewToggleSplit() {
	r := &m.review
	width := m.reviewRowsWidth()
	before, had := m.reviewRowUnderCursor()
	r.split = !r.split
	if e := r.currentFile(); r.split && e != nil && e.file != nil && len(e.file.Hunks) > 0 && !reviewSplitFits(e.file, width) {
		m.reviewNotify("Too narrow for two columns. Widen the window, or s for one column", "info", m.Settings.NotificationDuration)
	}
	if !had {
		return
	}
	rows := m.reviewRows(width)
	for i, row := range rows {
		if row.kind != before.kind || row.hunk != before.hunk {
			continue
		}
		switch row.kind {
		case reviewRowLine:
			if row.line != before.line && row.other != before.line && (before.other < 0 || row.line != before.other) {
				continue
			}
		case reviewRowNote:
			if row.note != before.note || row.first != before.first {
				continue
			}
		}
		r.cursor = i
		return
	}
	r.cursor = min(r.cursor, max(len(rows)-1, 0))
}

// reviewScrollStep is how far one sideways key scrolls the code.
const reviewScrollStep = 8

// ReviewScrollX scrolls the code sideways by dir steps, left for dir < 0,
// no further than the longest line of the file needs.
func (m *OS) ReviewScrollX(dir int) {
	r := &m.review
	e := r.currentFile()
	if e == nil || e.file == nil {
		return
	}
	if e.path != r.xPath {
		r.xPath, r.xOff = e.path, 0
	}
	widest := 0
	for h := range e.file.Hunks {
		for _, text := range r.reviewHunk(e.file, h, -1).text {
			widest = max(widest, ansi.StringWidth(text))
		}
	}
	r.xOff = min(max(r.xOff+dir*reviewScrollStep, 0), max(widest-reviewScrollStep, 0))
}
