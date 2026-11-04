package main

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Pictures: fit to the viewer, a small one at its own size; + - 0, a pinch
// or Ctrl and the wheel zoom at the pointer; a drag, the wheel or the arrows
// pan a picture zoomed past the viewer; a double click goes between fit and
// its own size. A GIF of several frames plays, space holds it, and J and K
// step a frame at a time. An SVG draws as a drawing, at every zoom sharp.

// picPad is the margin a picture keeps from the card's edges at fit.
const picPad = 16

// gifMemMax bounds a GIF's frames in memory, a byte a pixel each: a larger
// one shows its first frame.
const gifMemMax = 256 << 20

// lookImage is a picture's state.
type lookImage struct {
	bm     *ui.Bitmap
	svg    *ui.SVG
	w, h   int // its own size
	format string
	err    string
	// shownFrames counts the frames the bitmap showed in: its pixels leave
	// the CPU after a few. bmW is its width in pixels, sharpAsked that it
	// was asked again at preview.MaxPx.
	shownFrames int
	bmW         int
	sharpAsked  bool
	// scale is DIPs per picture pixel, 0 to fit; shown is the scale the
	// last frame drew at, which the header says.
	scale, shown float32
	// cx and cy are the picture's point at the viewer's centre, in its own
	// pixels, while zoomed.
	cx, cy float32
	boxW   float32
	boxH   float32

	// A GIF of frames: the player plays them; step is a second decoder
	// for J and K while it holds, drawn into stepBm.
	player *ui.VideoPlayer
	data   []byte
	frames int
	stepAt int // the frame shown while stepping, -1 while it plays
	step   ui.VideoDecoder
	stepBm *ui.Bitmap
	// playFrom is when the GIF began playing: it holds after gifPlayMax,
	// so a viewer left open costs nothing.
	playFrom time.Time
}

// gifPlayMax is how long a GIF plays before it holds on its frame. Space or
// the play button plays it again.
const gifPlayMax = 20 * time.Second

// fit is the scale that shows the picture whole in the box, a small one at
// its own size.
func (im *lookImage) fit() float32 {
	if im.w == 0 || im.h == 0 || im.boxW <= 0 || im.boxH <= 0 {
		return 1
	}
	return min(im.boxW/float32(im.w), im.boxH/float32(im.h), 1)
}

// effScale is the scale the picture shows at.
func (im *lookImage) effScale() float32 {
	if im.scale > 0 {
		return im.scale
	}
	return im.fit()
}

// zoom zooms by k about the box point (px, py), the centre for -1; 0 goes
// back to fit.
func (im *lookImage) zoom(k, px, py float32) {
	if k == 0 {
		im.scale = 0
		return
	}
	s := im.effScale()
	ns := min(max(s*k, im.fit()*0.25, 0.02), 32)
	if px < 0 {
		px, py = im.boxW/2, im.boxH/2
	}
	if im.scale == 0 {
		im.cx, im.cy = float32(im.w)/2, float32(im.h)/2
	}
	// The picture's point under (px, py) stays there.
	ox, oy := im.cx+(px-im.boxW/2)/s, im.cy+(py-im.boxH/2)/s
	im.cx, im.cy = ox-(px-im.boxW/2)/ns, oy-(py-im.boxH/2)/ns
	im.scale = ns
	if ns <= im.fit()+1e-4 {
		im.scale = 0
	}
	im.clamp()
}

// pan moves the view by dx, dy DIPs.
func (im *lookImage) pan(dx, dy float32) {
	if im.scale == 0 {
		return
	}
	im.cx += dx / im.scale
	im.cy += dy / im.scale
	im.clamp()
}

// clamp keeps the picture over the box where it is larger, centred where
// it is not.
func (im *lookImage) clamp() {
	s := im.effScale()
	clampAxis := func(c *float32, size, box float32) {
		half := box / 2 / s
		if size*s <= box {
			*c = size / 2
			return
		}
		*c = min(max(*c, half), size-half)
	}
	clampAxis(&im.cx, float32(im.w), im.boxW)
	clampAxis(&im.cy, float32(im.h), im.boxH)
}

// firstPx is the long side a picture is first decoded at: sharp at the
// viewer's widest on a 2x screen. Zooming in past it decodes it again at
// preview.MaxPx, once.
const firstPx = 2560

