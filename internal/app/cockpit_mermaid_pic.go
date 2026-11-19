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

// Mermaid pictures: a fenced Mermaid block in the agents thread or in quick
// look's Markdown drawn as a picture, where the host has kitty graphics, in
// place of its text drawing. [agents] mermaid picks the default, and the
// block's header (or T) flips one block.
//
// The picture is drawn off the loop, once per source, width, cell size and
// look, and comes back as a mermaidPicMsg. It then goes the way of an image
// card (cockpit_thread_image.go) or a picture of quick look's Markdown: it is
// one of the thread's images, so it is prepared, placed, scrolled and cropped
// the same way, and a click on it opens the image view. Until it comes, and
// when it cannot be drawn, the block keeps its text drawing.

// MermaidPicture, when set, draws a Mermaid source as a PNG at most cols cells
// wide and rows cells tall, for cells cellW by cellH pixels, in the colours of
// pal and ansi on the ground bg, and gives the cells it fills. The binary sets
// it, so the browser build links no rasteriser.
var MermaidPicture func(src string, cols, rows, cellW, cellH int, pal overlay.Palette, ansi [16]color.Color, bg color.Color) (png []byte, c, r int, err error)

// ckMermaidRows bounds the rows of a picture the thread shows. A taller
// diagram shows its top rows and a line that says how to see the rest.
const ckMermaidRows = 20

// ckMermaidDrawRows bounds the rows a picture is drawn at: a taller diagram
// is drawn smaller.
const ckMermaidDrawRows = 3 * ckMermaidRows

// mermaidPicKey is one picture: a source at a width, for a cell size and look.
type mermaidPicKey struct {
	src          string
	cols, cw, ch int
	look         uint64
	depth        overlay.Depth
}

// mermaidPic is a picture of a Mermaid block: its id among the thread's
// images, and the image once drawn, or why it was not.
type mermaidPic struct {
	id string
	ti *ckThreadImage
}

// mermaidPicMsg carries a drawn picture back to the loop.
type mermaidPicMsg struct {
	key        mermaidPicKey
	img        *composer.Image
	cols, rows int
	err        error
}

// mermaidSem lets one picture be drawn at a time, so a reply with many
// diagrams takes one core.
var mermaidSem = make(chan struct{}, 1)

// mermaidShowsPicture reports whether the Mermaid block key is drawn as a
// picture, and can whether this client can draw one at all.
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

// mermaidPicFor is the picture of src at most cols cells wide, asked for on
// first use. It is nil when no program takes the drawing back.
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

// applyMermaidPic takes a drawn picture: the blocks are laid out again, and
// the picture is prepared for the host.
func (m *OS) applyMermaidPic(msg mermaidPicMsg) tea.Cmd {
	p := m.mermaidPics[msg.key]
	if p == nil {
		return nil
	}
	p.ti.loading = false
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

// threadPicPrepare prepares a picture of the thread at its own box.
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

// ckPics is where a layout puts the pictures of its Mermaid blocks: the hit
// of a picture's rows, their indent, the images the rows name, and the rows
// as cells where the host draws them so.
type ckPics struct {
	hit    string
	indent int
	add    func(id string, ti *ckThreadImage)
	cells  func(ti *ckThreadImage) []string
}

// threadPics is the thread's ckPics: the rows are an image card's.
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

// mermaidPictureLines lays out the picture of a Mermaid block: its rows, at
// most ckMermaidRows, and a line under a taller one. It reports false while
// the picture is drawn and when it cannot be, and the block keeps its text.
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
