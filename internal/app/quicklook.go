package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/color"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"tuios.dev/shot/mosaic"
)

const overlayKindQuickLook = "quicklook"

const (
	quickLookMarkdownMax = 256 << 10
	quickLookMaxPx       = 1600
	quickLookMaxW        = 110
	quickLookMaxH        = 48
	quickLookPicW        = 180
	quickLookPicH        = 60
	quickLookSmallW      = 60
	quickLookSmallH      = 20
	quickLookTimeout     = 25 * time.Second
	quickLookWheelStep   = 3
	quickLookPromptMax   = 256
)

type quickLookTarget struct {
	host   string
	path   string
	name   string
	dir    bool
	up     bool
	refuse string
}

type quickLookResult struct {
	target           quickLookTarget
	kind             string
	size             int64
	mime             string
	note             string
	lines            []string
	spans            [][]diffview.Span
	more             bool
	older            bool
	stopped          bool
	startOff, endOff int64
	parts            []quickLookPart
	firstLine        int
	cutLong          bool
	md               string
	img              *composer.Image
	width, height    int
	pages            int
	durationMs       int64
	page             int
	atMs             int64
	table            *quickLookTable
	archive          []quickLookArchiveRow
	archiveItems     int
	unpacked         int64
	formatted        bool
	facts            string
	entries          []fileEntry
	capped           bool
}

type quickLookMsg struct{ res *quickLookResult }

type quickLookState struct {
	open                          bool
	target                        quickLookTarget
	fetching                      bool
	res                           *quickLookResult
	scroll                        int
	jump                          quickLookMove
	inside                        bool
	full                          bool
	seek                          session.PreviewSeek
	resume                        int64
	prompt                        quickLookPromptKind
	input                         textedit.Line
	query                         string
	status                        string
	statusLine                    bool
	wrap                          bool
	wrapSet                       bool
	rowSpans                      []diffview.Span
	copy                          *quickLookCopy
	textX, textY, textW, bodyRows int
	mouse                         CopyMouseGesture
	held                          bool
	mdImgs                        map[string]*ckThreadImage
	mdImgsFor                     *quickLookResult
	mdNeed                        bool
	mdGen                         int
	mdPics                        []previewPlacement
	mdFrom                        int
	mdHeadAt, mdHeadLevel         []int
	ruler                         quickLookRuler
	rulerDrag                     bool
	rulerGrab                     int
	atOff                         int64
	hostOlder                     map[string]bool
	hostAsked                     map[string]bool
	borrowed                      bool
	free                          bool
	wantLine                      int
	box                           overlay.Rect
	pic                           quickLookPic
	pageWant                      int
	pageHeld                      []*quickLookResult
	pageOut                       int
	pageCancel                    context.CancelFunc
	pageSlow                      bool
	pageBar                       quickLookBarBox
	barDrag                       bool
	view                          string
	viewScroll                    int
	details                       *quickLookDetails
	detailsOut                    bool
	column                        int
	filmWant                      int64
	filmOut                       int64
	stripOn                       bool
	strip                         [quickLookStripFrames]*composer.Image
	stripFailed                   [quickLookStripFrames]bool
	stripPics                     []quickLookPic
	mdOpen                        bool
	mdLines                       []ckLine
	mdKey                         quickLookMDKey
	look                          ckLook
	dv                            *diffview.Theme
	lookKey                       ckLookKey
	framed                        quickLookFrame
}

type quickLookFrame struct {
	mdGen                                              int
	mdOpen                                             bool
	ok                                                 bool
	look                                               ckLookKey
	res                                                *quickLookResult
	target                                             quickLookTarget
	scroll                                             int
	inside                                             bool
	prompt                                             quickLookPromptKind
	input                                              string
	inputAt                                            int
	query                                              string
	status                                             string
	wrap                                               bool
	view                                               string
	viewScroll, column                                 int
	details                                            *quickLookDetails
	innerW, bodyH                                      int
	key                                                string
	seekKey                                            string
	insideKey                                          string
	canvas, fgMute, frame, accent, accentBright, fgDim color.Color
	border                                             lipgloss.Border
	content                                            string
	geo                                                overlay.Geometry
}

type quickLookPic struct {
	ok         bool
	key        previewKey
	x, y       int
	cols, rows int
}

type quickLookMDKey struct {
	gen   int
	open  bool
	res   *quickLookResult
	width int
	rows  int
	look  ckLookKey
}

// QuickLookOpen reports whether the preview is up.
func (m *OS) QuickLookOpen() bool { return m.quickLook.open }

