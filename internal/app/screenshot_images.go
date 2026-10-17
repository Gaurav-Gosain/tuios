package app

import (
	"image"
	"image/color"
	"maps"
	"math"
	"slices"
	"sort"
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"tuios.dev/shot"
	"tuios.dev/vt"
)

// The pictures in a screenshot.
//
// A pane's kitty and sixel images are drawn by the host terminal, so they are
// in no cell the capture reads. A screenshot finds them in what this client
// already keeps to show them:
//   - a kitty placement, from the passthrough's placements and the payload it
//     kept for the image (kitty_keep.go);
//   - a kitty Unicode placeholder, from the placeholder cells and the box of
//     its virtual placement;
//   - a sixel image, from its marker cells and the decoded image the sixel
//     passthrough holds.
//
// So a screenshot shows the images this client shows, and no others. A
// served client (SSH, web) has its own passthroughs, holding only the panes
// that client can see.
//
// Finding the images is cheap and happens with the cells, on the capture.
// Decoding them is the slow part and happens in the render command, off the
// update loop, for the images on the picture only and under
// shotImageMaxPixels. Nothing here runs unless a screenshot is taken.

// shotImageMaxPixels bounds the pixels one screenshot decodes, across all its
// images. It is the reply preview's bound (replyPreviewMaxPixels), for the
// same reason: every image size is the guest's to choose. An image past what
// is left is left out of the picture.
const shotImageMaxPixels = 24 << 20

// shotPiece is one image on a capture, before its pixels are decoded.
type shotPiece struct {
	// key names the decoded image, so pieces of one image decode it once.
	key any
	// declared is the image's area as far as it is known before decoding,
	// 0 when it is not.
	declared int
	decode   func(maxPixels int) (image.Image, error)
	// source gives the rectangle of the decoded image that fills the box.
	source func(pix image.Image) image.Rectangle
	// col, row, cols, rows are the image's whole box in grid cells, and sub
	// is the part of it the capture shows, in cells of the box. An empty sub
	// is the whole box.
	col, row, cols, rows int
	sub                  image.Rectangle
	z                    int32
}

// decodeShotPieces decodes the pieces' images and returns them as shot
// images, in drawing order.
func decodeShotPieces(pieces []shotPiece) []shot.Image {
	if len(pieces) == 0 {
		return nil
	}
	// sort.SliceStable is linked already. slices.SortStableFunc would add
	// its own copy of the sort for this one type.
	sort.SliceStable(pieces, func(i, j int) bool { return pieces[i].z < pieces[j].z })
	type result struct {
		pix image.Image
		err error
	}
	decoded := make(map[any]result)
	budget := shotImageMaxPixels
	out := make([]shot.Image, 0, len(pieces))
	for _, p := range pieces {
		res, done := decoded[p.key]
		if !done {
			if p.declared > budget {
				res.err = errImageTooLarge
			} else {
				res.pix, res.err = p.decode(budget)
			}
			if res.err == nil && res.pix != nil {
				b := res.pix.Bounds()
				budget -= b.Dx() * b.Dy()
			}
			decoded[p.key] = res
		}
		if res.err != nil || res.pix == nil {
			continue
		}
		src := p.source(res.pix)
		img := shot.Image{
			Pixels: res.pix, Source: src,
			Col: p.col, Row: p.row, Cols: p.cols, Rows: p.rows,
			Below: p.z < 0,
		}
		if !p.sub.Empty() && p.sub != image.Rect(0, 0, p.cols, p.rows) {
			img = subShotImage(img, p.sub)
		}
		out = append(out, img)
	}
	return out
}

