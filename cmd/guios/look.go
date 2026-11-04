package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/preview"
)

// A file of Files shows in the viewer as what it is: a picture, a PDF's
// pages, a film's frames, a table, a JSON tree, an archive's listing, a
// font's specimen, Markdown drawn, and a text in the editor. The work each
// kind needs runs on goroutines (internal/guios/preview), and what it makes
// comes back to the view through App.update. The look of a file holds what
// was read for it; closing the viewer, or showing another file, cancels its
// reads and lets go of its pictures.
//
// The keys follow the TUI's quick look (internal/config's quick_look
// binds): J and K move by the kind's unit (a page, a frame, 5 % of a film,
// half a screen of text), ctrl+d and ctrl+u by half a screen, g and G to the
// start and the end, + - 0 zoom a picture or a page, f fills the panes'
// room with the viewer, t shows a film's filmstrip and v the file's next
// view.

// editMax bounds the texts the viewer edits: a larger text shows through
// the line index (preview.TextFile), read only.
const editMax = 16 << 20

// fileLook is what the viewer read of a file to show it.
type fileLook struct {
	path, name string
	kind       preview.Kind
	detected   bool
	size       int64
	mod        time.Time
	mode       os.FileMode
	ctx        context.Context
	cancel     context.CancelFunc
	err        string // why nothing shows
	busy       bool   // the first read is under way
	since      time.Time
	// view is the view shown, an index into views(): the kind's own first.
	view int
	// checking tells a stat of the file is under way, to see it changed.
	checking bool

	img   *lookImage
	pdf   *lookPDF
	film  *lookFilm
	audio *lookAudio
	table *lookTable
	tree  *lookJSON
	arc   *lookArchive
	font  *lookFont
	md    *lookMD
	big   *lookBig
	hex   *lookHex
}

// lookView names a view of a file, as its tab says.
type lookView int

const (
	viewOwn     lookView = iota // the kind's own: the picture, the pages, the table
	viewText                    // its text in the editor
	viewHex                     // its bytes
	viewDetails                 // what the file is: size, dates, permissions, what was read
)

// views are the views of the file, in the order v steps through them.
func (l *fileLook) views() []lookView {
	switch l.kind {
	case preview.Text:
		return []lookView{viewText, viewDetails}
	case preview.Markdown, preview.SVG, preview.JSON, preview.Table:
		if l.size <= editMax {
			return []lookView{viewOwn, viewText, viewDetails}
		}
		return []lookView{viewOwn, viewDetails}
	case preview.Binary, preview.Unknown:
		return []lookView{viewHex, viewDetails}
	}
	return []lookView{viewOwn, viewHex, viewDetails}
}

// current is the view shown.
func (l *fileLook) current() lookView {
	vs := l.views()
	return vs[min(l.view, len(vs)-1)]
}

// viewName is a view's name on its tab.
func (l *fileLook) viewName(v lookView) string {
	switch v {
	case viewText:
		if l.kind == preview.Text {
			return "Text"
		}
		return "Source"
	case viewHex:
		return "Hex"
	case viewDetails:
		return "Details"
	}
	switch l.kind {
	case preview.Markdown:
		return "Preview"
	case preview.SVG:
		return "Drawing"
	case preview.JSON:
		return "Tree"
	case preview.Table:
		return "Table"
	case preview.PDF:
		return "Pages"
	case preview.Video:
		return "Film"
	case preview.Font:
		return "Specimen"
	case preview.Archive:
		return "Contents"
	}
	return "Preview"
}

// wantsText tells that the view shown is the file's text, which the editor
// reads through fs.read (files.go).
func (l *fileLook) wantsText() bool {
	return l.detected && l.current() == viewText && l.size <= editMax
}

