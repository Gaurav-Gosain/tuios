package app

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const (
	ckImgRows = 12
	ckImgCols = 64
)

const ckImgIndent = 4

const ckThreadImagesMax = 16

type ckThreadImage struct {
	img        *composer.Image
	err        string
	loading    bool
	cols, rows int
}

type threadImageMsg struct {
	key, id string
	data    []byte
	err     error
}

func (m *OS) threadImagesCmd(t *agentThread) tea.Cmd {
	if !t.read {
		return nil
	}
	var cmds []tea.Cmd
	call, nonce := m.agentCaller(), m.inboxNonce()
	for _, en := range t.entries {
		if en.Image == nil {
			continue
		}
		if t.images == nil {
			t.images = make(map[string]*ckThreadImage)
		}
		if _, ok := t.images[en.ID]; ok {
			continue
		}
		if len(t.images) >= ckThreadImagesMax {
			t.images[en.ID] = &ckThreadImage{err: "Not shown: this thread shows " + strconv.Itoa(ckThreadImagesMax) + " images at most."}
			continue
		}
		t.images[en.ID] = &ckThreadImage{loading: true}
		key, sess, window, id := t.key, t.session, t.window, en.ID
		cmds = append(cmds, func() tea.Msg {
			params := map[string]any{"session": sess, "window": window, "human_nonce": nonce, "image": id}
			raw, err := call("agent-transcript", params, threadReadTimeout)
			msg := threadImageMsg{key: key, id: id, err: err}
			if err != nil {
				return msg
			}
			var res struct {
				Data []byte `json:"data"`
			}
			if msg.err = jsonx.Unmarshal(raw, &res); msg.err == nil {
				msg.data = res.Data
			}
			return msg
		})
	}
	if cmd := m.threadPicturesCmd(t); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *OS) applyThreadImage(msg threadImageMsg) tea.Cmd {
	t := m.Inbox.threads.byKey[msg.key]
	if t == nil || t.images == nil {
		return nil
	}
	ti := t.images[msg.id]
	if ti == nil {
		return nil
	}
	ti.loading = false
	switch {
	case msg.err != nil:
		ti.err = "The image could not be read."
	default:
		img, err := composer.NewImage(msg.data)
		switch {
		case errors.Is(err, composer.ErrImageTooLarge):
			ti.err = "The image is larger than 8 MB."
		case err != nil:
			ti.err = "The transcript holds no image this view can show."
		default:
			ti.img = img
		}
	}
	t.gen++
	m.cockpitTouch()
	return m.threadPicturesCmd(t)
}

