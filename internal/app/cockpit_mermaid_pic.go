package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/color"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// MermaidPicture, when set, draws a Mermaid source as a PNG at most cols cells
// wide and rows cells tall, for cells cellW by cellH pixels, in the colours of
// pal and ansi on the ground bg, and gives the cells it fills. The binary sets
// it, so the browser build links no rasteriser.
var MermaidPicture func(src string, cols, rows, cellW, cellH int, pal overlay.Palette, ansi [16]color.Color, bg color.Color) (png []byte, c, r int, err error)

const ckMermaidRows = 20

const ckMermaidDrawRows = 3 * ckMermaidRows

type mermaidPicKey struct {
	src          string
	cols, cw, ch int
	look         uint64
	depth        overlay.Depth
}

type mermaidPic struct {
	id string
	ti *ckThreadImage
}

type mermaidPicMsg struct {
	key        mermaidPicKey
	img        *composer.Image
	cols, rows int
	err        error
}

var mermaidSem = make(chan struct{}, 1)

func (m *OS) mermaidShowsPicture(key string) (pic, can bool) {
	mode := m.replyPreviewer().Mode()
	if MermaidPicture == nil || mode == composer.PreviewText {
		return false, false
	}
	def := false
	setting := config.AgentsMermaidAuto
	if m.UserConfig != nil {
		setting = m.UserConfig.Agents.MermaidMode()
	}
	switch setting {
	case config.AgentsMermaidPicture:
		def = true
	case config.AgentsMermaidAuto:
		def = mode == composer.PreviewPixels
	}
	return def != m.richPicture[key], true
}

// ToggleBlockPicture draws the Mermaid block key as a picture, or as text.
func (m *OS) ToggleBlockPicture(key string) {
	if m.richPicture == nil {
		m.richPicture = make(map[string]bool)
	}
	m.richPicture[key] = !m.richPicture[key]
	m.blockView++
	m.blocksChanged()
}

func (m *OS) mermaidPicFor(src string, cols int, pal overlay.Palette) *mermaidPic {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	k := mermaidPicKey{src: src, cols: cols, cw: cw, ch: ch, look: theme.Generation(), depth: pal.Depth}
	if p := m.mermaidPics[k]; p != nil {
		return p
	}
	prog := m.frameRate.program
	if prog == nil || MermaidPicture == nil {
		return nil
	}
	if m.mermaidPics == nil || len(m.mermaidPics) >= ckBlocksMax {
		m.mermaidPics = make(map[mermaidPicKey]*mermaidPic)
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d\x00%d\x00%d\x00%d\x00%d", src, cols, cw, ch, k.look, k.depth))
	p := &mermaidPic{id: "mmd" + hex.EncodeToString(sum[:12]), ti: &ckThreadImage{loading: true}}
	m.mermaidPics[k] = p
	ansi, bg := theme.GetANSIPalette(), pal.Canvas
	go func() {
		mermaidSem <- struct{}{}
		msg := mermaidPicMsg{key: k}
		func() {
			defer func() {
				if r := recover(); r != nil {
					msg.err = errfmt.Errorf("the diagram could not be drawn: %v", r)
				}
			}()
			data, c, r, err := MermaidPicture(src, cols, ckMermaidDrawRows, cw, ch, pal, ansi, bg)
			if err != nil {
				msg.err = err
				return
			}
			msg.img, msg.err = composer.NewImage(data)
			msg.cols, msg.rows = c, r
		}()
		<-mermaidSem
		prog.Send(msg)
	}()
	return p
}

func (m *OS) applyMermaidPic(msg mermaidPicMsg) tea.Cmd {
	p := m.mermaidPics[msg.key]
	if p == nil {
		return nil
	}
	p.ti.loading = false
	if msg.err != nil && needsSketchPlugin(msg.key.src) {
		sketchFailed.Store(true)
	}
	switch {
	case errors.Is(msg.err, composer.ErrImageTooLarge):
		p.ti.err = "The picture is larger than 8 MB."
	case msg.err != nil || msg.img == nil:
		p.ti.err = "The diagram could not be drawn as a picture."
	default:
		p.ti.img, p.ti.cols, p.ti.rows = msg.img, msg.cols, msg.rows
	}
	m.blockView++
	m.blocksChanged()
	if p.ti.img == nil {
		return nil
	}
	if m.quickLook.open {
		m.quickLook.mdNeed = true
	}
	return tea.Batch(m.threadPicPrepare(p.ti), m.quickLookMDImagesCmd())
}

func (m *OS) threadPicPrepare(ti *ckThreadImage) tea.Cmd {
	p := m.replyPreviewer()
	mode := p.Mode()
	if mode == composer.PreviewText || ti.img == nil {
		return nil
	}
	k := pixelsKey(ti.img, ti.cols, ti.rows)
	if mode == composer.PreviewCells {
		k = p.cellsKey(ti.img, ti.cols, ti.rows)
	}
	p.mu.Lock()
	asked := p.asked[k]
	p.asked[k] = true
	p.mu.Unlock()
	if asked {
		return nil
	}
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	img, ground := ti.img, m.cockpitPalette().Card
	return func() tea.Msg {
		return replyPreviewMsg{pics: map[previewKey]*previewPicture{k: preparePicture(img, k, cw, ch, ground)}}
	}
}

type ckPics struct {
	hit    string
	indent int
	add    func(id string, ti *ckThreadImage)
	cells  func(ti *ckThreadImage) []string
}

func (m *OS) threadPics(t *agentThread) *ckPics {
	return &ckPics{hit: "timg:", indent: ckImgIndent,
		add: func(id string, ti *ckThreadImage) {
			if t.images == nil {
				t.images = make(map[string]*ckThreadImage)
			}
			t.images[id] = ti
		},
		cells: func(ti *ckThreadImage) []string {
			p := m.replyPreviewer()
			switch p.Mode() {
			case composer.PreviewPixels:
				return m.placeholderLines(ti.img, ti.cols, ti.rows)
			case composer.PreviewCells:
				cells := p.Cells(ti.img, ti.cols, ti.rows)
				if cells == nil {
					m.mermaidCellsWait = true
				}
				return cells
			}
			return nil
		}}
}

func (md *ckMD) mermaidPictureLines(src string, avail int) bool {
	m, pal := md.m, md.l.pal
	pics := md.pics
	if pics == nil {
		return false
	}
	p := m.mermaidPicFor(src, max(avail-pics.indent-1, 8), pal)
	if p == nil || p.ti.img == nil {
		return false
	}
	ti := p.ti
	pics.add(p.id, ti)
	cells := pics.cells(ti)
	show := min(ti.rows, ckMermaidRows)
	for r := range show {
		ln := ckLine{indent: pics.indent, hit: pics.hit + strconv.Itoa(r) + "/" + strconv.Itoa(ti.rows) + "/" + strconv.Itoa(ti.cols) + "/" + p.id}
		if r < len(cells) {
			ln.raw = cells[r]
		}
		md.lines = append(md.lines, ln)
	}
	if show < ti.rows {
		more := "Showing " + strconv.Itoa(show) + " of " + strconv.Itoa(ti.rows) + " rows."
		if pics.hit == "timg:" {
			more += " Click the picture to see all of it."
		}
		md.lines = append(md.lines, ckLine{indent: pics.indent, segs: []ckSeg{{t: more, fg: pal.FgMute}}})
	}
	return true
}