// openLook starts reading a file to show: what kind it is from its name and
// first bytes, then the kind's read.
func (a *App) openLook(path string) *fileLook {
	ctx, cancel := context.WithCancel(context.Background())
	l := &fileLook{path: path, name: filepath.Base(path), ctx: ctx, cancel: cancel, busy: true, since: time.Now()}
	go func() {
		fi, err := os.Stat(path)
		var head []byte
		if err == nil && !fi.IsDir() {
			head = readHead(path, 8192)
		}
		a.update(func() {
			if ctx.Err() != nil {
				return
			}
			l.busy = false
			if err != nil {
				l.err = "The file is gone."
				return
			}
			l.size, l.mod, l.mode = fi.Size(), fi.ModTime(), fi.Mode()
			l.kind = preview.Detect(l.name, head)
			if l.kind == preview.Text && l.size > editMax {
				// Too large to edit: the line index reads it.
				l.view = 0
			}
			l.detected = true
			a.startLook(l)
		})
	}()
	return l
}

// readHead reads the first n bytes of a file.
func readHead(path string, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, n)
	k, _ := io.ReadFull(f, b)
	return b[:k]
}

// startLook starts the read the view shown needs, once.
func (a *App) startLook(l *fileLook) {
	switch l.current() {
	case viewText:
		if l.size > editMax {
			a.startBig(l)
		}
		return
	case viewHex:
		a.startHex(l)
		return
	case viewDetails:
		return
	}
	switch l.kind {
	case preview.Image, preview.Animation:
		a.startImage(l)
	case preview.SVG:
		a.startSVG(l)
	case preview.PDF:
		a.startPDF(l)
	case preview.Video:
		a.startFilm(l)
	case preview.Audio:
		a.startAudio(l)
	case preview.Table:
		a.startTable(l)
	case preview.JSON:
		a.startJSON(l)
	case preview.Archive:
		a.startArchive(l)
	case preview.Font:
		a.startFont(l)
	case preview.Markdown:
		a.startMD(l)
	}
}

// closeLook stops a look's reads and lets go of what it holds.
func (l *fileLook) close() {
	if l == nil {
		return
	}
	l.cancel()
	if l.pdf != nil && l.pdf.doc != nil {
		doc := l.pdf.doc
		go doc.Close()
	}
	if l.big != nil && l.big.tf != nil {
		l.big.tf.Close()
	}
	if l.img != nil && l.img.player != nil {
		l.img.player.Close()
	}
	if l.hex != nil {
		l.hex.close()
	}
}

// setView shows another view of the file.
func (a *App) setView(l *fileLook, i int) {
	vs := l.views()
	i = ((i % len(vs)) + len(vs)) % len(vs)
	if i == l.view {
		return
	}
	l.view = i
	a.startLook(l)
}

// checkLook sees whether the file changed since it was read, and reads it
// again when it did, keeping the place shown (the page, the zoom).
func (a *App) checkLook(fv *fileView) {
	l := fv.look
	if l == nil || l.checking || !l.detected {
		return
	}
	l.checking = true
	path := l.path
	go func() {
		fi, err := os.Stat(path)
		a.update(func() {
			l.checking = false
			if fv.look != l {
				return
			}
			if err != nil {
				l.err = "The file is gone."
				return
			}
			if fi.ModTime().Equal(l.mod) && fi.Size() == l.size {
				return
			}
			// Read anew: the same view, the same place where the kind
			// keeps one.
			next := a.openLook(path)
			next.view = l.view
			keepPlace(l, next)
			l.close()
			fv.look = next
		})
	}()
}

// lookBody is the viewer's body for a file: the view shown.
func (a *App) lookBody(c *ui.Context, pal *palette, fv *fileView) {
	l := fv.look
	switch {
	case l.err != "" && l.current() == viewOwn:
		a.sideMessage(c, pal, l.err, "")
		return
	case !l.detected:
		lookWaiting(c, pal, l)
		return
	}
	switch l.current() {
	case viewHex:
		a.hexView(c, pal, l)
		return
	case viewDetails:
		a.detailsView(c, pal, l)
		return
	case viewText:
		a.bigView(c, pal, l)
		return
	}
	switch l.kind {
	case preview.Image, preview.Animation, preview.SVG:
		a.imageView(c, pal, l)
	case preview.PDF:
		a.pdfView(c, pal, l)
	case preview.Video:
		a.filmView(c, pal, l)
	case preview.Audio:
		a.audioView(c, pal, l)
	case preview.Table:
		a.tableView(c, pal, l)
	case preview.JSON:
		a.jsonView(c, pal, l)
	case preview.Archive:
		a.archiveView(c, pal, l)
	case preview.Font:
		a.fontView(c, pal, l)
	case preview.Markdown:
		a.mdView(c, pal, l)
	default:
		a.hexView(c, pal, l)
	}
}

