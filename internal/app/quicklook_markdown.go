package app

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const (
	// quickLookMermaidMax bounds the diagrams drawn in one document, and the source
	// of each.
	quickLookMermaidMax    = 8
	quickLookMermaidBytes  = 16 << 10
	quickLookMermaidCols   = 100
	quickLookMermaidBudget = 2 * time.Second
)

// quickLookDrawMermaid turns the Mermaid fences of a document into plain fences that
// hold the diagram in text. It runs off the loop, in the read. A diagram that fails or
// takes too long stays as its source.
func quickLookDrawMermaid(md string, ascii bool) string {
	if !strings.Contains(md, "```mermaid") {
		return md
	}
	var b strings.Builder
	drawn := 0
	for {
		start := strings.Index(md, "```mermaid\n")
		if start < 0 || (start > 0 && md[start-1] != '\n') {
			break
		}
		end := strings.Index(md[start+11:], "\n```")
		if end < 0 {
			break
		}
		src := md[start+11 : start+11+end]
		b.WriteString(md[:start])
		lines, ok := quickLookMermaidText(src, drawn)
		for i, l := range lines {
			lines[i] = quickLookMermaidGlyphs(l, ascii)
		}
		if ok {
			drawn++
			b.WriteString("```\n")
			b.WriteString(strings.Join(lines, "\n"))
		} else {
			b.WriteString("```text\n")
			b.WriteString(src)
		}
		md = md[start+11+end+1:]
		b.WriteString("\n")
	}
	b.WriteString(md)
	return b.String()
}

// quickLookMermaidGlyphs swaps each glyph the code block would drop for one it keeps,
// so the columns of the drawing stay lined up. The code block drops the geometric
// shapes, which some terminals draw as emoji: the arrow heads are among them. At ASCII
// the box lines go too.
func quickLookMermaidGlyphs(line string, ascii bool) string {
	return strings.Map(func(r rune) rune {
		if printableRune(r, ascii) {
			return r
		}
		switch r {
		case '►', '▶', '▸':
			return '>'
		case '◄', '◀', '◂':
			return '<'
		case '▲', '▴':
			return '^'
		case '▼', '▾':
			return 'v'
		case '─', '━', '┄', '┈', '╌':
			return '-'
		case '│', '┃', '┆', '┊', '╎':
			return '|'
		}
		if r >= 0x2500 && r <= 0x257f {
			return '+'
		}
		return '*'
	}, line)
}

// quickLookMermaidText draws one diagram as text with the sketch plugin, under
// the time limit. The renderer stays out of this binary: without the plugin a
// diagram stays as its source.
func quickLookMermaidText(src string, drawn int) ([]string, bool) {
	if drawn >= quickLookMermaidMax || len(src) > quickLookMermaidBytes {
		return nil, false
	}
	done := make(chan []string, 1)
	go func() {
		cells, _, err := drawWithSketchPlugin(src, quickLookMermaidCols)
		if err != nil {
			done <- nil
			return
		}
		lines := make([]string, len(cells))
		var b strings.Builder
		for i, row := range cells {
			b.Reset()
			for _, c := range row {
				// 0 is the cell a wide character covers.
				if c.Rune != 0 {
					b.WriteRune(c.Rune)
				}
			}
			lines[i] = strings.TrimRight(b.String(), " ")
		}
		done <- lines
	}()
	select {
	case lines := <-done:
		return lines, lines != nil
	case <-time.After(quickLookMermaidBudget):
		return nil, false
	}
}

// quickLookHasDetails reports whether the Markdown on the panel has closed sections.
func quickLookHasDetails(ql *quickLookState) bool {
	return ql.res != nil && ql.res.kind == "markdown" && !ql.mdOpen && strings.Contains(ql.res.md, "<details")
}

// QuickLookOpenDetails opens the closed sections of a Markdown file.
func (m *OS) QuickLookOpenDetails() {
	if ql := &m.quickLook; ql.open && quickLookHasDetails(ql) {
		ql.mdOpen = true
	}
}

// Pictures and large headings in a Markdown file. The file is laid out by the
// renderer the agents thread uses (cockpit_markdown.go), with two block kinds
// plugged in here through ckMD.hook: an image alone in its paragraph is drawn
// as a picture, read from beside the file on the machine that has it, through
// the previews' pipeline (inbox_reply_preview.go). A top heading is drawn
// twice the size on a host that said it draws OSC 66 text sizing, and in bold
// elsewhere. Tables keep every cell (ckMD.whole).

