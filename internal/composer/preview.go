package composer

import (
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ThumbRows is how many rows a thumbnail is high.
const ThumbRows = 3

const maxThumbs = 4

const (
	thumbMinCols = 3
	thumbMaxCols = 24
)

// PreviewMode is how the host shows an image.
type PreviewMode uint8

const (
	// PreviewText shows a line of words for each image.
	PreviewText PreviewMode = iota
	// PreviewCells shows each image as lines of block glyphs.
	PreviewCells
	// PreviewPixels shows each image as a picture the host draws after the
	// frame, in cells the composer leaves blank.
	PreviewPixels
)

// Previewer is the host's side of the previews.
type Previewer interface {
	// Mode is how images are shown now.
	Mode() PreviewMode
	// Cells is img drawn in cols by rows cells, one string a row, for
	// PreviewCells. Nil while it is not ready: the box is left blank.
	Cells(img *Image, cols, rows int) []string
	// Version changes whenever Mode or what Cells returns changes.
	Version() uint64
	// CellAspect is a cell's height over its width, in pixels.
	CellAspect() float64
	// CanOpen reports whether the host can open an image in the desktop's
	// image viewer.
	CanOpen() bool
}

// Placement is one picture in the last frame: the image, and its box in
// cells, Row and Col counted from the first line and column Render returned.
type Placement struct {
	Image      *Image
	Row, Col   int
	Cols, Rows int
}

// Box is one picture the host should have ready: an image at a size.
type Box struct {
	Image      *Image
	Cols, Rows int
}

// SetPreviewer gives the composer the host's previewer. Nil shows images as
// text.
func (m *Model) SetPreviewer(p Previewer) { m.preview = p }

// Placements is where the last frame put pictures, for PreviewPixels.
func (m *Model) Placements() []Placement { return m.places }

func (m *Model) previewMode() PreviewMode {
	if m.preview == nil {
		return PreviewText
	}
	return m.preview.Mode()
}

func (m *Model) cellAspect() float64 {
	if m.preview != nil {
		if a := m.preview.CellAspect(); a > 0 {
			return a
		}
	}
	return 2
}

// ThumbBox is the box a thumbnail of img takes, for a cell aspect (cell
// height over width): ThumbRows high, as wide as the image's shape.
func ThumbBox(img *Image, aspect float64) (cols, rows int) {
	return fitBox(img, aspect, thumbMaxCols, ThumbRows, true)
}

func fitBox(img *Image, aspect float64, maxCols, maxRows int, fixedRows bool) (cols, rows int) {
	if img.Width <= 0 || img.Height <= 0 || aspect <= 0 {
		return min(maxRows*2, maxCols), maxRows
	}
	ratio := float64(img.Width) / float64(img.Height) * aspect
	rows = maxRows
	cols = int(math.Round(float64(rows) * ratio))
	if !fixedRows && cols > maxCols {
		cols = maxCols
		rows = max(1, int(math.Round(float64(cols)/ratio)))
	}
	return min(max(cols, thumbMinCols), maxCols), rows
}

// PreviewBoxes are the pictures the draft shows now: a thumbnail of each image
// the strip holds, and the open image's view. The host prepares those that
// are not ready.
func (m *Model) PreviewBoxes() []Box {
	if m.previewMode() == PreviewText {
		return nil
	}
	aspect := m.cellAspect()
	var out []Box
	for _, c := range m.cells {
		if c.block == nil || c.block.img == nil {
			continue
		}
		if len(out) == maxThumbs {
			break
		}
		cols, rows := ThumbBox(c.block.img, aspect)
		out = append(out, Box{Image: c.block.img, Cols: cols, Rows: rows})
	}
	if p := m.pager; p != nil && p.block.img != nil && p.viewCols > 0 {
		out = append(out, Box{Image: p.block.img, Cols: p.viewCols, Rows: p.viewRows})
	}
	return out
}

func (m *Model) renderStrip(width int, in inks) []string {
	var imgs []*block
	for _, c := range m.cells {
		if c.block != nil && c.block.img != nil {
			imgs = append(imgs, c.block)
		}
	}
	if len(imgs) == 0 {
		return nil
	}
	mode := m.previewMode()
	dim := on(in.surface).Foreground(in.st.readable(in.st.Dim, in.surface))
	pad := on(in.surface).Render(strings.Repeat(" ", gutter))
	if mode == PreviewText {
		out := make([]string, 0, min(len(imgs), maxThumbs)+1)
		for i, b := range imgs {
			if i == maxThumbs {
				out = append(out, fill(pad+dim.Render("+"+strconv.Itoa(len(imgs)-maxThumbs)+" more"), width, in.surface))
				break
			}
			line := "Image #" + strconv.Itoa(b.n) + " · " + b.img.measureWords() + " · " + byteSize(int64(len(b.img.Data)))
			out = append(out, fill(pad+dim.Render(ansi.Truncate(line, width-gutter, "…")), width, in.surface))
		}
		return out
	}

	aspect := m.cellAspect()
	rows := make([]strings.Builder, ThumbRows+1)
	for r := range rows {
		rows[r].WriteString(pad)
	}
	col := gutter
	shown := 0
	for _, b := range imgs {
		cols, h := ThumbBox(b.img, aspect)
		if shown == maxThumbs || col+cols > width {
			break
		}
		if shown > 0 {
			for r := range rows {
				rows[r].WriteString(on(in.surface).Render(" "))
			}
			col++
		}
		var cells []string
		if mode == PreviewCells {
			cells = m.preview.Cells(b.img, cols, h)
		}
		blank := on(in.field).Render(strings.Repeat(" ", cols))
		for r := range ThumbRows {
			if r < len(cells) {
				rows[r].WriteString(cells[r])
			} else {
				rows[r].WriteString(blank)
			}
		}
		label := ansi.Truncate("#"+strconv.Itoa(b.n), cols, "")
		rows[ThumbRows].WriteString(dim.Render(label + strings.Repeat(" ", cols-ansi.StringWidth(label))))
		if mode == PreviewPixels {
			m.places = append(m.places, Placement{Image: b.img, Row: 0, Col: col, Cols: cols, Rows: h})
		}
		col += cols
		shown++
	}
	if more := len(imgs) - shown; more > 0 {
		rows[1].WriteString(dim.Render(" +" + strconv.Itoa(more) + " more"))
	}
	out := make([]string, len(rows))
	for r := range rows {
		out[r] = fill(rows[r].String(), width, in.surface)
	}
	return out
}

func (m *Model) renderImageView(width int, in inks) []string {
	p := m.pager
	img := p.block.img
	head := on(in.surface).Foreground(in.st.readable(in.st.Text, in.surface)).Bold(true).
		Render("Image #" + strconv.Itoa(p.block.n))
	head += on(in.surface).Foreground(in.st.readable(in.st.Dim, in.surface)).
		Render(" · " + img.measureWords() + " · " + byteSize(int64(len(img.Data))))
	out := []string{fill(ansi.Truncate(head, width, "…"), width, in.surface)}
	dim := on(in.surface).Foreground(in.st.readable(in.st.Dim, in.surface))
	pad := on(in.surface).Render(strings.Repeat(" ", gutter))
	mode := m.previewMode()
	if mode == PreviewText {
		p.viewCols, p.viewRows = 0, 0
		line := "This terminal cannot show images."
		if m.preview != nil && m.preview.CanOpen() {
			line += " Press o to open the image."
		}
		for _, l := range wrapWords(line, width-gutter) {
			out = append(out, fill(pad+dim.Render(l), width, in.surface))
		}
		return out
	}
	cols, rows := fitBox(img, m.cellAspect(), width-gutter, m.pagerRows(), false)
	p.viewCols, p.viewRows = cols, rows
	var cells []string
	if mode == PreviewCells {
		cells = m.preview.Cells(img, cols, rows)
	}
	blank := on(in.field).Render(strings.Repeat(" ", cols))
	for r := range rows {
		line := blank
		if r < len(cells) {
			line = cells[r]
		}
		out = append(out, fill(pad+line, width, in.surface))
	}
	if mode == PreviewPixels {
		m.places = append(m.places, Placement{Image: img, Row: 1, Col: gutter, Cols: cols, Rows: rows})
	}
	return out
}

// OpenedImage is the image the view shows, nil when none is open.
func (m *Model) OpenedImage() *Image {
	if m.pager != nil && m.pager.block.img != nil {
		return m.pager.block.img
	}
	return nil
}

func byteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	prec := 2
	switch {
	case v >= 100:
		prec = 0
	case v >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if prec > 0 {
		s = strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
	}
	return s + " " + "KMGTP"[exp:exp+1] + "iB"
}