// lookWaiting is a view whose read is under way: nothing for the first
// 150 ms, so a fast read never flashes, then a spinner.
func lookWaiting(c *ui.Context, pal *palette, l *fileLook) {
	if wait := 150*time.Millisecond - time.Since(l.since); wait > 0 {
		ui.Box(c).Grow(1)
		c.After(wait)
		return
	}
	ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
		ui.Spinner(c)
		ui.Text(c, "Reading "+l.name).FontSize(12).TextColor(pal.textFaint).SingleLine()
	})
}

// lookFailed shows why a view has nothing, with what to do.
func lookFailed(c *ui.Context, pal *palette, msg string) {
	ui.Column(c).Grow(1).Center().Gap(6).Padding(24).Children(func() {
		ui.Text(c, msg).FontSize(13).FontWeight(500).TextColor(pal.textMuted).TextAlign(ui.Center).MaxWidth(360).LineHeight(1.4)
	})
}

// lookMeta is what the header says after the file's name: the place in it
// (a page, a time, a zoom) and its size.
func (a *App) lookMeta(l *fileLook) string {
	if l == nil || !l.detected {
		return ""
	}
	var parts []string
	switch l.current() {
	case viewOwn:
		switch {
		case l.pdf != nil && l.pdf.doc != nil:
			parts = append(parts, "Page "+itoa(l.pdf.page+1)+" of "+itoa(l.pdf.pages))
			parts = append(parts, itoa(int(l.pdf.shownZoom*100+0.5))+"%")
		case l.img != nil && l.img.w > 0:
			parts = append(parts, itoa(l.img.w)+" × "+itoa(l.img.h))
			if l.img.frames > 1 {
				parts = append(parts, itoa(l.img.frames)+" frames")
			}
			if l.img.shown > 0 {
				parts = append(parts, itoa(int(l.img.shown*100+0.5))+"%")
			}
		case l.film != nil && l.film.facts != nil:
			parts = append(parts, filmClock(l.film.at)+" / "+filmClock(l.film.facts.DurationMs))
		case l.table != nil && l.table.data != nil:
			parts = append(parts, countWord(len(l.table.data.Rows), "row", "rows"), countWord(len(l.table.data.Header), "column", "columns"))
		case l.arc != nil && l.arc.entries != nil:
			parts = append(parts, countWord(len(l.arc.entries), "entry", "entries"))
		}
	case viewText:
		if l.big != nil && l.big.tf != nil {
			n, done := l.big.tf.Count()
			if done {
				parts = append(parts, countWord(int(n), "line", "lines"))
			} else {
				parts = append(parts, "Counting lines")
			}
		}
	}
	parts = append(parts, sizeText(l.size))
	return strings.Join(parts, " · ")
}

// countWord is n and its word, one or many.
func countWord(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return groupDigits(n) + " " + many
}

// groupDigits writes n with a comma between each three digits.
func groupDigits(n int) string {
	s := itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// humanSize is a size in bytes as a person reads it.
func sizeText(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return itoa(int(n)) + " B"
	case n < k*k:
		return oneDecimal(float64(n)/k) + " KB"
	case n < k*k*k:
		return oneDecimal(float64(n)/(k*k)) + " MB"
	}
	return oneDecimal(float64(n)/(k*k*k)) + " GB"
}

func oneDecimal(f float64) string {
	if f >= 100 {
		return itoa(int(f + 0.5))
	}
	t := int(f*10 + 0.5)
	if t%10 == 0 {
		return itoa(t / 10)
	}
	return itoa(t/10) + "." + itoa(t%10)
}

// clock is a time in a film as m:ss, or h:mm:ss past an hour.
func filmClock(ms int64) string {
	s := max(ms, 0) / 1000
	h, m := s/3600, s/60%60
	s %= 60
	two := func(n int64) string {
		if n < 10 {
			return "0" + itoa(int(n))
		}
		return itoa(int(n))
	}
	if h > 0 {
		return itoa(int(h)) + ":" + two(m) + ":" + two(s)
	}
	return itoa(int(m)) + ":" + two(s)
}

