//go:build unix

package app

import (
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // decoded so a theme shipping one is not a blank row
	_ "image/jpeg" // same
	"image/png"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// Icons are drawn as real pictures, not glyphs, and only where the host can
// show one.
//
// The size is the whole design. A launcher row is one text line, so an icon
// gets one row of height and the two columns beside the marker, which is
// roughly twenty pixels square on an ordinary terminal. That is favicon scale,
// and at favicon scale a Firefox or an OBS mark is still recognisable while a
// single cell is mush. Two by one is therefore the budget, and the icon is
// fitted to it rather than the row being grown to fit the icon.
//
// Three things follow from drawing a picture in a cell grid. The escape
// sequence has to be written after the frame the cells were reserved in, or the
// frame paints over it: that is what flushLauncherIconsForFrame and the
// reserved blanks in launcherRow are for. Decoding and scaling are filesystem and CPU work, so
// they happen in a tea.Cmd and arrive as a message, never on the Update
// goroutine. And nothing about any of it may run on a timer, so a decode is
// asked for once per icon and the result is kept for the life of the process.
//
// When the host cannot draw an image there is no icon column at all. A column
// of identical placeholder glyphs is worse than the list this replaced, and the
// point of the column is the picture.

const (
	// launcherIconCols and launcherIconRows are the icon's box in cells.
	launcherIconCols = 2
	launcherIconRows = 1

	// launcherIconIDBase is where tuios's own kitty image ids start.
	// KittyPassthrough hands guest images ids counting up from 1, so a pane has
	// to draw four billion images before its allocator reaches this range. It
	// does wrap rather than skip the range, so this is very unlikely rather than
	// impossible.
	launcherIconIDBase = 0xF000_0000

	// launcherIconChunk is the kitty protocol's maximum escape-sequence payload.
	launcherIconChunk = 4096
)

// launcherIcons holds the decoded icons and what has been put on the host.
//
// It is reached from the Update goroutine and written by a decode command, so
// every field is behind the mutex.
type launcherIcons struct {
	mu sync.Mutex
	// once guards building the finder, which reads the user's icon theme out of
	// their desktop configuration. That is filesystem work, so it happens in
	// the decode command's goroutine on the first decode rather than on the
	// Update goroutine when the store is first reached.
	once sync.Once
	// finder resolves a themed name to a file. Built once: it caches its own
	// theme walks, and rebuilding it per open would throw those away.
	finder *applist.IconFinder
	// pixels is the scaled image for an icon at a size. A nil value is a name
	// that resolved to nothing, cached so it is not looked up again.
	//
	// The size is part of the key because the pixels were scaled for it. A host
	// that changes its font, or that answers the capability probe late, changes
	// what a row can hold, and the box filter's whole point is being run at the
	// size the icon is actually drawn at.
	pixels map[iconKey]*image.RGBA
	// asked marks icons already handed to a decode, so a list that rebuilds on
	// every keystroke does not queue the same work again.
	asked map[iconKey]bool
	// hostID is the kitty image id an icon was uploaded under, so it is
	// transmitted once and merely re-placed afterwards. Keyed by size for the
	// same reason pixels is: a different size is a different upload.
	hostID map[iconKey]uint32
	nextID uint32
	// shown is what is currently on screen, keyed by the pair the host keys a
	// placement on, so an unchanged list emits nothing and a changed one erases
	// what it replaces.
	//
	// The key is the image id as well as the placement id because that is what
	// the protocol scopes a placement to: a placement id belongs to an image,
	// so drawing a different picture at the same row is a second placement and
	// not a replacement of the first. Keyed by row alone, scrolling the list
	// left one orphaned image behind per row it moved, since the record of the
	// picture that had been there was overwritten before anything deleted it.
	//
	// The value records where the icon was drawn, because the panel is
	// draggable: the same row can need a new placement without its picture
	// having changed.
	shown map[launcherIconID]launcherIconPlacement
}

// launcherIconID is one placement on the host: the image, and the placement id
// within that image. Deleting one needs both, and needs them as they were when
// it was drawn rather than re-derived from the row's name and the current cell
// size, which is a lookup that can miss.
type launcherIconID struct {
	img, placement uint32
}

func newLauncherIcons() *launcherIcons {
	return &launcherIcons{
		pixels: make(map[iconKey]*image.RGBA),
		asked:  make(map[iconKey]bool),
		hostID: make(map[iconKey]uint32),
		nextID: launcherIconIDBase,
		shown:  make(map[launcherIconID]launcherIconPlacement),
	}
}

// iconFinder builds the finder on first use.
//
// Call it only from a decode command's own goroutine: working out which icon
// theme the user has set means reading their desktop configuration, and the
// Update goroutine has no business waiting on a file for that.
func (s *launcherIcons) iconFinder() *applist.IconFinder {
	s.once.Do(func() {
		f := applist.NewIconFinder(applist.CurrentIconTheme())
		// The standard library decodes no SVG, and half of a themed icon set is
		// SVG, so the finder is told to pass over those rather than hand back a
		// file that cannot be turned into pixels.
		f.RasterOnly = true
		s.finder = f
	})
	return s.finder
}

// iconKey is one icon at one drawn size.
type iconKey struct {
	name string
	w, h int
}

// launcherIconsMsg carries decoded icons back to the Update goroutine.
type launcherIconsMsg struct {
	pixels map[iconKey]*image.RGBA
}

// iconCellSize is the icon's box in pixels, from the host's reported cell size.
func iconCellSize() (w, h int) {
	caps := GetHostCapabilities()
	cw, ch := caps.CellWidth, caps.CellHeight
	if cw <= 0 || ch <= 0 {
		return 0, 0
	}
	return cw * launcherIconCols, ch * launcherIconRows
}

// launcherGraphicsReady reports whether icons can be drawn at all: the host has
// to speak the kitty graphics protocol and to have told us how big a cell is.
//
// Sixel is deliberately not a second path. It has no way to delete a placement,
// so every scroll of the list would have to erase and repaint by hand, and the
// terminals that speak sixel and not kitty are not where this feature earns its
// keep.
func (m *OS) launcherGraphicsReady() bool {
	if m.PostRenderWriter == nil {
		return false
	}
	if !GetHostCapabilities().KittyGraphics {
		return false
	}
	w, h := iconCellSize()
	return w > 0 && h > 0
}

// launcherIconState returns the icon store, building it on first use.
func (m *OS) launcherIconState() *launcherIcons {
	if m.launcherIcons == nil {
		m.launcherIcons = newLauncherIcons()
	}
	return m.launcherIcons
}

// LauncherIconCmd asks for the icons the visible rows need that have not been
// looked up yet, or nil when there are none.
//
// Resolving an icon walks the theme's directories and decoding it reads and
// scales a file, which is why this is a command and not a call. Only the rows
// actually on screen are asked for: a launcher listing several thousand
// programs would otherwise decode a few hundred icons nobody is looking at.
func (m *OS) LauncherIconCmd(names []string) tea.Cmd {
	if !m.launcherGraphicsReady() {
		return nil
	}
	st := m.launcherIconState()
	w, h := iconCellSize()

	st.mu.Lock()
	var want []iconKey
	for _, n := range names {
		k := iconKey{name: n, w: w, h: h}
		if n == "" || st.asked[k] {
			continue
		}
		st.asked[k] = true
		want = append(want, k)
	}
	st.mu.Unlock()

	if len(want) == 0 {
		return nil
	}
	return func() tea.Msg {
		finder := st.iconFinder()
		out := make(map[iconKey]*image.RGBA, len(want))
		for _, k := range want {
			out[k] = loadIcon(finder, k.name, k.w, k.h)
		}
		return launcherIconsMsg{pixels: out}
	}
}

// applyLauncherIcons files decoded icons away. A nil image is kept, because a
// name with no icon is an answer and re-deriving it is the expensive half.
func (m *OS) applyLauncherIcons(msg launcherIconsMsg) {
	st := m.launcherIconState()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, img := range msg.pixels {
		st.pixels[k] = img
	}
}

// loadIcon resolves, decodes and scales one icon, returning nil when any step
// has no answer. Every failure is the same answer to the caller, which is that
// this row has no picture.
func loadIcon(finder *applist.IconFinder, name string, w, h int) *image.RGBA {
	// The size asked for is the box's shorter side, since the icon is square
	// and the row is what constrains it.
	path := finder.Find(name, min(w, h))
	if path == "" {
		return nil
	}
	f, err := os.Open(path) // #nosec G304 - the path came from the icon theme walk
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return fitSquare(src, w, h)
}

// fitSquare scales src to the largest square that fits a w by h box and centres
// it, keeping the alpha channel.
//
// The transparency is left for the terminal to composite rather than being
// blended against a colour here. The colour it would have to blend against is
// the row's own, and a row changes colour when it is selected, so a baked
// backdrop would show as a patch of the wrong shade under the cursor.
//
// The scale is a box filter rather than nearest neighbour: an icon drawn for
// 48 pixels going to 20 loses three quarters of its pixels, and picking one of
// every four turns a smooth edge into a staircase, which at this size is most
// of what the eye sees.
func fitSquare(src image.Image, w, h int) *image.RGBA {
	side := min(w, h)
	if side <= 0 {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	offX, offY := (w-side)/2, (h-side)/2

	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil
	}
	for y := range side {
		y0, y1 := b.Min.Y+y*b.Dy()/side, b.Min.Y+(y+1)*b.Dy()/side
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range side {
			x0, x1 := b.Min.X+x*b.Dx()/side, b.Min.X+(x+1)*b.Dx()/side
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, sa, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					r, g, bl, a := src.At(xx, yy).RGBA()
					sr, sg, sb, sa, n = sr+uint64(r), sg+uint64(g), sb+uint64(bl), sa+uint64(a), n+1
				}
			}
			// image.RGBA is alpha-premultiplied and RGBA() returns
			// premultiplied channels, so the averages go straight in.
			i := dst.PixOffset(offX+x, offY+y)
			dst.Pix[i] = uint8((sr / n) >> 8)
			dst.Pix[i+1] = uint8((sg / n) >> 8)
			dst.Pix[i+2] = uint8((sb / n) >> 8)
			dst.Pix[i+3] = uint8((sa / n) >> 8)
		}
	}
	return dst
}

