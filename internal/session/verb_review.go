package session

import (
	"encoding/json"
	"slices"
)

// Review: the diff of what an agent changed, the notes the person leaves on
// it, and sending those notes to the agent.
//
// review-diff reads, and is held like every read: a pane without the admin
// grant reaches its own session and its fan group (scopeRead), and a link needs
// write, because the answer carries file contents that list must not reach
// (the reasoning bundle-worktree follows). review-note writes the daemon's note
// store and send-review types into the pane through the delivery queue, so
// both are scopeWrite, and send-review is a typing verb (typingVerbs), which
// holds the target to panes that hold nothing the caller does not. A note or a
// message is the person's only with a live human nonce.

// Error codes the review verbs raise, on top of the shared ones.
const (
	// ErrVerbNotRepo reports a pane or session with no git repository under
	// it, so there is nothing to review. Nothing was read.
	ErrVerbNotRepo = "not_repo"
	// ErrVerbNoNotes reports a send-review that found no unsent notes to
	// send. Nothing was typed.
	ErrVerbNoNotes = "no_notes"
)

// reviewParams are what review-diff takes.
type reviewParams struct {
	Session     string   `json:"session"`
	Window      string   `json:"window"`
	Base        string   `json:"base"`
	Against     string   `json:"against"`
	Uncommitted bool     `json:"uncommitted"`
	Paths       []string `json:"paths"`
	Context     *int     `json:"context"`
}

// verbReviewDiff answers review-diff.
func (d *Daemon) verbReviewDiff(_ *connState, params json.RawMessage) (any, *verbError) {
	var p reviewParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Context != nil && (*p.Context < 0 || *p.Context > 20) {
		return nil, invalidParam("context", "context is 0 to 20 lines")
	}
	return nil, notBuilt("review-diff")
}

// reviewNoteParams are what review-note takes.
type reviewNoteParams struct {
	Action     string `json:"action"`
	Session    string `json:"session"`
	Window     string `json:"window"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side"`
	Quote      string `json:"quote"`
	Hunk       string `json:"hunk"`
	Text       string `json:"text"`
	ID         string `json:"id"`
	HumanNonce string `json:"human_nonce"`
}

// verbReviewNote answers review-note.
func (d *Daemon) verbReviewNote(_ *connState, params json.RawMessage) (any, *verbError) {
	var p reviewNoteParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(reviewNoteActions, p.Action) {
		return nil, invalidParam("action", "action is one of the note actions", reviewNoteActions...)
	}
	if p.Side != "" && !slices.Contains(reviewNoteSides, p.Side) {
		return nil, invalidParam("side", "side is new or old", reviewNoteSides...)
	}
	return nil, notBuilt("review-note")
}

// sendReviewParams are what send-review takes.
type sendReviewParams struct {
	Session    string   `json:"session"`
	Window     string   `json:"window"`
	IDs        []string `json:"ids"`
	Now        bool     `json:"now"`
	HumanNonce string   `json:"human_nonce"`
}

// verbSendReview answers send-review.
func (d *Daemon) verbSendReview(_ *connState, params json.RawMessage) (any, *verbError) {
	var p sendReviewParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	return nil, notBuilt("send-review")
}
