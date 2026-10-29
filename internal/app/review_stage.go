package app

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Staging from the review overlay: a stages the hunk under the cursor, d
// unstages it, A and D do the same for the whole file, and z undoes the last
// of them. The keys are bindings in [keybindings.review]. Each is a review-stage call on the reviewed pane, carrying this
// client's attach nonce, so only the index of the pane's worktree changes and
// the working files never do.
//
// The overlay shows the diff against the base, while git stages from the
// index diffs. Both index diffs are read once the overlay has a diff, and
// again after each stage, and never while it is closed. The working file is
// the new side of the base diff and of the unstaged diff, so a hunk is
// matched to the index by where its changes sit in the working file. The
// staged diff runs from HEAD to the index, so its places are carried into the
// working file through the unstaged diff first. A hunk shows "staged" when
// its changes are all in the index, "partly staged" when some are.
//
// A key stages exactly the lines of the hunk shown: each index hunk that holds
// some of them is staged with the lines it has in common with it, and the diff
// is read again between two calls, since a stage moves the index side of the
// hunks after it.

// reviewStageMaxCalls bounds the review-stage calls one key makes.
const reviewStageMaxCalls = 16

// reviewUndoMax bounds the undo history the overlay keeps.
const reviewUndoMax = 32

// reviewStageUndo is the undo object review-stage answers with.
type reviewStageUndo struct {
	Entries []review.IndexEntry `json:"entries"`
	After   []review.IndexEntry `json:"after"`
}

// reviewUndoStep is one stage or unstage z can take back.
type reviewUndoStep struct {
	session, window string
	path, what      string
	undo            reviewStageUndo
}

// reviewIndex is the two index diffs of the reviewed worktree, by path.
type reviewIndex struct {
	unstaged, staged map[string]*review.File
}

// reviewHunkStage is how much of a hunk is in the index.
type reviewHunkStage int

const (
	reviewHunkUnknown reviewHunkStage = iota
	reviewHunkUnstaged
	reviewHunkPartly
	reviewHunkStaged
)

// ReviewIndexMsg is the two index diffs, read for the hunk marks.
type ReviewIndexMsg struct {
	Gen   uint64
	Index *reviewIndex
}

// ReviewStagedMsg is the answer to a stage, an unstage or an undo.
type ReviewStagedMsg struct {
	Gen    uint64
	Action string
	// What says what changed: "hunk" or "file".
	What string
	Step *reviewUndoStep
	Err  error
}

// reviewIndexable reports whether the review shows one pane's own changes,
// the only ones it can stage: not a comparison of two attempts, and not the
// tour's fake repository.
func (r *reviewState) reviewIndexable() bool {
	return r.diff != nil && r.query.Against == "" && r.query.Dir == ""
}

// reviewIndexCmd reads both index diffs of the reviewed pane for the hunk
// marks. A daemon that cannot read them leaves the marks off.
func (m *OS) reviewIndexCmd() tea.Cmd {
	r := &m.review
	if !r.reviewIndexable() {
		return nil
	}
	call := m.inboxCaller()
	gen, sess, win := r.gen, r.diff.Session, r.diff.Window
	return func() tea.Msg {
		idx := &reviewIndex{}
		for _, mode := range []string{review.IndexUnstaged, review.IndexStaged} {
			files, err := reviewIndexFiles(call, sess, win, mode, "")
			if err != nil {
				return ReviewIndexMsg{Gen: gen}
			}
			if mode == review.IndexUnstaged {
				idx.unstaged = files
			} else {
				idx.staged = files
			}
		}
		return ReviewIndexMsg{Gen: gen, Index: idx}
	}
}

// reviewIndexFiles reads one index diff, of one path or of every file.
func reviewIndexFiles(call inboxVerbCall, sess, win, mode, path string) (map[string]*review.File, error) {
	params := map[string]any{"session": sess, "window": win, "index": mode}
	if path != "" {
		params["paths"] = []string{path}
	}
	raw, err := call("review-diff", params, reviewDiffTimeout)
	if err != nil {
		return nil, err
	}
	var res reviewDiffResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make(map[string]*review.File, len(res.Files))
	for i := range res.Files {
		out[res.Files[i].Path] = &res.Files[i]
	}
	return out, nil
}

// applyReviewIndex keeps the index diffs that arrived.
func (m *OS) applyReviewIndex(msg ReviewIndexMsg) {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return
	}
	r.index = msg.Index
	r.stageMarks = nil
}