const (
	// quickLookMDImagesMax bounds the pictures one file reads.
	quickLookMDImagesMax = 16
	// quickLookMDImageRows bounds a picture's rows.
	quickLookMDImageRows = 20
	// quickLookMDImageIndent is where a picture starts in the body.
	quickLookMDImageIndent = 2
)

// quickLookMDImageMsg carries a picture of a Markdown file back to the loop.
type quickLookMDImageMsg struct {
	res  *quickLookResult
	key  string
	img  *composer.Image
	note string
}

// quickLookBigHead is a heading line drawn twice the size: where in the body,
// its words and how they look.
type quickLookBigHead struct {
	x, y, cols int
	level      uint8
	text, sgr  string
}

// quickLookMDLayout lays out the Markdown on the panel at width.
func (m *OS) quickLookMDLayout(l ckLook, r *quickLookResult, width int, open bool) []ckLine {
	md := &ckMD{m: m, l: l, whole: true, big: m.hostCaps().TextSizing}
	md.hook = func(md *ckMD, b markdown.Block, first, rest []ckSeg, width int) bool {
		if b.Kind != markdown.Image {
			return false
		}
		m.quickLookMDImage(md, r, b, first, rest, width)
		return true
	}
	return md.layout(r.md, width, markdown.Options{Max: quickLookMarkdownMax, CloseDetails: !open, Images: true})
}

// quickLookMDSource is where an image of the file is: a path on the file's
// machine, or ok false for a web address and anything else not a file.
func quickLookMDSource(t quickLookTarget, src string) (string, bool) {
	if u, err := url.Parse(src); err == nil && u.Scheme != "" && len(u.Scheme) > 1 {
		if u.Scheme != "file" {
			return "", false
		}
		src = u.Path
	}
	if s, err := url.PathUnescape(src); err == nil {
		src = s
	}
	if src == "" {
		return "", false
	}
	if t.host != "" {
		if !path.IsAbs(src) {
			src = path.Join(path.Dir(t.path), src)
		}
		return path.Clean(src), true
	}
	src = filepath.FromSlash(src)
	if !filepath.IsAbs(src) {
		src = filepath.Join(filepath.Dir(t.path), src)
	}
	return filepath.Clean(src), true
}

// quickLookMDImage lays out an image of the file: its picture and its words
// under it, or its words and why there is no picture.
func (m *OS) quickLookMDImage(md *ckMD, r *quickLookResult, b markdown.Block, first, rest []ckSeg, width int) {
	ql := &m.quickLook
	pal := md.l.pal
	words := strings.TrimSpace(b.Text)
	if words == "" {
		words = "image"
	}
	note := func(why string) {
		segs := []ckSeg{{t: "Image: " + words, fg: pal.FgDim, attrs: ckBold}}
		if why != "" {
			segs = append(segs, ckSeg{t: "   " + why, fg: pal.FgMute})
		}
		md.text(segs, first, rest, max(width-ckSegsWidth(rest), 4))
	}
	key, ok := quickLookMDSource(r.target, b.URL)
	if !ok {
		segs := []ckSeg{{t: "Image: " + words, fg: pal.FgDim, attrs: ckBold}}
		if link := ckLinkTarget(b.URL); link != "" {
			segs = append(segs, ckSeg{t: "   "}, ckSeg{t: b.URL, fg: pal.Accent, link: link})
		}
		md.text(segs, first, rest, max(width-ckSegsWidth(rest), 4))
		return
	}
	if ql.mdImgsFor != r {
		ql.mdImgs, ql.mdImgsFor = map[string]*ckThreadImage{}, r
	}
	ti := ql.mdImgs[key]
	if ti == nil {
		if len(ql.mdImgs) >= quickLookMDImagesMax {
			note("Not shown: a file shows " + strconv.Itoa(quickLookMDImagesMax) + " images at most.")
			return
		}
		ti = &ckThreadImage{}
		ql.mdImgs[key] = ti
		ql.mdNeed = true
	}
	p := m.replyPreviewer()
	switch {
	case ti.err != "":
		note(ti.err)
		return
	case ti.img == nil:
		note("Reading the image" + overlay.Ellipsis())
		return
	case p.Mode() == composer.PreviewText:
		note("This terminal cannot draw the picture.")
		return
	}
	cols, rows := m.quickLookMDImageBox(ti.img, width)
	var cells []string
	switch p.Mode() {
	case composer.PreviewPixels:
		// A host that draws placeholders gets the picture as cells that
		// scroll and clip with the text.
		cells = m.placeholderLines(ti.img, cols, rows)
		if cells == nil {
			ql.mdNeed = true
		}
	case composer.PreviewCells:
		p.mu.Lock()
		if pic := p.pics[quickLookCellsKey(p, ti.img, cols, rows)]; pic != nil {
			cells = pic.lines
		}
		p.mu.Unlock()
		if cells == nil {
			// Still being prepared: the rows keep their place.
			ql.mdNeed = true
		}
	}
	hit := "qimg:"
	for row := range rows {
		ln := ckLine{indent: quickLookMDImageIndent, hit: hit + strconv.Itoa(row) + "/" + strconv.Itoa(rows) + "/" + strconv.Itoa(cols) + "/" + key}
		if row < len(cells) {
			ln.raw = cells[row]
		}
		md.lines = append(md.lines, ln)
	}
	if b.Text != "" {
		md.text([]ckSeg{{t: b.Text, fg: pal.FgMute, attrs: ckItalic}}, rest, rest, max(width-ckSegsWidth(rest), 4))
	}
}