// SidebarQuickLook opens the preview of the row under the files cursor, or
// closes it when it is up.
func (m *OS) SidebarQuickLook() tea.Cmd {
	if f := m.menuFile; f.Active() && f.Name != "" && !f.Up {
		return m.quickLookMenu(f)
	}
	if m.quickLook.open {
		m.CloseQuickLook()
		return nil
	}
	if !m.SidebarFocused || m.SidebarFocusOnSecondary() || !m.filesOn() {
		return nil
	}
	t, ok := m.quickLookCursorTarget()
	if !ok {
		m.ShowNotification("Put the cursor on a file first.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.quickLook.open = true
	m.retrySketch()
	return m.quickLookShow(t)
}

func (m *OS) quickLookMenu(f fileMenuTarget) tea.Cmd {
	if !m.filesOn() {
		return nil
	}
	t := quickLookTarget{path: filepath.Join(f.Dir, f.Name), name: f.Name, dir: f.IsDir}
	m.quickLookPlace(&t)
	row := slices.IndexFunc(m.SidebarNav, func(r sidebarNavRow) bool {
		got, ok := m.quickLookRowTarget(r)
		return ok && got == t
	})
	if row < 0 {
		m.ShowNotification("The file is not in the list now.", "info", m.Settings.NotificationDuration)
		return nil
	}
	ql := &m.quickLook
	if !m.SidebarFocused {
		ql.borrowed = true
	}
	if !m.SidebarFocused || m.SidebarFocusOnSecondary() {
		m.EnterSidebarFocus()
		m.sidebarFollowSession = ""
	}
	m.sidebarSetCursor(row)
	ql.open = true
	m.retrySketch()
	return m.quickLookShow(t)
}

func (m *OS) quickLookPlace(t *quickLookTarget) {
	switch {
	case m.filesView.Host != "" && m.AttachedHost != "":
		t.refuse = "tuios cannot show a file on a host that you reach through another host."
	case m.filesView.Host != "":
		t.host = m.filesView.Host
	case m.AttachedHost != "":
		t.host = m.AttachedHost
	}
	if m.learnFiles() {
		t.refuse = "Quick look is not in this demo."
	}
}

// CloseQuickLook closes the preview for a key or a click. When Preview took the
// keyboard for the rail, the rail gives it back here, the way it does when esc
// leaves it, so the pane the person was typing in has the keys again.
func (m *OS) CloseQuickLook() {
	borrowed := m.quickLook.borrowed
	m.dropQuickLook()
	if borrowed {
		m.ExitSidebarFocus()
	}
}

func (m *OS) dropQuickLook() {
	ql := &m.quickLook
	ql.open = false
	ql.copy = nil
	ql.mouse, ql.held, ql.textW = CopyMouseGesture{}, false, 0
	ql.mdImgs, ql.mdImgsFor, ql.mdNeed = nil, nil, false
	ql.mdPics = ql.mdPics[:0]
	ql.target = quickLookTarget{}
	ql.borrowed = false
	ql.free, ql.wantLine = false, 0
	ql.res = nil
	ql.scroll = 0
	ql.jump = quickLookNoMove
	ql.inside = false
	ql.full = false
	ql.prompt, ql.query, ql.status = quickLookNoPrompt, "", ""
	ql.input.Set("")
	ql.wrapSet = false
	ql.box = overlay.Rect{}
	ql.pic = quickLookPic{}
	ql.mdLines = nil
	ql.framed = quickLookFrame{}
	m.quickLookDropPages()
	ql.view, ql.viewScroll, ql.details, ql.column, ql.mdOpen = "", 0, nil, 0, false
}

// QuickLookSync keeps the preview on the row under the cursor. The input
// path calls it after every key and click, and the loop after a resize. It
// is one comparison while the preview is closed.
func (m *OS) QuickLookSync() tea.Cmd {
	ql := &m.quickLook
	if !ql.open || ql.free {
		return nil
	}
	if !m.SidebarFocused || m.SidebarFocusOnSecondary() || !m.filesOn() {
		m.dropQuickLook()
		return nil
	}
	t, ok := m.quickLookCursorTarget()
	if !ok {
		m.dropQuickLook()
		return nil
	}
	if t != ql.target {
		return m.quickLookShow(t)
	}
	return nil
}

func (m *OS) quickLookCursorTarget() (quickLookTarget, bool) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return quickLookTarget{}, false
	}
	return m.quickLookRowTarget(row)
}

func (m *OS) quickLookRowTarget(row sidebarNavRow) (quickLookTarget, bool) {
	switch row.Kind {
	case sidebarRowFileUp:
		return quickLookTarget{up: true, name: ".."}, true
	case sidebarRowFileEntry:
	default:
		return quickLookTarget{}, false
	}
	var dir string
	var e fileEntry
	var ok bool
	if m.filesTreeOn() {
		dir, e, ok = m.fileTreeEntry(row.WindowID)
	} else if i := row.WindowIndex; i >= 0 && i < len(m.filesView.Entries) {
		dir, e, ok = m.filesView.Dir, m.filesView.Entries[i], true
	}
	if !ok {
		return quickLookTarget{}, false
	}
	t := quickLookTarget{path: filepath.Join(dir, e.Name), name: e.Name, dir: e.Dir}
	m.quickLookPlace(&t)
	return t, true
}

func (m *OS) quickLookShow(t quickLookTarget) tea.Cmd {
	ql := &m.quickLook
	if ql.target != t {
		m.quickLookDropPages()
		ql.view, ql.viewScroll, ql.details, ql.column, ql.mdOpen = "", 0, nil, 0, false
		ql.res = nil
	}
	ql.target = t
	ql.scroll = 0
	ql.jump = quickLookNoMove
	ql.inside = false
	ql.prompt, ql.query, ql.status = quickLookNoPrompt, "", ""
	ql.input.Set("")
	if !ql.wrapSet {
		ql.wrap = quickLookWrapsByDefault(t.name)
	}
	if t.up || t.refuse != "" {
		return nil
	}
	if ql.res != nil && ql.res.target == t {
		return nil
	}
	if ql.fetching {
		return nil
	}
	ql.fetching = true
	return m.quickLookFetch(t)
}

func (m *OS) quickLookFetch(t quickLookTarget) tea.Cmd {
	if t.dir {
		client, attached, origin := m.DaemonClient, m.AttachedHost, m.filesView.Origin
		return func() tea.Msg {
			l := readListing(client, attached, origin, t.path, true, 0, false)
			res := &quickLookResult{target: t, kind: "dir", entries: l.Entries, capped: l.Capped}
			if l.Err != "" {
				res.kind, res.note = "error", l.Err
			}
			return quickLookMsg{res: res}
		}
	}
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if t.host != "" {
		call = m.verbOnHostCaller()
	}
	ascii := overlay.UseASCII()
	ext := strings.ToLower(filepath.Ext(t.name))
	if isPDF, isFilm := ext == ".pdf", session.PreviewIsFilm(t.name); isPDF || isFilm || ext == ".gif" || ext == ".json" {
		noPicture := m.replyPreviewer().Mode() == composer.PreviewText
		want := session.PreviewWant{MaxPx: m.quickLookPagePx(), Words: isPDF && noPicture, NoFrame: isFilm && noPicture}
		if isPDF || ext == ".gif" {
			want.Page = 1
		}
		want.Format = ext == ".json"
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
			defer cancel()
			raw, err := quickLookWantReply(ctx, call, t, want)
			return quickLookMsg{res: quickLookDecode(t, raw, err, ascii)}
		}
	}
	return func() tea.Msg {
		var raw json.RawMessage
		var err error
		if t.host == "" {
			ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
			out, perr := session.PreviewLocalFile(ctx, t.path, quickLookMaxPx)
			cancel()
			if perr == nil {
				raw, err = jsonx.Marshal(out)
			} else {
				err = perr
			}
		} else {
			raw, err = call(t.host, "file-preview", map[string]any{"path": t.path, "max_px": quickLookMaxPx})
		}
		return quickLookMsg{res: quickLookDecode(t, raw, err, ascii)}
	}
}