// reviewKey is a change's place in the working file: its line, doubled, and
// one more for an added line. A removed line sits before the working line
// after it.
func reviewChangeKey(line int, add bool) int {
	if add {
		return 2*line + 1
	}
	return 2 * line
}

// reviewHunkKeys calls see with the place of each change of h, and the index
// of the line in h.Lines. Places are on h's new side.
func reviewHunkKeys(h *review.Hunk, see func(key, line int)) {
	next := h.NewStart
	if h.NewLines == 0 {
		next = h.NewStart + 1
	}
	for i, ln := range h.Lines {
		switch ln.Op {
		case review.OpAdd:
			see(reviewChangeKey(ln.New, true), i)
			next = ln.New + 1
		case review.OpDelete:
			see(reviewChangeKey(next, false), i)
		default:
			next = ln.New + 1
		}
	}
}

// reviewIndexToWork carries line pos of the index into the working file,
// through u, the unstaged diff of the file (index to working file). nil u
// means the two are the same.
func reviewIndexToWork(u *review.File, pos int) int {
	if u == nil {
		return pos
	}
	off := 0
	for hi := range u.Hunks {
		h := &u.Hunks[hi]
		start := h.OldStart
		if h.OldLines == 0 {
			start = h.OldStart + 1
		}
		if pos < start {
			break
		}
		if h.OldLines > 0 && pos < h.OldStart+h.OldLines {
			next := h.NewStart
			if h.NewLines == 0 {
				next = h.NewStart + 1
			}
			for _, ln := range h.Lines {
				switch ln.Op {
				case review.OpContext:
					if ln.Old == pos {
						return ln.New
					}
					next = ln.New + 1
				case review.OpAdd:
					next = ln.New + 1
				case review.OpDelete:
					if ln.Old == pos {
						return next
					}
				}
			}
			return pos + off
		}
		off += h.NewLines - h.OldLines
	}
	return pos + off
}

// reviewStagedKeys calls see with the place in the working file of each
// change of staged hunk h, carried through u.
func reviewStagedKeys(h *review.Hunk, u *review.File, see func(key, line int)) {
	reviewHunkKeys(h, func(key, line int) {
		see(reviewChangeKey(reviewIndexToWork(u, key/2), key%2 == 1), line)
	})
}

// reviewKeySet is the places of every change of h.
func reviewKeySet(h *review.Hunk) map[int]bool {
	set := map[int]bool{}
	reviewHunkKeys(h, func(key, _ int) { set[key] = true })
	return set
}

// reviewHunkStageOf is how much of hunk h of the base diff is in the index,
// by the index diffs of its file: u unstaged, s staged.
func reviewHunkStageOf(h *review.Hunk, u, s *review.File) reviewHunkStage {
	want := reviewKeySet(h)
	if len(want) == 0 {
		return reviewHunkUnknown
	}
	inU, inS := false, false
	if u != nil {
		for i := range u.Hunks {
			reviewHunkKeys(&u.Hunks[i], func(key, _ int) { inU = inU || want[key] })
		}
	}
	if s != nil {
		for i := range s.Hunks {
			reviewStagedKeys(&s.Hunks[i], u, func(key, _ int) { inS = inS || want[key] })
		}
	}
	switch {
	case inS && inU:
		return reviewHunkPartly
	case inS:
		return reviewHunkStaged
	case inU:
		return reviewHunkUnstaged
	}
	return reviewHunkUnknown
}

// reviewHunkMark is how much of hunk h of file f the index holds, for its
// mark. It is worked out once per file and index read.
func (r *reviewState) reviewHunkMark(f *review.File, h int) reviewHunkStage {
	if r.index == nil || f == nil || h < 0 || h >= len(f.Hunks) {
		return reviewHunkUnknown
	}
	marks, ok := r.stageMarks[f]
	if !ok {
		u, s := r.index.unstaged[f.Path], r.index.staged[f.Path]
		marks = make([]reviewHunkStage, len(f.Hunks))
		for i := range f.Hunks {
			marks[i] = reviewHunkStageOf(&f.Hunks[i], u, s)
		}
		if r.stageMarks == nil {
			r.stageMarks = map[*review.File][]reviewHunkStage{}
		}
		r.stageMarks[f] = marks
	}
	return marks[h]
}