// subShotImage narrows an image to a rectangle of its box, given in cells of
// the box, with the matching part of its source.
func subShotImage(img shot.Image, sub image.Rectangle) shot.Image {
	src := img.Source
	if src.Empty() {
		src = img.Pixels.Bounds()
	}
	at := func(base, size, cells, n int) int {
		return base + int(math.Round(float64(size)*float64(n)/float64(cells)))
	}
	img.Source = image.Rect(
		at(src.Min.X, src.Dx(), img.Cols, sub.Min.X), at(src.Min.Y, src.Dy(), img.Rows, sub.Min.Y),
		at(src.Min.X, src.Dx(), img.Cols, sub.Max.X), at(src.Min.Y, src.Dy(), img.Rows, sub.Max.Y),
	)
	img.Col, img.Row = img.Col+sub.Min.X, img.Row+sub.Min.Y
	img.Cols, img.Rows = sub.Dx(), sub.Dy()
	return img
}

// kittyShotSource is the rectangle of a kitty image that fills a placement's
// box of cols by rows cells.
//
// kitty draws an image at its own pixel size unless the guest gave the box
// with c= and r=, and then it scales the image to fill the box. A placement
// record keeps only the box, so which of the two it was is read back from its
// size: a box that is the image's own size rounded up to whole cells is the
// image at its own size, and the rest of the box is empty.
func kittyShotSource(pix image.Image, cols, rows, sx, sy, sw, sh, xoff, yoff, cellW, cellH int) image.Rectangle {
	b := pix.Bounds()
	if sw <= 0 {
		sw = b.Dx() - sx
	}
	if sh <= 0 {
		sh = b.Dy() - sy
	}
	if cellW > 0 && cellH > 0 && cols == (sw+cellW-1)/cellW && rows == (sh+cellH-1)/cellH {
		x0, y0 := b.Min.X+sx-xoff, b.Min.Y+sy-yoff
		return image.Rect(x0, y0, x0+cols*cellW, y0+rows*cellH)
	}
	return image.Rect(b.Min.X+sx, b.Min.Y+sy, b.Min.X+sx+sw, b.Min.Y+sy+sh)
}

// virtualShotSource is the rectangle of an image that fills the box of a
// virtual placement: kitty fits the image in the box, keeping its aspect
// ratio, and centres it.
func virtualShotSource(pix image.Image, cols, rows, cellW, cellH int) image.Rectangle {
	b := pix.Bounds()
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = 10, 20
	}
	w, h := float64(b.Dx()), float64(b.Dy())
	bw, bh := float64(cols*cellW), float64(rows*cellH)
	if w <= 0 || h <= 0 || bw <= 0 || bh <= 0 {
		return b
	}
	scale := math.Min(bw/w, bh/h)
	sw, sh := bw/scale, bh/scale
	x0, y0 := float64(b.Min.X)+(w-sw)/2, float64(b.Min.Y)+(h-sh)/2
	return image.Rect(int(math.Round(x0)), int(math.Round(y0)), int(math.Round(x0+sw)), int(math.Round(y0+sh)))
}

// shotCellHit is one image cell found on a captured grid.
type shotCellHit struct {
	x, y int
	key  shotHitKey
	r, c int // the cell's row and column inside the image
}

// shotHitKey says which image a cell belongs to.
type shotHitKey struct {
	sixel     bool
	id        uint32
	placement uint32
}

// shotHitRect is a rectangle of cells of one image: at x, y on the grid, w by
// h cells, starting at row r and column c of the image.
type shotHitRect struct {
	key        shotHitKey
	x, y, w, h int
	r, c       int
}

