package app

import (
	"encoding/json"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// Staging from the review overlay: a stages the hunk under the cursor, d
// unstages it, A and D do the same for the whole file, and z undoes the last
// of them. The keys are bindings in [keybindings.review]. Each is a
// review-stage call on the reviewed pane, carrying this client's attach
// nonce, so only the index of the pane's worktree changes and the working
// files never do. The daemon reads the index diff and builds the patch; the
// overlay only names the hunk and its lines.
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
// A key stages exactly the lines of the hunk shown that the first index hunk
// holding some of them has in common with it.

// reviewUndo is review-stage's undo object, kept as the daemon wrote it.
type reviewUndo struct {
	Entries json.RawMessage `json:"entries"`
	After   json.RawMessage `json:"after"`
}

// reviewUndoStep is one stage or unstage z can take back.
type reviewUndoStep struct {
	session, window string
	undo            reviewUndo
}

// reviewIndex is the two index diffs of the reviewed worktree, by path.
type reviewIndex struct {
	unstaged, staged map[string]*review.File
}

// reviewHunkStage says where a hunk's changes are: reviewInWork for some not
// in the index, reviewInIndex for some in it. Zero is neither, or not known.
type reviewHunkStage uint8

const (
	reviewInWork reviewHunkStage = 1 << iota
	reviewInIndex
)

// ReviewIndexMsg is the two index diffs, read for the hunk marks.
type ReviewIndexMsg struct {
	Gen   uint64
	Index *reviewIndex
}

// ReviewStagedMsg is the answer to a stage, an unstage or an undo.
type ReviewStagedMsg struct {
	Gen uint64
	// Done is what the dock says on success, "Hunk staged" and the like.
	Done string
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
		u, err := reviewIndexFiles(call, sess, win, review.IndexUnstaged, "")
		if err != nil {
			return ReviewIndexMsg{Gen: gen}
		}
		s, err := reviewIndexFiles(call, sess, win, review.IndexStaged, "")
		if err != nil {
			return ReviewIndexMsg{Gen: gen}
		}
		return ReviewIndexMsg{Gen: gen, Index: &reviewIndex{unstaged: u, staged: s}}
	}
}

// reviewIndexFiles reads one index diff, of one path or of every file.
func reviewIndexFiles(call inboxVerbCall, sess, win, mode, path string) (map[string]*review.File, error) {
	params := map[string]any{"session": sess, "window": win, "index": mode}
	if path != "" {
		params["paths"] = []string{path}
	}
	res, err := reviewDiffCall(call, params)
	if err != nil {
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
	if msg.Gen == r.gen && r.open {
		r.index, r.stageMarks = msg.Index, nil
	}
}

// reviewKeys maps the place in the working file of each change of h to the
// change's index in h.Lines. A place is the line doubled, plus one for an
// added line; a removed line sits before the working line after it. With
// carry, h is a hunk of the staged diff, whose index lines are carried into
// the working file through u, the unstaged diff of the file.
func reviewKeys(h *review.Hunk, carry bool, u *review.File) map[int]int {
	keys := map[int]int{}
	next := h.NewStart
	if h.NewLines == 0 {
		next++
	}
	for i, ln := range h.Lines {
		pos, add := next, 0
		switch ln.Op {
		case review.OpAdd:
			pos, add = ln.New, 1
			next = ln.New + 1
		case review.OpDelete:
		default:
			next = ln.New + 1
			continue
		}
		if carry {
			pos = reviewIndexToWork(u, pos)
		}
		keys[2*pos+add] = i
	}
	return keys
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
		if h.OldLines == 0 && pos <= h.OldStart || pos < h.OldStart {
			break
		}
		if pos < h.OldStart+h.OldLines {
			next := h.NewStart
			if h.NewLines == 0 {
				next++
			}
			for _, ln := range h.Lines {
				if ln.Old == pos {
					if ln.Op == review.OpContext {
						return ln.New
					}
					return next
				}
				if ln.Op != review.OpDelete {
					next = ln.New + 1
				}
			}
			return pos + off
		}
		off += h.NewLines - h.OldLines
	}
	return pos + off
}