// lookBar is the row under the viewer's header: the file's views as tabs,
// and what the header has no room for, the place in the file and its size.
func (a *App) lookBar(c *ui.Context, pal *palette, l *fileLook) {
	if l == nil || !l.detected {
		return
	}
	vs := l.views()
	ui.Row(c.Key("look-bar")).Height(32).Shrink(0).Padding(0, 12, 0, 12).Gap(2).AlignItems(ui.Center).Children(func() {
		for i, v := range vs {
			name := l.viewName(v)
			b := ui.ButtonBase(c.Key("view-"+name)).Height(24).Padding(0, 8).Radius(6).Center().Label(name).Tooltip(name + " (v)").Shrink(0)
			bg, fg := ui.Transparent, pal.textMuted
			switch {
			case i == l.view:
				bg, fg = pal.rowSelected, pal.text
				chosen(b, pal)
			case b.Hovered():
				bg, fg = pal.rowHover, pal.text
			}
			b.Background(bg).Children(func() {
				ui.Text(c, name).FontSize(12).FontWeight(500).TextColor(fg).SingleLine()
			})
			if b.Clicked() {
				a.setView(l, i)
			}
		}
		ui.Box(c).Grow(1).MinWidth(8)
		if meta := a.lookMeta(l); meta != "" {
			ui.Text(c, meta).FontSize(12).FontFeatures("tnum").TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0).Tooltip(meta)
		}
	})
}

// lookKeys are the keys of a file's view while the viewer has the keyboard
// and no text field in it takes them: the TUI's quick look keys.
func (a *App) lookKeys(card ui.Element, v *sideView, l *fileLook) {
	if l == nil || !l.detected {
		return
	}
	// The editor takes every key for its text.
	if l.current() == viewText && l.size <= editMax {
		return
	}
	if card.Shortcut(0, ui.KeyV) {
		a.setView(l, l.view+1)
	}
	if card.Shortcut(ui.Shift, ui.KeyV) {
		a.setView(l, l.view-1)
	}
	if card.Shortcut(0, ui.KeyF) {
		v.full = !v.full
	}
	seek := func(dir int) {
		a.lookSeek(l, dir)
	}
	if card.Shortcut(ui.Shift, ui.KeyJ) || card.Shortcut(ui.Shift, ui.KeyDown) {
		seek(1)
	}
	if card.Shortcut(ui.Shift, ui.KeyK) || card.Shortcut(ui.Shift, ui.KeyUp) {
		seek(-1)
	}
	if card.Shortcut(ui.Ctrl, ui.KeyD) || card.Shortcut(0, ui.KeyPageDown) || card.Shortcut(0, ui.KeyD) {
		a.lookHalf(l, 1)
	}
	if card.Shortcut(ui.Ctrl, ui.KeyU) || card.Shortcut(0, ui.KeyPageUp) || card.Shortcut(0, ui.KeyU) {
		a.lookHalf(l, -1)
	}
	if card.Shortcut(0, ui.KeyG) || card.Shortcut(0, ui.KeyHome) {
		a.lookEdge(l, false)
	}
	if card.Shortcut(ui.Shift, ui.KeyG) || card.Shortcut(0, ui.KeyEnd) {
		a.lookEdge(l, true)
	}
	if card.Shortcut(0, ui.KeyEqual) || card.Shortcut(ui.Shift, ui.KeyEqual) || card.Shortcut(ui.Cmd, ui.KeyEqual) {
		a.lookZoom(l, 1.25)
	}
	if card.Shortcut(0, ui.KeyMinus) || card.Shortcut(ui.Cmd, ui.KeyMinus) {
		a.lookZoom(l, 0.8)
	}
	if card.Shortcut(0, ui.Key0) || card.Shortcut(ui.Cmd, ui.Key0) {
		a.lookZoom(l, 0)
	}
	if card.Shortcut(0, ui.KeyT) && l.film != nil {
		l.film.strip = !l.film.strip
		if l.film.strip {
			a.readStrip(l)
		}
	}
	if card.Shortcut(0, ui.KeyJ) || card.Shortcut(0, ui.KeyDown) {
		a.lookLine(l, 1)
	}
	if card.Shortcut(0, ui.KeyK) || card.Shortcut(0, ui.KeyUp) {
		a.lookLine(l, -1)
	}
	if card.Shortcut(0, ui.KeyH) || card.Shortcut(0, ui.KeyLeft) {
		a.lookColumn(l, -1)
	}
	if card.Shortcut(0, ui.KeyL) || card.Shortcut(0, ui.KeyRight) {
		a.lookColumn(l, 1)
	}
	if card.Shortcut(0, ui.KeySpace) {
		a.lookSpace(l)
	}
	if card.Shortcut(ui.Shift, ui.KeySemicolon) {
		a.lookGoTo(l)
	}
	if card.Shortcut(0, ui.KeySlash) || card.Shortcut(ui.Cmd, ui.KeyF) {
		a.lookFind(l)
	}
	if card.Shortcut(0, ui.KeyN) {
		a.lookFindNext(l, false)
	}
	if card.Shortcut(ui.Shift, ui.KeyN) {
		a.lookFindNext(l, true)
	}
	if card.Shortcut(0, ui.KeyW) && l.big != nil {
		l.big.wrap = !l.big.wrap
	}
}

