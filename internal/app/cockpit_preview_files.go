package app

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/ghpr"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The body of the selected attachment in the card preview: a picture through
// the reply previews' pipeline (kitty graphics where the host has them, block
// glyphs where it does not, a line of words at 16 colours), a Markdown file as
// Markdown, a diff and a text highlighted, a Mermaid diagram drawn as text by
// the sketch plugin with its source as the fallback, and a pull request with its state.
//
// The bytes are read with get-task-attachment when the preview first shows
// the attachment, after a frame and never in one, and kept by their hash: a
// card that comes back, or a second card with the same file, reads nothing.
// What is kept is bounded (kbFilesMax). A pull request's state is kept by its
// address, and the daemon asks gh at most once a minute for it. An
// attachment is shown, never typed into a pane or given to an agent.

// kbFilesMax bounds the bytes the preview keeps.
const kbFilesMax = 32 << 20

// kbBodyLines bounds the lines a text attachment shows in the preview.
const kbBodyLines = 400

// The picture box of an attachment in the preview, in cells.
const (
	kbImgRows = 16
	kbImgCols = 96
)

// kbFile is one attachment's content, once read.
type kbFile struct {
	loading bool
	err     string
	size    int
	// img is a picture.
	img *composer.Image
	// text is a text kind's content, or a Mermaid diagram's source, and
	// diagram the diagram drawn as text.
	text    string
	diagram []string
	// pr is a pull request link's state, and prErr why it could not be read.
	pr    *ghpr.PR
	prErr string
	// seq orders the files for the bound: the oldest read goes first.
	seq uint64
}

// kbFiles keeps the files the preview read.
type kbFiles struct {
	m     map[string]*kbFile
	bytes int
	seq   uint64
}

// kbFileKey is what a file is kept by: its hash, or a link's address.
func kbFileKey(a session.TaskAttachment) string {
	if a.Hash != "" {
		return a.Hash
	}
	return "url:" + a.URL
}

// kbFileMsg carries an attachment's content back to the Update goroutine.
type kbFileMsg struct {
	key   string
	f     kbFile
	bytes int
}

// kbPreviewSelected is the attachment the preview has selected, if any.
func (m *OS) kbPreviewSelected() (session.TaskAttachment, bool) {
	d, pv := m.Inbox.ck.card, &m.Inbox.ck.kb.pv
	if d == nil || pv.att < 0 || pv.att >= len(d.attachments) {
		return session.TaskAttachment{}, false
	}
	return d.attachments[pv.att], true
}

