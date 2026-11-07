package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/guios/editreview"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Held edits: the data path behind the edit card.
//
// A held Edit, MultiEdit or Write is marked held_edit on its item
// (session.AttentionItem.HeldEdit), and the daemon allows it only when the
// reply names edit_sha, the digest get-approval serves with the body
// (edit_unreviewed otherwise). The item does not carry the digest.
//
// This file fetches the body, checks its digest, and turns it into the lines
// the card draws (internal/guios/editreview): wrapped to the card's width,
// never clipped, with tabs as spaces, trailing spaces marked, and a head on
// each hunk that says what it does. The card draws those lines and nothing
// else for the body, and reports every span it drew. An allow goes only once
// every line was reported at the width the card draws now: a card that shows
// part of a long body offers no allow until the person scrolled through all
// of it.
//
// The card calls, on each frame it shows an edit item:
//
//	view := a.ckEditView(it, width) // fetches on the first call for a hold
//	switch {
//	case view.Loading: // draw "Reading the edit..."
//	case view.Err != "": // draw view.Err and offer the pane
//	default:
//		// draw view.Lines[from:to] by Kind, one per row, as given
//		a.ckEditShown(it, view.SHA, width, from, to)
//		// view.Complete says whether an allow may be offered
//	}
//
// A width change starts the count again, since the lines wrap differently.

// ckEdit is the body get-approval served for one hold.
type ckEdit struct {
	requestID string
	// sha is the edit_sha get-approval served with body, checked against
	// the body's own digest.
	sha     string
	loading bool
	body    *integration.EditBody
	err     string
	// lines and review are the body drawn at review's width, and which of
	// those lines the card reported as shown.
	lines  []editreview.Line
	review *editreview.Review
}

// CkEditView is what the card draws for a held edit.
type CkEditView struct {
	Loading bool
	// Err is why there is no body to draw. The card then sends the person
	// to the pane.
	Err string
	// Lines are the body at the width asked for. The card draws them as they
	// are: each fits the width and holds no tab or line break.
	Lines []editreview.Line
	// SHA is the edit_sha get-approval served, the body's own digest. The
	// card passes it back to ckEditShown.
	SHA string
	// Complete says every line was shown at this width, so an allow may be
	// offered.
	Complete bool
}

// ckIsEdit reports whether an item is a held edit.
func ckIsEdit(it session.AttentionItem) bool {
	return ckHeld(it) && it.Kind == session.AttentionApproval && it.HeldEdit
}

// ckEditView is the held edit it as a card of width cells draws it, and
// starts the fetch the first time it is asked about a hold. It must be called
// on the UI goroutine.
func (a *App) ckEditView(it session.AttentionItem, width int) CkEditView { //nolint:unused // the edit card (pane d1) calls it; see the file comment
	if !ckIsEdit(it) {
		return CkEditView{Err: "This is not a held edit."}
	}
	e := a.ck.edit
	if e == nil || e.requestID != it.RequestID {
		a.ckFetchEdit(it)
		e = a.ck.edit
	}
	switch {
	case e.loading:
		return CkEditView{Loading: true}
	case e.err != "":
		return CkEditView{Err: e.err}
	}
	if e.review == nil || e.review.Width() != width {
		e.lines = editreview.Lines(*e.body, width)
		e.review = editreview.New(len(e.lines), width)
	}
	if e.lines == nil {
		return CkEditView{Err: "The card is too narrow to show the edit. Open the pane to answer it."}
	}
	return CkEditView{Lines: e.lines, SHA: e.sha, Complete: e.review.Complete()}
}

// ckEditShown records that the card drew lines from up to, not including,
// to of the view at width. A span for another hold, another body or another
// width counts for nothing.
func (a *App) ckEditShown(it session.AttentionItem, sha string, width, from, to int) { //nolint:unused // the edit card (pane d1) calls it; see the file comment
	if e := a.ck.edit; e != nil && e.body != nil && e.requestID == it.RequestID && sha == e.sha {
		e.review.Shown(width, from, to)
	}
}

// ckEditAllowSHA is the edit_sha an allow of it carries, and false when no
// allow may go: the body was not fetched or its digest did not check, or not
// every line of it was shown at the width the card draws now.
func (a *App) ckEditAllowSHA(it session.AttentionItem) (string, bool) {
	e := a.ck.edit
	if e == nil || e.body == nil || e.requestID != it.RequestID || !e.review.Complete() {
		return "", false
	}
	return e.sha, true
}

// ckFetchEdit reads the body of a held edit with get-approval. The body is
// kept only when its own digest is the edit_sha served with it and it is
// showable, so the card never draws a body the allow would not name.
func (a *App) ckFetchEdit(it session.AttentionItem) { //nolint:unused // the edit card (pane d1) calls it through ckEditView
	e := &ckEdit{requestID: it.RequestID, loading: true}
	a.ck.edit = e
	cl := a.cockpitClient()
	if cl == nil {
		e.loading, e.err = false, "Not connected to the daemon."
		return
	}
	params := map[string]any{"request_id": it.RequestID, "session": it.Session}
	go func() {
		raw, err := cl.Verb("get-approval", params, true)
		var d session.ApprovalDetail
		if err == nil {
			err = jsonUnmarshal(raw, &d)
		}
		a.update(func() {
			if a.ck.edit != e {
				return
			}
			e.loading = false
			switch {
			case err != nil:
				e.err = "Could not read the edit. Open the pane to answer it."
			case d.Edit == nil || d.EditSHA == "" || d.Edit.Digest() != d.EditSHA || !d.Edit.Showable():
				e.err = "The edit changed. Open the pane to answer it."
			default:
				e.body, e.sha = d.Edit, d.EditSHA
			}
			a.refreshCockpit()
		})
	}()
}