type quickLookReply struct {
	Kind       string                        `json:"kind"`
	Size       int64                         `json:"size"`
	Mime       string                        `json:"mime"`
	Note       string                        `json:"note"`
	Text       string                        `json:"text"`
	Truncated  bool                          `json:"truncated"`
	Image      string                        `json:"image"`
	Width      int                           `json:"width"`
	Height     int                           `json:"height"`
	Pages      int                           `json:"pages"`
	DurationMs int64                         `json:"duration_ms"`
	Page       int                           `json:"page"`
	Words      *string                       `json:"words"`
	NoTool     string                        `json:"no_tool"`
	Formatted  bool                          `json:"formatted"`
	Entries    []session.PreviewArchiveEntry `json:"entries"`
	Capped     bool                          `json:"capped"`
	Head       string                        `json:"head"`
	AtMs       *int64                        `json:"at_ms"`
	VideoCodec string                        `json:"video_codec"`
	AudioCodec string                        `json:"audio_codec"`
	FPS        float64                       `json:"fps"`
}

func quickLookDecode(t quickLookTarget, raw json.RawMessage, err error, ascii bool) *quickLookResult {
	res := &quickLookResult{target: t}
	if err != nil {
		res.kind, res.note = "error", quickLookErrorText(t, err)
		return res
	}
	var r quickLookReply
	if jsonx.Unmarshal(raw, &r) != nil {
		res.kind, res.note = "error", "The answer could not be read."
		return res
	}
	res.size, res.mime, res.note = r.Size, r.Mime, r.Note
	res.width, res.height, res.pages, res.durationMs = r.Width, r.Height, r.Pages, r.DurationMs
	res.page = max(r.Page, 1)
	if r.Kind == "video" {
		res.facts = quickLookFilmFacts(&r)
		if r.AtMs != nil {
			res.atMs = *r.AtMs
		} else {
			res.atMs, res.older = r.DurationMs/20, r.Image != ""
		}
	}
	switch r.Kind {
	case "text", "svg":
		if isMarkdownName(t.name) {
			res.kind = "markdown"
			res.md, res.more = r.Text, r.Truncated
			if len(res.md) > quickLookMarkdownMax {
				cut := strings.LastIndexByte(res.md[:quickLookMarkdownMax], '\n')
				res.md, res.more = res.md[:max(cut, 0)], true
			}
			return res
		}
		res.kind = "text"
		res.formatted = r.Formatted
		text := r.Text
		if r.Truncated {
			if i := strings.LastIndexByte(text, '\n'); i >= 0 {
				text = text[:i+1]
			}
		}
		res.lines, res.spans, res.cutLong = quickLookLines(t.path, text, ascii)
		res.more = r.Truncated
		if comma, ok := quickLookIsCSV(t.name); ok {
			if res.table = quickLookParseTable(text, comma, r.Truncated, ascii); res.table != nil {
				res.kind = "table"
				return res
			}
		}
		res.endOff = int64(len(text))
		res.firstLine = 1
		res.parts = []quickLookPart{{lines: len(res.lines), bytes: res.endOff}}
	case "image", "pdf", "video":
		res.kind = r.Kind
		if r.Image != "" {
			if data, derr := base64.StdEncoding.DecodeString(r.Image); derr == nil {
				if img, ierr := composer.NewImage(data); ierr == nil && img.Width > 0 && img.Height > 0 {
					img.Name = t.name
					res.img = img
				}
			}
		}
		if r.Words != nil {
			if words := strings.Trim(*r.Words, " \f\r\n"); words != "" {
				res.lines, _, _ = quickLookLines("", words, ascii)
			} else {
				res.note = "This page has no words."
			}
		}
		if r.NoTool != "" {
			res.note = "This machine has no " + r.NoTool + "."
		}
		if res.img == nil && res.note == "" && res.lines == nil {
			res.note = "tuios cannot draw this picture."
		}
	case "dir":
		res.kind, res.note = "binary", "This is a folder."
	case "archive":
		res.kind = "archive"
		res.archive, res.unpacked = quickLookArchiveRows(r.Entries, ascii)
		res.archiveItems, res.capped = len(r.Entries), r.Capped
	default:
		res.kind = "binary"
		if res.note == "" && r.Head != "" {
			quickLookDecodeHead(res, r.Head)
		}
	}
	return res
}

func quickLookErrorText(t quickLookTarget, err error) string {
	if ve, ok := errors.AsType[*session.VerbCallError](err); ok && ve.Message != "" {
		return ve.Message
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "The file is not there now."
	case errors.Is(err, fs.ErrPermission):
		return "tuios has no permission to read the file."
	case t.host != "":
		return "tuios could not reach " + t.host + "."
	}
	return "tuios could not read the file."
}

func isMarkdownName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

func (m *OS) applyQuickLook(msg quickLookMsg) tea.Cmd {
	ql := &m.quickLook
	ql.fetching = false
	if !ql.open || msg.res == nil {
		return nil
	}
	if msg.res.target == ql.target {
		ql.res = msg.res
		if quickLookPaged(msg.res) || msg.res.kind == "pdf" {
			ql.pageWant = msg.res.page
			ql.quickLookHoldPage(msg.res)
		}
		ql.filmWant = msg.res.atMs
		if n := ql.wantLine; n > 0 {
			ql.wantLine = 0
			m.quickLookGoToLine(int64(n))
		}
		return nil
	}
	return m.quickLookShow(ql.target)
}

func (m *OS) quickLookSize() (innerW, bodyH int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	cw := m.ViewContentWidth()
	if cw <= 0 {
		cw = rw
	}
	clear := m.modalRoomWidth()
	if clear <= 0 {
		clear = rw
	}
	r := m.viewReserve()
	usable := max(rh-r.Top-r.Bottom, 1)
	ql := &m.quickLook
	switch {
	case ql.full:
		innerW = min(max(cw-4, 24), clear-4, rw-2)
		bodyH = min(max(usable-4, 3), max(rh-2, 1))
	case rw < quickLookSmallW || rh < quickLookSmallH:
		innerW = max(rw-2, 1)
		bodyH = max(rh-2, 1)
	default:
		capW, capH := quickLookMaxW, quickLookMaxH
		if session.PreviewShowsPicture(ql.target.name) {
			capW, capH = quickLookPicW, quickLookPicH
		}
		innerW = min(max(cw*4/5, 24), capW, clear-4, rw-2)
		bodyH = min(max(usable*4/5-2, 3), capH, max(rh-2, 1))
	}
	return max(innerW, overlay.MinDialogWidth), bodyH
}

