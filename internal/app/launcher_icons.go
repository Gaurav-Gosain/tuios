//go:build unix

package app

import (
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"maps"
	"math"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

const (
	launcherIconCols = 2
	launcherIconRows = 1

	launcherIconIDBase = 0xF000_0000

	launcherIconChunk = 4096
)

type launcherIcons struct {
	mu          sync.Mutex
	once        sync.Once
	finder      *applist.IconFinder
	pixels      map[iconKey]*image.RGBA
	asked       map[iconKey]bool
	hostID      map[iconKey]uint32
	eraseHooked bool
	nextID      uint32
	shown       map[launcherIconID]launcherIconPlacement
}

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

func (s *launcherIcons) iconFinder() *applist.IconFinder {
	s.once.Do(func() {
		f := applist.NewIconFinder(applist.CurrentIconTheme())
		f.RasterOnly = true
		s.finder = f
	})
	return s.finder
}

type iconKey struct {
	name string
	w, h int
}

type launcherIconsMsg struct {
	pixels map[iconKey]*image.RGBA
}

func (m *OS) iconCellSize() (w, h int) {
	caps := m.hostCaps()
	cw, ch := caps.CellWidth, caps.CellHeight
	if cw <= 0 || ch <= 0 {
		return 0, 0
	}
	return cw * launcherIconCols, ch * launcherIconRows
}

func (m *OS) launcherGraphicsReady() bool {
	if m.PostRenderWriter == nil {
		return false
	}
	if !m.hostCaps().KittyGraphics {
		return false
	}
	w, h := m.iconCellSize()
	return w > 0 && h > 0
}

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
	w, h := m.iconCellSize()

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

func (m *OS) applyLauncherIcons(msg launcherIconsMsg) {
	st := m.launcherIconState()
	st.mu.Lock()
	defer st.mu.Unlock()
	maps.Copy(st.pixels, msg.pixels)
}

func loadIcon(finder *applist.IconFinder, name string, w, h int) *image.RGBA {
	path := finder.Find(name, min(w, h))
	if path == "" {
		return nil
	}
	f, err := os.Open(path) // #nosec G304 - the path came from the icon theme walk
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return fitSquare(src, w, h)
}

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
			i := dst.PixOffset(offX+x, offY+y)
			dst.Pix[i] = uint8((sr / n) >> 8)
			dst.Pix[i+1] = uint8((sg / n) >> 8)
			dst.Pix[i+2] = uint8((sb / n) >> 8)
			dst.Pix[i+3] = uint8((sa / n) >> 8)
		}
	}
	return dst
}

type launcherIconPlacement struct {
	Name string
	X, Y int
}

func (m *OS) flushLauncherIconsForFrame() {
	if len(m.launcherIconCells) == 0 {
		m.flushLauncherIcons(nil)
		return
	}
	h, ok := m.overlayHitByKind("launcher")
	if !ok {
		return
	}
	out := make([]launcherIconPlacement, 0, len(m.launcherIconCells))
	for _, p := range m.launcherIconCells {
		out = append(out, launcherIconPlacement{Name: p.Name, X: h.OriginX + p.X, Y: h.OriginY + p.Y})
	}
	m.flushLauncherIcons(out)
}

func (m *OS) flushLauncherIcons(placements []launcherIconPlacement) {
	if m.launcherIcons == nil && len(placements) == 0 {
		return
	}
	st := m.launcherIconState()
	if !st.eraseHooked && m.PostRenderWriter != nil {
		st.eraseHooked = true
		m.PostRenderWriter.AddEraseHook(st.hostErased)
	}

	w, h := m.iconCellSize()

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
		at := launcherIconID{img: id, placement: uint32(i + 1)}
		want[at] = p

		was, drawn := st.shown[at]
		if drawn && was == p {
			continue
		}
		if drawn {
			buf = appendKittyUnplace(buf, at.img, at.placement)
		}
		buf = appendKittyPlace(buf, at.img, at.placement, p.X, p.Y)
	}
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