// groupShotHits joins image cells into rectangles: a run of cells along a row
// whose image columns follow on, and then runs in rows below that line up.
// hits are in row-major order.
func groupShotHits(hits []shotCellHit) []shotHitRect {
	var runs []shotHitRect
	for _, h := range hits {
		if n := len(runs); n > 0 {
			last := &runs[n-1]
			if last.key == h.key && last.y == h.y && last.x+last.w == h.x &&
				last.r == h.r && last.c+last.w == h.c {
				last.w++
				continue
			}
		}
		runs = append(runs, shotHitRect{key: h.key, x: h.x, y: h.y, w: 1, h: 1, r: h.r, c: h.c})
	}
	type open struct {
		key        shotHitKey
		x, w, c    int
		nextY, nxR int
	}
	at := make(map[open]int)
	var out []shotHitRect
	for _, run := range runs {
		k := open{run.key, run.x, run.w, run.c, run.y, run.r}
		if i, ok := at[k]; ok {
			delete(at, k)
			out[i].h++
			k.nextY, k.nxR = run.y+1, run.r+1
			at[k] = i
			continue
		}
		out = append(out, run)
		k.nextY, k.nxR = run.y+1, run.r+1
		at[k] = len(out) - 1
	}
	return out
}

// shotDiacritics is the reverse of kitty.Diacritic.
var shotDiacritics = sync.OnceValue(func() map[rune]int {
	m := make(map[rune]int, 297)
	first := kitty.Diacritic(0)
	for i := 0; ; i++ {
		r := kitty.Diacritic(i)
		if i > 0 && r == first {
			break
		}
		m[r] = i
	}
	return m
})

// placeholderHit reads a kitty placeholder cell. The emulator gives every
// placeholder cell its own row and column as it stores it (vt's
// rewriteKittyPlaceholder), so a cell is read on its own.
func placeholderHit(x, y int, cell *uv.Cell) (shotCellHit, bool) {
	id, ok := vt.KittyPlaceholderImageID(cell.Content, cell.Style.Fg)
	if !ok || id == 0 {
		return shotCellHit{}, false
	}
	marks := shotDiacritics()
	n, r, c := 0, 0, 0
	for i, ch := range cell.Content {
		if i == 0 {
			continue
		}
		v, ok := marks[ch]
		if !ok {
			continue
		}
		if n == 0 {
			r = v
		} else {
			c = v
		}
		n++
		if n == 2 {
			break
		}
	}
	if n < 2 {
		return shotCellHit{}, false
	}
	return shotCellHit{x: x, y: y, r: r, c: c,
		key: shotHitKey{id: id, placement: placeholderPlacementID(cell.Style.UnderlineColor)}}, true
}

// placeholderPlacementID reads the placement id a placeholder cell carries in
// its underline colour.
func placeholderPlacementID(c color.Color) uint32 {
	switch v := c.(type) {
	case nil:
		return 0
	case ansi.IndexedColor:
		return uint32(v)
	case ansi.BasicColor:
		return uint32(v)
	}
	r, g, b, a := c.RGBA()
	if a == 0 {
		return 0
	}
	return uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
}

// scanImageCells finds the sixel and kitty placeholder cells of a screen and
// blanks them on the grid, which would otherwise draw them as missing glyphs.
// The grid has the screen's size.
func scanImageCells(t interface {
	CellAt(x, y int) *uv.Cell
}, g *shot.Grid) []shotCellHit {
	var hits []shotCellHit
	for y := range g.Rows {
		row := g.Cells[y]
		for x := range g.Cols {
			cell := t.CellAt(x, y)
			if cell == nil || len(cell.Content) < 4 {
				continue
			}
			switch {
			case vt.IsSixelMarker(cell.Content):
				row[x].Cluster = ""
				if id, r, c, ok := vt.ParseSixelMarker(cell.Content); ok && id != 0 {
					hits = append(hits, shotCellHit{x: x, y: y, r: r, c: c, key: shotHitKey{sixel: true, id: id}})
				}
			case vt.IsKittyPlaceholder(cell.Content):
				row[x].Cluster = ""
				if h, ok := placeholderHit(x, y, cell); ok {
					hits = append(hits, h)
				}
			}
		}
	}
	return hits
}

// shotKittyPlacement is what a capture needs of one kitty placement.
type shotKittyPlacement struct {
	kept                 *keptImage
	guestX, absoluteLine int
	imageCols, rows      int
	sx, sy, sw, sh       int
	xoff, yoff           int
	z                    int32
	slices               []placementSlice
	hostX, hostY         int
	clipTop, clipLeft    int
	showCols, showRows   int
}

