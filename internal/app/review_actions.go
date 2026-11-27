package app

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Acting on a hunk from the review: X reverts it in the working file, z puts
// it back, and o opens the file in an editor pane at the hunk.
//
// X is a review-revert call on the reviewed pane, carrying this client's
// attach nonce. It names the hunk by its header and the digest of its lines,
// so the daemon reverts only the hunk the person saw. An agent's hunk is
// reverted at once: its turn's checkpoint keeps it. The person's own hunk,
// or one no one is known to have made, is asked about first, since nothing
// else keeps it. A hunk of an agent that writes now is refused: a revert
// under a live writer races its next edit. The agent is not told of a
// revert. The status line says so, and c leaves it a note.
//
// A revert is a step of the same undo list as a stage, so z takes back the
// last of either. Until then the diff shows where the hunk was.

// reviewRevertStep is a revert z can take back: the daemon's id for its
// patch, and what the marker row says.
type reviewRevertStep struct {
	id, path    string
	line, lines int
	// who made the hunk, and in which turn: "claude, turn 3", or empty for
	// the person's own lines.
	who string
}

// reviewRevertAsk is a revert of the person's own lines, waiting for y.
type reviewRevertAsk struct {
	path string
	hunk review.Hunk
	step reviewRevertStep
}

// ReviewRevertedMsg is the answer to a revert, or to its undo.
type ReviewRevertedMsg struct {
	Gen  uint64
	Undo bool
	Step *reviewUndoStep
	// Agent is the agent that made the hunk, empty for the person.
	Agent string
	Err   error
}

// reviewWriterRefusal is what X says on a hunk whose agent works now.
func reviewWriterRefusal(who string) string {
	return who + " writes this hunk now, so X is refused until it stops. c sends it a note instead."
}

// reviewHunkSize is how many lines h changes: its added lines or its
// removed ones, whichever are more, so a line replaced is one.
func reviewHunkSize(h review.Hunk) int {
	add, del := 0, 0
	for _, ln := range h.Lines {
		switch ln.Op {
		case review.OpAdd:
			add++
		case review.OpDelete:
			del++
		}
	}
	return max(add, del)
}

// reviewHunkWriter is the agent writing now among those that made h's
// changed lines, empty for none. A pane of this session says its state now.
// For one in another session the diff says whether it worked when it was
// read.
func (m *OS) reviewHunkWriter(h review.Hunk) string {
	r := &m.review
	for _, ln := range h.Lines {
		a, ok := r.reviewAuthorOf(ln.Origin)
		if ln.Op == review.OpContext || !ok || a.Kind != "agent" {
			continue
		}
		if w := m.windowByID(a.Pane); w != nil {
			if w.AgentState == "working" {
				return reviewText(a.Agent)
			}
			continue
		}
		for _, b := range r.diff.Authors {
			if b.Kind == "agent" && b.Pane == a.Pane && b.Working {
				return reviewText(a.Agent)
			}
		}
	}
	return ""
}

// reviewActAllowed says whether a key that writes may act here, and says why
// not in the dock.
func (m *OS) reviewActAllowed(what string) bool {
	r := &m.review
	switch {
	case r.diff == nil || r.staging:
		return false
	case !r.reviewIndexable():
		m.reviewNotify("You cannot "+what+" here. This works on one pane's own changes", "info", m.Settings.NotificationDuration)
		return false
	case m.ProcessingRemoteKeys:
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return false
	}
	return true
}