// lookSeek moves by the kind's unit: a page, a frame, 5 % of a film, half
// a screen of the rest.
func (a *App) lookSeek(l *fileLook, dir int) {
	switch {
	case l.current() != viewOwn:
		a.lookHalf(l, dir)
	case l.pdf != nil:
		a.pdfGo(l, l.pdf.page+dir)
	case l.img != nil && l.img.frames > 1:
		a.gifStep(l, dir)
	case l.film != nil && l.film.facts != nil:
		a.filmSeek(l, l.film.want+int64(dir)*l.film.facts.DurationMs/20)
	default:
		a.lookHalf(l, dir)
	}
}

// lookHalf scrolls by half the view.
func (a *App) lookHalf(l *fileLook, dir int) {
	switch {
	case l.current() == viewHex && l.hex != nil:
		l.hex.list.ScrollTo(max(l.hex.first()+dir*l.hex.rows()/2, 0), ui.Start)
	case l.current() == viewText && l.big != nil:
		l.big.scrollBy(dir * l.big.rows() / 2)
	case l.pdf != nil:
		l.pdf.scroll.ScrollTo(l.pdf.scroll.X, l.pdf.scroll.Y+float32(dir)*l.pdf.viewH/2, ui.Smooth)
	case l.table != nil:
		f, last := l.table.list.Visible()
		l.table.list.ScrollTo(max(f+dir*(last-f)/2, 0), ui.Start)
	case l.md != nil:
		l.md.scroll.ScrollTo(0, l.md.scroll.Y+float32(dir)*l.md.viewH/2, ui.Smooth)
	case l.font != nil:
		l.font.scroll.ScrollTo(0, l.font.scroll.Y+float32(dir)*l.font.viewH/2, ui.Smooth)
	case l.arc != nil:
		f, last := l.arc.state.List.Visible()
		l.arc.state.List.ScrollTo(max(f+dir*(last-f)/2, 0), ui.Start)
	case l.tree != nil:
		f, last := l.tree.state.List.Visible()
		l.tree.state.List.ScrollTo(max(f+dir*(last-f)/2, 0), ui.Start)
	case l.film != nil && l.film.facts != nil:
		a.filmSeek(l, l.film.want+int64(dir)*l.film.facts.DurationMs/20)
	}
}

// lookLine moves one step: a line, a row, a page in a PDF, a second in a
// film, a pan when a picture is zoomed.
func (a *App) lookLine(l *fileLook, dir int) {
	switch {
	case l.current() == viewHex && l.hex != nil:
		l.hex.list.ScrollTo(max(l.hex.first()+dir, 0), ui.Start)
	case l.current() == viewText && l.big != nil:
		l.big.scrollBy(dir)
	case l.current() != viewOwn:
	case l.img != nil && l.img.scale > 0:
		l.img.pan(0, float32(dir)*60)
	case l.pdf != nil:
		l.pdf.scroll.ScrollTo(l.pdf.scroll.X, l.pdf.scroll.Y+float32(dir)*60, ui.Smooth)
	case l.film != nil && l.film.facts != nil:
		a.filmSeek(l, l.film.want+int64(dir)*1000)
	case l.table != nil:
		f, _ := l.table.list.Visible()
		l.table.list.ScrollTo(max(f+dir, 0), ui.Start)
	case l.md != nil:
		l.md.scroll.ScrollTo(0, l.md.scroll.Y+float32(dir)*60, ui.Smooth)
	case l.font != nil:
		l.font.scroll.ScrollTo(0, l.font.scroll.Y+float32(dir)*60, ui.Smooth)
	case l.img != nil && l.img.frames > 1:
		a.gifStep(l, dir)
	}
}