// startImage decodes the picture on a goroutine.
func (a *App) startImage(l *fileLook) {
	if l.img != nil {
		return
	}
	im := &lookImage{stepAt: -1}
	l.img = im
	ctx, path, px := l.ctx, l.path, l.viewPx
	if px == 0 {
		px = firstPx
	}
	go func() {
		pic, err := preview.DecodeImage(ctx, path, px)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.update(func() { im.err = lookErrText(err) })
			return
		}
		// A GIF of several frames plays.
		var player *ui.VideoPlayer
		frames := 1
		if pic.Format == "gif" {
			if g, err := gif.DecodeAll(bytes.NewReader(pic.Data)); err == nil && len(g.Image) > 1 &&
				int64(g.Config.Width)*int64(g.Config.Height)*int64(len(g.Image)) <= gifMemMax {
				frames = len(g.Image)
				if d, err := ui.NewGIFDecoder(pic.Data); err == nil {
					if p, err := ui.NewVideoPlayer(d); err == nil {
						player = p.SetLoop(true)
					}
				}
			}
		}
		var bm *ui.Bitmap
		if player == nil {
			bm, err = bitmapOf(pic)
			if err != nil {
				a.update(func() { im.err = locales.LookPictureUnreadable() })
				return
			}
		}
		a.update(func() {
			if ctx.Err() != nil {
				if player != nil {
					player.Close()
				}
				return
			}
			im.bm, im.player, im.frames, im.data = bm, player, frames, pic.Data
			im.w, im.h, im.format = pic.W, pic.H, pic.Format
			if bm != nil {
				bw, _ := bm.Size()
				im.bmW = bw
			}
			l.mark("decoded")
			if pic.Img != nil && max(pic.Img.Bounds().Dx(), pic.Img.Bounds().Dy()) > 1600 {
				a.packLater(l, im, pic.Img, bm)
			}
			memtrim.Request()
		})
	}()
}

// packLater packs a decoded picture as a JPEG or PNG on a goroutine and
// shows that in its place: the bitmap of the packed bytes lets its pixels
// go once the GPU has them, where one of the pixels keeps them.
func (a *App) packLater(l *fileLook, im *lookImage, img image.Image, was *ui.Bitmap) {
	ctx := l.ctx
	go func() {
		packed, err := packBitmap(img)
		if err != nil || ctx.Err() != nil {
			return
		}
		a.update(func() {
			if im.bm == was {
				im.bm, im.shownFrames = packed, 0
			}
		})
	}()
}

// needSharper reports whether the picture shows larger than its bitmap
// holds, by a quarter, and a sharper one can be read.
func (im *lookImage) needSharper(devScale float32) bool {
	if im.sharpAsked || im.bm == nil || im.bmW == 0 || im.bmW >= im.w || im.bmW >= preview.MaxPx || im.boxW <= 1 {
		return false
	}
	return im.effScale()*float32(im.w)*devScale > float32(im.bmW)*1.25
}

// wantSharper decodes the picture again at preview.MaxPx once it shows
// larger than its bitmap holds.
func (a *App) wantSharper(l *fileLook, im *lookImage, devScale float32) {
	if !im.needSharper(devScale) {
		return
	}
	im.sharpAsked = true
	ctx, path := l.ctx, l.path
	go func() {
		pic, err := preview.DecodeImage(ctx, path, preview.MaxPx)
		if err != nil || ctx.Err() != nil {
			return
		}
		bm, err := bitmapOf(pic)
		if err != nil {
			return
		}
		a.update(func() {
			if ctx.Err() == nil {
				im.bm, im.shownFrames = bm, 0
				im.bmW, _ = bm.Size()
				l.mark("sharper")
				if pic.Img != nil {
					a.packLater(l, im, pic.Img, bm)
				}
				memtrim.Request()
			}
		})
	}()
}

// bitmapOf makes a picture a bitmap: one the viewer's decoder reads as its
// bytes, which can let its pixels go once shown and decode them again, and
// a decoded one as its pixels, at once (packLater packs it after).
func bitmapOf(pic *preview.Picture) (*ui.Bitmap, error) {
	if pic.Data != nil {
		return ui.DecodeBitmap(pic.Data)
	}
	return ui.NewBitmap(pic.Img), nil
}

// packBitmap is a decoded picture as a JPEG, or a PNG where it has
// transparency, decoded back as a bitmap that keeps the bytes.
func packBitmap(img image.Image) (*ui.Bitmap, error) {
	var buf bytes.Buffer
	if opaque(img) {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
			return nil, err
		}
	} else {
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&buf, img); err != nil {
			return nil, err
		}
	}
	return ui.DecodeBitmap(buf.Bytes())
}

// opaque reports whether a picture has no transparent pixel worth keeping.
func opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}

