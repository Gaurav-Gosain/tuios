package app

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/charmbracelet/x/ansi/kitty"
)

// Image cards as kitty Unicode placeholders.
//
// On a host that draws them, a thread's image card is drawn as cells of
// U+10EEEE that name the picture, and the host draws the part of the picture
// that belongs to each cell. The cells are text: they scroll, clip and move
// with the thread in the same frame, so a card the window cuts shows the rows
// in the window, and a scroll never shows the picture a frame behind the
// text. The picture goes to the host once, with one virtual placement that
// gives its cell box. Other kitty hosts get a placement cropped to the rows
// on screen (threadImagePlacements), and the rest get block glyphs.
//
// The cells name the picture's host id when the frame is drawn, so the id is
// made then, before the picture is uploaded: a cell that names an id the host
// does not hold yet draws nothing, and the picture shows when its upload
// lands.

// placeholderPic is a picture drawn as placeholder cells: its host id and
// what the host holds of it.
type placeholderPic struct {
	id uint32
	// up says the host holds the picture, placed that it holds the virtual
	// placement.
	up, placed bool
}

// placeholderVirtual is the placement id of a placeholder picture's virtual
// placement.
const placeholderVirtual = 1

// threadPlaceholders reports whether image cards are drawn as placeholder
// cells: a kitty host that draws them, by appearance.kitty_placeholders.
func (m *OS) threadPlaceholders() bool {
	return m.replyPixelsReady() && m.placeholdersEnabled()
}

// placeholderID is the host id of the picture of k drawn as placeholders,
// made on first use.
func (p *replyPreview) placeholderID(k previewKey) uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ph == nil {
		p.ph = make(map[previewKey]*placeholderPic)
	}
	if ph := p.ph[k]; ph != nil {
		return ph.id
	}
	id := p.nextID
	p.nextID++
	p.ph[k] = &placeholderPic{id: id}
	return id
}

// placeholderLine is row of a picture cols cells wide as placeholder cells
// that name image id: the low 24 bits of the id in the foreground colour, the
// row and the column in the first two marks, and the top 8 bits in a third.
func placeholderLine(id uint32, row, cols int) string {
	var b strings.Builder
	b.WriteString("\x1b[38;2;")
	b.WriteString(strconv.Itoa(int(id >> 16 & 0xff)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(id >> 8 & 0xff)))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(id & 0xff)))
	b.WriteByte('m')
	hi := id >> 24
	for c := range cols {
		b.WriteRune(kitty.Placeholder)
		b.WriteRune(kitty.Diacritic(row))
		b.WriteRune(kitty.Diacritic(c))
		if hi != 0 {
			b.WriteRune(kitty.Diacritic(int(hi)))
		}
	}
	b.WriteString("\x1b[39m")
	return b.String()
}

// placeholderShotPieces are the pictures of a screenshot's placeholder cells
// that name a picture drawn here, so a shot of the thread shows them as the
// host does.
func (p *replyPreview) placeholderShotPieces(rects []shotHitRect) []shotPiece {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []shotPiece
	for _, r := range rects {
		for k, ph := range p.ph {
			if ph.id != r.key.id {
				continue
			}
			pic := p.pics[k]
			if pic == nil || len(pic.png) == 0 {
				break
			}
			data := pic.png
			out = append(out, shotPiece{
				key:    k,
				decode: func(int) (image.Image, error) { return png.Decode(bytes.NewReader(data)) },
				source: func(pix image.Image) image.Rectangle { return pix.Bounds() },
				col:    r.x - r.c, row: r.y - r.r, cols: k.cols, rows: k.rows,
				sub: image.Rect(r.c, r.r, r.c+r.w, r.r+r.h),
				z:   math.MaxInt32,
			})
			break
		}
	}
	return out
}

// placeholderLines are the rows of an image card's picture as placeholder
// cells, nil when cards are not drawn so.
func (m *OS) placeholderLines(img *composer.Image, cols, rows int) []string {
	if !m.threadPlaceholders() {
		return nil
	}
	id := m.replyPreviewer().placeholderID(pixelsKey(img, cols, rows))
	out := make([]string, rows)
	for r := range rows {
		out[r] = placeholderLine(id, r, cols)
	}
	return out
}