// kbPreviewFileFetch reads the selected attachment's content when the
// preview shows it and it is not kept. A plain link has nothing to read.
func (m *OS) kbPreviewFileFetch() tea.Cmd {
	pv := &m.Inbox.ck.kb.pv
	pv.fileWant = false
	a, ok := m.kbPreviewSelected()
	_, card, cardOK := m.cockpitCard()
	if !ok || !cardOK || !m.kbPreviewShown() || card.Host != "" || (a.Kind == session.AttachLink) ||
		(m.DaemonClient == nil && m.Inbox.call == nil) {
		return nil
	}
	key := kbFileKey(a)
	files := &pv.files
	if files.m == nil {
		files.m = map[string]*kbFile{}
	}
	if _, kept := files.m[key]; kept {
		return nil
	}
	files.m[key] = &kbFile{loading: true}
	call, sess, id, name, kind := m.inboxCaller(), card.Session, card.ID, a.Name, a.Kind
	return func() tea.Msg {
		raw, err := call("get-task-attachment", map[string]any{"session": sess, "id": id, "name": name}, threadReadTimeout)
		msg := kbFileMsg{key: key}
		if err != nil {
			msg.f.err = "tuios cannot read the attachment now: " + printableTitle(err.Error())
			return msg
		}
		var res struct {
			Content string   `json:"content"`
			PR      *ghpr.PR `json:"pr"`
			PRError string   `json:"pr_error"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil {
			msg.f.err = "tuios cannot read the attachment now."
			return msg
		}
		msg.f.pr, msg.f.prErr = res.PR, res.PRError
		if res.Content == "" {
			return msg
		}
		data, err := base64.StdEncoding.DecodeString(res.Content)
		if err != nil {
			msg.f.err = "The attachment came back damaged."
			return msg
		}
		msg.bytes = len(data)
		msg.f = kbFileFrom(kind, data, msg.f)
		return msg
	}
}

// kbFileFrom makes a kept file from an attachment's bytes, off the Update
// goroutine: a picture is decoded, and a Mermaid diagram is drawn.
func kbFileFrom(kind string, data []byte, f kbFile) kbFile {
	f.size = len(data)
	switch kind {
	case session.AttachImage:
		img, err := composer.NewImage(data)
		switch {
		case errors.Is(err, composer.ErrImageTooLarge):
			f.err = "The picture is larger than this view shows."
		case err != nil:
			f.err = "This view cannot show the picture."
		default:
			f.img = img
		}
	case session.AttachMermaid:
		f.text = string(data)
		f.diagram = mermaidLines(f.text)
	case session.AttachFile:
	default:
		f.text = string(data)
	}
	return f
}

// mermaidLines draws a Mermaid source as text through the sketch plugin, as
// a TML surface's diagram is drawn (drawWithSketchPlugin), and nil when the
// plugin is not installed or cannot draw it. The diagram engine stays out of
// the binary: linked in, it adds 2.7 MB.
func mermaidLines(src string) []string {
	rows, _, err := drawWithSketchPlugin(src, kbImgCols)
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, len(rows))
	for y, row := range rows {
		var b strings.Builder
		for _, c := range row {
			if c.Rune == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteRune(c.Rune)
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// applyKBFile keeps an attachment's content, drops the oldest files past the
// bound, and prepares the picture.
func (m *OS) applyKBFile(msg kbFileMsg) tea.Cmd {
	files := &m.Inbox.ck.kb.pv.files
	if files.m == nil {
		return nil
	}
	f := msg.f
	files.seq++
	f.seq = files.seq
	files.m[msg.key] = &f
	files.bytes += msg.bytes
	for files.bytes > kbFilesMax && len(files.m) > 1 {
		oldest, seq := "", uint64(0)
		for k, v := range files.m {
			if k != msg.key && !v.loading && (oldest == "" || v.seq < seq) {
				oldest, seq = k, v.seq
			}
		}
		if oldest == "" {
			break
		}
		files.bytes -= files.m[oldest].size
		delete(files.m, oldest)
	}
	m.cockpitTouch()
	return m.kbPreviewPictureCmd()
}

// kbPreviewImageBox is the cell box of a picture in a preview width cells
// wide.
func (m *OS) kbPreviewImageBox(img *composer.Image, width int) (int, int) {
	maxCols, maxRows := min(width-2, kbImgCols), kbImgRows
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

// kbPreviewPictureCmd prepares the selected attachment's picture for the box
// the preview last drew it in.
func (m *OS) kbPreviewPictureCmd() tea.Cmd {
	pv := &m.Inbox.ck.kb.pv
	a, ok := m.kbPreviewSelected()
	if !ok || pv.imgWidth <= 0 {
		return nil
	}
	f := pv.files.m[kbFileKey(a)]
	if f == nil || f.img == nil {
		return nil
	}
	p := m.replyPreviewer()
	mode := p.Mode()
	if mode == composer.PreviewText {
		return nil
	}
	cols, rows := m.kbPreviewImageBox(f.img, pv.imgWidth)
	k := pixelsKey(f.img, cols, rows)
	if mode == composer.PreviewCells {
		k = p.cellsKey(f.img, cols, rows)
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
	img, ground := f.img, theme.UI().Card
	return func() tea.Msg {
		return replyPreviewMsg{pics: map[previewKey]*previewPicture{k: preparePicture(img, k, cw, ch, ground)}}
	}
}

// ckAttachmentBody is the selected attachment's body as preview lines width
// cells wide.
func (m *OS) ckAttachmentBody(l ckLook, a session.TaskAttachment, width int) []ckLine {
	pal := l.pal
	pv := &m.Inbox.ck.kb.pv
	var out []ckLine
	note := func(s string) []ckLine { return append(out, ckLine{segs: []ckSeg{{t: s, fg: pal.FgMute}}}) }
	if a.Kind == session.AttachLink {
		return append(out, ckLine{segs: []ckSeg{{t: printableTitle(a.URL), fg: pal.Accent}}})
	}
	f := pv.files.m[kbFileKey(a)]
	if f == nil {
		pv.fileWant = true
		return note("Reading...")
	}
	switch {
	case f.loading:
		return note("Reading...")
	case f.err != "":
		return note(f.err)
	}
	switch a.Kind {
	case session.AttachPR:
		return append(out, m.ckPRLines(l, a, f, width)...)
	case session.AttachImage:
		return append(out, m.kbPictureLines(l, f, a, width)...)
	case session.AttachMermaid:
		if f.diagram != nil {
			return append(out, kbCut(m.ckMarkdown(l, ckFence(strings.Join(f.diagram, "\n"), ""), width), pal)...)
		}
		out = note("Install the sketch plugin to draw the diagram. Its source:")
		return append(out, m.ckMarkdown(l, ckFence(f.text, "mermaid"), width)...)
	case session.AttachMarkdown:
		return append(out, kbCut(m.ckMarkdown(l, f.text, width), pal)...)
	case session.AttachDiff:
		return append(out, kbCut(m.ckMarkdown(l, ckFence(f.text, "diff"), width), pal)...)
	case session.AttachText:
		return append(out, kbCut(m.ckMarkdown(l, ckFence(f.text, ""), width), pal)...)
	}
	return note("This kind of file has no preview. tuios task attachment get saves it.")
}

// kbCut keeps the first kbBodyLines lines, and says how many it left out.
func kbCut(lines []ckLine, pal overlay.Palette) []ckLine {
	if len(lines) <= kbBodyLines {
		return lines
	}
	more := len(lines) - kbBodyLines
	return append(lines[:kbBodyLines:kbBodyLines], ckLine{segs: []ckSeg{{t: strconv.Itoa(more) + " more lines. tuios task attachment get saves the whole file.", fg: pal.FgMute}}})
}

// ckFence puts text in a Markdown code block of lang, with a fence longer
// than any run of backticks in it, so the text cannot close it.
func ckFence(text, lang string) string {
	run, longest := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + lang + "\n" + strings.TrimRight(text, "\n") + "\n" + fence + "\n"
}

// ckPRLines are a pull request link's state.
func (m *OS) ckPRLines(l ckLook, a session.TaskAttachment, f *kbFile, width int) []ckLine {
	pal := l.pal
	if f.pr == nil {
		why := "gh could not read it"
		if f.prErr != "" {
			why = printableTitle(f.prErr)
		}
		return []ckLine{{segs: []ckSeg{{t: printableTitle(a.URL), fg: pal.Accent}}}, {segs: []ckSeg{{t: ckTrunc(why, width), fg: pal.FgMute}}}}
	}
	pr := f.pr
	ink := pal.Fg
	switch pr.State {
	case ghpr.StateMerged:
		ink = pal.Accent
	case ghpr.StateClosed:
		ink = pal.FgMute
	}
	state := []ckSeg{{t: "#" + strconv.Itoa(pr.Number) + " ", fg: pal.FgDim}, {t: pr.State, fg: ink, attrs: ckBold}}
	if pr.Checks != "" && pr.State == ghpr.StateOpen {
		checkInk := pal.FgDim
		switch pr.Checks {
		case ghpr.ChecksFail:
			checkInk = pal.Warn
		case ghpr.ChecksPass:
			checkInk = pal.Fg
		}
		state = append(state, ckSeg{t: sepWord() + "checks " + pr.Checks, fg: checkInk})
	}
	out := []ckLine{{segs: state}}
	if pr.Title != "" {
		out = append(out, ckLine{segs: []ckSeg{{t: ckTrunc(printableTitle(pr.Title), width), fg: pal.Fg}}})
	}
	return out
}

// kbPictureLines are a picture's rows in the preview. A row carries
// "pimg:row/rows/cols/hash" so the kitty picture is placed where the frame
// drew it.
func (m *OS) kbPictureLines(l ckLook, f *kbFile, a session.TaskAttachment, width int) []ckLine {
	pv := &m.Inbox.ck.kb.pv
	pv.imgWidth = width
	p := m.replyPreviewer()
	mode := p.Mode()
	o := m.inboxKeyOr(config.ActionInboxArtifact, "o")
	tip := ckLine{segs: []ckSeg{{t: strconv.Itoa(f.img.Width) + "x" + strconv.Itoa(f.img.Height) + sepWord() + o + " shows it whole", fg: l.pal.FgMute}}}
	if mode == composer.PreviewText {
		return []ckLine{tip}
	}
	cols, rows := m.kbPreviewImageBox(f.img, width)
	var cells []string
	switch mode {
	case composer.PreviewPixels:
		cells = m.placeholderLines(f.img, cols, rows)
	case composer.PreviewCells:
		if cells = p.Cells(f.img, cols, rows); cells == nil {
			cells = make([]string, rows)
		}
	}
	out := []ckLine{tip}
	key := kbFileKey(a)
	for r := range rows {
		ln := ckLine{hit: "pimg:" + strconv.Itoa(r) + "/" + strconv.Itoa(rows) + "/" + strconv.Itoa(cols) + "/" + key}
		if r < len(cells) && cells[r] != "" {
			ln.raw = cells[r]
		}
		out = append(out, ln)
	}
	return out
}

// kbPreviewOpen is the artifact key in the preview: the selected picture in
// the image view.
func (m *OS) kbPreviewOpen() tea.Cmd {
	a, ok := m.kbPreviewSelected()
	if !ok {
		return nil
	}
	f := m.Inbox.ck.kb.pv.files.m[kbFileKey(a)]
	if f == nil || f.img == nil {
		return nil
	}
	_, card, _ := m.cockpitCard()
	m.Inbox.ck.img = &ckImageView{imgs: []*composer.Image{f.img}, from: "attached to " + card.ID + " " + printableTitle(card.Title), saved: map[int]string{}}
	m.cockpitTouch()
	return m.CockpitImagePicture()
}

// kbPreviewPlacements adds the kitty picture of the attachment the preview
// drew, at the rows the frame drew it on, cropped to the rows in view.
// Nothing is placed while a menu or the image view is over the board.
func (m *OS) kbPreviewPlacements(want []previewPlacement) []previewPlacement {
	if !m.ShowInbox || !m.kbPreviewShown() || m.Inbox.ck.img != nil || m.ContextMenu != nil || !m.overlayOnTop("inbox") || !m.replyPixelsReady() {
		return want
	}
	virtual := m.threadPlaceholders()
	first, last, x, y, cols, rows := -1, -1, 0, 0, 0, 0
	key := ""
	for _, h := range m.Inbox.ck.hits {
		rest, ok := strings.CutPrefix(h.action, "pimg:")
		if !ok {
			continue
		}
		row, rs, cs, k, ok := ckImgHit("timg:" + rest)
		if !ok || row < 0 {
			continue
		}
		if first < 0 {
			first, x, y, cols, rows, key = row, h.x0, h.y0, cs, rs, k
		}
		last = row
	}
	if first < 0 {
		return want
	}
	f := m.Inbox.ck.kb.pv.files.m[key]
	if f == nil || f.img == nil {
		return want
	}
	return append(want, previewPlacement{key: pixelsKey(f.img, cols, rows), x: x, y: y, cols: cols, rows: last - first + 1, skip: first, virtual: virtual})
}