// reviewMatch finds the first hunk of index diff file x that holds changes
// of want, and the indices of those lines in it, in order. carry says x is
// the staged diff, whose places are carried through u.
func reviewMatch(x, u *review.File, want map[int]int, carry bool) (*review.Hunk, []int) {
	if x == nil {
		return nil, nil
	}
	for i := range x.Hunks {
		var lines []int
		for key, line := range reviewKeys(&x.Hunks[i], carry, u) {
			if _, ok := want[key]; ok {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			slices.Sort(lines)
			return &x.Hunks[i], lines
		}
	}
	return nil, nil
}

// reviewHunkMark says where the changes of hunk h of file f are, for its
// mark. It is worked out once per file and index read.
func (r *reviewState) reviewHunkMark(f *review.File, h int) reviewHunkStage {
	if r.index == nil || f == nil || h < 0 || h >= len(f.Hunks) {
		return 0
	}
	marks, ok := r.stageMarks[f]
	if !ok {
		u, s := r.index.unstaged[f.Path], r.index.staged[f.Path]
		marks = make([]reviewHunkStage, len(f.Hunks))
		for i := range f.Hunks {
			want := reviewKeys(&f.Hunks[i], false, nil)
			if x, _ := reviewMatch(u, nil, want, false); x != nil {
				marks[i] |= reviewInWork
			}
			if x, _ := reviewMatch(s, u, want, true); x != nil {
				marks[i] |= reviewInIndex
			}
		}
		if r.stageMarks == nil {
			r.stageMarks = map[*review.File][]reviewHunkStage{}
		}
		r.stageMarks[f] = marks
	}
	return marks[h]
}

// ReviewStage stages (or with unstage, unstages) the hunk under the cursor,
// or with whole the file shown.
func (m *OS) ReviewStage(unstage, whole bool) tea.Cmd {
	r := &m.review
	if !m.reviewStageAllowed() {
		return nil
	}
	e := r.currentFile()
	if e == nil || e.file == nil {
		m.reviewNotify("Move to a changed file to stage it", "info", m.Settings.NotificationDuration)
		return nil
	}
	var hunk *review.Hunk
	done := "File "
	if !whole {
		row, ok := m.reviewRowUnderCursor()
		if !ok || row.hunk < 0 || row.hunk >= len(e.file.Hunks) {
			m.reviewNotify("Move to a hunk to stage it", "info", m.Settings.NotificationDuration)
			return nil
		}
		hunk, done = &e.file.Hunks[row.hunk], "Hunk "
	}
	action := "stage"
	if unstage {
		action = "unstage"
	}
	return m.reviewStageCmd(map[string]any{"action": action, "path": e.file.Path}, hunk, done+action+"d")
}

// ReviewUndoStage takes back the last stage or unstage made in this review.
func (m *OS) ReviewUndoStage() tea.Cmd {
	r := &m.review
	if !m.reviewStageAllowed() {
		return nil
	}
	if len(r.undo) == 0 {
		m.reviewNotify("Nothing to undo", "info", m.Settings.NotificationDuration)
		return nil
	}
	step := r.undo[len(r.undo)-1]
	return m.reviewStageCmd(map[string]any{"action": "undo", "session": step.session, "window": step.window, "undo": step.undo}, nil, "Undone")
}

// reviewStageCmd sends review-stage with params as the person: as they are
// for a whole file or an undo, and for hunk with the index hunk that holds
// its first lines, and those lines. A hunk git splits in two in the index
// shows partly staged after one key, and the next key stages the rest. done
// is what the dock says when it worked.
func (m *OS) reviewStageCmd(params map[string]any, hunk *review.Hunk, done string) tea.Cmd {
	r := &m.review
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	params["human_nonce"] = nonce
	if params["session"] == nil {
		params["session"], params["window"] = r.diff.Session, r.diff.Window
	}
	call := m.inboxCaller()
	gen := r.gen
	r.staging = true
	return func() tea.Msg {
		msg := ReviewStagedMsg{Gen: gen, Done: done}
		sess, _ := params["session"].(string)
		win, _ := params["window"].(string)
		if hunk != nil {
			path, _ := params["path"].(string)
			unstage := params["action"] == "unstage"
			u, err := reviewIndexFiles(call, sess, win, review.IndexUnstaged, path)
			x := u[path]
			if err == nil && unstage {
				var s map[string]*review.File
				s, err = reviewIndexFiles(call, sess, win, review.IndexStaged, path)
				x = s[path]
			}
			if err != nil {
				msg.Err = err
				return msg
			}
			h, lines := reviewMatch(x, u[path], reviewKeys(hunk, false, nil), unstage)
			if h == nil {
				msg.Err = errReviewNothingToStage
				return msg
			}
			params["hunk"] = h.Header
			if len(lines) < len(h.Changes()) {
				params["lines"] = lines
			}
		}
		raw, err := call("review-stage", params, reviewVerbTimeout)
		var res struct {
			Undo reviewUndo `json:"undo"`
		}
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		msg.Err = err
		if err == nil {
			msg.Step = &reviewUndoStep{session: sess, window: win, undo: res.Undo}
		}
		return msg
	}
}

// errReviewNothingToStage is a stage or unstage that found no change of the
// hunk on that side of the index.
var errReviewNothingToStage = errors.New("nothing to change")

// reviewStageAllowed says whether a stage key may act here, and says why not
// in the dock.
func (m *OS) reviewStageAllowed() bool {
	r := &m.review
	switch {
	case r.diff == nil || r.staging:
		return false
	case !r.reviewIndexable():
		m.reviewNotify("You cannot stage changes here. Staging works on one pane's own changes", "info", m.Settings.NotificationDuration)
		return false
	case m.ProcessingRemoteKeys:
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return false
	}
	return true
}

// applyReviewStaged shows what a stage, an unstage or an undo did, and reads
// the index again for the marks.
func (m *OS) applyReviewStaged(msg ReviewStagedMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return nil
	}
	r.staging = false
	undo := msg.Done == "Undone"
	switch {
	case undo:
		// Undone, or refused: either way z goes on to the step before.
		r.undo = r.undo[:len(r.undo)-1]
	case msg.Step != nil:
		r.undo = append(r.undo, *msg.Step)
	}
	text := msg.Done
	switch {
	case msg.Err == errReviewNothingToStage:
		text = "Nothing in this hunk is on that side of the index"
	case msg.Err != nil:
		text = "Nothing changed: " + reviewErrorText(msg.Err)
	case !undo:
		if key := m.reviewKey(config.ActionReviewUndoStage); key != "" {
			text += ". " + key + " undoes it"
		}
	}
	kind := "info"
	if msg.Err != nil {
		kind = "error"
	}
	m.reviewNotify(text, kind, m.Settings.NotificationDuration)
	return m.reviewIndexCmd()
}

// reviewStageHints adds the stage keys that do something where the cursor is
// to the footer: stage while some of the hunk is not in the index, unstage
// while some is, and undo while there is a step to take back.
func (m *OS) reviewStageHints(add func(key, label string)) {
	r := &m.review
	if !r.reviewIndexable() {
		return
	}
	hint := func(action, label string) {
		if key := m.reviewKey(action); key != "" {
			add(key, label)
		}
	}
	if row, ok := m.reviewRowUnderCursor(); ok && row.hunk >= 0 {
		var mark reviewHunkStage
		if e := r.currentFile(); e != nil {
			mark = r.reviewHunkMark(e.file, row.hunk)
		}
		if mark != reviewInIndex {
			hint(config.ActionReviewStageHunk, "stage")
		}
		if mark&reviewInIndex != 0 {
			hint(config.ActionReviewUnstageHunk, "unstage")
		}
	}
	if len(r.undo) > 0 {
		hint(config.ActionReviewUndoStage, "undo")
	}
}