func (m *OS) quickLookPicBox(img *composer.Image, innerW, bodyH int) (x, y, cols, rows int) {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	availW, availH := max(innerW-2, 1), max(bodyH-1, 1)
	if quickLookHasBar(m.quickLook.res) {
		availH = max(availH-1, 1)
	}
	if m.quickLookStripShows(bodyH) {
		_, rows := m.quickLookStripBox(innerW)
		availH = max(availH-rows-2, 1)
	}
	cols, rows = ckFitBox(img, availW, availH, float64(ch)/float64(cw), 1)
	if m.replyPreviewer().Mode() == composer.PreviewPixels && cols*cw > img.Width {
		f := float64(img.Width) / float64(cols*cw)
		cols, rows = max(int(float64(cols)*f+0.5), 1), max(int(float64(rows)*f+0.5), 1)
	}
	return 1 + (availW-cols)/2, 1 + (availH-rows)/2, cols, rows
}

func (m *OS) previewResizing() bool {
	return m.viewportResizing && time.Since(m.viewportResizeAt) <= resizeDeferralTimeout
}

func (m *OS) quickLookPicture() tea.Cmd {
	ql := &m.quickLook
	if !ql.open {
		return nil
	}
	r := ql.res
	if r != nil && r.kind == "markdown" && !m.previewResizing() {
		return m.quickLookMDImagesCmd()
	}
	if r == nil || r.img == nil || r.target != ql.target || m.previewResizing() {
		return nil
	}
	if m.replyPreviewer().Mode() == composer.PreviewText {
		return nil
	}
	innerW, bodyH := m.quickLookSize()
	_, _, cols, rows := m.quickLookPicBox(r.img, innerW, bodyH)
	cmd := m.quickLookPrepare(r.img, cols, rows)
	if m.quickLookStripShows(bodyH) {
		cols, rows = m.quickLookStripBox(innerW)
		for _, img := range ql.strip {
			if img != nil {
				cmd = tea.Batch(cmd, m.quickLookPrepare(img, cols, rows))
			}
		}
	}
	return cmd
}

func (m *OS) quickLookPrepare(img *composer.Image, cols, rows int) tea.Cmd {
	p := m.replyPreviewer()
	k := pixelsKey(img, cols, rows)
	if p.Mode() == composer.PreviewCells {
		k = quickLookCellsKey(p, img, cols, rows)
	}
	p.mu.Lock()
	if p.asked[k] {
		p.mu.Unlock()
		return nil
	}
	p.asked[k] = true
	p.mu.Unlock()
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	ground := m.overlayUI().Canvas
	return func() tea.Msg {
		return replyPreviewMsg{pics: map[previewKey]*previewPicture{k: preparePicture(img, k, cw, ch, ground)}}
	}
}

// QuickLookMouseClick handles a press while the preview is up. A press on
// the panel stops there. A press anywhere else closes the preview and goes
// on to what it was aimed at, so a click on a rail row still acts on it.
func (m *OS) QuickLookMouseClick(x, y int) bool {
	ql := &m.quickLook
	if !ql.open {
		return false
	}
	if ql.box.Contains(x, y) {
		ql.held = true
		if q := ql.copy; q != nil && q.cm.Implicit && !m.QuickLookCopyOnText(x, y) {
			m.QuickLookCopyEnd()
		}
		if !m.quickLookRulerClick(x, y) {
			m.quickLookBarClick(x, y)
			m.quickLookBlockClick(x, y)
		}
		return true
	}
	m.CloseQuickLook()
	return false
}

// QuickLookWheel scrolls the preview's text when the wheel turns over it.
func (m *OS) QuickLookWheel(x, y int, up bool) bool {
	ql := &m.quickLook
	if !ql.open || !ql.box.Contains(x, y) {
		return false
	}
	if m.quickLookCopyWheel(quickLookWheelStep * wheelDelta(up)) {
		return true
	}
	if up {
		m.QuickLookScroll(-quickLookWheelStep)
	} else {
		m.QuickLookScroll(quickLookWheelStep)
	}
	return true
}

func (m *OS) quickLookPlacement() (previewPlacement, bool) {
	pic := m.quickLook.pic
	if !m.quickLook.open || !pic.ok || pic.key.mode != composer.PreviewPixels ||
		m.AnyOverlayOpen() || m.ContextMenu != nil {
		return previewPlacement{}, false
	}
	return previewPlacement{key: pic.key, x: pic.x, y: pic.y, cols: pic.cols, rows: pic.rows}, true
}

func (m *OS) quickLookMDPlacements(want []previewPlacement) []previewPlacement {
	ql := &m.quickLook
	if !ql.open || len(ql.mdPics) == 0 || m.AnyOverlayOpen() || m.ContextMenu != nil {
		return want
	}
	for _, pl := range ql.mdPics {
		pl.x += ql.box.X0 + 1
		pl.y += ql.box.Y0 + 1
		want = append(want, pl)
	}
	return want
}

func (m *OS) quickLookStripPlacements(want []previewPlacement) []previewPlacement {
	if _, ok := m.quickLookPlacement(); !ok {
		return want
	}
	for _, pic := range m.quickLook.stripPics {
		if pic.key.mode == composer.PreviewPixels {
			want = append(want, previewPlacement{key: pic.key, x: pic.x, y: pic.y, cols: pic.cols, rows: pic.rows})
		}
	}
	return want
}

func (m *OS) quickLookLook(pal overlay.Palette) (ckLook, *diffview.Theme) {
	ql := &m.quickLook
	key := ckLookKey{theme: theme.Generation(), depth: pal.Depth, ascii: overlay.UseASCII(), plain: m.Settings.NoNerdFont,
		canvas: pal.Canvas, panel: pal.Panel, fg: pal.Fg, accent: pal.Accent, glyphs: theme.Glyphs()}
	if ql.dv == nil || ql.lookKey != key {
		ql.look = m.buildCockpitLook(pal)
		ql.dv = diffTheme(pal, pal.Canvas)
		ql.lookKey = key
	}
	return ql.look, ql.dv
}