func (m *OS) threadPicturesCmd(t *agentThread) tea.Cmd {
	if t.imgWidth <= 0 || len(t.images) == 0 {
		return nil
	}
	p := m.replyPreviewer()
	mode := p.Mode()
	if mode == composer.PreviewText {
		return nil
	}
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	type job struct {
		key previewKey
		img *composer.Image
	}
	var jobs []job
	for _, ti := range t.images {
		if ti.img == nil {
			continue
		}
		cols, rows := m.ckImageBox(ti.img, t.imgWidth)
		if ti.cols > 0 {
			cols, rows = ti.cols, ti.rows
		}
		k := pixelsKey(ti.img, cols, rows)
		if mode == composer.PreviewCells {
			k = p.cellsKey(ti.img, cols, rows)
		}
		p.mu.Lock()
		asked := p.asked[k]
		p.asked[k] = true
		p.mu.Unlock()
		if !asked {
			jobs = append(jobs, job{key: k, img: ti.img})
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	ground := m.cockpitPalette().Card
	return func() tea.Msg {
		out := make(map[previewKey]*previewPicture, len(jobs))
		for _, j := range jobs {
			out[j.key] = preparePicture(j.img, j.key, cw, ch, ground)
		}
		return replyPreviewMsg{pics: out}
	}
}

func (m *OS) ckImageBox(img *composer.Image, width int) (int, int) {
	maxCols, maxRows := min(width-ckImgIndent-2, ckImgCols), ckImgRows
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

func (m *OS) ckImageCard(l ckLook, t *agentThread, en threadEntry, width int) []ckLine {
	pal := l.pal
	t.imgWidth = width
	ti := t.images[en.ID]
	what := ckImageWhat(en, nil)
	if ti != nil && ti.img != nil {
		what = ckImageWhat(en, ti.img)
	}
	head := ckLine{indent: ckImgIndent, segs: []ckSeg{{t: what, fg: pal.FgDim, attrs: ckBold}}}
	switch {
	case ti == nil || ti.loading:
		head.segs = append(head.segs, ckSeg{t: "   Reading the image...", fg: pal.FgMute})
		return []ckLine{head}
	case ti.err != "":
		head.segs = append(head.segs, ckSeg{t: "   " + ti.err, fg: pal.FgMute})
		return []ckLine{head}
	}
	head.hit = "timg:-/-/-/" + en.ID
	head.segs = append(head.segs, ckSeg{t: "   click to view", fg: pal.FgMute})
	p := m.replyPreviewer()
	mode := p.Mode()
	if mode == composer.PreviewText {
		return []ckLine{head}
	}
	cols, rows := m.ckImageBox(ti.img, width)
	var cells []string
	if mode == composer.PreviewPixels {
		cells = m.placeholderLines(ti.img, cols, rows)
	}
	if mode == composer.PreviewCells {
		if cells = p.Cells(ti.img, cols, rows); cells == nil {
			cells = make([]string, rows)
		}
	}
	ls := []ckLine{head}
	for r := range rows {
		ln := ckLine{indent: ckImgIndent, hit: "timg:" + strconv.Itoa(r) + "/" + strconv.Itoa(rows) + "/" + strconv.Itoa(cols) + "/" + en.ID}
		if r < len(cells) && cells[r] != "" {
			ln.raw = cells[r]
		}
		ls = append(ls, ln)
	}
	return append(ls, ckLine{})
}

func ckImgHit(action string) (row, rows, cols int, id string, ok bool) {
	rest, ok := strings.CutPrefix(action, "timg:")
	if !ok {
		return 0, 0, 0, "", false
	}
	parts := strings.SplitN(rest, "/", 4)
	if len(parts) != 4 || parts[3] == "" {
		return 0, 0, 0, "", false
	}
	if parts[0] == "-" {
		return -1, 0, 0, parts[3], true
	}
	row, e1 := strconv.Atoi(parts[0])
	rows, e2 := strconv.Atoi(parts[1])
	cols, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, 0, "", false
	}
	return row, rows, cols, parts[3], true
}

func (m *OS) threadImagePlacements(want []previewPlacement) []previewPlacement {
	if !m.ShowInbox || !m.threadShown() || m.Inbox.ck.img != nil || m.ContextMenu != nil || !m.overlayOnTop("inbox") || !m.replyPixelsReady() {
		return want
	}
	e, ok := m.cockpitAgent()
	if !ok {
		return want
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil || len(t.images) == 0 {
		return want
	}
	virtual := m.threadPlaceholders()
	type run struct {
		id               string
		x, y             int
		first, last      int
		cols, rows, next int
	}
	var cur *run
	flush := func() {
		if cur == nil {
			return
		}
		if ti := t.images[cur.id]; ti != nil && ti.img != nil {
			want = append(want, previewPlacement{key: pixelsKey(ti.img, cur.cols, cur.rows), x: cur.x + ckImgIndent, y: cur.y,
				cols: cur.cols, rows: cur.last - cur.first + 1, skip: cur.first, virtual: virtual})
		}
		cur = nil
	}
	for _, h := range m.Inbox.ck.hits {
		row, rows, cols, id, ok := ckImgHit(h.action)
		if !ok || row < 0 || row >= rows {
			continue
		}
		if cur != nil && cur.id == id && cur.cols == cols && cur.rows == rows && row == cur.last+1 && h.y0 == cur.next {
			cur.last, cur.next = row, h.y0+1
			continue
		}
		flush()
		cur = &run{id: id, x: h.x0, y: h.y0, first: row, last: row, cols: cols, rows: rows, next: h.y0 + 1}
	}
	flush()
	return want
}

func (m *OS) cockpitOpenThreadImage(action string) tea.Cmd {
	_, _, _, id, ok := ckImgHit("timg:" + action)
	if !ok {
		return nil
	}
	e, found := m.cockpitAgent()
	if !found {
		return nil
	}
	t := m.Inbox.threads.byKey[agentKey(e)]
	if t == nil || t.images[id] == nil || t.images[id].img == nil {
		return nil
	}
	from := "from the conversation of " + printableTitle(e.Title)
	for _, en := range t.entries {
		if en.ID == id && en.Role == "user" {
			from = "pasted by you for " + printableTitle(e.Title)
		}
	}
	m.Inbox.ck.img = &ckImageView{imgs: []*composer.Image{t.images[id].img}, from: from, saved: map[int]string{}}
	m.cockpitTouch()
	return m.CockpitImagePicture()
}

func ckImageWhat(en threadEntry, img *composer.Image) string {
	kind, w, h := "", 0, 0
	if en.Image != nil {
		_, kind, _ = strings.Cut(en.Image.MediaType, "/")
		w, h = en.Image.Width, en.Image.Height
	}
	if img != nil {
		if img.Format != "" {
			kind = img.Format
		}
		if img.Width > 0 && img.Height > 0 {
			w, h = img.Width, img.Height
		}
	}
	what := "Image"
	if kind != "" {
		what = strings.ToUpper(kind) + " image"
	}
	if w > 0 && h > 0 {
		x := "×"
		if overlay.UseASCII() {
			x = "x"
		}
		what += "  " + strconv.Itoa(w) + x + strconv.Itoa(h)
	}
	return what
}

func ckThreadMinute(working bool, now time.Time) int64 {
	if !working {
		return 0
	}
	return now.Unix() / 60
}

func (m *OS) threadPicsVersion(t *agentThread) uint64 {
	if len(t.images) == 0 {
		return 0
	}
	return m.replyPreviewer().Version()
}
