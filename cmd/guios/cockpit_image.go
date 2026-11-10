package main

import (
	"bytes"
	"image"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Images in a thread. A page of the conversation names an image on its
// "[image]" entry (Entry.Image) and leaves its data in the transcript; the
// thread reads the data by the entry's id (agent-transcript with image)
// when the item first shows, and draws it as a thumbnail in its bubble. A
// click, or Space on the agent's row, shows it whole over the agents view.
// A daemon that names no image leaves the "[image]" text, drawn as before.

// Thumbnail bounds, in DIPs.
const (
	ckThumbW = 240
	ckThumbH = 160
)

// ckThreadImage is one image of a thread, as read.
type ckThreadImage struct {
	bm      *ui.Bitmap
	w, h    int
	loading bool
	failed  bool
}

// threadImage is the entry's image, read once.
func (a *App) threadImage(th *ckThread, e transcriptview.Entry) *ckThreadImage {
	if th.images == nil {
		th.images = map[string]*ckThreadImage{}
	}
	im := th.images[e.ID]
	if im != nil {
		return im
	}
	im = &ckThreadImage{loading: true}
	th.images[e.ID] = im
	cl := a.cockpitClient()
	if cl == nil {
		im.loading, im.failed = false, true
		return im
	}
	ag, id := th.agent, e.ID
	go func() {
		var out struct {
			Data []byte `json:"data"`
		}
		err := cl.VerbInto("agent-transcript", map[string]any{"session": ag.Session, "window": ag.Window, "image": id}, true, &out)
		var bm *ui.Bitmap
		var w, h int
		if err == nil {
			if img, _, derr := image.Decode(bytes.NewReader(out.Data)); derr == nil {
				b := img.Bounds()
				w, h = b.Dx(), b.Dy()
				bm = ui.NewBitmap(img)
			}
		}
		a.update(func() {
			im.loading = false
			im.bm, im.w, im.h = bm, w, h
			im.failed = bm == nil
		})
	}()
	return im
}

// thumbSize is an image's size fit inside the thumbnail bounds.
func thumbSize(w, h int) (float32, float32) {
	if w <= 0 || h <= 0 {
		return ckThumbW, ckThumbH
	}
	k := min(float32(ckThumbW)/float32(w), float32(ckThumbH)/float32(h), 1)
	return max(float32(w)*k, 24), max(float32(h)*k, 24)
}

// imageItem draws an image of the thread: in a bubble on the right for the
// person's, on the left for the agent's.
func (a *App) imageItem(c *ui.Context, pal *palette, th *ckThread, it *ckItem) {
	user := it.e.Role == transcriptview.RoleUser
	im := a.threadImage(th, it.e)
	align := ui.Start
	if user {
		align = ui.End
	}
	ui.Column(c).AlignItems(align).Gap(4).Children(func() {
		if user {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, locales.CockpitYou()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.textMuted)
				ui.Text(c, clock(it.at)).FontSize(fsSmall).TextColor(pal.textFaint)
			})
		}
		switch {
		case im.bm != nil:
			w, h := thumbSize(im.w, im.h)
			b := ui.ButtonBase(c.Key("img")).Size(w, h).Radius(rCard).Clip().Cursor(ui.CursorPointer).Label(locales.CockpitImage())
			pointerTooltip(c, b, "img-tip", locales.CockpitImageOpen())
			b.Children(func() {
				ui.Image(c, im.bm).Size(w, h).Fit(ui.Cover).Radius(rCard)
			})
			if b.Clicked() {
				c.Invalidate()
				a.ck.lightbox = im
				a.ck.fleetFocus.Focus()
			}
		case im.loading:
			ui.Box(c).Size(ckThumbW, ckThumbH).Radius(rCard).Background(pal.accent.Alpha(0.10)).Center().Children(func() { ui.Spinner(c) })
		default:
			ui.Text(c, it.e.Text).FontSize(fsProse).TextColor(pal.textMuted).Padding(10, 14).Radius(14, 14, 4, 14).Background(pal.accent.Alpha(0.16))
		}
	})
}

// lightbox shows an image whole over the agents view. A click, Escape or
// Space closes it.
func (a *App) lightbox(c *ui.Context, pal *palette) {
	im := a.ck.lightbox
	if im == nil || im.bm == nil {
		return
	}
	veil := ui.Column(c.Key("lightbox")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Radius(rPane).
		Background(pal.card.Alpha(0.94)).AlignItems(ui.Center).Justify(ui.Center).Cursor(ui.CursorPointer).Label(locales.CockpitImage())
	if veil.Clicked() {
		c.Invalidate()
		a.ck.lightbox = nil
	}
	// Whole, at most its own size: a small image is not blown up.
	room := veil.Bounds()
	w, h := float32(im.w), float32(im.h)
	if room.W > 64 && room.H > 64 {
		k := min((room.W-64)/w, (room.H-64)/h, 1)
		w, h = w*k, h*k
	}
	veil.Children(func() {
		ui.Image(c, im.bm).Size(w, h).Fit(ui.Contain).Radius(rCard)
	})
}