// launcherIconPlacement is one icon on screen: which picture, and the cell it
// starts at.
type launcherIconPlacement struct {
	Name string
	X, Y int
}

// flushLauncherIconsForFrame draws the icons for the frame just rendered.
//
// The renderer records each icon's cell relative to the panel, because that is
// all it knows: where the panel itself lands is decided afterwards, when it is
// placed as a layer and can be dragged. So the offset is applied here, from the
// hit geometry that placement recorded.
func (m *OS) flushLauncherIconsForFrame() {
	if len(m.launcherIconCells) == 0 {
		m.flushLauncherIcons(nil)
		return
	}
	h, ok := m.overlayHitByKind("launcher")
	if !ok {
		// The panel has not been placed this frame, so there is nowhere to put
		// the pictures. Nothing is drawn rather than something drawn at the
		// wrong place.
		return
	}
	out := make([]launcherIconPlacement, 0, len(m.launcherIconCells))
	for _, p := range m.launcherIconCells {
		out = append(out, launcherIconPlacement{Name: p.Name, X: h.OriginX + p.X, Y: h.OriginY + p.Y})
	}
	m.flushLauncherIcons(out)
}

// flushLauncherIcons puts the launcher's icons on the host for the frame just
// drawn, and takes down the ones that are no longer under a row.
//
// It runs after the frame because the cells were reserved by drawing blanks;
// writing the image first would only have it painted over.
func (m *OS) flushLauncherIcons(placements []launcherIconPlacement) {
	if m.launcherIcons == nil && len(placements) == 0 {
		return
	}
	st := m.launcherIconState()

	w, h := iconCellSize()

	st.mu.Lock()
	var buf []byte
	want := make(map[launcherIconID]launcherIconPlacement, len(placements))
	for i, p := range placements {
		key := iconKey{name: p.Name, w: w, h: h}
		img := st.pixels[key]
		if img == nil {
			continue
		}
		id, up := st.hostID[key]
		if !up {
			id = st.nextID
			st.nextID++
			st.hostID[key] = id
			buf = appendKittyTransmit(buf, id, img)
		}
		// The placement id is the row's index in the drawn list, and the host
		// scopes it to the image, so the pair is what identifies this picture
		// on screen.
		at := launcherIconID{img: id, placement: uint32(i + 1)}
		want[at] = p

		was, drawn := st.shown[at]
		if drawn && was == p {
			// Same picture, same cell: the frame redrew the text around it and
			// the placement is still there, so there is nothing to say. Skipping
			// is what keeps an open launcher from re-placing a dozen images on
			// every keystroke.
			continue
		}
		if drawn {
			// The panel moved under a drag. Taking the old placement down before
			// putting the new one up costs one escape on a path that only runs
			// while the panel is being dragged, and it is what keeps the code
			// from relying on a host treating a repeated a=p as a replacement
			// rather than a second placement. Both escapes are inside the same
			// synchronised update, so nothing is presented half done.
			buf = appendKittyUnplace(buf, at.img, at.placement)
		}
		buf = appendKittyPlace(buf, at.img, at.placement, p.X, p.Y)
	}
	// Anything that was on screen and is not now has to be deleted by hand: the
	// frame under it was redrawn, but a kitty placement outlives the cells it
	// covers. Each is deleted by the ids it was drawn under, which is why they
	// are the key: re-deriving them from the row's name and the current cell
	// size is a lookup that misses the moment either changes, and a miss here
	// is an image nothing will ever take down.
	for at := range st.shown {
		if _, kept := want[at]; kept {
			continue
		}
		buf = appendKittyUnplace(buf, at.img, at.placement)
	}
	st.shown = want
	st.mu.Unlock()

	if len(buf) == 0 {
		return
	}
	out := wrapSync(buf)
	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(out)
		return
	}
	m.WriteHost(out)
}

