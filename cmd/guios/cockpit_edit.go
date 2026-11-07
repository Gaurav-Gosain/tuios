package main

import (
	"log"
	"math"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
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
	// offered. Left is how many lines were not shown yet.
	Complete bool
	Left     int
}

// ckIsEdit reports whether an item is a held edit.
func ckIsEdit(it session.AttentionItem) bool {
	return ckHeld(it) && it.Kind == session.AttentionApproval && it.HeldEdit
}

// ckEditView is the held edit it as a card of width cells draws it, and
// starts the fetch the first time it is asked about a hold. It must be called
// on the UI goroutine.
func (a *App) ckEditView(it session.AttentionItem, width int) CkEditView {
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
	return CkEditView{Lines: e.lines, SHA: e.sha, Complete: e.review.Complete(), Left: e.review.Left()}
}

// ckEditShown records that the card drew lines from up to, not including,
// to of the view at width. A span for another hold, another body or another
// width counts for nothing.
func (a *App) ckEditShown(it session.AttentionItem, sha string, width, from, to int) {
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
func (a *App) ckFetchEdit(it session.AttentionItem) {
	e := &ckEdit{requestID: it.RequestID, loading: true}
	a.ck.edit = e
	// A new body starts at its top.
	a.ck.editScroll.Y = 0
	cl := a.cockpitClient()
	if cl == nil {
		e.loading, e.err = false, "Not connected to the daemon."
		return
	}
	params := map[string]any{"request_id": it.RequestID, "session": it.Session}
	go func() {
		// get-approval only reads, so it takes no nonce: a call that sent
		// one was refused, and the card said it could not read the edit.
		raw, err := cl.Verb("get-approval", params, false)
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
				log.Printf("Warning: get-approval %s: %v", it.RequestID, err)
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

// ckEditRows is the most rows of a held edit's body the card shows at once;
// a longer body scrolls in them.
const ckEditRows = 14

// ckEditMinRows is the fewest rows the body gets in a short window, and
// ckEditChrome the room the card's head, hint and answers take around it.
const (
	ckEditMinRows = 4
	ckEditChrome  = 150
)

// ckEditBlock draws a held edit's body in the card: the lines editreview
// gives for the card's width, one per row, as given, with the taken out
// text on a red wash and the put in text on a green one, at 12 %, and a
// mark beside each row that goes on from the row above. It reports to the
// review exactly the rows that are inside both the body's own viewport and
// the card's, so Allow comes only after the person had every line on
// screen at this width.
func (a *App) ckEditBlock(c *ui.Context, pal *palette, it session.AttentionItem) {
	ck := &a.ck
	mono := a.termFamily()
	rowH := float32(fsCode) * 1.5
	box := ui.Column(c.Key("edit-box")).Radius(rControl).Background(ckCodeBg(pal)).Clip()
	b := box.Bounds()
	// The text column's width in cells: the box, less its padding and the
	// gutter of continuation marks, over one cell of the code font.
	cell, _ := c.MeasureText(0, ui.Span{Text: "0000000000", Size: fsCode, Font: mono})
	cell /= 10
	width := 0
	if b.W > 0 && cell > 0 {
		width = int((b.W - 2*sp3 - ckEditGutter) / cell)
	}
	view := a.ckEditView(it, width)
	box.Children(func() {
		switch {
		case b.W <= 0:
			// The first frame: the width is known in the next one.
			c.AnimationFrame()
			ui.Box(c).Height(rowH)
		case view.Loading:
			ui.Text(c, locales.CockpitEditReading()).FontSize(fsSmall).TextColor(pal.textMuted).Padding(sp2, sp3)
		case view.Err != "":
			ui.Text(c, view.Err).FontSize(fsSmall).TextColor(pal.textMuted).Padding(sp2, sp3).MaxLines(3)
		default:
			n := len(view.Lines)
			// As many rows as fit in the room the card may take, with its
			// head and answers: the tab above gives way down to its least
			// height. The body then scrolls inside the card rather than the
			// card around a body taller than it.
			rows := ckEditRows
			if room := ck.tabH + ck.cardView.H - ckTabMinH; ck.cardView.H > 0 {
				rows = max(ckEditMinRows, min(ckEditRows, int((room-ckEditChrome)/rowH)))
			}
			sc := ui.Scroll(c.Key("edit-body")).TrackScroll(&ck.editScroll).Height(float32(min(n, rows)) * rowH)
			// The rows on screen: inside the body's viewport, and inside
			// the card's, which may scroll the body itself out of sight.
			vb := sc.Bounds()
			top, bottom := vb.Y, vb.Y+vb.H
			if cv := ck.cardView; cv.H > 0 {
				top, bottom = max(top, cv.Y), min(bottom, cv.Y+cv.H)
			}
			// A row's height as the frame laid the rows out: the content's
			// height over its rows, so the rows counted are the ones drawn
			// however the text sized them.
			step := rowH
			if total := ck.editScroll.MaxY + vb.H; vb.H > 0 && n > 0 && total > 0 {
				step = total / float32(n)
			}
			if vb.H <= 0 || vb != ck.editLast[0] || ck.cardView != ck.editLast[1] {
				// The bounds are the last frame's: while the body or the
				// card still moves, draw again, or the rows the settled
				// layout shows would count only once something else
				// redraws the card.
				ck.editLast = [2]ui.Rect{vb, ck.cardView}
				// Invalidate in a build schedules no frame; this does.
				c.AnimationFrame()
			}
			if vb.H > 0 && bottom > top {
				// A row counts only when the whole of it is on screen.
				from := int(math.Ceil(float64((top-vb.Y+ck.editScroll.Y)/step) - 0.01))
				to := int((bottom - vb.Y + ck.editScroll.Y + 0.5) / step)
				a.ckEditShown(it, view.SHA, width, from, min(to, n))
			}
			sc.Children(func() {
				ui.Column(c).Padding(0, sp3, 0, sp3).Children(func() {
					for i := range view.Lines {
						l := view.Lines[i]
						ckEditRow(c, pal, mono, rowH, l)
					}
				})
			})
		}
	})
	// The count after this frame's rows were reported, as the answers
	// under it read it.
	view = a.ckEditView(it, width)
	if !view.Loading && view.Err == "" && !view.Complete && view.Left > 0 {
		// Why there is no Allow yet: the person has not seen all of it.
		ui.Text(c, locales.CockpitEditLeft(view.Left)).FontSize(fsSmall).TextColor(pal.textMuted)
	}
}

// ckEditGutter is the room left of an edit's rows for the continuation mark.
const ckEditGutter = 14

// ckEditRow is one row of a held edit's body.
func ckEditRow(c *ui.Context, pal *palette, mono string, rowH float32, l editreview.Line) {
	bg := ui.Transparent
	fg := pal.text
	switch l.Kind {
	case editreview.KindOld:
		bg = pal.danger.Alpha(0.12)
	case editreview.KindNew:
		bg = pal.done.Alpha(0.12)
	case editreview.KindHead:
		fg = pal.textMuted
	case editreview.KindNote:
		fg = pal.textFaint
	}
	ui.Row(c).Height(rowH).AlignItems(ui.Center).Background(bg).Children(func() {
		ui.Box(c).Width(ckEditGutter).Shrink(0).Children(func() {
			if l.Cont {
				ui.Text(c, "↪").FontSize(fsCaption).TextColor(pal.textFaint)
			}
		})
		t := ui.Text(c, l.Text).Font(mono).FontSize(fsCode).TextColor(fg).NoWrap()
		if l.Kind == editreview.KindHead {
			t.Font("").FontSize(fsSmall).FontWeight(wSemibold)
		}
	})
}