// reviewMatch finds the first hunk of index diff file x that holds changes
// of want, and the indices of those lines in it. staged says x is the staged
// diff, whose places are carried through u.
func reviewMatch(x, u *review.File, want map[int]bool, staged bool) (*review.Hunk, []int) {
	if x == nil {
		return nil, nil
	}
	for i := range x.Hunks {
		h := &x.Hunks[i]
		var lines []int
		see := func(key, line int) {
			if want[key] {
				lines = append(lines, line)
			}
		}
		if staged {
			reviewStagedKeys(h, u, see)
		} else {
			reviewHunkKeys(h, see)
		}
		if len(lines) > 0 {
			return h, lines
		}
	}
	return nil, nil
}

// ReviewStage stages (or with unstage, unstages) the hunk under the cursor,
// or with whole the file shown.
func (m *OS) ReviewStage(unstage, whole bool) tea.Cmd {
	r := &m.review
	if r.diff == nil {
		return nil
	}
	if !m.reviewStageAllowed() {
		return nil
	}
	e := r.currentFile()
	if e == nil || e.file == nil {
		m.reviewNotify("Move to a changed file to stage it", "info", m.Settings.NotificationDuration)
		return nil
	}
	var hunk *review.Hunk
	if !whole {
		row, ok := m.reviewRowUnderCursor()
		if !ok || row.hunk < 0 || row.hunk >= len(e.file.Hunks) {
			m.reviewNotify("Move to a hunk to stage it", "info", m.Settings.NotificationDuration)
			return nil
		}
		hunk = &e.file.Hunks[row.hunk]
	}
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	action, what := "stage", "file"
	if unstage {
		action = "unstage"
	}
	if !whole {
		what = "hunk"
	}
	call := m.inboxCaller()
	gen, sess, win, path := r.gen, r.diff.Session, r.diff.Window, e.file.Path
	r.staging = true
	return func() tea.Msg {
		msg := ReviewStagedMsg{Gen: gen, Action: action, What: what}
		step := &reviewUndoStep{session: sess, window: win, path: path, what: action + " " + what}
		params := map[string]any{"action": action, "session": sess, "window": win, "human_nonce": nonce, "path": path}
		if whole {
			undo, err := reviewStageCall(call, params)
			if err != nil {
				msg.Err = err
				return msg
			}
			step.undo = undo
			msg.Step = step
			return msg
		}
		want := reviewKeySet(hunk)
		calls := 0
		for calls < reviewStageMaxCalls {
			u, err := reviewIndexFiles(call, sess, win, review.IndexUnstaged, path)
			if err != nil {
				msg.Err = err
				break
			}
			x := u[path]
			if unstage {
				s, err := reviewIndexFiles(call, sess, win, review.IndexStaged, path)
				if err != nil {
					msg.Err = err
					break
				}
				x = s[path]
			}
			h, lines := reviewMatch(x, u[path], want, unstage)
			if h == nil {
				break
			}
			p := maps.Clone(params)
			p["path"] = x.Path
			p["hunk"] = h.Header
			if len(lines) < len(h.Changes()) {
				p["lines"] = lines
			}
			undo, err := reviewStageCall(call, p)
			if err != nil {
				msg.Err = err
				break
			}
			if calls == 0 {
				step.undo.Entries = undo.Entries
			}
			step.undo.After = undo.After
			calls++
		}
		if calls > 0 {
			msg.Step = step
		} else if msg.Err == nil {
			msg.Err = errReviewNothingToStage
		}
		return msg
	}
}

// errReviewNothingToStage is a stage or unstage that found no change of the
// hunk on that side of the index.
var errReviewNothingToStage = errors.New("nothing to change")