// startSVG parses the drawing on a goroutine.
func (a *App) startSVG(l *fileLook) {
	if l.img != nil {
		return
	}
	im := &lookImage{stepAt: -1}
	l.img = im
	ctx, path := l.ctx, l.path
	go func() {
		data, err := readCapped(path, 8<<20)
		var svg *ui.SVG
		if err == nil {
			svg, err = ui.ParseSVG(data)
		}
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				im.err = locales.LookSVGUnsupported()
				return
			}
			im.svg = svg
			l.mark("parsed")
			w, h := svg.Size()
			im.w, im.h, im.format = max(int(w+0.5), 1), max(int(h+0.5), 1), "svg"
		})
	}()
}

// readCapped reads a file of at most limit bytes.
func readCapped(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > limit {
		return nil, errFileTooLarge
	}
	return os.ReadFile(path)
}

type lookError string

func (e lookError) Error() string { return string(e) }

const errFileTooLarge = lookError("preview: the file is too large to show")

// imageView is a picture, a GIF or a drawing, with its zoom.
func (a *App) imageView(c *ui.Context, pal *palette, l *fileLook) {
	im := l.img
	switch {
	case im == nil:
		lookWaiting(c, pal, l)
		return
	case im.err != "":
		lookFailed(c, pal, im.err)
		return
	case im.bm == nil && im.svg == nil && im.player == nil:
		lookWaiting(c, pal, l)
		return
	}
	ui.Column(c.Key("pic-view")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		stage := ui.Box(c.Key("stage")).Grow(1).MinHeight(0).Clip().Background(pal.sidebar.Alpha(0.35))
		if im.scale > 0 {
			stage.Cursor(ui.CursorGrab)
		}
		if b := stage.Bounds(); b.W > 0 {
			// The picture keeps a margin from the card's edges.
			w, h := max(b.W-2*picPad, 1), max(b.H-2*picPad, 1)
			if w != im.boxW || h != im.boxH {
				im.boxW, im.boxH = w, h
				im.clamp()
				c.Invalidate()
			}
		}
		stage.HandleInput(func(ev ui.InputEvent) bool {
			ev.X, ev.Y = ev.X-picPad, ev.Y-picPad
			switch {
			case ev.Kind == ui.InputZoom:
				im.zoom(ev.Scale, ev.X, ev.Y)
			case ev.Kind == ui.InputScroll && ev.Mods&(ui.Ctrl|ui.Super) != 0:
				k := float32(1.25)
				if ev.DY > 0 {
					k = 0.8
				}
				if ev.Precise {
					k = 1 - ev.DY/200
				}
				im.zoom(k, ev.X, ev.Y)
			case ev.Kind == ui.InputScroll && im.scale > 0:
				im.pan(ev.DX, ev.DY)
			default:
				return false
			}
			return true
		})
		if dx, dy, ok := stage.Dragged(); ok && im.scale > 0 {
			im.pan(-dx, -dy)
		}
		if stage.DoubleClicked() {
			if im.scale > 0 {
				im.scale = 0
			} else {
				px, py, _ := stage.PointerPosition()
				im.zoom(1/im.fit(), px-picPad, py-picPad)
			}
		}
		playing := im.player != nil && im.stepAt < 0
		if playing && im.scale == 0 {
			// The player's own element draws the frames as they come.
			stage.Children(func() {
				ui.Video(c.Key("gif"), im.player).Absolute().Left(picPad).Right(picPad).Top(picPad).Bottom(picPad).Fit(ui.ScaleDown)
			})
			im.shown = im.fit()
		} else {
			src := ui.ImageSource(nil)
			switch {
			case im.svg != nil:
				src = im.svg
			case im.stepBm != nil && im.stepAt >= 0:
				src = im.stepBm
			case im.bm != nil:
				src = im.bm
			}
			if src != nil {
				stage.Draw(func(p *ui.Painter, r ui.Rect) {
					s := im.effScale()
					im.shown = s
					if im.needSharper(p.Scale()) {
						dev := p.Scale()
						a.update(func() { a.wantSharper(l, im, dev) })
					}
					cx, cy := float32(im.w)/2, float32(im.h)/2
					if im.scale > 0 {
						cx, cy = im.cx, im.cy
					}
					dw, dh := float32(im.w)*s, float32(im.h)*s
					dst := ui.Rect{X: r.X + r.W/2 - cx*s, Y: r.Y + r.H/2 - cy*s, W: dw, H: dh}
					// The element's Clip holds its children; its own
					// drawing is clipped here, so a zoomed picture stays
					// in its box.
					p.Clip(r, 0, func() { p.Image(src, dst, ui.FillBox) })
				})
			}
		}
		if im.bm != nil {
			// Once on the GPU, the pixels of a bitmap of bytes leave the
			// CPU; they decode again from the bytes if needed.
			if im.shownFrames++; im.shownFrames == 3 {
				a.defer_(im.bm.ReleasePixels)
			}
			if im.shownFrames < 3 {
				c.After(16 * 1e6)
			}
		}
		if im.player != nil {
			a.gifBar(c, pal, l)
		}
	})
}