// lookColumn moves sideways: a table's columns, a film by a second, a
// zoomed picture's pan.
func (a *App) lookColumn(l *fileLook, dir int) {
	switch {
	case l.current() != viewOwn:
	case l.img != nil && l.img.scale > 0:
		l.img.pan(float32(dir)*60, 0)
	case l.film != nil && l.film.facts != nil:
		a.filmSeek(l, l.film.want+int64(dir)*1000)
	case l.pdf != nil:
		l.pdf.scroll.ScrollTo(l.pdf.scroll.X+float32(dir)*60, l.pdf.scroll.Y, ui.Smooth)
	}
}

// lookEdge goes to the start or the end.
func (a *App) lookEdge(l *fileLook, end bool) {
	switch {
	case l.current() == viewHex && l.hex != nil:
		if end {
			l.hex.list.ScrollToEnd()
		} else {
			l.hex.list.ScrollTo(0, ui.Start)
		}
	case l.current() == viewText && l.big != nil:
		if end {
			l.big.list.ScrollToEnd()
		} else {
			l.big.list.ScrollTo(0, ui.Start)
		}
	case l.current() != viewOwn:
	case l.pdf != nil:
		if end {
			a.pdfGo(l, l.pdf.pages-1)
		} else {
			a.pdfGo(l, 0)
		}
	case l.film != nil && l.film.facts != nil:
		if end {
			a.filmSeek(l, l.film.facts.DurationMs)
		} else {
			a.filmSeek(l, 0)
		}
	case l.table != nil:
		if end {
			l.table.list.ScrollToEnd()
		} else {
			l.table.list.ScrollTo(0, ui.Start)
		}
	case l.md != nil:
		y := float32(0)
		if end {
			y = l.md.scroll.MaxY
		}
		l.md.scroll.ScrollTo(0, y, ui.Smooth)
	case l.tree != nil:
		if end {
			l.tree.state.List.ScrollToEnd()
		} else {
			l.tree.state.List.ScrollTo(0, ui.Start)
		}
	case l.arc != nil:
		if end {
			l.arc.state.List.ScrollToEnd()
		} else {
			l.arc.state.List.ScrollTo(0, ui.Start)
		}
	case l.img != nil && l.img.frames > 1:
		a.gifStep(l, 0)
	}
}

// lookZoom zooms a picture or a PDF's pages by k, or back to fit for 0.
func (a *App) lookZoom(l *fileLook, k float32) {
	switch {
	case l.current() != viewOwn:
	case l.pdf != nil:
		a.pdfZoom(l, k, -1, -1)
	case l.img != nil:
		l.img.zoom(k, -1, -1)
	case l.font != nil:
		if k == 0 {
			l.font.size = 0
		} else {
			l.font.size = min(max(l.font.scaleOr1()*k, 0.5), 4)
		}
		l.font.bm = nil
	}
}

// lookSpace plays or holds a GIF, and does nothing for the rest.
func (a *App) lookSpace(l *fileLook) {
	if l.img != nil && l.img.player != nil && l.current() == viewOwn {
		a.gifPlay(l, !l.img.player.Playing())
	}
}

// keepPlace carries the place shown (page, zoom, view) from a look to the
// one that reads the same file again after it changed.
func keepPlace(old, next *fileLook) {
	if old.pdf != nil {
		next.pdf = &lookPDF{keep: old.pdf.page + 1, zoomK: old.pdf.zoomK, fitPage: old.pdf.fitPage}
	}
	if old.film != nil {
		next.film = &lookFilm{want: old.film.want, at: old.film.at}
	}
}