// captureKitty reads what a capture needs from the passthrough: the window's
// placements on the screen it shows, every image a placeholder cell may name,
// the virtual boxes of those images and the host's cell size. An empty
// windowID reads the placements the host is showing now, for every window.
func (kp *KittyPassthrough) captureKitty(windowID string, alt bool, hostIDs []uint32) (
	placements []shotKittyPlacement, kept map[uint32]*keptImage, boxes map[uint32]map[uint32]virtualBox, cellW, cellH int,
) {
	if kp == nil {
		return nil, nil, nil, 0, 0
	}
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if !kp.enabled {
		return nil, nil, nil, 0, 0
	}
	caps := kp.hostCaps()
	cellW, cellH = caps.CellWidth, caps.CellHeight
	add := func(win string, p *PassthroughPlacement) {
		k := kp.kept[win][p.HostImageID]
		if k == nil || p.Virtual {
			return
		}
		placements = append(placements, shotKittyPlacement{
			kept: k, guestX: p.GuestX, absoluteLine: p.AbsoluteLine,
			imageCols: max(p.ImageCols, p.Cols), rows: p.Rows,
			sx: p.SourceX, sy: p.SourceY, sw: p.SourceWidth, sh: p.SourceHeight,
			xoff: p.XOffset, yoff: p.YOffset, z: p.ZIndex,
			slices: slices.Clone(p.Slices),
			hostX:  p.HostX, hostY: p.HostY, clipTop: p.ClipTop, clipLeft: p.ClipLeft,
			showCols: p.MaxShowableCols, showRows: p.MaxShowable,
		})
	}
	if windowID != "" {
		for _, p := range kp.placements[windowID] {
			if p.PlacedOnAltScreen == alt {
				add(windowID, p)
			}
		}
	} else {
		for win, ps := range kp.placements {
			for _, p := range ps {
				if !p.Hidden {
					add(win, p)
				}
			}
		}
	}
	if len(hostIDs) > 0 {
		kept = make(map[uint32]*keptImage, len(hostIDs))
		boxes = make(map[uint32]map[uint32]virtualBox, len(hostIDs))
		for _, id := range hostIDs {
			for win, ids := range kp.kept {
				if windowID != "" && win != windowID {
					continue
				}
				if k := ids[id]; k != nil {
					kept[id] = k
					boxes[id] = maps.Clone(kp.virtualBoxes[win][id])
					break
				}
			}
		}
	}
	return placements, kept, boxes, cellW, cellH
}

// shotSixel is what a capture needs of one sixel image.
type shotSixel struct {
	img          *vt.SixelImage
	cellW, cellH int
	rows, cols   int
}

// captureSixel reads the sixel images with the given ids, and the
// rectangles the host is showing now when frame is set.
func (sp *SixelPassthrough) captureSixel(ids []uint32, frame bool) (map[uint32]shotSixel, []sixelRect) {
	if sp == nil {
		return nil, nil
	}
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var rects []sixelRect
	if frame {
		rects = slices.Clone(sp.frame.rects)
		for _, r := range rects {
			ids = append(ids, r.id)
		}
	}
	out := make(map[uint32]shotSixel, len(ids))
	for _, id := range ids {
		if e := sp.images[id]; e != nil && e.img != nil {
			out[id] = shotSixel{img: e.img, cellW: e.cellW, cellH: e.cellH, rows: e.rows, cols: e.cols}
		}
	}
	return out, rects
}

