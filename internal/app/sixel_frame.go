package app

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

// GraphicsFrameBytes is the output that has to follow each frame the
// renderer writes: the sixel images on it. The renderer's writer calls it.
func (m *OS) GraphicsFrameBytes(frame []byte) []byte {
	return m.SixelPassthrough.FrameBytes(frame)
}

type sixelRect struct {
	id             uint32
	x, y           int
	r0, c0, r1, c1 int
	ground         uint64
}

func (r sixelRect) rows() int { return r.r1 - r.r0 }
func (r sixelRect) cols() int { return r.c1 - r.c0 }

type sixelFrameState struct {
	gen                    uint64
	rects                  []sixelRect
	visible                map[uint32]bool
	drawn                  map[uint32]bool
	hostW, hostH, hostRows int

	shown    map[sixelRect]bool
	force    bool
	damage   hostDamage
	hostCols int

	placements    map[sixelRect]uint32
	nextPlacement uint32

	cache map[sixelCacheKey][]byte
	jobs  map[any]bool

	pending []byte

	seq uint64
}

type sixelCacheKey struct {
	rect         sixelRect
	hostW, hostH int
}

func (f *sixelFrameState) dropCache(id uint32) {
	for k := range f.cache {
		if k.rect.id == id {
			delete(f.cache, k)
		}
	}
}

