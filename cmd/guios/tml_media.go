package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif" // data:image/gif
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"
	_ "golang.org/x/image/webp" // data:image/webp
	"tuios.dev/plot"
	"tuios.dev/sketch"
	"tuios.dev/sketch/raster"
	"tuios.dev/tml"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The media of a TML surface: images and Mermaid diagrams. Both decode or
// draw on a goroutine and show when ready, so a large one never holds a
// frame.

// Bounds on what a surface may ask guios to decode. The validator holds a
// data: image to 4 MiB; its pixels are held here, since a small PNG can
// say it is huge.
const (
	tmlImagePixels = 4096 * 4096
	// tmlMermaidScale is the most device pixels per DIP a diagram is drawn
	// at.
	tmlMermaidScale = 3
)

// tmlMedia is one image or diagram of a surface, decoded or drawn.
type tmlMedia struct {
	busy bool
	bm   *ui.Bitmap
	// w and h are the size in DIPs it shows at, at most the width.
	w, h float32
	err  string
	// scale and dark are what a diagram was drawn for, and avail the width
	// it last had.
	scale float32
	dark  bool
	avail float32
}

// media is the entry for key, made on first use.
func (v *tmlView) media(key string) *tmlMedia {
	if v.medias == nil {
		v.medias = map[string]*tmlMedia{}
	}
	m, ok := v.medias[key]
	if !ok {
		m = &tmlMedia{}
		v.medias[key] = m
	}
	return m
}

// mediaKey names an image or diagram by its content, so an edit to the
// source decodes again and a move in the document does not.
func mediaKey(kind, src string) string {
	sum := sha256.Sum256([]byte(src))
	return kind + ":" + base64.RawStdEncoding.EncodeToString(sum[:12])
}

// image draws a data: image. A file: image is in the owner's files
// folder on the daemon's machine, which guios does not read, so it shows
// its alt text as loom does.
func (r *tmlDraw) image(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	alt := r.str(n, "alt", vars)
	src := n.AttrString("src")
	payload, ok := dataImage(src)
	if !ok {
		r.mediaNote(ptr, alt, "")
		return
	}
	m := r.v.media(mediaKey("img", src))
	if m.bm == nil && m.err == "" && !m.busy {
		m.busy = true
		r.a.tmlBackground(func() func() {
			bm, w, h, err := decodeTMLImage(payload)
			return func() {
				m.busy = false
				if err != "" {
					m.err = err
					return
				}
				m.bm, m.w, m.h = bm, w, h
			}
		})
	}
	switch {
	case m.err != "":
		r.mediaNote(ptr, alt, m.err)
	case m.bm == nil:
		ui.Box(c.Key(ptr)).Height(tmlCellH * 4).Radius(rControl).Background(chartTrack(pal))
	default:
		el := r.box(ui.Box(c.Key(ptr)), n, vars).AlignSelf(ui.Start).MaxWidth(m.w).Width(m.w).AspectRatio(m.w / m.h)
		if acts := r.actions(n, "on-press"); len(acts) > 0 && el.Clicked() {
			r.run(acts, ptr, vars, nil)
		}
		bm := m.bm
		el.Radius(rControl).Clip().Draw(func(p *ui.Painter, b ui.Rect) {
			p.Image(bm, b, ui.FillBox)
		})
	}
}

// tmlBackground runs work on a goroutine after the frame, and what it
// returns on the main thread, which draws again.
func (a *App) tmlBackground(work func() func()) {
	if a.tmlWork != nil {
		a.tmlWork(work)
		return
	}
	a.defer_(func() {
		go func() {
			done := work()
			a.update(done)
		}()
	})
}

// dataImage is the payload of a data:image URI of a format TML takes.
func dataImage(src string) ([]byte, bool) {
	for _, t := range [...]string{"png", "jpeg", "gif", "webp"} {
		prefix := "data:image/" + t + ";base64,"
		if rest, ok := strings.CutPrefix(src, prefix); ok {
			b, err := base64.StdEncoding.DecodeString(rest)
			return b, err == nil
		}
	}
	return nil, false
}

// decodeTMLImage decodes an image once its header says its size is within
// bounds, and returns it with its size in DIPs.
func decodeTMLImage(data []byte) (*ui.Bitmap, float32, float32, string) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, locales.TmlImageUnreadable()
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > tmlImagePixels {
		return nil, 0, 0, locales.TmlImageTooLarge()
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, locales.TmlImageUnreadable()
	}
	// An image counts its pixels as DIPs at 2x, as a screenshot of a
	// Retina screen is meant to show.
	return ui.NewBitmap(img), float32(cfg.Width) / 2, float32(cfg.Height) / 2, ""
}

// mediaNote is what shows in place of an image or diagram that cannot:
// its alt text, and why when there is a reason.
func (r *tmlDraw) mediaNote(ptr, alt, why string) {
	c, pal := r.c, r.pal
	ui.Row(c.Key(ptr)).Gap(8).AlignItems(ui.Center).Padding(8, 12).Radius(rControl).Background(chartTrack(pal)).AlignSelf(ui.Start).Children(func() {
		ui.Icon(c, tmlImageIcon).Size(14, 14).TextColor(pal.textFaint)
		if alt != "" {
			ui.Text(c, alt).FontSize(fsSmall).TextColor(pal.textMuted).Italic()
		}
		if why != "" {
			ui.Text(c, why).FontSize(fsSmall).TextColor(pal.textFaint)
		}
	})
}