// quickLookMDImageBox is the cell box of a picture in a body width cells
// wide. A small image keeps its own size.
func (m *OS) quickLookMDImageBox(img *composer.Image, width int) (int, int) {
	maxCols, maxRows := max(width-quickLookMDImageIndent-1, 1), quickLookMDImageRows
	if h := m.quickLook.bodyRows; h > 4 {
		maxRows = min(maxRows, h-2)
	}
	if img.Width > 0 && img.Height > 0 {
		cw, ch := m.hostCellSize()
		if cw <= 0 || ch <= 0 {
			cw, ch = 8, 16
		}
		maxCols = min(maxCols, (img.Width+cw-1)/cw)
		maxRows = min(maxRows, (img.Height+ch-1)/ch)
	}
	return ckFitBox(img, max(maxCols, 1), max(maxRows, 1), m.replyPreviewer().CellAspect(), 1)
}

// quickLookMDImagesCmd reads the pictures the layout asked for, and prepares
// those read at the size the layout gives them. It is nil when the layout
// asked for nothing since the last call.
func (m *OS) quickLookMDImagesCmd() tea.Cmd {
	ql := &m.quickLook
	if !ql.mdNeed || ql.res == nil || ql.res.kind != "markdown" || ql.mdImgsFor != ql.res {
		return nil
	}
	ql.mdNeed = false
	r := ql.res
	var cmds []tea.Cmd
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if r.target.host != "" {
		call = m.verbOnHostCaller()
	}
	ascii := overlay.UseASCII()
	for key, ti := range ql.mdImgs {
		switch {
		case ti.img != nil:
			cols, rows := m.quickLookMDImageBox(ti.img, ql.textW)
			if m.replyPreviewer().Mode() != composer.PreviewText {
				cmds = append(cmds, m.quickLookPrepare(ti.img, cols, rows))
			}
		case ti.err == "" && !ti.loading:
			ti.loading = true
			t := quickLookTarget{host: r.target.host, path: key, name: filepath.Base(key)}
			cmds = append(cmds, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
				defer cancel()
				var raw json.RawMessage
				var err error
				if t.host == "" {
					out, perr := session.PreviewLocalFile(ctx, t.path, quickLookMaxPx)
					if raw, err = jsonx.Marshal(out); perr != nil {
						err = perr
					}
				} else {
					raw, err = call(t.host, "file-preview", map[string]any{"path": t.path, "max_px": quickLookMaxPx})
				}
				res := quickLookDecode(t, raw, err, ascii)
				msg := quickLookMDImageMsg{res: r, key: key, img: res.img}
				if res.img == nil {
					msg.note = "The image could not be read."
					if res.kind != "image" && res.kind != "error" {
						msg.note = "The file is not a picture."
					}
				}
				return msg
			})
		}
	}
	return tea.Batch(cmds...)
}

// applyQuickLookMDImage takes a picture of the file on the panel.
func (m *OS) applyQuickLookMDImage(msg quickLookMDImageMsg) tea.Cmd {
	ql := &m.quickLook
	if !ql.open || ql.mdImgsFor != msg.res {
		return nil
	}
	ti := ql.mdImgs[msg.key]
	if ti == nil {
		return nil
	}
	ti.loading, ti.img, ti.err = false, msg.img, msg.note
	ql.mdGen++
	ql.mdNeed = true
	return m.quickLookMDImagesCmd()
}

