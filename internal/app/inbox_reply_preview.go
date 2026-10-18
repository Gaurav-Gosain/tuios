package app

import (
	"bytes"
	"crypto/sha256"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/shot/mosaic"
)

// Previews of the images in a reply (see composer/preview.go for the layout).
//
// The host decides how a picture is shown, the way it does for a pane's
// images:
//
//   - kitty graphics, when the host terminal has them and said how large a
//     cell is: the picture is uploaded once per image and size and placed
//     after the frame, as the launcher's icons are (launcher_icons.go).
//   - block glyphs from shot/mosaic otherwise, at 256 colours and
//     truecolor. They are ordinary cells, so they need nothing after the frame.
//     A sixel host gets these too: sixel cannot take a placement down, which
//     a panel that moves and closes needs, so the launcher and the screenshot
//     preview skip it for the same reason.
//   - a line of words at 16 colours.
//
// Decoding and scaling are CPU work, so they run in a tea.Cmd, once per image
// and size, and the result is kept for the life of the client, bounded by
// replyPreviewMax. Nothing runs on a timer, and with no image in an open reply
// nothing here runs at all.

// replyPreviewMax bounds the pictures kept. Each is a thumbnail or one view,
// tens of kilobytes at most.
const replyPreviewMax = 64

// replyPreviewMaxPixels bounds the images a preview decodes, by the size the
// header gives. A small file can declare a huge image, and decoding it would
// take gigabytes. A larger image is still sent; it is shown blank here.
const replyPreviewMaxPixels = 24 << 20

// replyPreviewIDBase is where the reply previews' kitty image ids start, apart
// from the launcher's (0xF000_0000) and the screenshot preview's
// (0xF100_0000).
const replyPreviewIDBase = 0xF200_0000

// previewKey is one picture: an image at a box, for a way of showing it.
type previewKey struct {
	sum        [sha256.Size]byte
	cols, rows int
	mode       composer.PreviewMode
	// For block glyphs: the colours and glyphs, and the ground a
	// transparent pixel is laid on.
	colors mosaic.Colors
	kind   mosaic.Kind
	ground [4]uint32
}

// previewPicture is a prepared picture: lines of glyphs, or PNG bytes for the
// host. A nil picture is an image that cannot be decoded here.
type previewPicture struct {
	lines []string
	png   []byte
}

// replyPreview is the client's previewer. It is reached from the Update
// goroutine and from the render; the decode commands only build pictures and
// hand them back as messages.
type replyPreview struct {
	m *OS

	mu      sync.Mutex
	version uint64
	pics    map[previewKey]*previewPicture
	order   []previewKey
	asked   map[previewKey]bool
	// hostID is the kitty image id a picture was uploaded under, and shown
	// what is placed on the host now. See inbox_reply_preview_unix.go.
	hostID map[previewKey]uint32
	nextID uint32
	shown  map[previewID]previewPlacement

	// openDir is the private folder of the copy the desktop viewer opened,
	// removed when the view closes and when the client ends.
	openDir string
}

// previewID is one placement on the host: the image, and the placement id
// within it, as kitty scopes them.
type previewID struct {
	img, placement uint32
}

// previewPlacement is one picture on the host: which, and its cell box.
type previewPlacement struct {
	key        previewKey
	x, y       int
	cols, rows int
}

// replyPreviewMsg carries prepared pictures back to the Update goroutine.
type replyPreviewMsg struct {
	pics map[previewKey]*previewPicture
}

// replyPreviewer is the OS's previewer, made on first use.
func (m *OS) replyPreviewer() *replyPreview {
	if m.replyPreview == nil {
		m.replyPreview = &replyPreview{
			m:      m,
			pics:   make(map[previewKey]*previewPicture),
			asked:  make(map[previewKey]bool),
			hostID: make(map[previewKey]uint32),
			nextID: replyPreviewIDBase,
			shown:  make(map[previewID]previewPlacement),
		}
	}
	return m.replyPreview
}

// Mode is how images are shown on this host now.
func (p *replyPreview) Mode() composer.PreviewMode {
	m := p.m
	if m.replyPixelsReady() {
		return composer.PreviewPixels
	}
	if colors, colored := symbolColors(); !colored || colors == mosaic.ANSI16 {
		return composer.PreviewText
	}
	return composer.PreviewCells
}

// Version changes when a picture arrives.
func (p *replyPreview) Version() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

// CellAspect is a host cell's height over its width, 2 when unknown.
func (p *replyPreview) CellAspect() float64 {
	if w, h := p.m.hostCellSize(); w > 0 && h > 0 {
		return float64(h) / float64(w)
	}
	return 2
}

// CanOpen reports whether an image can be opened in the desktop's viewer:
// only on the machine the person is at.
func (p *replyPreview) CanOpen() bool {
	return p.m.replyImagesOK() && desktopOpener() != ""
}