func (m *OS) renderQuickLook() (string, int, int) {
	ql := &m.quickLook
	pal := m.overlayUI()
	innerW, bodyH := m.quickLookSize()
	l, dv := m.quickLookLook(pal)
	key := "space"
	if keys := m.KeybindRegistry.Keys(config.TableSidebarFiles, "file_quick_look"); len(keys) > 0 {
		key = keys[0]
	}
	var seekKey, insideKey string
	if keys := m.KeybindRegistry.Keys(config.TableQuickLook, "seek_down"); len(keys) > 0 {
		seekKey = keys[0]
	}
	if keys := m.KeybindRegistry.Keys(config.TableQuickLook, "go_inside"); len(keys) > 0 {
		insideKey = keys[0]
	}
	f := quickLookFrame{mdGen: ql.mdGen, mdOpen: ql.mdOpen, view: ql.view, viewScroll: ql.viewScroll, column: ql.column, details: ql.details, ok: true, look: ql.lookKey, res: ql.res, target: ql.target, scroll: ql.scroll, inside: ql.inside,
		prompt: ql.prompt, input: ql.input.String(), inputAt: ql.inputAt(), query: ql.query, status: ql.status, wrap: ql.wrap,
		innerW: innerW, bodyH: bodyH, key: key, seekKey: seekKey, insideKey: insideKey, border: m.Settings.OverlayBorder(),
		canvas: pal.Canvas, fgMute: pal.FgMute, frame: pal.Frame, accent: pal.Accent,
		accentBright: pal.AccentBright, fgDim: pal.FgDim}
	var content string
	var geo overlay.Geometry
	var pic quickLookPic
	if k := &ql.framed; ql.copy == nil && k.ok && k.look == f.look && k.res == f.res && k.target == f.target && k.scroll == f.scroll && k.inside == f.inside &&
		k.prompt == f.prompt && k.input == f.input && k.query == f.query && k.status == f.status && k.wrap == f.wrap && k.view == f.view && k.mdOpen == f.mdOpen && k.mdGen == f.mdGen && k.viewScroll == f.viewScroll && k.column == f.column && k.details == f.details &&
		k.innerW == f.innerW && k.bodyH == f.bodyH && k.key == f.key && k.seekKey == f.seekKey && k.insideKey == f.insideKey && k.border == f.border &&
		k.canvas == f.canvas && k.fgMute == f.fgMute && k.frame == f.frame && k.accent == f.accent &&
		k.accentBright == f.accentBright && k.fgDim == f.fgDim {
		content, geo = k.content, k.geo
	} else {
		ql.stripPics = ql.stripPics[:0]
		c := (*ckCanvas)(nil).reset(innerW, bodyH, ckColor(pal.Canvas))
		pic = m.drawQuickLook(c, l, dv, innerW, bodyH)
		title := ql.target.name
		if ql.target.dir {
			title += "/"
		}
		hints := m.quickLookHints(key)
		content, geo = overlay.Dialog{Title: title, Width: innerW, Body: c.render(), Hints: hints}.Render(pal)
		ql.framed = quickLookFrame{}
		if r := ql.res; ql.copy == nil && r != nil && r.target == ql.target && r.kind != "image" && r.kind != "pdf" && r.kind != "video" {
			f.scroll = ql.scroll
			f.viewScroll = ql.viewScroll
			f.content, f.geo = content, geo
			ql.framed = f
		}
	}
	x, y := m.overlayOrigin(overlayKindQuickLook, geo)
	ql.box = overlay.Rect{X0: x, Y0: y, X1: x + geo.Width, Y1: y + geo.Height}
	ql.pic = quickLookPic{}
	if pic.ok {
		pic.x += x + 1
		pic.y += y + 1
		ql.pic = pic
	}
	for i := range ql.stripPics {
		ql.stripPics[i].x += x + 1
		ql.stripPics[i].y += y + 1
	}
	return content, x, y
}