// Invalidate says the host's screen was repainted from scratch, so every
// visible image has to be sent again. Called when the host is resized, since
// the renderer then erases and redraws every cell.
func (sp *SixelPassthrough) Invalidate() {
	if sp == nil {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.frame.force = true
}

// SetFrame records the latest frame's visible rectangles. It sends nothing:
// the bytes go out from FrameBytes, with the frame's own text.
func (sp *SixelPassthrough) SetFrame(rects []sixelRect, hostW, hostH, hostCols, hostRows int) {
	if sp == nil {
		return
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	f := &sp.frame
	f.gen++
	f.rects = append(f.rects[:0], rects...)
	if f.visible == nil {
		f.visible = make(map[uint32]bool)
	}
	clear(f.visible)
	for _, r := range rects {
		f.visible[r.id] = true
	}
	for id := range f.drawn {
		f.visible[id] = true
	}
	clear(f.drawn)
	if hostW != f.hostW || hostH != f.hostH || hostRows != f.hostRows || hostCols != f.hostCols {
		f.hostW, f.hostH, f.hostRows, f.hostCols = hostW, hostH, hostRows, hostCols
		f.force = true
	}
}

// FrameBytes returns what the host has to be sent after frame, the bytes the
// renderer is about to write. The renderer's writer calls it once per write,
// so the images land on top of the text they belong with and never under it.
//
// A rectangle is sent when it is new, and again whenever the renderer wrote
// into any of its cells, which is what took the picture off the host there
// (see hostDamage). That also covers the one race in this: bubbletea stores a
// View's frame just after View returns, so a write that starts in that gap
// carries the previous frame while this already holds the new rectangles. The
// images are then drawn a frame early; the next write, carrying the frame they
// belong to, rewrites their cells and so has them sent again.
func (sp *SixelPassthrough) FrameBytes(frame []byte) []byte {
	if sp == nil {
		return nil
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	f := &sp.frame
	out := f.pending
	f.pending = nil
	if f.hostCols > 0 && f.hostRows > 0 {
		f.damage.resize(f.hostCols, f.hostRows)
	}
	f.damage.feed(frame)

	before := len(out)
	switch sp.mode {
	case sixelNative:
		out = sp.nativeBytesLocked(out)
	case sixelViaKitty:
		out = sp.kittyBytesLocked(out)
	default:
		clear(f.shown)
	}
	f.damage.reset()
	if len(out) > before {
		sixelPassthroughLog("FrameBytes gen=%d rects=%d wrote=%d", f.gen, len(f.rects), len(out)-before)
	}
	return out
}

func (sp *SixelPassthrough) nativeBytesLocked(out []byte) []byte {
	f := &sp.frame
	if f.shown == nil {
		f.shown = make(map[sixelRect]bool)
	}
	next := make(map[sixelRect]bool, len(f.rects))
	for _, r := range f.rects {
		if f.shown[r] && !f.force && !f.damage.hit(r) {
			next[r] = true
			continue
		}
		if !f.damage.grounded(r) {
			continue
		}
		seq, ready := sp.sixelCropLocked(r)
		if !ready {
			continue
		}
		next[r] = true
		if len(seq) == 0 {
			continue
		}
		out = append(out, "\x1b7"...)
		out = appendCUP(out, r.y, r.x)
		out = append(out, seq...)
		out = append(out, "\x1b8"...)
	}
	f.shown = next
	f.force = false
	return out
}

const sixelSyncPixels = 128 << 10

func (sp *SixelPassthrough) sixelCropLocked(r sixelRect) (seq []byte, ready bool) {
	f := &sp.frame
	e := sp.images[r.id]
	if e == nil || e.img == nil {
		return nil, true
	}
	hw, hh := f.hostW, f.hostH
	if hw <= 0 || hh <= 0 {
		hw, hh = e.cellW, e.cellH
	}
	whole := r.r0 == 0 && r.c0 == 0 && r.r1 == e.rows && r.c1 == e.cols
	if whole && hw == e.cellW && hh == e.cellH && len(e.raw) > 0 && e.img.Exact {
		out := make([]byte, 0, len(e.raw)+4)
		out = append(out, "\x1bP"...)
		out = append(out, e.raw...)
		return append(out, "\x1b\\"...), true
	}
	key := sixelCacheKey{rect: r, hostW: hw, hostH: hh}
	key.rect.x, key.rect.y, key.rect.ground = 0, 0, 0
	if seq, ok := f.cache[key]; ok {
		return seq, true
	}
	src := image.Rect(r.c0*e.cellW, r.r0*e.cellH, r.c1*e.cellW, r.r1*e.cellH).
		Intersect(image.Rect(0, 0, e.img.Width, e.img.Height))
	if src.Empty() {
		return nil, true
	}
	dw := max(1, src.Dx()*hw/e.cellW)
	dh := max(1, src.Dy()*hh/e.cellH)
	img := e.img
	encode := func() []byte { return vt.EncodeSixel(img, src, dw, dh) }
	store := func(seq []byte) {
		if f.cache == nil {
			f.cache = make(map[sixelCacheKey][]byte)
		}
		if len(f.cache) > 256 {
			for k := range f.cache {
				if !f.visible[k.rect.id] {
					delete(f.cache, k)
				}
			}
		}
		f.cache[key] = seq
	}
	if dw*dh <= sixelSyncPixels {
		seq := encode()
		store(seq)
		return seq, true
	}
	sp.startJobLocked(key, e, encode, store)
	return nil, false
}

func (sp *SixelPassthrough) startJobLocked(key any, e *sixelEntry, work func() []byte, store func([]byte)) {
	f := &sp.frame
	if f.jobs == nil {
		f.jobs = make(map[any]bool)
	}
	if f.jobs[key] {
		return
	}
	f.jobs[key] = true
	go func() {
		sixelJobSlot <- struct{}{}
		out := work()
		<-sixelJobSlot
		sp.mu.Lock()
		delete(f.jobs, key)
		var send []byte
		direct := sp.direct
		if sp.images[sp.idOf(e)] == e {
			store(out)
			if direct != nil {
				send = sp.kickLocked()
			}
		}
		sp.mu.Unlock()
		if len(send) > 0 && direct != nil {
			direct(send)
		}
	}()
}

var sixelJobSlot = make(chan struct{}, 1)

func (sp *SixelPassthrough) idOf(e *sixelEntry) uint32 {
	for id, v := range sp.images {
		if v == e {
			return id
		}
	}
	return 0
}

func (sp *SixelPassthrough) kickLocked() []byte {
	switch sp.mode {
	case sixelNative:
		return sp.nativeBytesLocked(nil)
	case sixelViaKitty:
		return sp.kittyBytesLocked(nil)
	}
	return nil
}

func (sp *SixelPassthrough) kittyBytesLocked(out []byte) []byte {
	f := &sp.frame
	if f.placements == nil {
		f.placements = make(map[sixelRect]uint32)
	}
	next := make(map[sixelRect]bool, len(f.rects))
	for _, r := range f.rects {
		next[r] = true
	}
	var waiting []sixelRect
	for _, r := range f.rects {
		if _, placed := f.placements[r]; placed {
			continue
		}
		e := sp.images[r.id]
		if e == nil || e.img == nil {
			continue
		}
		if e.kittyID == 0 {
			if e.kittyPayload == nil {
				img, id := e.img, sixelKittyIDBase+r.id
				ent := e
				sp.startJobLocked(r.id, e,
					func() []byte { return kittyTransmitRGBA(nil, id, img) },
					func(b []byte) { ent.kittyPayload = b })
			}
			if e.kittyPayload == nil {
				waiting = append(waiting, r)
				continue
			}
			e.kittyID = sixelKittyIDBase + r.id
			out = append(out, e.kittyPayload...)
			e.kittyPayload = nil
		}
		f.nextPlacement++
		if f.nextPlacement == 0 {
			f.nextPlacement = 1
		}
		pid := f.nextPlacement
		f.placements[r] = pid
		src := image.Rect(r.c0*e.cellW, r.r0*e.cellH, r.c1*e.cellW, r.r1*e.cellH).
			Intersect(image.Rect(0, 0, e.img.Width, e.img.Height))
		out = append(out, "\x1b7"...)
		out = appendCUP(out, r.y, r.x)
		out = fmt.Appendf(out, "\x1b_Ga=p,i=%d,p=%d,x=%d,y=%d,w=%d,h=%d,c=%d,r=%d,C=1,q=2\x1b\\",
			e.kittyID, pid, src.Min.X, src.Min.Y, src.Dx(), src.Dy(), r.cols(), r.rows())
		out = append(out, "\x1b8"...)
	}
	for r, pid := range f.placements {
		if next[r] && !f.force {
			continue
		}
		if !f.force && overlapsAny(r, waiting) {
			continue
		}
		if e := sp.images[r.id]; e != nil && e.kittyID != 0 {
			out = kittyDeletePlacement(out, e.kittyID, pid)
		}
		delete(f.placements, r)
	}
	f.shown = next
	f.force = false
	return out
}

func overlapsAny(r sixelRect, rs []sixelRect) bool {
	for _, o := range rs {
		if r.x < o.x+o.cols() && o.x < r.x+r.cols() &&
			r.y < o.y+o.rows() && o.y < r.y+r.rows() {
			return true
		}
	}
	return false
}

func (sp *SixelPassthrough) takeDownLocked() []byte {
	var out []byte
	for id, e := range sp.images {
		if e.kittyID != 0 {
			out = append(out, kittyFreeImage(e.kittyID)...)
			e.kittyID = 0
		}
		_ = id
	}
	clear(sp.frame.placements)
	clear(sp.frame.shown)
	sp.frame.force = true
	return out
}

const sixelKittyIDBase = 0x7e000000

func kittyTransmitRGBA(out []byte, id uint32, img *vt.SixelImage) []byte {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(img.RGBA(image.Rect(0, 0, img.Width, img.Height)))
	_ = w.Close()
	payload := base64.StdEncoding.EncodeToString(z.Bytes())
	const chunk = 4096
	for i := 0; i < len(payload) || i == 0; i += chunk {
		end := min(i+chunk, len(payload))
		more := 0
		if end < len(payload) {
			more = 1
		}
		if i == 0 {
			out = fmt.Appendf(out, "\x1b_Ga=t,f=32,o=z,s=%d,v=%d,i=%d,q=2,m=%d;", img.Width, img.Height, id, more)
		} else {
			out = fmt.Appendf(out, "\x1b_Gm=%d;", more)
		}
		out = append(out, payload[i:end]...)
		out = append(out, "\x1b\\"...)
		if end == len(payload) {
			break
		}
	}
	return out
}

func kittyDeletePlacement(out []byte, id, pid uint32) []byte {
	return fmt.Appendf(out, "\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", id, pid)
}

func kittyFreeImage(id uint32) []byte {
	return fmt.Appendf(nil, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
}

func appendCUP(out []byte, y, x int) []byte {
	out = append(out, "\x1b["...)
	out = strconv.AppendInt(out, int64(y+1), 10)
	out = append(out, ';')
	out = strconv.AppendInt(out, int64(x+1), 10)
	return append(out, 'H')
}

type sixelHit struct {
	x, y     int
	row, col int
}

type sixelGroupKey struct {
	id     uint32
	ox, oy int
}

func (m *OS) scanSixelFrame(canvas *frameCanvas) {
	sp := m.SixelPassthrough
	if sp == nil {
		return
	}
	m.drawImageSymbols(canvas)
	sp.mu.Lock()
	mode := sp.mode
	sp.mu.Unlock()

	var groups map[sixelGroupKey][]sixelHit
	infos := map[uint32]sixelInfo{}
	var dim color.Color
	for y, line := range canvas.Lines {
		for x := range line {
			c := &line[x]
			if !vt.IsSixelMarker(c.Content) {
				continue
			}
			id, row, col, ok := vt.ParseSixelMarker(c.Content)
			if !ok {
				id = 0
			}
			info, seen := infos[id]
			if !seen {
				info = sp.info(id)
				infos[id] = info
			}
			if info.rows == 0 {
				blankCellKeepGround(c)
				continue
			}
			if mode == sixelPlaceholder || mode == sixelSymbols || !info.picture {
				if dim == nil {
					dim = theme.UI().FgDim
				}
				placeholderCell(c, row, col, info.rows, info.cols, dim)
				continue
			}
			sixelBlankCell(c, id)
			if groups == nil {
				groups = make(map[sixelGroupKey][]sixelHit)
			}
			k := sixelGroupKey{id: id, ox: x - col, oy: y - row}
			groups[k] = append(groups[k], sixelHit{x: x, y: y, row: row, col: col})
		}
	}

	hw, hh := m.hostCellSize()
	rows := len(canvas.Lines)
	var rects []sixelRect
	for k, hits := range groups {
		for _, r := range sixelRects(k.id, hits, mode == sixelNative, rows) {
			r.ground = groundHash(canvas, r)
			rects = append(rects, r)
		}
	}
	slices.SortFunc(rects, func(a, b sixelRect) int {
		if a.y != b.y {
			return a.y - b.y
		}
		if a.x != b.x {
			return a.x - b.x
		}
		return int(a.id) - int(b.id)
	})
	cols := 0
	if rows > 0 {
		cols = len(canvas.Lines[0])
	}
	sp.SetFrame(rects, hw, hh, cols, rows)
	m.sweepSixelImages()
}

func groundHash(canvas *frameCanvas, r sixelRect) uint64 {
	h := uint64(14695981039346656037)
	for y := r.y; y < r.y+r.rows(); y++ {
		line := canvas.Lines[y]
		for x := r.x; x < r.x+r.cols() && x < len(line); x++ {
			var v uint32
			if bg := line[x].Style.Bg; !isNilColor(bg) {
				cr, cg, cb, ca := bg.RGBA()
				v = (cr>>8)<<24 | (cg>>8)<<16 | (cb>>8)<<8 | (ca >> 8)
				if ca == 0 {
					v = 1
				}
			}
			h = (h ^ uint64(v)) * 1099511628211
		}
	}
	return h
}

type sixelInfo struct {
	picture    bool
	rows, cols int
}

func (sp *SixelPassthrough) info(id uint32) sixelInfo {
	sp.mu.Lock()
	e := sp.images[id]
	if id == 0 || e == nil {
		sp.mu.Unlock()
		return sixelInfo{}
	}
	info := sixelInfo{picture: e.img != nil, rows: e.rows, cols: e.cols}
	sp.mu.Unlock()
	return info
}

func blankCellKeepGround(c *uv.Cell) {
	bg := c.Style.Bg
	*c = uv.Cell{Content: " ", Width: 1}
	c.Style.Bg = bg
}

func sixelRects(id uint32, hits []sixelHit, native bool, hostRows int) []sixelRect {
	slices.SortFunc(hits, func(a, b sixelHit) int {
		if a.y != b.y {
			return a.y - b.y
		}
		return a.x - b.x
	})
	type run struct{ x0, x1, y0, y1, r0, c0 int }
	var done []run
	open := map[[2]int]*run{}
	lastY := -1
	var rowRuns []run
	flush := func(y int) {
		next := map[[2]int]*run{}
		for _, rr := range rowRuns {
			k := [2]int{rr.x0, rr.x1}
			if o, ok := open[k]; ok && o.y1 == y-1 {
				o.y1 = y
				next[k] = o
				delete(open, k)
				continue
			}
			nr := rr
			next[k] = &nr
		}
		for _, o := range open {
			done = append(done, *o)
		}
		open = next
		rowRuns = rowRuns[:0]
	}
	for i := 0; i < len(hits); {
		h := hits[i]
		if h.y != lastY {
			if lastY >= 0 {
				flush(lastY)
			}
			lastY = h.y
		}
		j := i + 1
		for j < len(hits) && hits[j].y == h.y && hits[j].x == hits[j-1].x+1 {
			j++
		}
		rowRuns = append(rowRuns, run{x0: h.x, x1: hits[j-1].x, y0: h.y, y1: h.y, r0: h.row, c0: h.col})
		i = j
	}
	if lastY >= 0 {
		flush(lastY)
	}
	for _, o := range open {
		done = append(done, *o)
	}
	out := make([]sixelRect, 0, len(done))
	for _, d := range done {
		y1 := d.y1
		if native && y1 >= hostRows-1 {
			y1 = hostRows - 2
		}
		if y1 < d.y0 {
			continue
		}
		out = append(out, sixelRect{
			id: id, x: d.x0, y: d.y0,
			r0: d.r0, c0: d.c0,
			r1: d.r0 + (y1 - d.y0) + 1,
			c1: d.c0 + (d.x1 - d.x0) + 1,
		})
	}
	return out
}

func sixelBlankCell(c *uv.Cell, id uint32) {
	bg := c.Style.Bg
	*c = uv.Cell{Content: " ", Width: 1}
	c.Style.Bg = bg
	c.Style.Attrs = uv.AttrConceal
	if id&1 == 1 {
		c.Style.Attrs |= uv.AttrItalic
	}
}

func placeholderCell(c *uv.Cell, row, col, rows, cols int, fg color.Color) {
	blankCellKeepGround(c)
	c.Style.Fg = fg
	c.Style.Attrs = uv.AttrFaint
	last := func(v, n int) bool { return v == n-1 }
	switch {
	case rows == 1 || cols == 1:
		c.Content = "·"
	case row == 0 && col == 0:
		c.Content = "┌"
	case row == 0 && last(col, cols):
		c.Content = "┐"
	case last(row, rows) && col == 0:
		c.Content = "└"
	case last(row, rows) && last(col, cols):
		c.Content = "┘"
	case row == 0 || last(row, rows):
		c.Content = "─"
	case col == 0 || last(col, cols):
		c.Content = "│"
	default:
		const label = "image"
		mid := (rows - 1) / 2
		start := (cols - len(label)) / 2
		if row == mid && cols-2 >= len(label) && col >= start && col < start+len(label) {
			c.Content = string(label[col-start])
		}
	}
}
