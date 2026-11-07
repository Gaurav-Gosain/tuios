package main

import (
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Held edits: the data path behind the edit card.
//
// A held Edit, MultiEdit or Write is marked held_edit on its item
// (session.AttentionItem.HeldEdit), and the daemon allows it only when the
// reply names edit_sha, the digest get-approval serves with the body
// (edit_unreviewed otherwise). The item does not carry the digest. The body is served by
// get-approval while the hold runs. This file fetches it, checks that its
// digest is the item's, and keeps it for the card to draw.
//
// An allow of an edit is sent only after the card has drawn that exact body
// and said so with ckEditDrawn. Until a card calls it, an edit offers reject
// and the pane, and never an allow: fail closed.
//
// The card calls, on each frame it shows an edit item:
//
//	view := a.ckEditView(it) // fetches on the first call for a hold
//	switch {
//	case view.Loading: // draw "Reading the edit..."
//	case view.Err != "": // draw view.Err and offer the pane
//	default: // draw view.Body (tool, path, hunks), then:
//		a.ckEditDrawn(it, view.SHA)
//	}

// ckEdit is the body get-approval served for one hold.
type ckEdit struct {
	requestID string
	// sha is the edit_sha get-approval served with body, checked against
	// the body's own digest.
	sha     string
	loading bool //nolint:unused // the edit card (pane d1) calls it; see the file comment
	body    *integration.EditBody
	err     string //nolint:unused // the edit card (pane d1) calls it; see the file comment
	// drawn says the card drew body whole. Only then may an allow go.
	drawn bool
}

// CkEditView is what the card draws for a held edit.
type CkEditView struct {
	Loading bool
	// Err is why there is no body to draw. The card then sends the person
	// to the pane.
	Err string
	// Body is the edit, whole, with SHA its digest, the edit_sha
	// get-approval served with it.
	Body *integration.EditBody
	SHA  string
}

// ckIsEdit reports whether an item is a held edit.
func ckIsEdit(it session.AttentionItem) bool {
	return ckHeld(it) && it.Kind == session.AttentionApproval && it.HeldEdit
}

// ckEditView is the held edit it as the card should draw it, and starts the
// fetch the first time it is asked about a hold. It must be called on the UI
// goroutine.
func (a *App) ckEditView(it session.AttentionItem) CkEditView { //nolint:unused // the edit card (pane d1) calls it; see the file comment
	if !ckIsEdit(it) {
		return CkEditView{Err: "not a held edit"}
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
	return CkEditView{Body: e.body, SHA: e.sha}
}

// ckEditDrawn records that the card drew the body of it whose digest is sha,
// whole: every hunk, nothing clipped. It is the one call that lets an allow
// of the edit go. A sha that is not the fetched body's is ignored.
func (a *App) ckEditDrawn(it session.AttentionItem, sha string) { //nolint:unused // the edit card (pane d1) calls it; see the file comment
	if e := a.ck.edit; e != nil && e.body != nil && e.requestID == it.RequestID && sha == e.sha {
		e.drawn = true
	}
}

// ckEditAllowSHA is the edit_sha an allow of it carries, and false when no
// allow may go: the body was not fetched, its digest is not the item's, or the
// card has not drawn it.
func (a *App) ckEditAllowSHA(it session.AttentionItem) (string, bool) {
	e := a.ck.edit
	if e == nil || !e.drawn || e.body == nil || e.requestID != it.RequestID {
		return "", false
	}
	return e.sha, true
}

// ckFetchEdit reads the body of a held edit with get-approval. The body is
// kept only when its own digest is the edit_sha served with it, so the
// card never draws a body the allow would not name.
func (a *App) ckFetchEdit(it session.AttentionItem) { //nolint:unused // the edit card (pane d1) calls it; see the file comment
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
