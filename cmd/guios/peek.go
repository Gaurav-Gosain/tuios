package main

import (
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// peek is the image a path printed in a pane shows while Command hovers
// it, in a flyout beside the path: the file, as of its modification time,
// decoded on a goroutine.
type peek struct {
	path string // the file, or for a host's, what finds it there
	name string // the file's name, as the caption says it
	mod  time.Time
	bm   *ui.Bitmap // nil while it decodes, or when it is no image
	// shown tells that a pane still hovers it this frame; the flyout
	// closes, and the bitmap goes, at the end of a frame without.
	shown bool

	win    *mygo.Window // the flyout, once the bitmap is
	anchor mygo.Rectangle
	w, h   int
}

// peekable are the extensions of the images ui.DecodeBitmap reads.
var peekable = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

// maxPeekBytes keeps a huge file from being read whole for a glance.
const maxPeekBytes = 64 << 20

// The most a preview's picture takes, in DIPs; its padding, and the height
// of its caption.
const (
	peekW, peekH         = 360, 260
	peekPad, peekCaption = 6, 22
	// peekMinW leaves a small picture's caption room.
	peekMinW = 150
)

// imagePeek shows the image a path names, which Command hovers in a pane's
// terminal at at, in the terminal's coordinates, in a flyout below the
// path, or above it where the screen has no room below. term is the
// terminal's box in the window. A remote pane's image is its host's,
// which kerod there reads.
func (a *App) imagePeek(paneID, link string, at, term ui.Rect) {
	if !peekable[strings.ToLower(filepath.Ext(lineSuffix.ReplaceAllString(link, "")))] {
		return
	}
	if h := a.hostOf(paneID); h.remote() {
		a.remotePeek(h, paneID, link)
	} else if !a.localPeek(paneID, link) {
		return
	}
	a.peek.shown = true
	if a.peek.bm == nil {
		return
	}
	w, h := peekSize(a.peek.bm)
	r := func(v float32) int { return int(math.Round(float64(v))) }
	anchor := mygo.Rectangle{X: r(term.X + at.X), Y: r(term.Y + at.Y), Width: r(at.W), Height: r(at.H)}
	// The flyout is a window: it opens, or follows the link, once the
	// view is built.
	a.defer_(func() { a.showPeek(anchor, r(max(w+2*peekPad, peekMinW)), r(h+peekPad+peekCaption)) })
}

// localPeek decodes the image of a path of this machine, as of its
// modification time, reporting whether there is one.
func (a *App) localPeek(paneID, link string) bool {
	path := a.resolvePath(paneID, link)
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() || fi.Size() > maxPeekBytes {
		return false
	}
	if a.peek.path != path || !a.peek.mod.Equal(fi.ModTime()) {
		a.closePeek()
		a.peek = peek{path: path, name: filepath.Base(path), mod: fi.ModTime()}
		mod := fi.ModTime()
		go func() {
			data, err := os.ReadFile(path)
			if err != nil {
				return
			}
			bm, err := ui.DecodeBitmap(data)
			if err != nil {
				return
			}
			a.update(func() {
				if a.peek.path == path && a.peek.mod.Equal(mod) {
					a.peek.bm = bm
				}
			})
		}()
	}
	return true
}

// remotePeek decodes the image of a path of a host, which its kerod
// finds (fs.stat) and reads (fs.read), once as long as it is hovered.
func (a *App) remotePeek(h *host, paneID, link string) {
	path, base := lineSuffix.ReplaceAllString(link, ""), a.workDir(paneID)
	key := h.name + "\x00" + base + "\x00" + path
	if a.peek.path == key {
		return
	}
	a.closePeek()
	a.peek = peek{path: key, name: pathpkg.Base(path)}
	cl := h.cl
	if cl == nil {
		return
	}
	go func() {
		var st proto.FSStat
		if err := cl.Call("fs.stat", proto.FSStatParams{Path: path, Base: base}, &st); err != nil || !st.Exists || st.Dir || st.Size > maxPeekBytes {
			return
		}
		var r proto.FSRead
		if err := cl.Call("fs.read", proto.FSReadParams{Path: st.Path, Max: maxPeekBytes}, &r); err != nil {
			return
		}
		bm, err := ui.DecodeBitmap(r.Data)
		if err != nil {
			return
		}
		a.update(func() {
			if a.peek.path == key {
				a.peek.bm = bm
			}
		})
	}()
}

// peekSize is the size the picture shows at: its own, up to peekW by
// peekH.
func peekSize(bm *ui.Bitmap) (w, h float32) {
	pw, ph := bm.Size()
	if pw == 0 || ph == 0 {
		return 1, 1
	}
	s := min(1, peekW/float32(pw), peekH/float32(ph))
	return max(float32(pw)*s, 1), max(float32(ph)*s, 1)
}

// showPeek opens the flyout of the image, w by h, against anchor, or
// moves it there.
func (a *App) showPeek(anchor mygo.Rectangle, w, h int) {
	p := &a.peek
	if a.win == nil || p.bm == nil {
		return
	}
	if p.win == nil {
		win := mygo.NewFlyout(mygo.FlyoutOptions{
			Parent:       a.win,
			Anchor:       anchor,
			Gap:          6,
			Width:        w,
			Height:       h,
			Shadow:       true,
			CornerRadius: 10,
			Content:      ui.View(a.peekView),
		})
		win.OnClosed(func() {
			if a.peek.win == win {
				a.peek.win = nil
			}
		})
		p.win, p.anchor, p.w, p.h = win, anchor, w, h
		return
	}
	if anchor != p.anchor {
		p.anchor = anchor
		p.win.SetAnchor(anchor)
	}
	if w != p.w || h != p.h {
		p.w, p.h = w, h
		p.win.SetContentSize(w, h)
	}
}

// closePeek closes the flyout of the image, if open.
func (a *App) closePeek() {
	if a.peek.win != nil {
		a.peek.win.Close()
		a.peek.win = nil
	}
}

// peekView is the flyout's: the picture, with the file's name and the
// picture's size in pixels under it. The system draws its shadow and edge.
func (a *App) peekView(c *ui.Context) {
	a.looks()
	pal := paletteFor(c)
	c.Root().Background(ui.Transparent)
	bm := a.peek.bm
	if bm == nil {
		return
	}
	w, h := peekSize(bm)
	pw, ph := bm.Size()
	ui.Column(c).Fill().Padding(peekPad, peekPad, 0, peekPad).AlignItems(ui.Center).Radius(rCard).Background(pal.popover).Children(func() {
		ui.Image(c, bm).Size(w, h).Fit(ui.Contain).Radius(rChip)
		ui.Row(c).Height(peekCaption).AlignSelf(ui.Stretch).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.peek.name).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Grow(1).MinWidth(0)
			ui.Text(c, itoa(pw)+" × "+itoa(ph)).FontSize(fsSmall).FontFeatures("tnum").TextColor(pal.textFaint).SingleLine().Shrink(0)
		})
	})
}