// gifBar is a GIF's controls: play or hold, and the frame while it holds.
func (a *App) gifBar(c *ui.Context, pal *palette, l *fileLook) {
	im := l.img
	if im.player.Playing() && im.stepAt < 0 {
		if im.playFrom.IsZero() {
			im.playFrom = c.Now()
		} else if c.Now().Sub(im.playFrom) > gifPlayMax {
			im.player.Pause()
		}
	}
	ui.Row(c.Key("gif-bar")).Height(36).Shrink(0).Padding(0, 12).Gap(8).AlignItems(ui.Center).Children(func() {
		playing := im.stepAt < 0 && im.player.Playing()
		icon, label := pauseIcon, locales.LookHold()
		if !playing {
			icon, label = playIcon, locales.LookPlay()
		}
		iconButton(c, "gif-play", icon, label, pal, func() { a.gifPlay(l, !playing) })
		iconButton(c, "gif-prev", chevronLeftIcon, locales.LookPrevFrame(), pal, func() { a.gifStep(l, -1) })
		iconButton(c, "gif-next", chevronIcon, locales.LookNextFrame(), pal, func() { a.gifStep(l, 1) })
		text := locales.LookFrames(im.frames)
		if im.stepAt >= 0 {
			text = locales.LookFrameOf(itoa(im.stepAt+1), itoa(im.frames))
		}
		ui.Text(c, text).FontSize(fsSmall).FontFeatures("tnum").TextColor(pal.textMuted).SingleLine()
	})
}

// gifPlay plays a GIF or holds it.
func (a *App) gifPlay(l *fileLook, play bool) {
	im := l.img
	if im == nil || im.player == nil {
		return
	}
	if play {
		im.stepAt = -1
		im.playFrom = time.Time{}
		im.player.Play()
		return
	}
	im.player.Pause()
	if im.stepAt < 0 {
		a.gifStepTo(l, 0)
	}
}

// gifStep shows the next or the previous frame, holding the GIF; 0 shows
// the first.
func (a *App) gifStep(l *fileLook, dir int) {
	im := l.img
	if im == nil || im.player == nil || im.frames < 2 {
		return
	}
	im.player.Pause()
	at := max(im.stepAt, 0) + dir
	if dir == 0 {
		at = 0
	}
	a.gifStepTo(l, ((at%im.frames)+im.frames)%im.frames)
}

// gifStepTo draws frame n into the step bitmap, on a goroutine: frames draw
// over the ones before them, so it decodes from the first.
func (a *App) gifStepTo(l *fileLook, n int) {
	im := l.img
	im.stepAt = n
	data, ctx := im.data, l.ctx
	go func() {
		d := im.step
		if d == nil {
			var err error
			if d, err = ui.NewGIFDecoder(data); err != nil {
				return
			}
		}
		if err := d.Rewind(); err != nil {
			return
		}
		var frame ui.VideoFrame
		for i := 0; i <= n; i++ {
			f, err := d.Next()
			if err != nil {
				break
			}
			frame = f
		}
		if frame.Image == nil {
			return
		}
		// A copy: the decoder draws the next frame into the same picture.
		b := frame.Image.Bounds()
		cp := image.NewRGBA(b)
		copyImage(cp, frame.Image)
		a.update(func() {
			if ctx.Err() != nil || im.stepAt != n {
				return
			}
			im.step = d
			if im.stepBm == nil {
				im.stepBm = ui.NewBitmap(cp)
			} else {
				im.stepBm.Update(cp)
			}
		})
	}()
}

// copyImage copies src into dst, of the same bounds.
func copyImage(dst *image.RGBA, src image.Image) {
	if s, ok := src.(*image.RGBA); ok && s.Rect == dst.Rect {
		copy(dst.Pix, s.Pix)
		return
	}
	b := dst.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x, y, src.At(x, y))
		}
	}
}

var (
	playIcon  = icon(`<path d="M5 3.2v9.6a.6.6 0 0 0 .9.5l7.6-4.8a.6.6 0 0 0 0-1L5.9 2.7a.6.6 0 0 0-.9.5z" fill="currentColor" stroke="none"/>`)
	pauseIcon = icon(`<rect x="4" y="3" width="2.6" height="10" rx=".8" fill="currentColor" stroke="none"/><rect x="9.4" y="3" width="2.6" height="10" rx=".8" fill="currentColor" stroke="none"/>`)
)