// quickLookMDMarks notes, for the lines of the Markdown on the body from line
// from, where the frame drew pictures and large headings: the pictures the
// host places after the frame, and the headings it draws larger.
func (m *OS) quickLookMDMarks(lines []ckLine, from, rows, top int) {
	ql := &m.quickLook
	ql.mdPics, ql.bigHeads = ql.mdPics[:0], ql.bigHeads[:0]
	virtual := m.threadPlaceholders()
	var cur *previewPlacement
	var curKey string
	for i := 0; i < rows && from+i < len(lines); i++ {
		ln := &lines[from+i]
		if ln.big > 0 && i+1 < rows && from+i+1 < len(lines) {
			ql.bigHeads = append(ql.bigHeads, m.quickLookBigHead(ln, 1+ln.indent, top+i))
		}
		row, n, cols, key, ok := quickLookMDImageHit(ln.hit)
		if !ok {
			cur = nil
			continue
		}
		if cur != nil && curKey == key && cur.skip+cur.rows == row {
			cur.rows++
			continue
		}
		ti := ql.mdImgs[key]
		if ti == nil || ti.img == nil {
			cur = nil
			continue
		}
		ql.mdPics = append(ql.mdPics, previewPlacement{key: pixelsKey(ti.img, cols, n), x: 1 + quickLookMDImageIndent, y: top + i,
			cols: cols, rows: 1, skip: row, virtual: virtual})
		cur, curKey = &ql.mdPics[len(ql.mdPics)-1], key
	}
}

// quickLookMDImageHit reads a picture row's hit: the row, the picture's rows
// and columns, and its key. The key is last, since a path holds "/".
func quickLookMDImageHit(action string) (row, rows, cols int, key string, ok bool) {
	rest, ok := strings.CutPrefix(action, "qimg:")
	if !ok {
		return 0, 0, 0, "", false
	}
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 {
		return 0, 0, 0, "", false
	}
	row, e1 := strconv.Atoi(parts[0])
	rows, e2 := strconv.Atoi(parts[1])
	cols, e3 := strconv.Atoi(parts[2])
	return row, rows, cols, parts[3], e1 == nil && e2 == nil && e3 == nil
}

// quickLookBigHead is a heading line as the host draws it larger: its words,
// and the colour and weight of the first of them.
func (m *OS) quickLookBigHead(ln *ckLine, x, y int) quickLookBigHead {
	var b strings.Builder
	for _, s := range ln.segs {
		b.WriteString(s.t)
	}
	text := strings.TrimSpace(b.String())
	// The words go inside an OSC string: no control character may end it.
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 {
			return -1
		}
		return r
	}, text)
	lead := len(b.String()) - len(strings.TrimLeft(b.String(), " "))
	st := ansi.Style{}.Bold()
	for _, s := range ln.segs {
		if strings.TrimSpace(s.t) != "" {
			if s.fg != nil {
				st = st.ForegroundColor(s.fg)
			}
			break
		}
	}
	if bg := m.overlayUI().Canvas; bg != nil {
		st = st.BackgroundColor(bg)
	}
	return quickLookBigHead{x: x + lead, y: y, cols: 2 * ckTextWidth(text), level: ln.big, text: text, sgr: st.String()}
}

// quickLookTextSizing is the large headings of the panel the last frame drew,
// as the OSC 66 sequences that draw them, or nil. The host keeps a heading
// until a cell of it is written, so each frame draws them again: a frame that
// wrote over one has taken it away. They are left off while anything is over
// the panel, and while a selection is on the text, which is drawn plain.
func (m *OS) quickLookTextSizing() []byte {
	ql := &m.quickLook
	if !ql.open || len(ql.bigHeads) == 0 || ql.copy != nil || m.AnyOverlayOpen() || m.ContextMenu != nil || ql.res == nil || ql.res.kind != "markdown" {
		return nil
	}
	var buf []byte
	for _, h := range ql.bigHeads {
		scale := "s=2"
		if h.level > 1 {
			// A second heading is drawn at one and a half times the size,
			// in the same two rows.
			scale = "s=2:n=3:d=4"
		}
		buf = append(buf, "\x1b7\x1b["...)
		buf = strconv.AppendInt(buf, int64(ql.box.Y0+1+h.y+1), 10)
		buf = append(buf, ';')
		buf = strconv.AppendInt(buf, int64(ql.box.X0+1+h.x+1), 10)
		buf = append(buf, 'H')
		buf = append(buf, h.sgr...)
		buf = append(buf, "\x1b]66;"+scale+";"...)
		buf = append(buf, h.text...)
		buf = append(buf, "\x07\x1b[m\x1b8"...)
	}
	return buf
}