func (m *OS) drawQuickLook(c *ckCanvas, l ckLook, dv *diffview.Theme, w, h int) quickLookPic {
	ql := &m.quickLook
	ql.textW = 0
	ql.ruler = quickLookRuler{}
	ql.mdPics = ql.mdPics[:0]
	pal := l.pal
	t := ql.target
	center := func(y int, s string, warn bool) {
		ink := pal.FgDim
		if warn {
			ink = pal.Warn
		}
		lines := wrapPlain(s, max(w-4, 1))
		y = max(y-len(lines)/2, 1)
		for i, ln := range lines {
			seg := ckSeg{t: ln, fg: ink}
			c.text(max((w-ckWidth(seg))/2, 1), y+i, w-1, seg)
		}
	}
	mid := max(h/2, 1)
	switch {
	case t.up:
		center(mid, "Move the cursor to a file.", false)
		return quickLookPic{}
	case t.refuse != "":
		center(mid, t.refuse, true)
		return quickLookPic{}
	}
	r := ql.res
	if r == nil || r.target != t {
		c.text(1, 0, w-1, ckSeg{t: "Reading" + overlay.Ellipsis(), fg: pal.FgMute})
		return quickLookPic{}
	}

	bodyTop, bodyRows := 1, max(h-1, 0)
	if quickLookText(r) && m.quickLookSearchRow() {
		bodyRows = max(bodyRows-1, 1)
		quickLookDrawSearchRow(c, pal, ql, h-1, w)
	}
	meta := quickLookMeta(r)
	right := []ckSeg{}
	if ql.inside {
		right = append(right, ckSeg{t: "Inside the file", fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " "})
	}
	ql.pageBar = quickLookBarBox{}
	if len(m.quickLookViews()) > 1 {
		m.quickLookDrawTabs(c, pal, r, bodyTop, w)
		bodyTop, bodyRows = bodyTop+2, max(bodyRows-2, 1)
	}
	if ql.quickLookOffMain() {
		if st := ql.status; st != "" {
			c.text(1, h-1, w-1, ckSeg{t: st, fg: pal.FgDim})
			bodyRows = max(bodyRows-1, 1)
		}
		rx := c.textR(w, 0, right...)
		c.text(1, 0, rx-1, ckSeg{t: meta, fg: pal.FgMute})
		m.quickLookDrawView(c, pal, r, bodyTop, bodyRows, w)
		return quickLookPic{}
	}
	if r.table != nil {
		if s := quickLookColumnsShown(r.table, ql.column, w); s != "" {
			right = append(right, ckSeg{t: s, fg: pal.FgMute}, ckSeg{t: " "})
		}
	}
	if quickLookPaged(r) {
		right = append(right, ckSeg{t: quickLookPagePosition(r, ql.pageWant), fg: pal.Fg}, ckSeg{t: " "})
	}
	if quickLookFilm(r) && r.atMs != ql.filmWant && !r.older && m.replyPreviewer().Mode() != composer.PreviewText {
		right = append(right, ckSeg{t: "Reading", fg: pal.FgMute}, ckSeg{t: " "})
	}
	if quickLookHasBar(r) {
		bodyRows = max(bodyRows-1, 1)
		switch {
		case m.quickLookSearchRow():
			quickLookDrawSearchRow(c, pal, ql, h-1, w)
		case quickLookPaged(r):
			quickLookDrawBar(c, pal, ql, quickLookUnit(r)+" "+strconv.Itoa(ql.pageWant), strconv.Itoa(r.pages), int64(ql.pageWant-1), int64(r.pages-1), h-1, w)
		default:
			quickLookDrawBar(c, pal, ql, quickLookClock(ql.filmWant), quickLookClock(r.durationMs), ql.filmWant, r.durationMs, h-1, w)
		}
	}
	if pos := quickLookPosition(r, ql.scroll, bodyRows); pos != "" {
		right = append(right, ckSeg{t: pos, fg: pal.FgMute}, ckSeg{t: " "})
	}
	if t.host != "" {
		right = append(right, ckSeg{t: "on " + t.host, fg: pal.FgMute}, ckSeg{t: " "})
	}
	rx := c.textR(w, 0, right...)
	c.text(1, 0, rx-1, ckSeg{t: meta, fg: pal.FgMute})

	scrollTo := func(n int) int {
		ql.scroll = max(min(ql.scroll, n-bodyRows), 0)
		return ql.scroll
	}
	switch r.kind {
	case "text":
		codeX := 1
		gutter := 0
		if r.firstLine > 0 {
			gutter = len(strconv.Itoa(r.firstLine + len(r.lines) - 1))
			codeX = 1 + gutter + 2
		}
		codeW := max(w-codeX-2, 1)
		last := len(r.lines) - bodyRows
		if ql.wrap {
			last = quickLookLastWrapped(r.lines, bodyRows, codeW)
		}
		from := max(min(ql.scroll, last), 0)
		if ql.scroll > from && (!r.more || r.older || r.stopped) {
			ql.scroll = from
		}
		if ql.scroll < 0 && (r.startOff == 0 || r.older || r.stopped) {
			ql.scroll = 0
		}
		ql.textX, ql.textY, ql.textW, ql.bodyRows = codeX, bodyTop, codeW, bodyRows
		if q := ql.copy; q != nil {
			if q.w == codeW && q.h == bodyRows {
				m.drawQuickLookCopy(c, pal, dv, r, q, codeX, gutter, bodyTop)
				break
			}
			m.QuickLookCopyEnd()
		}
		m.drawQuickLookRuler(c, l, quickLookTextRuler(r, from), w-1, bodyTop, bodyRows)
		row := 0
		for n := from; row < bodyRows && n < len(r.lines); n++ {
			if r.firstLine > 0 {
				num := strconv.Itoa(r.firstLine + n)
				c.text(1+gutter-len(num), bodyTop+row, 0, ckSeg{t: num, fg: pal.FgMute})
			}
			var sp []diffview.Span
			if n < len(r.spans) {
				sp = r.spans[n]
			}
			if !ql.wrap {
				text := r.lines[n]
				if end := quickLookCutEnd(text, codeW); end < len(text) {
					ql.rowSpans = quickLookRowSpans(ql.rowSpans, sp, 0, end)
					sp, text = ql.rowSpans, text[:end]
				}
				c.draw(codeX, bodyTop+row, codeW, dv.Code(text, sp, diffview.Range{}, diffview.Context, false, 0, codeW))
				quickLookLightMatches(c, pal, ql.query, r.lines[n], codeX, bodyTop+row, codeW, 0)
				row++
				continue
			}
			rows := quickLookWrapRows(r.lines[n], codeW)
			at, col := 0, 0
			for k := 0; k < rows && row < bodyRows; k++ {
				end, cells := quickLookWrapNext(r.lines[n], at, codeW)
				ql.rowSpans = quickLookRowSpans(ql.rowSpans, sp, at, end)
				c.draw(codeX, bodyTop+row, codeW, dv.Code(r.lines[n][at:end], ql.rowSpans, diffview.Range{}, diffview.Context, false, 0, codeW))
				quickLookLightMatches(c, pal, ql.query, r.lines[n], codeX, bodyTop+row, cells, col)
				at, col = end, col+cells
				row++
			}
		}
	case "markdown":
		width := max(w-3, 4)
		ql.textX, ql.textY, ql.textW, ql.bodyRows = 1, bodyTop, width, bodyRows
		mk := quickLookMDKey{gen: ql.mdGen, res: r, width: width, rows: bodyRows, look: ql.lookKey, open: ql.mdOpen}
		if ql.mdKey != mk || ql.mdLines == nil {
			ql.mdLines, ql.mdKey = m.quickLookMDLayout(l, r, width, ql.mdOpen), mk
		}
		if q := ql.copy; q != nil && q.md != nil {
			if q.w == width && q.h == bodyRows {
				m.drawQuickLookMDCopy(c, q, 1, bodyTop)
				break
			}
			m.QuickLookCopyEnd()
		}
		from := scrollTo(len(ql.mdLines))
		for i := 0; i < bodyRows && from+i < len(ql.mdLines); i++ {
			drawCkLineAt(c, 1, bodyTop+i, width, ql.mdLines[from+i])
		}
		marks := quickLookMDHeadMarks(ql.mdHeadAt, ql.mdHeadLevel, len(ql.mdLines), bodyRows)
		m.drawQuickLookRuler(c, l, quickLookRuler{r: ckRuler{lines: len(ql.mdLines), start: from, marks: marks, track: true}}, w-1, bodyTop, bodyRows)
		m.quickLookMDMarks(ql.mdLines, from, bodyRows, bodyTop)
		ql.mdFrom = from
	case "table":
		quickLookDrawTable(c, pal, ql, r.table, bodyTop, bodyRows, w)
	case "archive":
		quickLookDrawArchive(c, pal, ql, r, bodyTop, bodyRows, w)
	case "hex":
		quickLookDrawHex(c, pal, ql, r, bodyTop, bodyRows, w)
	case "dir":
		if len(r.entries) == 0 {
			center(mid, "This folder is empty.", false)
			break
		}
		from := scrollTo(len(r.entries))
		for i := 0; i < bodyRows && from+i < len(r.entries); i++ {
			e := r.entries[from+i]
			name, ink := e.Name, pal.Fg
			if e.Dir {
				name, ink = name+"/", pal.AccentBright
			}
			c.text(2, bodyTop+i, w-1, ckSeg{t: name, fg: ink})
		}
	case "image", "pdf", "video":
		if r.img == nil && r.lines != nil {
			quickLookDrawPageWords(c, pal, r.lines, bodyTop, bodyRows, w)
			break
		}
		if r.kind == "video" && m.replyPreviewer().Mode() == composer.PreviewText {
			center(mid, "This terminal cannot draw the frame.", false)
			if r.facts != "" {
				center(mid+1, r.facts, false)
			}
			break
		}
		if r.img == nil {
			center(mid, r.note, false)
			break
		}
		return m.drawQuickLookPicture(c, l, r, w, h, center, mid)
	case "error":
		center(mid, r.note, true)
	default:
		note := r.note
		if note == "" {
			note = "This file is not text. It has no preview."
		}
		center(mid, note, false)
	}
	return quickLookPic{}
}