// Cells is a picture as glyph lines, nil while it is being prepared or when it
// cannot be.
func (p *replyPreview) Cells(img *composer.Image, cols, rows int) []string {
	k := p.cellsKey(img, cols, rows)
	p.mu.Lock()
	defer p.mu.Unlock()
	if pic := p.pics[k]; pic != nil {
		return pic.lines
	}
	return nil
}

// cellsKey is the key of a glyph picture as this frame would draw it.
func (p *replyPreview) cellsKey(img *composer.Image, cols, rows int) previewKey {
	colors, _ := symbolColors()
	kind := mosaic.Quadrant
	if sp := p.m.SixelPassthrough; sp != nil && sp.symbols != mosaic.Off {
		kind = sp.symbols
	}
	return previewKey{sum: img.Sum, cols: cols, rows: rows, mode: composer.PreviewCells, colors: colors, kind: kind, ground: rgbaKey(theme.UI().Card)}
}

// pixelsKey is the key of a picture the host draws.
func pixelsKey(img *composer.Image, cols, rows int) previewKey {
	return previewKey{sum: img.Sum, cols: cols, rows: rows, mode: composer.PreviewPixels}
}

// rgbaKey is c's channels.
func rgbaKey(c color.Color) [4]uint32 {
	if c == nil {
		return [4]uint32{}
	}
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

// replyPreviewCmd prepares the pictures the open draft shows that are not
// ready. It is called after a key, a paste or an image reaches the draft, and
// it is nil, at the cost of a loop over at most five boxes, when they are all
// ready.
func (m *OS) replyPreviewCmd() tea.Cmd {
	d, ok := m.openDraft()
	if !ok {
		return nil
	}
	boxes := d.comp.PreviewBoxes()
	if len(boxes) == 0 {
		return nil
	}
	p := m.replyPreviewer()
	mode := p.Mode()
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	type job struct {
		key previewKey
		img *composer.Image
	}
	var jobs []job
	p.mu.Lock()
	for _, b := range boxes {
		k := pixelsKey(b.Image, b.Cols, b.Rows)
		if mode == composer.PreviewCells {
			p.mu.Unlock()
			k = p.cellsKey(b.Image, b.Cols, b.Rows)
			p.mu.Lock()
		}
		if p.asked[k] {
			continue
		}
		p.asked[k] = true
		jobs = append(jobs, job{key: k, img: b.Image})
	}
	p.mu.Unlock()
	if len(jobs) == 0 {
		return nil
	}
	ground := theme.UI().Card
	return func() tea.Msg {
		out := make(map[previewKey]*previewPicture, len(jobs))
		for _, j := range jobs {
			out[j.key] = preparePicture(j.img, j.key, cw, ch, ground)
		}
		return replyPreviewMsg{pics: out}
	}
}

// applyReplyPreview files prepared pictures away and has the next frame draw
// them.
func (m *OS) applyReplyPreview(msg replyPreviewMsg) {
	p := m.replyPreviewer()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, pic := range msg.pics {
		if _, ok := p.pics[k]; !ok {
			p.order = append(p.order, k)
		}
		p.pics[k] = pic
	}
	for len(p.order) > replyPreviewMax {
		old := p.order[0]
		p.order = p.order[1:]
		delete(p.pics, old)
		delete(p.asked, old)
	}
	p.version++
}

// preparePicture decodes img and draws it for k: as glyph lines, or as PNG
// bytes of the box's size in pixels. It returns nil for an image this build
// cannot decode, such as WebP, which is then shown blank.
func preparePicture(img *composer.Image, k previewKey, cellW, cellH int, ground color.Color) *previewPicture {
	if img.Width <= 0 || img.Height <= 0 || img.Width > replyPreviewMaxPixels/img.Height {
		return nil
	}
	src, _, err := image.Decode(bytes.NewReader(img.Data))
	if err != nil {
		return nil
	}
	if k.mode == composer.PreviewPixels {
		dst := boxScale(src, k.cols*cellW, k.rows*cellH)
		var b bytes.Buffer
		if png.Encode(&b, dst) != nil {
			return nil
		}
		return &previewPicture{png: b.Bytes()}
	}
	// Four by eight pixels a cell is enough for every glyph set, octants
	// included, and keeps the palette small.
	const pw, ph = 4, 8
	dst := image.NewRGBA(image.Rect(0, 0, k.cols*pw, k.rows*ph))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(ground), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), boxScale(src, k.cols*pw, k.rows*ph), image.Point{}, draw.Over)
	cells := mosaic.Encode(indexed(dst), pw, ph, k.rows, k.cols, k.kind, k.colors)
	if cells == nil {
		return nil
	}
	return &previewPicture{lines: glyphLines(cells, k.cols)}
}

