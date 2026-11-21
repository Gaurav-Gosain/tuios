package app

import (
	"context"
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func quickLookHasDetails(ql *quickLookState) bool {
	return ql.res != nil && ql.res.kind == "markdown" && !ql.mdOpen && strings.Contains(ql.res.md, "<details")
}

// QuickLookOpenDetails opens the closed sections of a Markdown file.
func (m *OS) QuickLookOpenDetails() {
	if ql := &m.quickLook; ql.open && quickLookHasDetails(ql) {
		ql.mdOpen = true
	}
}

const (
	quickLookMDImagesMax   = 16
	quickLookMDImageRows   = 20
	quickLookMDImageIndent = 2
	quickLookMDWordsMax    = 48
)

type quickLookMDImageMsg struct {
	res  *quickLookResult
	key  string
	img  *composer.Image
	note string
}

func (m *OS) quickLookMDLayout(l ckLook, r *quickLookResult, width int, open bool) []ckLine {
	md := &ckMD{m: m, l: l, heads: true, blockScope: "ql:" + r.target.host + ":" + r.target.path}
	md.pics = m.quickLookPics(r)
	md.hook = func(md *ckMD, b markdown.Block, first, rest []ckSeg, width int) bool {
		if b.Kind != markdown.Image {
			return false
		}
		m.quickLookMDImage(md, r, b, first, rest, width)
		return true
	}
	lines := md.layout(r.md, width, markdown.Options{Max: quickLookMarkdownMax, CloseDetails: !open, Images: true, Dark: quickLookDark(l)})
	m.quickLook.mdHeadAt, m.quickLook.mdHeadLevel = md.headAt, md.headLevel
	return lines
}

func quickLookMDHeadMarks(at, level []int, lines, rows int) []int {
	var marks []int
	for top := 3; top >= 1; top-- {
		marks = marks[:0]
		for i, n := range at {
			if level[i] <= top {
				marks = append(marks, n)
			}
		}
		if (ckRuler{lines: lines, marks: marks}).markRows(rows) != nil {
			return marks
		}
	}
	return nil
}

func quickLookDark(l ckLook) bool {
	c := l.pal.Canvas
	if c == nil {
		return true
	}
	if _, none := c.(lipgloss.NoColor); none {
		return true
	}
	return hostLuminance(c) < 128
}

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

func (m *OS) quickLookMDImage(md *ckMD, r *quickLookResult, b markdown.Block, first, rest []ckSeg, width int) {
	ql := &m.quickLook
	pal := md.l.pal
	avail := max(width-ckSegsWidth(rest), 4)
	words := ckTrunc(strings.Join(strings.Fields(b.Text), " "), quickLookMDWordsMax)
	if words == "" {
		words = "image"
	}
	note := func(why string) {
		segs := []ckSeg{{t: "Image: " + words, fg: pal.FgDim, attrs: ckBold}}
		if why != "" {
			segs = append(segs, ckSeg{t: "   " + why, fg: pal.FgMute})
		}
		md.text(segs, first, rest, avail)
	}
	key, ok := quickLookMDSource(r.target, b.URL)
	if !ok || !markdown.Drawable(b.URL, "") {
		md.text([]ckSeg{{t: "Image: " + words, fg: pal.FgDim, attrs: ckBold, link: ckLinkTarget(b.URL)}}, first, rest, avail)
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
		cells = m.placeholderLines(ti.img, cols, rows)
		p.mu.Lock()
		ready := p.pics[pixelsKey(ti.img, cols, rows)] != nil
		p.mu.Unlock()
		if cells == nil || !ready {
			ql.mdNeed = true
		}
	case composer.PreviewCells:
		p.mu.Lock()
		if pic := p.pics[quickLookCellsKey(p, ti.img, cols, rows)]; pic != nil {
			cells = pic.lines
		}
		p.mu.Unlock()
		if cells == nil {
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
		md.text([]ckSeg{{t: ckTrunc(strings.Join(strings.Fields(b.Text), " "), avail), fg: pal.FgMute, attrs: ckItalic}}, rest, rest, avail)
	}
}

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
			if ti.cols > 0 {
				cols, rows = ti.cols, ti.rows
			}
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

func (m *OS) quickLookMDMarks(lines []ckLine, from, rows, top int) {
	ql := &m.quickLook
	ql.mdPics = ql.mdPics[:0]
	virtual := m.threadPlaceholders()
	var cur *previewPlacement
	var curKey string
	for i := 0; i < rows && from+i < len(lines); i++ {
		ln := &lines[from+i]
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

func (m *OS) quickLookPics(r *quickLookResult) *ckPics {
	ql := &m.quickLook
	return &ckPics{hit: "qimg:", indent: quickLookMDImageIndent,
		add: func(id string, ti *ckThreadImage) {
			if ql.mdImgsFor != r {
				ql.mdImgs, ql.mdImgsFor = map[string]*ckThreadImage{}, r
			}
			ql.mdImgs[id] = ti
		},
		cells: func(ti *ckThreadImage) []string {
			p := m.replyPreviewer()
			var cells []string
			switch p.Mode() {
			case composer.PreviewPixels:
				cells = m.placeholderLines(ti.img, ti.cols, ti.rows)
			case composer.PreviewCells:
				p.mu.Lock()
				if pic := p.pics[quickLookCellsKey(p, ti.img, ti.cols, ti.rows)]; pic != nil {
					cells = pic.lines
				}
				p.mu.Unlock()
			}
			if cells == nil {
				ql.mdNeed = true
			}
			return cells
		}}
}

func (m *OS) quickLookBlockClick(x, y int) {
	ql := &m.quickLook
	if ql.res == nil || ql.res.kind != "markdown" || ql.copy != nil || ql.textW <= 0 {
		return
	}
	row := y - (ql.box.Y0 + 1 + ql.textY)
	if i := ql.mdFrom + row; row >= 0 && row < ql.bodyRows && i < len(ql.mdLines) {
		main, alt, from, to, split := ckSplitRead(ql.mdLines[i].hit)
		key, ok := strings.CutPrefix(main, ckBlockHit)
		if !ok {
			return
		}
		if col := x - (ql.box.X0 + 1 + ql.textX); split && col >= ql.textW-from && col < ql.textW-to {
			m.ToggleBlockPicture(strings.TrimPrefix(alt, ckBlockPicHit))
			return
		}
		m.ToggleBlock(key)
	}
}

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