func (m *OS) drawQuickLookPicture(c *ckCanvas, l ckLook, r *quickLookResult, w, h int, center func(int, string, bool), mid int) quickLookPic {
	p := m.replyPreviewer()
	x, y, cols, rows := m.quickLookPicBox(r.img, w, h)
	if m.quickLookStripShows(h) {
		m.drawQuickLookStrip(c, w, h)
	}
	switch p.Mode() {
	case composer.PreviewText:
		center(mid, "This terminal cannot draw the picture.", false)
		return quickLookPic{}
	case composer.PreviewCells:
		k := quickLookCellsKey(p, r.img, cols, rows)
		p.mu.Lock()
		pic := p.pics[k]
		p.mu.Unlock()
		if pic != nil {
			for i, s := range pic.lines {
				c.draw(x, y+i, cols, s)
			}
		}
		return quickLookPic{ok: true, key: k, x: x, y: y, cols: cols, rows: rows}
	}
	return quickLookPic{ok: true, key: pixelsKey(r.img, cols, rows), x: x, y: y, cols: cols, rows: rows}
}

func quickLookMeta(r *quickLookResult) string {
	var parts []string
	switch r.kind {
	case "text":
		parts = append(parts, units.Bytes(r.size))
		switch {
		case r.older:
			parts = append(parts, "This machine runs an older tuios. It sends only the start of the file.")
		case r.stopped:
			parts = append(parts, "tuios could not read the rest of the file.")
		case !r.more && r.startOff == 0:
			parts = append(parts, plural.Count(len(r.lines), "line"))
		}
		if r.formatted {
			parts = append(parts, "Laid out for reading.")
		}
		if r.cutLong {
			parts = append(parts, "Long lines are cut.")
		}
	case "markdown":
		parts = append(parts, "Markdown", units.Bytes(r.size))
		if r.more {
			parts = append(parts, "Showing the start of the file.")
		}
	case "table", "archive", "hex":
		parts = quickLookViewMeta(r)
	case "dir":
		n := plural.Count(len(r.entries), "item")
		if r.capped {
			n = "More than " + n
		}
		parts = append(parts, "Folder", n)
	case "image":
		parts = append(parts, "Image")
		if r.width > 0 && r.height > 0 {
			parts = append(parts, strconv.Itoa(r.width)+" x "+strconv.Itoa(r.height))
		} else if r.img != nil {
			parts = append(parts, strconv.Itoa(r.img.Width)+" x "+strconv.Itoa(r.img.Height))
		}
		parts = append(parts, units.Bytes(r.size))
	case "pdf":
		parts = append(parts, "PDF")
		if r.pages == 1 {
			parts = append(parts, plural.Count(r.pages, "page"))
		}
		parts = append(parts, units.Bytes(r.size))
	case "video":
		parts = append(parts, "Film")
		if r.width > 0 && r.height > 0 {
			parts = append(parts, strconv.Itoa(r.width)+" x "+strconv.Itoa(r.height))
		} else if r.durationMs > 0 {
			s := r.durationMs / 1000
			parts = append(parts, strconv.FormatInt(s/60, 10)+":"+pad2(s%60))
		}
		parts = append(parts, units.Bytes(r.size))
	case "error":
		return "No preview"
	default:
		if r.mime != "" {
			mime, _, _ := strings.Cut(r.mime, ";")
			parts = append(parts, mime)
		}
		parts = append(parts, units.Bytes(r.size))
	}
	return strings.Join(parts, " · ")
}

func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

func drawCkLineAt(c *ckCanvas, x, y, width int, ln ckLine) {
	if ln.bg != nil {
		c.fill(x+ln.bgAt, y, width-ln.bgAt, 1, ln.bg)
	}
	if ln.bar != "" {
		c.text(x, y, x+width, ckSeg{t: ln.bar, fg: ln.barInk})
	}
	right := x + width
	if ln.right != nil {
		right = c.textR(x+width, y, ln.right...) - 1
	}
	if ln.raw != "" {
		c.draw(x+ln.indent, y, width-ln.indent, ln.raw)
		return
	}
	c.text(x+ln.indent, y, right, ln.segs...)
}

// SidebarCursorOnListing reports whether the rail has the keyboard and its
// cursor is on a row of the files section. It is SidebarCursorOnFile without
// the file actions switch, for the keys that read and change nothing.
func (m *OS) SidebarCursorOnListing() bool {
	if !m.SidebarFocused || !m.filesOn() {
		return false
	}
	row, ok := m.sidebarCursorRow()
	return ok && (row.Kind == sidebarRowFileEntry || row.Kind == sidebarRowFileUp)
}