// sixelPiece is the piece for a rectangle of a sixel image's cells, at x, y
// on the grid.
func sixelPiece(id uint32, s shotSixel, x, y, r0, c0, r1, c1 int) shotPiece {
	img := s.img
	return shotPiece{
		key:      img,
		declared: img.Width * img.Height,
		decode: func(maxPixels int) (image.Image, error) {
			if img.Width <= 0 || img.Height <= 0 || img.Width > maxPixels/img.Height {
				return nil, errImageTooLarge
			}
			return &image.NRGBA{
				Pix:    img.RGBA(image.Rect(0, 0, img.Width, img.Height)),
				Stride: img.Width * 4,
				Rect:   image.Rect(0, 0, img.Width, img.Height),
			}, nil
		},
		source: func(image.Image) image.Rectangle {
			// A sixel image covers its cells from their top-left corner at its
			// own pixel size, and the rest of the last row and column is empty.
			return image.Rect(0, 0, s.cols*s.cellW, s.rows*s.cellH)
		},
		col: x - c0, row: y - r0, cols: s.cols, rows: s.rows,
		sub: image.Rect(c0, r0, c1, r1),
	}
}

// kittyPiece is the piece for a kitty placement whose whole box has its
// top-left cell at col, row.
func kittyPiece(p shotKittyPlacement, col, row, cellW, cellH int) shotPiece {
	k := p.kept
	return shotPiece{
		key:      k,
		declared: k.pixels(),
		decode:   k.decode,
		source: func(pix image.Image) image.Rectangle {
			return kittyShotSource(pix, p.imageCols, p.rows, p.sx, p.sy, p.sw, p.sh, p.xoff, p.yoff, cellW, cellH)
		},
		col: col, row: row, cols: p.imageCols, rows: p.rows,
		z: p.z,
	}
}

// placeholderPieces turns grouped placeholder cells into pieces.
func placeholderPieces(rects []shotHitRect, kept map[uint32]*keptImage, boxes map[uint32]map[uint32]virtualBox, cellW, cellH int) []shotPiece {
	var out []shotPiece
	for _, r := range rects {
		k := kept[r.key.id]
		if k == nil {
			continue
		}
		box, ok := boxes[r.key.id][r.key.placement]
		if !ok {
			// A cell that names no placement, or one tuios did not see, takes
			// the image's only box when it has one.
			for _, b := range boxes[r.key.id] {
				box = b
				break
			}
		}
		cols, rows := box.cols, box.rows
		if cols <= 0 || rows <= 0 {
			// Without a box the image is at its own size, so the cells say how
			// far it reaches. Its last row and column are the most this run
			// shows.
			cols, rows = max(cols, r.c+r.w), max(rows, r.r+r.h)
		}
		out = append(out, shotPiece{
			key:      k,
			declared: k.pixels(),
			decode:   k.decode,
			source: func(pix image.Image) image.Rectangle {
				return virtualShotSource(pix, cols, rows, cellW, cellH)
			},
			col: r.x - r.c, row: r.y - r.r, cols: cols, rows: rows,
			sub: image.Rect(r.c, r.r, r.c+r.w, r.r+r.h),
			z:   box.z,
		})
	}
	return out
}

// hitIDs splits the image ids found on a grid by kind.
func hitIDs(hits []shotCellHit) (sixelIDs, kittyIDs []uint32) {
	for _, h := range hits {
		if h.key.sixel {
			if !slices.Contains(sixelIDs, h.key.id) {
				sixelIDs = append(sixelIDs, h.key.id)
			}
		} else if !slices.Contains(kittyIDs, h.key.id) {
			kittyIDs = append(kittyIDs, h.key.id)
		}
	}
	return sixelIDs, kittyIDs
}

// windowShotPieces finds the images on a window's screen, the grid the
// capture just read from it, and blanks their cells on the grid. The caller
// holds the window's IO lock, which is taken before the passthrough locks as
// the PTY reader takes it.
func (m *OS) windowShotPieces(windowID string, alt bool, t vt.Terminal, g *shot.Grid) []shotPiece {
	hits := scanImageCells(t, g)
	sixelIDs, kittyIDs := hitIDs(hits)
	placements, kept, boxes, cellW, cellH := m.KittyPassthrough.captureKitty(windowID, alt, kittyIDs)
	var pieces []shotPiece
	top := t.ScrollbackLen()
	for _, p := range placements {
		pieces = append(pieces, kittyPiece(p, p.guestX, p.absoluteLine-top, cellW, cellH))
	}
	pieces = append(pieces, hitPieces(hits, sixelIDs, kept, boxes, cellW, cellH, m.SixelPassthrough)...)
	return pieces
}