// reviewStageCall makes one review-stage call and returns its undo object.
func reviewStageCall(call inboxVerbCall, params map[string]any) (reviewStageUndo, error) {
	raw, err := call("review-stage", params, reviewVerbTimeout)
	if err != nil {
		return reviewStageUndo{}, err
	}
	var res struct {
		Undo reviewStageUndo `json:"undo"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return reviewStageUndo{}, err
	}
	return res.Undo, nil
}

// reviewStageAllowed says whether a stage key may act here, and says why not
// in the dock.
func (m *OS) reviewStageAllowed() bool {
	r := &m.review
	switch {
	case r.query.Against != "":
		m.reviewNotify("You cannot stage in a comparison of two attempts. esc goes back to the review", "info", m.Settings.NotificationDuration)
		return false
	case r.query.Dir != "":
		m.reviewNotify("The tour cannot stage changes", "info", m.Settings.NotificationDuration)
		return false
	case m.ProcessingRemoteKeys:
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return false
	case r.staging:
		return false
	}
	return true
}

// ReviewUndoStage takes back the last stage or unstage made in this review.
func (m *OS) ReviewUndoStage() tea.Cmd {
	r := &m.review
	if r.diff == nil || !m.reviewStageAllowed() {
		return nil
	}
	if len(r.undo) == 0 {
		m.reviewNotify("Nothing to undo", "info", m.Settings.NotificationDuration)
		return nil
	}
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	step := r.undo[len(r.undo)-1]
	call := m.inboxCaller()
	gen := r.gen
	r.staging = true
	return func() tea.Msg {
		msg := ReviewStagedMsg{Gen: gen, Action: "undo"}
		_, err := reviewStageCall(call, map[string]any{
			"action": "undo", "session": step.session, "window": step.window,
			"human_nonce": nonce, "undo": step.undo,
		})
		msg.Err = err
		return msg
	}
}

// applyReviewStaged shows what a stage, an unstage or an undo did, and reads
// the index again for the marks.
func (m *OS) applyReviewStaged(msg ReviewStagedMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return nil
	}
	r.staging = false
	if msg.Err != nil {
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok && msg.Action == "undo" && callErr.Code == session.ErrVerbIndexChanged {
			// That step cannot come back, so z goes on to the one before.
			r.undo = r.undo[:len(r.undo)-1]
		}
		m.reviewNotify(m.reviewStageErrorText(msg), "error", m.Settings.NotificationDuration*2)
		return m.reviewIndexCmd()
	}
	undoKey := m.reviewKey(config.ActionReviewUndoStage)
	switch {
	case msg.Action == "undo":
		r.undo = r.undo[:len(r.undo)-1]
		m.reviewNotify("Undone", "info", m.Settings.NotificationDuration)
	case msg.Step != nil:
		r.undo = append(r.undo, *msg.Step)
		if len(r.undo) > reviewUndoMax {
			r.undo = slices.Delete(r.undo, 0, len(r.undo)-reviewUndoMax)
		}
		text := "Hunk staged"
		switch {
		case msg.Action == "stage" && msg.What == "file":
			text = "File staged"
		case msg.Action == "unstage" && msg.What == "file":
			text = "File unstaged"
		case msg.Action == "unstage":
			text = "Hunk unstaged"
		}
		if undoKey != "" {
			text += ". " + undoKey + " undoes it"
		}
		m.reviewNotify(text, "info", m.Settings.NotificationDuration)
	}
	return m.reviewIndexCmd()
}

// reviewStageErrorText says why a stage, an unstage or an undo did nothing.
func (m *OS) reviewStageErrorText(msg ReviewStagedMsg) string {
	if errors.Is(msg.Err, errReviewNothingToStage) {
		if msg.Action == "unstage" {
			return "Nothing in this hunk is staged"
		}
		return "Nothing in this hunk is left to stage"
	}
	if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok {
		switch callErr.Code {
		case session.ErrVerbUnknownVerb:
			return "This daemon cannot stage changes. " + restartHintPlain
		case session.ErrVerbIndexChanged:
			return "The index changed after that step, so it was not undone. Nothing changed"
		case session.ErrVerbHunkChanged:
			return "The file changed after it was read. Nothing changed. Press r to read it again"
		case session.ErrVerbWholeFileOnly:
			text := "You can stage this file only as a whole. Nothing changed"
			if key := m.reviewKey(config.ActionReviewStageFile); key != "" && msg.Action == "stage" {
				text += ". Press " + key + " to stage the file"
			}
			return text
		}
	}
	return "Nothing changed: " + reviewErrorText(msg.Err)
}

// reviewStageHints adds the stage keys that do something where the cursor is
// to the footer: stage while some of the hunk is not in the index, unstage
// while some is, and undo while there is a step to take back.
func (m *OS) reviewStageHints(add func(key, label string)) {
	r := &m.review
	if !r.reviewIndexable() {
		return
	}
	row, ok := m.reviewRowUnderCursor()
	e := r.currentFile()
	if ok && row.hunk >= 0 && e != nil && e.file != nil {
		mark := r.reviewHunkMark(e.file, row.hunk)
		if mark != reviewHunkStaged {
			if key := m.reviewKey(config.ActionReviewStageHunk); key != "" {
				add(key, "stage")
			}
		}
		if mark == reviewHunkStaged || mark == reviewHunkPartly {
			if key := m.reviewKey(config.ActionReviewUnstageHunk); key != "" {
				add(key, "unstage")
			}
		}
	}
	if len(r.undo) > 0 {
		if key := m.reviewKey(config.ActionReviewUndoStage); key != "" {
			add(key, "undo")
		}
	}
}