func quickLookCellsKey(p *replyPreview, img *composer.Image, cols, rows int) previewKey {
	k := p.cellsKey(img, cols, rows)
	k.kind = mosaic.Half
	return k
}

func quickLookMoves(r *quickLookResult) bool {
	return r != nil && (r.kind == "text" || r.kind == "markdown" || r.kind == "dir" || r.kind == "table" || r.kind == "archive" || r.kind == "hex" || quickLookHasBar(r))
}

func (m *OS) quickLookHints(closeKey string) []overlay.Hint {
	ql := &m.quickLook
	reg := m.KeybindRegistry
	pair := func(keys func(string) []string, a, b string) string {
		ak, bk := keys(a), keys(b)
		if len(ak) == 0 || len(bk) == 0 {
			return ""
		}
		return ak[0] + " " + bk[0]
	}
	add := func(h []overlay.Hint, key, label string) []overlay.Hint {
		if key == "" {
			return h
		}
		return append(h, overlay.Hint{Key: key, Label: label})
	}
	first := func(keys []string) string {
		if len(keys) == 0 {
			return ""
		}
		return keys[0]
	}
	if ql.copy != nil && !ql.copy.cm.Implicit {
		return copyModeLegend(ql.copy.cm.State, m.copyModeExitKey())
	}
	if ql.free && !ql.inside {
		h := add(nil, "esc", "close")
		return add(h, first(reg.Keys(config.TableQuickLook, "full_size")), "full size")
	}
	if ql.inside {
		in := func(action string) []string { return reg.Keys(config.TableQuickLookInside, action) }
		h := add(nil, first(in("go_back")), "back to files")
		if ql.free {
			h, closeKey = add(nil, first(in("go_back")), "close"), ""
		}
		if ql.quickLookOffMain() {
			h = add(h, first(in("next_view")), "next view")
			h = add(h, pair(in, "line_down", "line_up"), "line")
			return add(h, closeKey, "close")
		}
		if ql.res != nil && ql.res.table != nil {
			h = add(h, pair(in, "column_left", "column_right"), "columns")
		}
		if quickLookHasDetails(ql) {
			h = add(h, first(in("open_details")), "open details")
		}
		if quickLookPaged(ql.res) {
			unit := strings.ToLower(quickLookUnit(ql.res))
			h = add(h, first(in("go_to")), "go to "+unit)
			h = add(h, pair(in, "line_down", "line_up"), unit)
			h = add(h, pair(in, "start", "end"), "first, last")
			h = add(h, first(in("full_size")), "full size")
			return add(h, closeKey, "close")
		}
		if quickLookFilm(ql.res) {
			h = add(h, pair(in, "line_down", "line_up"), "1 second")
			h = add(h, pair(in, "half_down", "half_up"), "skip 5%")
			if m.replyPreviewer().Mode() != composer.PreviewText {
				h = add(h, first(in("filmstrip")), "filmstrip")
			}
			h = add(h, pair(in, "start", "end"), "start, end")
			h = add(h, first(in("full_size")), "full size")
			return add(h, closeKey, "close")
		}
		if quickLookText(ql.res) {
			h = add(h, first(in("find")), "search")
			h = add(h, first(in("go_to")), "go to line")
			if ql.target.host == "" {
				h = add(h, first(in("open_editor")), "editor")
			}
			wrap := "wrap"
			if ql.wrap {
				wrap = "cut lines"
			}
			h = add(h, first(in("wrap")), wrap)
			if ql.res.kind == "text" {
				h = add(h, first(in(config.ActionCopyMode)), "select")
			}
		}
		h = add(h, pair(in, "line_down", "line_up"), "line")
		h = add(h, pair(in, "half_down", "half_up"), "half page")
		h = add(h, pair(in, "start", "end"), "start, end")
		h = add(h, first(in("full_size")), "full size")
		return add(h, closeKey, "close")
	}
	out := func(action string) []string { return reg.Keys(config.TableQuickLook, action) }
	h := add(nil, closeKey, "close")
	if quickLookMoves(ql.res) {
		label := "half page"
		if quickLookPaged(ql.res) {
			label = strings.ToLower(quickLookUnit(ql.res))
		}
		if quickLookFilm(ql.res) {
			label = "skip 5%"
		}
		h = add(h, pair(out, "seek_down", "seek_up"), label)
		if quickLookFilm(ql.res) && m.replyPreviewer().Mode() != composer.PreviewText {
			h = add(h, first(out("filmstrip")), "filmstrip")
		}
		h = add(h, first(out("go_inside")), "go inside")
	}
	h = append(h, overlay.Hint{Key: "↑↓", Label: "other files"})
	h = add(h, first(out("full_size")), "full size")
	if ql.res != nil && ql.res.kind != "dir" && ql.res.kind != "error" {
		h = add(h, first(out("next_view")), "next view")
	}
	return h
}

// QuickLookInside reports whether the keyboard is inside the previewed file.
func (m *OS) QuickLookInside() bool { return m.quickLook.open && m.quickLook.inside }

// QuickLookGoInside moves the keyboard inside the file. It reports false for a
// file with nothing to move through.
func (m *OS) QuickLookGoInside() bool {
	ql := &m.quickLook
	if !ql.open || !quickLookMoves(ql.res) {
		return false
	}
	ql.inside = true
	return true
}

// QuickLookGoBack moves the keyboard back to the list.
func (m *OS) QuickLookGoBack() { m.quickLook.inside = false }

// QuickLookFullSize fills the area of the panes, or goes back.
func (m *OS) QuickLookFullSize() { m.quickLook.full = !m.quickLook.full }

// QuickLookOpenTextForTest opens the preview on the row under the cursor with
// text as its result, with no read. It reports false off a file row.
func (m *OS) QuickLookOpenTextForTest(text string) bool {
	t, ok := m.quickLookCursorTarget()
	if !ok {
		return false
	}
	ql := &m.quickLook
	ql.open, ql.target = true, t
	lines, spans, _ := quickLookLines(t.path, text, false)
	ql.res = &quickLookResult{target: t, kind: "text", lines: lines, spans: spans, size: int64(len(text)), endOff: int64(len(text)), firstLine: 1}
	return true
}