// hitPieces turns the image cells found on a grid into pieces.
func hitPieces(hits []shotCellHit, sixelIDs []uint32, kept map[uint32]*keptImage, boxes map[uint32]map[uint32]virtualBox, cellW, cellH int, sp *SixelPassthrough) []shotPiece {
	if len(hits) == 0 {
		return nil
	}
	rects := groupShotHits(hits)
	var sixels map[uint32]shotSixel
	if len(sixelIDs) > 0 {
		sixels, _ = sp.captureSixel(sixelIDs, false)
	}
	var pieces, kitty []shotPiece
	var kittyRects []shotHitRect
	for _, r := range rects {
		if !r.key.sixel {
			kittyRects = append(kittyRects, r)
			continue
		}
		if s, ok := sixels[r.key.id]; ok {
			pieces = append(pieces, sixelPiece(r.key.id, s, r.x, r.y, r.r, r.c, r.r+r.h, r.c+r.w))
		}
	}
	kitty = placeholderPieces(kittyRects, kept, boxes, cellW, cellH)
	return append(pieces, kitty...)
}

// screenShotPieces finds the images on the composed screen: the kitty
// placements and sixel rectangles the host is showing, where it shows them,
// and the placeholder cells hits found on the composed grid. Positions are
// screen cells.
func (m *OS) screenShotPieces(hits []shotCellHit) []shotPiece {
	_, kittyIDs := hitIDs(hits)
	placements, kept, boxes, cellW, cellH := m.KittyPassthrough.captureKitty("", false, kittyIDs)
	var pieces []shotPiece
	for _, p := range placements {
		whole := kittyPiece(p, 0, 0, cellW, cellH)
		shown := p.slices
		if len(shown) == 0 {
			shown = []placementSlice{{HostX: p.hostX, HostY: p.hostY, ClipTop: p.clipTop, ClipLeft: p.clipLeft,
				Cols: p.showCols, Rows: p.showRows}}
		}
		for _, s := range shown {
			if s.Cols <= 0 || s.Rows <= 0 {
				continue
			}
			piece := whole
			piece.col, piece.row = s.HostX-s.ClipLeft, s.HostY-s.ClipTop
			piece.sub = image.Rect(s.ClipLeft, s.ClipTop, s.ClipLeft+s.Cols, s.ClipTop+s.Rows)
			pieces = append(pieces, piece)
		}
	}
	sixels, rects := m.SixelPassthrough.captureSixel(nil, true)
	for _, r := range rects {
		if s, ok := sixels[r.id]; ok {
			pieces = append(pieces, sixelPiece(r.id, s, r.x, r.y, r.r0, r.c0, r.r1, r.c1))
		}
	}
	var kittyRects []shotHitRect
	for _, r := range groupShotHits(hits) {
		if !r.key.sixel {
			kittyRects = append(kittyRects, r)
		}
	}
	pieces = append(pieces, placeholderPieces(kittyRects, kept, boxes, cellW, cellH)...)
	// The reply editor's previews and the launcher's icons are tuios's own
	// kitty pictures, placed beside the passthrough rather than through it, so
	// it does not list them.
	pieces = append(pieces, m.replyPreview.shotPieces()...)
	return append(pieces, m.launcherIcons.shotPieces()...)
}

// cropShotPieces moves pieces found in screen cells onto a grid cut out at
// x0, y0.
func cropShotPieces(pieces []shotPiece, x0, y0 int) []shotPiece {
	for i := range pieces {
		pieces[i].col -= x0
		pieces[i].row -= y0
	}
	return pieces
}