// boxScale scales src to w by h pixels, each the average of the source
// pixels under it, so a large picture made small keeps its edges smooth. It
// reads every source pixel once. The launcher's icons are scaled the same way.
func boxScale(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return dst
	}
	for y := range dst.Bounds().Dy() {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		y1 = max(y1, y0+1)
		for x := range dst.Bounds().Dx() {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			x1 = max(x1, x0+1)
			var sr, sg, sb, sa, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					r, g, bl, a := src.At(xx, yy).RGBA()
					sr, sg, sb, sa, n = sr+uint64(r), sg+uint64(g), sb+uint64(bl), sa+uint64(a), n+1
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8((sr / n) >> 8)
			dst.Pix[i+1] = uint8((sg / n) >> 8)
			dst.Pix[i+2] = uint8((sb / n) >> 8)
			dst.Pix[i+3] = uint8((sa / n) >> 8)
		}
	}
	return dst
}

// indexed is an opaque picture in the paletted form mosaic takes.
func indexed(img *image.RGBA) mosaic.Indexed {
	b := img.Bounds()
	out := mosaic.Indexed{Width: b.Dx(), Height: b.Dy(), Pix: make([]uint16, b.Dx()*b.Dy())}
	seen := make(map[color.RGBA]uint16)
	for y := range b.Dy() {
		for x := range b.Dx() {
			i := img.PixOffset(x, y)
			c := color.RGBA{img.Pix[i], img.Pix[i+1], img.Pix[i+2], 255}
			n, ok := seen[c]
			if !ok {
				out.Palette = append(out.Palette, c)
				n = uint16(len(out.Palette))
				seen[c] = n
			}
			out.Pix[y*b.Dx()+x] = n
		}
	}
	return out
}

// glyphLines writes cells as lines of styled text, cols cells a line. The
// colours are the picture's own; the frame's colour profile brings them down
// to what the host shows, as it does for a pane's glyph images.
func glyphLines(cells []mosaic.Cell, cols int) []string {
	lines := make([]string, 0, len(cells)/cols)
	var b strings.Builder
	for i, c := range cells {
		if i%cols == 0 && i > 0 {
			b.WriteString("\x1b[m")
			lines = append(lines, b.String())
			b.Reset()
		}
		b.WriteString("\x1b[38;2;")
		writeRGB(&b, c.Fg)
		b.WriteString(";48;2;")
		writeRGB(&b, c.Bg)
		b.WriteByte('m')
		b.WriteString(c.Glyph)
	}
	b.WriteString("\x1b[m")
	return append(lines, b.String())
}

// writeRGB writes c as the r;g;b of an SGR colour.
func writeRGB(b *strings.Builder, c color.RGBA) {
	b.WriteString(strconv.Itoa(int(c.R)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.G)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.B)))
}

// desktopOpener is the program that opens a file in the desktop's viewer, ""
// for none.
func desktopOpener() string {
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "linux", "freebsd", "openbsd", "netbsd":
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return "xdg-open"
		}
	}
	return ""
}

// replyOpenImage opens the image the reply's view shows in the desktop's
// viewer, from a copy in a private folder that goes when the view closes.
func (m *OS) replyOpenImage(img *composer.Image) tea.Cmd {
	p := m.replyPreviewer()
	opener := desktopOpener()
	if img == nil || opener == "" || !m.replyImagesOK() {
		return nil
	}
	p.removeOpened()
	dir, err := os.MkdirTemp("", "tuios-image-")
	if err != nil {
		m.ShowNotification("tuios cannot save a copy to open: "+err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	path := filepath.Join(dir, "image."+img.Format)
	if err := os.WriteFile(path, img.Data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		m.ShowNotification("tuios cannot save a copy to open: "+err.Error(), "error", m.Settings.NotificationDuration)
		return nil
	}
	p.openDir = dir
	return func() tea.Msg {
		cmd := exec.Command(opener, path)
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
		return nil
	}
}

// removeOpened removes the copy the desktop viewer was given, if any.
func (p *replyPreview) removeOpened() {
	if p == nil || p.openDir == "" {
		return
	}
	_ = os.RemoveAll(p.openDir)
	p.openDir = ""
}

// replyPreviewClosed is called when a draft editor or its image view
// closes: the copy for the desktop viewer goes.
func (m *OS) replyPreviewClosed() {
	if m.replyPreview != nil {
		d, ok := m.openDraft()
		if !ok || d.comp.OpenedImage() == nil {
			m.replyPreview.removeOpened()
		}
	}
}

// shotPieces is the previews the host shows now, for a screen capture. Each
// picture is already the size of its cell box, and it is drawn over the
// cells, as the host draws it. A nil previewer has none.
func (p *replyPreview) shotPieces() []shotPiece {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []shotPiece
	for _, pl := range p.shown {
		pic := p.pics[pl.key]
		if pic == nil || len(pic.png) == 0 {
			continue
		}
		data := pic.png
		out = append(out, shotPiece{
			key: pl.key,
			decode: func(maxPixels int) (image.Image, error) {
				cfg, err := png.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					return nil, err
				}
				if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPixels/cfg.Height {
					return nil, errImageTooLarge
				}
				return png.Decode(bytes.NewReader(data))
			},
			source: func(pix image.Image) image.Rectangle { return pix.Bounds() },
			col:    pl.x, row: pl.y, cols: pl.cols, rows: pl.rows,
			z: math.MaxInt32,
		})
	}
	return out
}