// clearLauncherIcons takes every launcher icon off the host. Called when the
// launcher closes, because a placement survives the panel that put it there.
func (m *OS) clearLauncherIcons() {
	if m.launcherIcons == nil {
		return
	}
	st := m.launcherIcons
	st.mu.Lock()
	var buf []byte
	for at := range st.shown {
		buf = appendKittyUnplace(buf, at.img, at.placement)
	}
	clear(st.shown)
	st.mu.Unlock()

	if len(buf) == 0 {
		return
	}
	out := wrapSync(buf)
	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(out)
		return
	}
	m.WriteHost(out)
}

// appendKittyTransmit uploads an image and leaves it resident under id, so the
// rows that show it afterwards only cost a placement.
//
// The payload is PNG rather than raw pixels because a twenty pixel icon
// compresses to a few hundred bytes, and the escape sequence is chunked at the
// protocol's limit either way.
func appendKittyTransmit(buf []byte, id uint32, img *image.RGBA) []byte {
	var raw []byte
	{
		w := &byteWriter{}
		if png.Encode(w, img) != nil {
			return buf
		}
		raw = w.b
	}
	enc := base64.StdEncoding.EncodeToString(raw)
	for off := 0; off < len(enc); off += launcherIconChunk {
		end := min(off+launcherIconChunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if off == 0 {
			// q=2 suppresses the host's replies. Without it the terminal
			// answers every chunk and those answers arrive as input.
			buf = append(buf, fmt.Sprintf("\x1b_Ga=t,f=100,i=%d,q=2,m=%d;", id, more)...)
		} else {
			buf = append(buf, fmt.Sprintf("\x1b_Gm=%d,q=2;", more)...)
		}
		buf = append(buf, enc[off:end]...)
		buf = append(buf, "\x1b\\"...)
	}
	return buf
}

// appendKittyPlace draws a resident image at a screen cell, scaled to the icon
// box. The cursor is saved and restored around it, since the frame that was
// just written left it where the renderer wanted it.
func appendKittyPlace(buf []byte, id, placement uint32, x, y int) []byte {
	buf = append(buf, "\x1b7"...)
	buf = append(buf, fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)...)
	buf = append(buf, fmt.Sprintf("\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d,q=2,C=1;\x1b\\",
		id, placement, launcherIconCols, launcherIconRows)...)
	buf = append(buf, "\x1b8"...)
	return buf
}

// appendKittyUnplace removes one placement, leaving the image resident so the
// row that shows it next does not pay for the upload again.
func appendKittyUnplace(buf []byte, id, placement uint32) []byte {
	return append(buf, fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,p=%d,q=2;\x1b\\", id, placement)...)
}

// wrapSync brackets a run of graphics escapes in a synchronised update, so the
// host draws them all in one go rather than showing the list half redrawn.
//
// The buffer is built fresh rather than appended onto syncBegin, which is a
// package-level slice: appending to it would share its backing array with every
// other caller the moment its capacity exceeded its length.
func wrapSync(buf []byte) []byte {
	out := make([]byte, 0, len(syncBegin)+len(buf)+len(syncEnd))
	out = append(out, syncBegin...)
	out = append(out, buf...)
	return append(out, syncEnd...)
}

// byteWriter is an io.Writer over a slice, so the PNG encoder does not need a
// bytes.Buffer's extra indirection for a payload this small.
type byteWriter struct{ b []byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