// ReviewRevert reverts the hunk under the cursor in the working file, or asks
// first when the lines are the person's.
func (m *OS) ReviewRevert() tea.Cmd {
	r := &m.review
	if !m.reviewActAllowed("revert a hunk") {
		return nil
	}
	e := r.currentFile()
	row, ok := m.reviewRowUnderCursor()
	if e == nil || e.file == nil || !ok || row.hunk < 0 || row.hunk >= len(e.file.Hunks) {
		m.reviewNotify("Move to a hunk to revert it", "info", m.Settings.NotificationDuration)
		return nil
	}
	h := e.file.Hunks[row.hunk]
	if who := m.reviewHunkWriter(h); who != "" {
		m.reviewNotify(reviewWriterRefusal(who), "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	step := reviewRevertStep{path: e.file.Path, line: reviewOpenLine(h, reviewRow{kind: reviewRowHunk}), lines: reviewHunkSize(h)}
	main, _ := reviewMainOrigin(h.Lines)
	if a, ok := r.reviewAuthorOf(main); ok && a.Kind == "agent" {
		step.who = reviewText(a.Agent)
		if a.Turn > 0 {
			step.who += ", turn " + strconv.FormatUint(a.Turn, 10)
		}
		return m.reviewRevertCmd(e.file.Path, h, step, reviewText(a.Agent))
	}
	r.revertAsk = &reviewRevertAsk{path: e.file.Path, hunk: h, step: step}
	return nil
}

// ReviewRevertAsking reports whether a revert of the person's lines waits
// for y.
func (m *OS) ReviewRevertAsking() bool { return m.review.open && m.review.revertAsk != nil }

// ReviewRevertAnswer reverts the hunk asked about, with yes, or keeps it.
func (m *OS) ReviewRevertAnswer(yes bool) tea.Cmd {
	r := &m.review
	ask := r.revertAsk
	r.revertAsk = nil
	if ask == nil || !yes {
		return nil
	}
	if !m.reviewActAllowed("revert a hunk") {
		return nil
	}
	return m.reviewRevertCmd(ask.path, ask.hunk, ask.step, "")
}

// reviewRevertCmd sends review-revert for hunk h of path as the person.
// agent is who made it, for the status line.
func (m *OS) reviewRevertCmd(path string, h review.Hunk, step reviewRevertStep, agent string) tea.Cmd {
	r := &m.review
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	params := map[string]any{
		"action": "revert", "session": r.diff.Session, "window": r.diff.Window,
		"path": path, "hunk": h.Header, "sum": h.Sum(), "human_nonce": nonce,
	}
	if r.query.Base != "" {
		params["base"] = r.query.Base
	}
	if r.query.Uncommitted {
		params["uncommitted"] = true
	}
	call := m.reviewCaller()
	gen, sess, win := r.gen, r.diff.Session, r.diff.Window
	r.staging = true
	return func() tea.Msg {
		msg := ReviewRevertedMsg{Gen: gen, Agent: agent}
		raw, err := call("review-revert", params, reviewVerbTimeout)
		var res struct {
			ID string `json:"id"`
		}
		if err == nil {
			err = jsonx.Unmarshal(raw, &res)
		}
		if msg.Err = err; err == nil {
			step.id = res.ID
			msg.Step = &reviewUndoStep{session: sess, window: win, revert: &step}
		}
		return msg
	}
}

// reviewRevertUndoCmd puts back the hunk of the revert step s.
func (m *OS) reviewRevertUndoCmd(s reviewUndoStep) tea.Cmd {
	r := &m.review
	nonce, ok := m.reviewNonce()
	if !ok {
		return nil
	}
	params := map[string]any{"action": "undo", "session": s.session, "window": s.window, "id": s.revert.id, "human_nonce": nonce}
	call := m.reviewCaller()
	gen := r.gen
	r.staging = true
	return func() tea.Msg {
		_, err := call("review-revert", params, reviewVerbTimeout)
		return ReviewRevertedMsg{Gen: gen, Undo: true, Err: err}
	}
}

// applyReviewReverted shows what a revert or its undo did, and reads the
// diff again.
func (m *OS) applyReviewReverted(msg ReviewRevertedMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return nil
	}
	r.staging = false
	var text string
	kind := "info"
	switch {
	case msg.Undo:
		// Put back, or refused: either way z goes on to the step before.
		if len(r.undo) > 0 {
			r.undo = r.undo[:len(r.undo)-1]
		}
		text = "The hunk is back in the file"
		if msg.Err != nil {
			text, kind = "The hunk was not put back: "+reviewErrorText(msg.Err), "error"
		}
	case msg.Err != nil:
		text, kind = "Nothing reverted: "+reviewErrorText(msg.Err), "error"
		if callErr, ok := errors.AsType[*session.VerbCallError](msg.Err); ok && callErr.Code == session.ErrVerbAgentWriting {
			// The pane started work after the key: the daemon's check.
			who, _, _ := strings.Cut(callErr.Message, " writes this hunk now")
			text, kind = reviewWriterRefusal(who), "warning"
		}
	default:
		r.undo = append(r.undo, *msg.Step)
		key := m.reviewKey(config.ActionReviewUndoStage)
		switch {
		case msg.Agent != "":
			text, kind = "Reverted in the file only. "+msg.Agent+" is not told. c leaves it a note.", "warning"
		case key != "":
			text = "Reverted. " + key + " puts it back"
		default:
			text = "Reverted"
		}
	}
	m.reviewNotify(text, kind, m.Settings.NotificationDuration*2)
	return m.ReviewReload()
}

// reviewLastRevert is the last step of the undo list when it is a revert in
// path.
func (r *reviewState) reviewLastRevert(path string) *reviewRevertStep {
	if len(r.undo) == 0 {
		return nil
	}
	s := r.undo[len(r.undo)-1].revert
	if s == nil || s.path != path {
		return nil
	}
	return s
}

// reviewRevertedWords is the marker row of revert s: "Reverted 4 lines at
// line 39 (claude, turn 3). z undoes".
func (m *OS) reviewRevertedWords(s *reviewRevertStep) string {
	words := "Reverted " + reviewCount(s.lines, "line") + " at line " + strconv.Itoa(s.line)
	if s.who != "" {
		words += " (" + s.who + ")"
	}
	words += "."
	if key := m.reviewKey(config.ActionReviewUndoStage); key != "" {
		words += "  " + key + " undoes"
	}
	return words
}

// ReviewOpenEditor opens the file under the cursor in an editor pane, at the line
// under the cursor or the hunk's first changed line, and closes the review.
func (m *OS) ReviewOpenEditor() tea.Cmd {
	r := &m.review
	e := r.currentFile()
	switch {
	case r.diff == nil || !r.reviewIndexable():
		m.reviewNotify("You cannot open files here. This works on one pane's own changes", "info", m.Settings.NotificationDuration)
		return nil
	case e == nil || e.file == nil:
		m.reviewNotify("Move to a changed file to open it", "info", m.Settings.NotificationDuration)
		return nil
	case e.file.Status == review.StatusDeleted:
		m.reviewNotify("The file is deleted, so there is nothing to open", "info", m.Settings.NotificationDuration)
		return nil
	case m.ProcessingRemoteKeys:
		m.reviewNotify(reviewRemoteRefusal, "error", m.Settings.NotificationDuration)
		return nil
	}
	line := 0
	if row, ok := m.reviewRowUnderCursor(); ok && row.hunk >= 0 && row.hunk < len(e.file.Hunks) {
		line = reviewOpenLine(e.file.Hunks[row.hunk], row)
	}
	full := filepath.Join(r.diff.Worktree, filepath.FromSlash(e.file.Path))
	editor := m.Settings.SidebarEditor
	m.CloseReview()
	if m.AttachedHost != "" {
		msg := remoteEditTarget(full, editor, false, false)
		msg.Line = line
		return m.handleFileEdit(msg)
	}
	return func() tea.Msg {
		msg := inspectEditTarget(full, editor)
		msg.Line = line
		return msg
	}
}

// reviewOpenLine is the line of the working file o opens at: the line under
// the cursor, the line after a removed one, or the hunk's first changed line.
func reviewOpenLine(h review.Hunk, row reviewRow) int {
	at := 0
	if row.kind == reviewRowLine && row.line >= 0 && row.line < len(h.Lines) {
		at = row.line
	} else if c := h.Changes(); len(c) > 0 {
		at = c[0]
	}
	for _, ln := range h.Lines[at:] {
		if ln.New > 0 {
			return ln.New
		}
	}
	return max(h.NewStart, 1)
}