func (s *launcherIcons) hostErased(kept bool) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !kept {
		clear(s.hostID)
		clear(s.shown)
		return nil
	}
	var buf []byte
	for at, p := range s.shown {
		buf = appendKittyPlace(buf, at.img, at.placement, p.X, p.Y)
	}
	return buf
}

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

func appendKittyTransmit(buf []byte, id uint32, img *image.RGBA) []byte {
	w := &byteWriter{}
	if png.Encode(w, img) != nil {
		return buf
	}
	return appendKittyTransmitPNG(buf, id, w.b)
}

func appendKittyTransmitPNG(buf []byte, id uint32, raw []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(raw)
	for off := 0; off < len(enc); off += launcherIconChunk {
		end := min(off+launcherIconChunk, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if off == 0 {
			buf = append(buf, fmt.Sprintf("\x1b_Ga=t,f=100,i=%d,q=2,m=%d;", id, more)...)
		} else {
			buf = append(buf, fmt.Sprintf("\x1b_Gm=%d,q=2;", more)...)
		}
		buf = append(buf, enc[off:end]...)
		buf = append(buf, "\x1b\\"...)
	}
	return buf
}

func appendKittyPlace(buf []byte, id, placement uint32, x, y int) []byte {
	return appendKittyPlaceBox(buf, id, placement, x, y, launcherIconCols, launcherIconRows)
}

func appendKittyPlaceBox(buf []byte, id, placement uint32, x, y, cols, rows int) []byte {
	buf = append(buf, "\x1b7"...)
	buf = append(buf, fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)...)
	buf = append(buf, fmt.Sprintf("\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d,q=2,C=1;\x1b\\",
		id, placement, cols, rows)...)
	buf = append(buf, "\x1b8"...)
	return buf
}

func appendKittyPlaceRows(buf []byte, id, placement uint32, x, y, cols, rows, skip, allRows, pw, ph int) []byte {
	if skip == 0 && rows == allRows || allRows <= 0 || pw <= 0 || ph <= 0 {
		return appendKittyPlaceBox(buf, id, placement, x, y, cols, rows)
	}
	rowPx := ph / allRows
	buf = append(buf, "\x1b7"...)
	buf = append(buf, fmt.Sprintf("\x1b[%d;%dH", y+1, x+1)...)
	buf = append(buf, fmt.Sprintf("\x1b_Ga=p,i=%d,p=%d,c=%d,r=%d,x=0,y=%d,w=%d,h=%d,q=2,C=1;\x1b\\",
		id, placement, cols, rows, skip*rowPx, pw, rows*rowPx)...)
	buf = append(buf, "\x1b8"...)
	return buf
}

func appendKittyUnplace(buf []byte, id, placement uint32) []byte {
	return append(buf, fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,p=%d,q=2;\x1b\\", id, placement)...)
}

func wrapSync(buf []byte) []byte {
	out := make([]byte, 0, len(syncBegin)+len(buf)+len(syncEnd))
	out = append(out, syncBegin...)
	out = append(out, buf...)
	return append(out, syncEnd...)
}

type byteWriter struct{ b []byte }

func (w *byteWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func (s *launcherIcons) shotPieces() []shotPiece {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.shown) == 0 {
		return nil
	}
	keys := make(map[uint32]iconKey, len(s.hostID))
	for k, id := range s.hostID {
		keys[id] = k
	}
	var out []shotPiece
	for at, pl := range s.shown {
		k, ok := keys[at.img]
		if !ok {
			continue
		}
		pix := s.pixels[k]
		if pix == nil {
			continue
		}
		b := pix.Bounds()
		out = append(out, shotPiece{
			key:      at.img,
			declared: b.Dx() * b.Dy(),
			decode:   func(int) (image.Image, error) { return pix, nil },
			source:   func(p image.Image) image.Rectangle { return p.Bounds() },
			col:      pl.X, row: pl.Y, cols: launcherIconCols, rows: launcherIconRows,
			z: math.MaxInt32,
		})
	}
	return out
}
