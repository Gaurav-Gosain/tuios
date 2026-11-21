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
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/shot"
	"tuios.dev/shot/mosaic"
)

const replyPreviewMax = 64

const replyPreviewBytes = 32 << 20

const replyPreviewMaxPixels = 24 << 20

const replyPreviewIDBase = 0xF200_0000

type previewKey struct {
	sum        [sha256.Size]byte
	cols, rows int
	mode       composer.PreviewMode
	colors     mosaic.Colors
	kind       mosaic.Kind
	ground     [4]uint32
}

type previewPicture struct {
	lines []string
	png   []byte
	w, h  int
}

func (pic *previewPicture) size() int {
	if pic == nil {
		return 0
	}
	n := cap(pic.png)
	for _, l := range pic.lines {
		n += len(l)
	}
	return n
}

type replyPreview struct {
	m *OS

	mu          sync.Mutex
	version     uint64
	pics        map[previewKey]*previewPicture
	order       []previewKey
	held        int
	asked       map[previewKey]bool
	hostID      map[previewKey]uint32
	hostOrder   []previewKey
	gone        []uint32
	nextID      uint32
	shown       map[previewID]previewPlacement
	ph          map[previewKey]*placeholderPic
	eraseHooked bool

	openDir string
}

type previewID struct {
	img, placement uint32
}

type previewPlacement struct {
	key        previewKey
	x, y       int
	cols, rows int
	skip       int
	virtual    bool
}

type replyPreviewMsg struct {
	pics map[previewKey]*previewPicture
}

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

func (p *replyPreview) Version() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

func (p *replyPreview) CellAspect() float64 {
	if w, h := p.m.hostCellSize(); w > 0 && h > 0 {
		return float64(h) / float64(w)
	}
	return 2
}

func (p *replyPreview) CanOpen() bool {
	return p.m.replyImagesOK() && desktopOpener() != ""
}

func (p *replyPreview) Cells(img *composer.Image, cols, rows int) []string {
	k := p.cellsKey(img, cols, rows)
	p.mu.Lock()
	defer p.mu.Unlock()
	if pic := p.pics[k]; pic != nil {
		return pic.lines
	}
	return nil
}

func (p *replyPreview) cellsKey(img *composer.Image, cols, rows int) previewKey {
	colors, _ := symbolColors()
	kind := mosaic.Quadrant
	if sp := p.m.SixelPassthrough; sp != nil && sp.symbols != mosaic.Off {
		kind = sp.symbols
	}
	return previewKey{sum: img.Sum, cols: cols, rows: rows, mode: composer.PreviewCells, colors: colors, kind: kind, ground: rgbaKey(theme.UI().Card)}
}

func pixelsKey(img *composer.Image, cols, rows int) previewKey {
	return previewKey{sum: img.Sum, cols: cols, rows: rows, mode: composer.PreviewPixels}
}

func rgbaKey(c color.Color) [4]uint32 {
	if c == nil {
		return [4]uint32{}
	}
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

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

func (m *OS) applyReplyPreview(msg replyPreviewMsg) {
	p := m.replyPreviewer()
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, pic := range msg.pics {
		if old, ok := p.pics[k]; ok {
			p.held -= old.size()
		} else {
			p.order = append(p.order, k)
		}
		p.pics[k] = pic
		p.held += pic.size()
	}
	if len(p.order) <= replyPreviewMax && p.held <= replyPreviewBytes {
		p.version++
		return
	}
	onScreen := make(map[previewKey]bool, len(msg.pics)+len(p.shown))
	for k := range msg.pics {
		onScreen[k] = true
	}
	for _, pl := range p.shown {
		onScreen[pl.key] = true
	}
	count := len(p.order)
	kept := p.order[:0]
	for _, old := range p.order {
		if (count <= replyPreviewMax && p.held <= replyPreviewBytes) || onScreen[old] {
			kept = append(kept, old)
			continue
		}
		count--
		p.held -= p.pics[old].size()
		delete(p.pics, old)
		delete(p.asked, old)
		if id, up := p.hostID[old]; up {
			delete(p.hostID, old)
			p.hostOrder = slices.DeleteFunc(p.hostOrder, func(k previewKey) bool { return k == old })
			p.gone = append(p.gone, id)
		}
	}
	clear(p.order[len(kept):])
	p.order = kept
	p.version++
}

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
		out := make([]byte, b.Len())
		copy(out, b.Bytes())
		return &previewPicture{png: out, w: dst.Bounds().Dx(), h: dst.Bounds().Dy()}
	}
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

func boxScale(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return dst
	}
	if w >= b.Dx() && h >= b.Dy() {
		shot.ScaleBilinear(dst, dst.Bounds(), src, b, false)
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

func writeRGB(b *strings.Builder, c color.RGBA) {
	b.WriteString(strconv.Itoa(int(c.R)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.G)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c.B)))
}

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

func (p *replyPreview) removeOpened() {
	if p == nil || p.openDir == "" {
		return
	}
	_ = os.RemoveAll(p.openDir)
	p.openDir = ""
}

func (m *OS) replyPreviewClosed() {
	if m.replyPreview != nil {
		d, ok := m.openDraft()
		if !ok || d.comp.OpenedImage() == nil {
			m.replyPreview.removeOpened()
		}
	}
}

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
			col:    pl.x, row: pl.y - pl.skip, cols: pl.cols, rows: max(pl.key.rows, pl.skip+pl.rows),
			sub: image.Rect(0, pl.skip, pl.cols, pl.skip+pl.rows),
			z:   math.MaxInt32,
		})
	}
	return out
}