// mermaid draws a diagram with sketch in the surface's colours, then to a
// bitmap at the screen's scale. A diagram that cannot be drawn shows its
// source under a note, as in loom.
func (r *tmlDraw) mermaid(n *tml.Node, ptr string, _ map[string]any) {
	c, pal := r.c, r.pal
	src := strings.TrimSpace(r.raw(n, nil))
	m := r.v.media(mediaKey("mmd", src))
	if m.err != "" {
		ui.Column(c.Key(ptr)).Gap(6).AlignItems(ui.Stretch).Children(func() {
			ui.Text(c, locales.TmlMermaidFailed(m.err)).FontSize(fsSmall).TextColor(pal.attnText)
			ui.Text(c, src).Font(r.mono).FontSize(fsCode).TextColor(pal.textMuted)
		})
		return
	}
	start := func(scale float32) {
		if m.busy || m.bm != nil && m.scale == scale && m.dark == pal.dark {
			return
		}
		m.busy = true
		th := r.sketchTheme()
		dark := pal.dark
		r.a.tmlBackground(func() func() {
			bm, w, h, err := drawMermaid(src, th, scale)
			return func() {
				m.busy = false
				m.scale, m.dark = scale, dark
				if err != "" {
					m.err = err
					return
				}
				m.bm, m.w, m.h = bm, w, h
			}
		})
	}
	// The drawing starts in the build, at the last scale a frame painted
	// at, so a diagram below the fold has its size before it scrolls into
	// view; the paint draws it again when the scale was another.
	if r.v.scale == 0 {
		r.v.scale = 2
	}
	start(min(r.v.scale, tmlMermaidScale))
	el := ui.Column(c.Key(ptr)).AlignSelf(ui.Stretch).AlignItems(ui.Center)
	el.Draw(func(p *ui.Painter, _ ui.Rect) {
		r.v.scale = p.Scale()
		start(min(p.Scale(), tmlMermaidScale))
	})
	if m.bm == nil {
		el.Height(tmlCellH * 4)
		return
	}
	// A diagram wider than the pane shrinks to fit it. The width is the last
	// frame's, so a change asks for one more frame.
	bm, w, h := m.bm, m.w, m.h
	avail := el.Bounds().W
	if avail <= 0 {
		avail = w
	}
	if avail != m.avail {
		m.avail = avail
		c.Invalidate()
	}
	k := min(1, avail/w)
	el.Children(func() {
		ui.Box(c.Key("d")).Width(w * k).Height(h * k).Shrink(0).Draw(func(p *ui.Painter, b ui.Rect) {
			p.Image(bm, b, ui.FillBox)
		})
	})
}

// drawMermaid renders the source to SVG and the SVG to a bitmap.
func drawMermaid(src string, th *sketch.Theme, scale float32) (*ui.Bitmap, float32, float32, string) {
	out, err := sketch.Render(src, sketch.RenderOptions{Theme: th})
	if err != nil {
		return nil, 0, 0, oneLineText(err.Error())
	}
	img, err := raster.Draw(out.Data, raster.Options{Scale: float64(scale) * fsBody / 16, Theme: th, Readable: true, MaxPixels: 16_000_000})
	if err != nil {
		return nil, 0, 0, oneLineText(err.Error())
	}
	// sketch sets text at 16 px; the diagram shows at the body text's size.
	k := float32(fsBody) / 16
	return ui.NewBitmap(img), float32(out.Width) * k, float32(out.Height) * k, ""
}

// sketchTheme is the surface's palette as a diagram theme: text and lines
// in the text colours, quiet node fills a step off the card, the accent,
// and the chart series colours.
func (r *tmlDraw) sketchTheme() *sketch.Theme {
	pal := r.pal
	nc := func(c ui.Color) color.Color { return color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A} }
	t := &sketch.Theme{
		Bg:         nc(pal.card),
		Fg:         nc(pal.text),
		Muted:      nc(pal.textMuted),
		Line:       nc(pal.textMuted),
		Surface:    nc(pal.card.Mix(pal.text, 0.06).Mix(pal.accent, 0.04)),
		SurfaceAlt: nc(pal.card.Mix(pal.text, 0.11)),
		Border:     nc(pal.card.Mix(pal.text, 0.24)),
		Accent:     nc(pal.accent),
		Critical:   nc(pal.danger),
		Radius:     6,
	}
	for i := range t.Series {
		t.Series[i] = nc(r.chartColor(plot.SeriesRole(i), pal.accent))
	}
	return t
}

// tmlImageIcon marks an image that does not show.
var tmlImageIcon = icon(`<rect x="2.5" y="3" width="11" height="10" rx="1.5"/><circle cx="6" cy="6.5" r="1.2"/><path d="M3 12l3.5-3.5 2.5 2.5 2-2 2.5 2.5"/>`)
