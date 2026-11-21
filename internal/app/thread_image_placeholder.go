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

type placeholderPic struct {
	id         uint32
	up, placed bool
}

const placeholderVirtual = 1

func (m *OS) threadPlaceholders() bool {
	return m.replyPixelsReady() && m.placeholdersEnabled()
}

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
