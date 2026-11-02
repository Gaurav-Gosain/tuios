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
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"tuios.dev/shot/mosaic"
)

// Quick look: a preview of the file under the rail's files cursor, as
// Finder's space bar shows one.
//
// space on a row of the files section opens it; space or esc closes it. The
// preview follows the cursor: j and k walk the listing and the preview
// shows each file as the cursor reaches it, without being opened again. It
// is not a mode. J and K move inside the file, by half a page for text, and the
// wheel over the panel does too. Every other key goes to the rail as before, and
// the preview closes when the rail lets go of the keyboard, when the cursor
// leaves the files section, or on a click outside it.
//
// Tab moves the keyboard inside the file, where the scope takes every key and
// plain keys act on the file, so d there cannot reach the rail's delete. esc or
// Tab comes back. f fills the area of the panes. Inside a text, / searches, n and
// N go on, : goes to a line, and w wraps long lines. In a PDF, J and K and the wheel
// turn the page, : goes to a page, and a click on the bar goes there. See
// quicklook_pages.go.
//
// The files menu's Preview is the same preview with a way in from anywhere: it
// gives the rail the keyboard, moves the cursor to the row, and closing gives
// the keyboard back if the rail did not have it before.
//
// The content comes from the machine the file is on. A file of a listing on
// another machine goes to that machine's file-preview verb over the link,
// which reads it there and sends back the small result: 256 KiB of a text, an
// image scaled down, one page of a PDF, a frame of a film.
// A file on this machine goes through the same code in this process
// (session.PreviewLocalFile), so a client with no daemon has quick look too.
// A folder is read the way the rail reads one (readListing).
//
// What it draws: code highlighted by internal/diffview, Markdown laid out by
// the Inbox's renderer, a picture through the reply previews' pipeline (kitty
// graphics where the host has them, block glyphs from shot/mosaic where it
// does not, a line of words at 16 colours, or a PDF page's own words there), and for
// anything else its type
// and size.
//
// A text is read in windows on demand. See quicklook_text.go.
//
// Nothing runs while it is closed: no timer, no watch, no read. A read runs
// once per row the cursor stops on, and only one at a time: a key held down
// starts one read, and the read for the row the cursor ended on follows it. The
// same holds for a window of a text, where the next read goes to the position the
// keys ended on.
//
// A picture waits for a resize of the terminal to settle, then is scaled once
// for the size the person stopped at. See keepPicturesFrame for why the host
// still holds it then.

// overlayKindQuickLook names the panel's layer.
const overlayKindQuickLook = "quicklook"

const (
	// quickLookMarkdownMax bounds the Markdown the preview lays out.
	quickLookMarkdownMax = 64 << 10
	// quickLookMaxPx is the long side of a picture asked for. The largest panel
	// that is not full size is quickLookPicW cells wide.
	quickLookMaxPx = 1600
	// quickLookMaxW and quickLookMaxH bound the panel's inner size in cells for
	// reading, and quickLookPicW and quickLookPicH for a picture, unless the
	// person asks for full size.
	quickLookMaxW = 110
	quickLookMaxH = 48
	quickLookPicW = 180
	quickLookPicH = 60
	// A terminal under quickLookSmallW by quickLookSmallH gives the panel all of
	// its screen.
	quickLookSmallW = 60
	quickLookSmallH = 20
	// quickLookTimeout bounds a read on this machine. The tools a PDF or a
	// film needs have their own limit inside it.
	quickLookTimeout = 25 * time.Second
	// quickLookWheelStep is the lines one wheel notch scrolls.
	quickLookWheelStep = 3
	// quickLookPromptMax bounds what the search prompt holds.
	quickLookPromptMax = 256
)

// quickLookTarget is the row the preview is for.
type quickLookTarget struct {
	// host is the machine the file is on, "" for this one.
	host string
	path string
	name string
	dir  bool
	// up is the ".." row, which has nothing to show.
	up bool
	// refuse says why nothing can be read for the row.
	refuse string
}

// quickLookResult is one finished read.
type quickLookResult struct {
	target quickLookTarget
	// kind is text, markdown, image, pdf, video, dir, binary or error.
	kind string
	size int64
	mime string
	note string
	// lines and spans are the held part of a text and its highlighting. more
	// says the file holds more than the lines kept, and older says the machine
	// has no windows to send the rest of it.
	lines []string
	spans [][]diffview.Span
	more  bool
	older bool
	// stopped says a window could not be read, so no more are asked for.
	stopped bool
	// startOff and endOff are the bytes of the file the lines hold, and parts
	// the windows they came in, oldest in the file first, so whole windows can
	// be let go. firstLine is the line number of lines[0], 0 when it is not
	// known. cutLong says a line was cut.
	startOff, endOff int64
	parts            []quickLookPart
	firstLine        int
	cutLong          bool
	// md is a Markdown file's text.
	md string
	// img is the picture: an image, a PDF page or a film frame. width and
	// height are the file's own size, when the machine said it.
	img           *composer.Image
	width, height int
	pages         int
	durationMs    int64
	// page is the PDF page in img.
	page int
	// entries is a folder's listing.
	entries []fileEntry
	capped  bool
}

// quickLookMsg carries a finished read back to the loop.
type quickLookMsg struct{ res *quickLookResult }

// quickLookState is the open preview.
type quickLookState struct {
	open   bool
	target quickLookTarget
	// fetching says a read is out. The reply asks for the row the cursor is
	// on now when that is not the row it was read for.
	fetching bool
	res      *quickLookResult
	scroll   int
	// jump is the window a key asked for that is not read yet. inside says the
	// keyboard is inside the file, and full that the panel fills the panes.
	jump   quickLookMove
	inside bool
	full   bool
	// seek is the search or count a quickLookSeek read asks for. prompt and input
	// are the row at the bottom of a text being typed in, query the last search,
	// and status what the last search or count said. wrap says long lines wrap,
	// and wrapSet that the person chose it for this open.
	seek session.PreviewSeek
	// resume is where a search that ran out of its time goes on from, 0 for
	// none.
	resume int64
	prompt quickLookPromptKind
	input  string
	query  string
	status string
	// statusLine says status answers a go to line, so the search row leaves the
	// query out: the query stays for n, and it is not what the message is about.
	statusLine bool
	wrap       bool
	wrapSet    bool
	// hostOlder holds the machines whose file-preview verb has no windows, and
	// hostAsked those it was asked of, once per link.
	hostOlder map[string]bool
	hostAsked map[string]bool
	// borrowed says the files menu's Preview took the keyboard for the rail.
	// Closing the preview gives it back. See CloseQuickLook.
	borrowed bool
	// box is the panel on the screen in the last frame, for the mouse.
	box overlay.Rect
	// pic is where the picture went in the last frame.
	pic quickLookPic
	// pageWant is the PDF page the keys asked for, pageHeld the pages kept, pageOut
	// and pageCancel the read that is out, pageSlow that the link's last read was
	// slow, and pageBar the bar in the last frame. See quicklook_pages.go.
	pageWant   int
	pageHeld   []*quickLookResult
	pageOut    int
	pageCancel context.CancelFunc
	pageSlow   bool
	pageBar    quickLookBarBox
	// mdLines is the Markdown laid out, for mdKey.
	mdLines []ckLine
	mdKey   quickLookMDKey
	// look and dv are the colours the body is drawn in, for lookKey.
	look    ckLook
	dv      *diffview.Theme
	lookKey ckLookKey
	// framed is the last panel drawn, with what it was drawn from, so a frame
	// composed for another reason reuses it.
	framed quickLookFrame
}

// quickLookFrame is a drawn panel and its key. Where the panel is placed is
// not kept: the origin follows the screen and the rail on every render.
type quickLookFrame struct {
	ok            bool
	look          ckLookKey
	res           *quickLookResult
	target        quickLookTarget
	scroll        int
	inside        bool
	prompt        quickLookPromptKind
	input         string
	query         string
	status        string
	wrap          bool
	innerW, bodyH int
	key           string
	seekKey       string
	insideKey     string
	// What Dialog.Render reads from the palette, and the frame's glyphs.
	canvas, fgMute, frame, accent, accentBright, fgDim color.Color
	border                                             lipgloss.Border
	content                                            string
	geo                                                overlay.Geometry
}

// quickLookPic is the picture's box on the screen and how it is shown.
type quickLookPic struct {
	ok         bool
	key        previewKey
	x, y       int
	cols, rows int
}

// quickLookMDKey is what a Markdown layout was made for.
type quickLookMDKey struct {
	res   *quickLookResult
	width int
	look  ckLookKey
}

// QuickLookOpen reports whether the preview is up.
func (m *OS) QuickLookOpen() bool { return m.quickLook.open }

// SidebarQuickLook opens the preview of the row under the files cursor, or
// closes it when it is up.
func (m *OS) SidebarQuickLook() tea.Cmd {
	// From the files menu: the row the menu was opened on, which need not be
	// the cursor row. See fileMenuTarget.
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
	return m.quickLookShow(t)
}

// quickLookMenu opens the preview on the files menu's row. The preview follows
// the rail's cursor and reads keys through the rail, so the row takes both: the
// rail gets the keyboard, and the cursor moves to the row.
func (m *OS) quickLookMenu(f fileMenuTarget) tea.Cmd {
	if !m.filesOn() {
		return nil
	}
	t := quickLookTarget{path: filepath.Join(f.Dir, f.Name), name: f.Name, dir: f.IsDir}
	m.quickLookPlace(&t)
	// The listing can change between the right-click and the pick.
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
		// Focus can ask the next frame to put the cursor on the session.
		m.sidebarFollowSession = ""
	}
	m.sidebarSetCursor(row)
	ql.open = true
	return m.quickLookShow(t)
}

// quickLookPlace sets the machine t is on, or why it cannot be read.
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

// dropQuickLook takes the preview down and leaves the rail's focus alone. A
// read still out finishes and is dropped.
func (m *OS) dropQuickLook() {
	ql := &m.quickLook
	ql.open = false
	ql.target = quickLookTarget{}
	ql.borrowed = false
	ql.res = nil
	ql.scroll = 0
	ql.jump = quickLookNoMove
	ql.inside = false
	ql.full = false
	ql.prompt, ql.input, ql.query, ql.status = quickLookNoPrompt, "", "", ""
	ql.wrapSet = false
	ql.box = overlay.Rect{}
	ql.pic = quickLookPic{}
	ql.mdLines = nil
	ql.framed = quickLookFrame{}
	m.quickLookDropPages()
}

// QuickLookSync keeps the preview on the row under the cursor. The input
// path calls it after every key and click, and the loop after a resize. It
// is one comparison while the preview is closed.
func (m *OS) QuickLookSync() tea.Cmd {
	ql := &m.quickLook
	if !ql.open {
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

// quickLookCursorTarget is the row under the rail's cursor, when it is a row
// of the files section.
func (m *OS) quickLookCursorTarget() (quickLookTarget, bool) {
	row, ok := m.sidebarCursorRow()
	if !ok {
		return quickLookTarget{}, false
	}
	return m.quickLookRowTarget(row)
}

// quickLookRowTarget is what a rail row shows, when it is a row of the files
// section.
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

// quickLookShow points the preview at t and reads it, unless a read is out:
// the reply asks again for the row the cursor is on by then.
func (m *OS) quickLookShow(t quickLookTarget) tea.Cmd {
	ql := &m.quickLook
	if ql.target != t {
		m.quickLookDropPages()
		// The hints and keys ask what the file is, and this is not that file.
		ql.res = nil
	}
	ql.target = t
	ql.scroll = 0
	ql.jump = quickLookNoMove
	ql.inside = false
	ql.prompt, ql.input, ql.query, ql.status = quickLookNoPrompt, "", "", ""
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

// quickLookFetch reads t off the loop.
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
	if strings.EqualFold(filepath.Ext(t.name), ".pdf") {
		want := session.PreviewPDF{MaxPx: m.quickLookPagePx(), Page: 1, Words: m.replyPreviewer().Mode() == composer.PreviewText}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
			defer cancel()
			raw, err := quickLookPageReply(ctx, call, t, want)
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
				raw, err = json.Marshal(out)
			} else {
				err = perr
			}
		} else {
			raw, err = call(t.host, "file-preview", map[string]any{"path": t.path, "max_px": quickLookMaxPx})
		}
		return quickLookMsg{res: quickLookDecode(t, raw, err, ascii)}
	}
}

// quickLookReply is the part of a file-preview answer the preview reads.
type quickLookReply struct {
	Kind       string  `json:"kind"`
	Size       int64   `json:"size"`
	Mime       string  `json:"mime"`
	Note       string  `json:"note"`
	Text       string  `json:"text"`
	Truncated  bool    `json:"truncated"`
	Image      string  `json:"image"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Pages      int     `json:"pages"`
	DurationMs int64   `json:"duration_ms"`
	Page       int     `json:"page"`
	Words      *string `json:"words"`
	NoTool     string  `json:"no_tool"`
}

// quickLookDecode turns a file-preview answer into what the panel draws. It
// runs off the loop: the highlighting and the picture's header are read here.
func quickLookDecode(t quickLookTarget, raw json.RawMessage, err error, ascii bool) *quickLookResult {
	res := &quickLookResult{target: t}
	if err != nil {
		res.kind, res.note = "error", quickLookErrorText(t, err)
		return res
	}
	var r quickLookReply
	if json.Unmarshal(raw, &r) != nil {
		res.kind, res.note = "error", "The answer could not be read."
		return res
	}
	res.size, res.mime, res.note = r.Size, r.Mime, r.Note
	res.width, res.height, res.pages, res.durationMs = r.Width, r.Height, r.Pages, r.DurationMs
	res.page = max(r.Page, 1)
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
		text := r.Text
		if r.Truncated {
			// The last line is cut where the bytes ran out, and the next window
			// starts it again.
			if i := strings.LastIndexByte(text, '\n'); i >= 0 {
				text = text[:i+1]
			}
		}
		res.lines, res.spans, res.cutLong = quickLookLines(t.path, text, ascii)
		res.more = r.Truncated
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
	default:
		res.kind = "binary"
	}
	return res
}

// quickLookErrorText is a failed read in words a person can act on.
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

// isMarkdownName reports whether a file name is a Markdown file's.
func isMarkdownName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".mdown", ".mkd":
		return true
	}
	return false
}

// applyQuickLook files a finished read away, or asks for the row the cursor
// moved to while it ran.
func (m *OS) applyQuickLook(msg quickLookMsg) tea.Cmd {
	ql := &m.quickLook
	ql.fetching = false
	if !ql.open || msg.res == nil {
		return nil
	}
	if msg.res.target == ql.target {
		ql.res = msg.res
		if msg.res.kind == "pdf" {
			ql.pageWant = msg.res.page
			ql.quickLookHoldPage(msg.res)
		}
		return nil
	}
	return m.quickLookShow(ql.target)
}

// quickLookSize is the panel's inner width and its body's height, in cells:
// four fifths of the panes' area, within bounds that keep a long line
// readable and the panel inside a small screen.
func (m *OS) quickLookSize() (innerW, bodyH int) {
	rw, rh := m.GetRenderWidth(), m.GetRenderHeight()
	cw := m.ViewContentWidth()
	if cw <= 0 {
		cw = rw
	}
	r := m.viewReserve()
	usable := max(rh-r.Top-r.Bottom, 1)
	ql := &m.quickLook
	switch {
	case ql.full:
		innerW = min(max(cw-4, 24), rw-2)
		bodyH = min(max(usable-4, 3), max(rh-2, 1))
	case rw < quickLookSmallW || rh < quickLookSmallH:
		innerW = max(rw-2, 1)
		bodyH = max(rh-2, 1)
	default:
		capW, capH := quickLookMaxW, quickLookMaxH
		if session.PreviewShowsPicture(ql.target.name) {
			capW, capH = quickLookPicW, quickLookPicH
		}
		innerW = min(max(cw*4/5, 24), capW, rw-2)
		bodyH = min(max(usable*4/5-2, 3), capH, max(rh-2, 1))
	}
	return max(innerW, overlay.MinDialogWidth), bodyH
}

// quickLookPicBox is the picture's box in the body: the largest that holds
// its shape below the line that names it, centred.
func (m *OS) quickLookPicBox(img *composer.Image, innerW, bodyH int) (x, y, cols, rows int) {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	availW, availH := max(innerW-2, 1), max(bodyH-1, 1)
	if quickLookPaged(m.quickLook.res) {
		availH = max(availH-1, 1)
	}
	cols, rows = ckFitBox(img, availW, availH, float64(ch)/float64(cw), 1)
	// The host draws pixels at their own size at most: a small picture
	// stretched over the panel is a blur. Block glyphs are coarse anyway, so
	// they keep the whole box.
	if m.replyPreviewer().Mode() == composer.PreviewPixels && cols*cw > img.Width {
		f := float64(img.Width) / float64(cols*cw)
		cols, rows = max(int(float64(cols)*f+0.5), 1), max(int(float64(rows)*f+0.5), 1)
	}
	return 1 + (availW-cols)/2, 1 + (availH-rows)/2, cols, rows
}

// previewResizing says a resize of the terminal is still in flight. A preview's
// picture waits for it to settle, as a pane's images do: it is decoded and scaled
// once for the size the person stopped at, and a drag of the window's edge sends
// the host no picture at all. The time is the same guard the resize deferral has:
// a settle that never comes does not hide the picture for good.
func (m *OS) previewResizing() bool {
	return m.viewportResizing && time.Since(m.viewportResizeAt) <= resizeDeferralTimeout
}

// quickLookPicture prepares the picture at the size the panel shows it, when
// it is not ready. Update asks after every message, because more than input
// moves the box or the key: a settled resize, a session sized by another client,
// a theme. It is one comparison while the preview is closed, and nil when there
// is nothing to do.
func (m *OS) quickLookPicture() tea.Cmd {
	ql := &m.quickLook
	if !ql.open {
		return nil
	}
	r := ql.res
	if r == nil || r.img == nil || r.target != ql.target || m.previewResizing() {
		return nil
	}
	p := m.replyPreviewer()
	mode := p.Mode()
	if mode == composer.PreviewText {
		return nil
	}
	innerW, bodyH := m.quickLookSize()
	_, _, cols, rows := m.quickLookPicBox(r.img, innerW, bodyH)
	k := pixelsKey(r.img, cols, rows)
	if mode == composer.PreviewCells {
		k = quickLookCellsKey(p, r.img, cols, rows)
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
	img, ground := r.img, m.overlayUI().Canvas
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
		m.quickLookBarClick(x, y)
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
	if up {
		m.QuickLookScroll(-quickLookWheelStep)
	} else {
		m.QuickLookScroll(quickLookWheelStep) // the draw holds it to the end
	}
	return true
}

// quickLookPlacement is the picture as a kitty placement, when the host
// draws it and nothing is open over the panel.
func (m *OS) quickLookPlacement() (previewPlacement, bool) {
	pic := m.quickLook.pic
	if !m.quickLook.open || !pic.ok || pic.key.mode != composer.PreviewPixels ||
		m.AnyOverlayOpen() || m.ContextMenu != nil {
		return previewPlacement{}, false
	}
	return previewPlacement{key: pic.key, x: pic.x, y: pic.y, cols: pic.cols, rows: pic.rows}, true
}

// quickLookLook is the colours the body is drawn in, on the panel's ground.
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

// renderQuickLook draws the panel and places it. It records where the panel
// and its picture went, for the mouse and for the host's graphics.
func (m *OS) renderQuickLook() (string, int, int) {
	ql := &m.quickLook
	pal := m.overlayUI()
	innerW, bodyH := m.quickLookSize()
	l, dv := m.quickLookLook(pal)
	key := "space"
	if keys := m.KeybindRegistry.GetSidebarFilesKeys("file_quick_look"); len(keys) > 0 {
		key = keys[0]
	}
	var seekKey, insideKey string
	if keys := m.KeybindRegistry.GetQuickLookKeys("seek_down"); len(keys) > 0 {
		seekKey = keys[0]
	}
	if keys := m.KeybindRegistry.GetQuickLookKeys("go_inside"); len(keys) > 0 {
		insideKey = keys[0]
	}
	// The border is in the key: the frame glyphs are a package global set from
	// border_style every frame, outside the palette.
	f := quickLookFrame{ok: true, look: ql.lookKey, res: ql.res, target: ql.target, scroll: ql.scroll, inside: ql.inside,
		prompt: ql.prompt, input: ql.input, query: ql.query, status: ql.status, wrap: ql.wrap,
		innerW: innerW, bodyH: bodyH, key: key, seekKey: seekKey, insideKey: insideKey, border: m.Settings.OverlayBorder(),
		canvas: pal.Canvas, fgMute: pal.FgMute, frame: pal.Frame, accent: pal.Accent,
		accentBright: pal.AccentBright, fgDim: pal.FgDim}
	var content string
	var geo overlay.Geometry
	var pic quickLookPic
	if k := &ql.framed; k.ok && k.look == f.look && k.res == f.res && k.target == f.target && k.scroll == f.scroll && k.inside == f.inside &&
		k.prompt == f.prompt && k.input == f.input && k.query == f.query && k.status == f.status && k.wrap == f.wrap &&
		k.innerW == f.innerW && k.bodyH == f.bodyH && k.key == f.key && k.seekKey == f.seekKey && k.insideKey == f.insideKey && k.border == f.border &&
		k.canvas == f.canvas && k.fgMute == f.fgMute && k.frame == f.frame && k.accent == f.accent &&
		k.accentBright == f.accentBright && k.fgDim == f.fgDim {
		content, geo = k.content, k.geo
	} else {
		c := (*ckCanvas)(nil).reset(innerW, bodyH, ckColor(pal.Canvas))
		pic = m.drawQuickLook(c, l, dv, innerW, bodyH)
		title := ql.target.name
		if ql.target.dir {
			title += "/"
		}
		hints := m.quickLookHints(key)
		content, geo = overlay.Dialog{Title: title, Width: innerW, Body: c.render(), Hints: hints}.Render(pal)
		// Only a finished text, markdown, folder, binary or error body is kept;
		// a picture's cells arrive later.
		ql.framed = quickLookFrame{}
		if r := ql.res; r != nil && r.target == ql.target && r.kind != "image" && r.kind != "pdf" && r.kind != "video" {
			f.scroll = ql.scroll // after the draw clamped it
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
	return content, x, y
}

// drawQuickLook draws the body on c: a line that says what the file is, and
// below it the content. It returns the picture's box in the body.
func (m *OS) drawQuickLook(c *ckCanvas, l ckLook, dv *diffview.Theme, w, h int) quickLookPic {
	ql := &m.quickLook
	pal := l.pal
	t := ql.target
	// center draws a sentence in the middle of the body, wrapped: a refusal
	// names a path, and its reason is at the end.
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

	// The line that says what it is, and where when it is not here.
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
	if quickLookPaged(r) {
		right = append(right, ckSeg{t: quickLookPagePosition(r, ql.pageWant), fg: pal.Fg}, ckSeg{t: " "})
		bodyRows = max(bodyRows-1, 1)
		if m.quickLookSearchRow() {
			quickLookDrawSearchRow(c, pal, ql, h-1, w)
		} else {
			quickLookDrawPageBar(c, pal, ql, r, h-1, w)
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
		// While a window is on its way, a key past the held text keeps its place,
		// so a held key loses no presses: the panel shows the edge, and the read
		// brings the text up to the position.
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
		quickLookBar(c, pal, w-1, bodyTop, bodyRows, r, from)
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
			rows := 1
			if ql.wrap {
				rows = quickLookWrapRows(r.lines[n], codeW)
			}
			for k := 0; k < rows && row < bodyRows; k++ {
				// One cell more than the row, so a wrapped row ends in text and
				// not in the ellipsis of a cut line.
				width := codeW
				if ql.wrap {
					width = codeW + 1
				}
				c.draw(codeX, bodyTop+row, codeW, dv.Code(r.lines[n], sp, diffview.Range{}, diffview.Context, false, k*codeW, width))
				quickLookLightMatches(c, pal, ql.query, r.lines[n], codeX, bodyTop+row, codeW, k*codeW)
				row++
			}
		}
	case "markdown":
		width := max(w-3, 4)
		mk := quickLookMDKey{res: r, width: width, look: ql.lookKey}
		if ql.mdKey != mk || ql.mdLines == nil {
			ql.mdLines, ql.mdKey = m.ckMarkdown(l, r.md, width), mk
		}
		from := scrollTo(len(ql.mdLines))
		for i := 0; i < bodyRows && from+i < len(ql.mdLines); i++ {
			drawCkLineAt(c, 1, bodyTop+i, width, ql.mdLines[from+i])
		}
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

// drawQuickLookPicture draws the picture as the host shows pictures: glyph
// lines here, an empty box for kitty graphics placed after the frame, or a
// line of words at 16 colours.
func (m *OS) drawQuickLookPicture(c *ckCanvas, l ckLook, r *quickLookResult, w, h int, center func(int, string, bool), mid int) quickLookPic {
	p := m.replyPreviewer()
	x, y, cols, rows := m.quickLookPicBox(r.img, w, h)
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

// quickLookMeta is the line that says what the file is.
func quickLookMeta(r *quickLookResult) string {
	var parts []string
	switch r.kind {
	case "text":
		parts = append(parts, quickLookSize(r.size))
		switch {
		case r.older:
			parts = append(parts, "This machine runs an older tuios. It sends only the start of the file.")
		case r.stopped:
			parts = append(parts, "tuios could not read the rest of the file.")
		case !r.more && r.startOff == 0:
			parts = append(parts, plural.Count(len(r.lines), "line"))
		}
		if r.cutLong {
			parts = append(parts, "Long lines are cut.")
		}
	case "markdown":
		parts = append(parts, "Markdown", quickLookSize(r.size))
		if r.more {
			parts = append(parts, "Showing the start of the file.")
		}
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
		parts = append(parts, quickLookSize(r.size))
	case "pdf":
		parts = append(parts, "PDF")
		if r.pages == 1 {
			parts = append(parts, plural.Count(r.pages, "page"))
		}
		parts = append(parts, quickLookSize(r.size))
	case "video":
		parts = append(parts, "Film")
		if r.durationMs > 0 {
			s := r.durationMs / 1000
			parts = append(parts, strconv.FormatInt(s/60, 10)+":"+pad2(s%60))
		}
		parts = append(parts, quickLookSize(r.size))
	case "error":
		return "No preview"
	default:
		if r.mime != "" {
			mime, _, _ := strings.Cut(r.mime, ";")
			parts = append(parts, mime)
		}
		parts = append(parts, quickLookSize(r.size))
	}
	return strings.Join(parts, " · ")
}

// quickLookSize is a file size in words.
func quickLookSize(n int64) string {
	if n >= 1<<30 {
		return strconv.FormatFloat(float64(n)/(1<<30), 'f', 1, 64) + " GB"
	}
	return ckBytes(int(n))
}

// pad2 is n in two digits.
func pad2(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}

// drawCkLineAt draws one laid-out line at x, y on c, width cells wide: the
// Inbox's ckRender, on a canvas of more than one line.
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

// quickLookCellsKey is the key of a picture drawn as block glyphs in the
// preview. It takes half blocks whatever glyph set the panes use: a preview
// is mostly photos and screenshots, and two colours stacked in a cell, each
// the average of its half, draw a smooth picture where the finer sets pick
// a shape per cell and turn a gradient into a checker. The colours keep the
// host's depth, so at 256 colours mosaic still dithers to the palette.
func quickLookCellsKey(p *replyPreview, img *composer.Image, cols, rows int) previewKey {
	k := p.cellsKey(img, cols, rows)
	k.kind = mosaic.Half
	return k
}

// quickLookMoves reports whether r is something the keys scroll.
func quickLookMoves(r *quickLookResult) bool {
	return r != nil && (r.kind == "text" || r.kind == "markdown" || r.kind == "dir" || quickLookPaged(r))
}

// quickLookHints is the keys that work now, most useful first. The panel's
// footer drops from the end what does not fit.
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
	if ql.inside {
		in := reg.GetQuickLookInsideKeys
		h := add(nil, first(in("go_back")), "back to files")
		if quickLookPaged(ql.res) {
			h = add(h, first(in("go_to")), "go to page")
			h = add(h, pair(in, "line_down", "line_up"), "page")
			h = add(h, pair(in, "start", "end"), "first, last")
			h = add(h, first(in("full_size")), "full size")
			return add(h, closeKey, "close")
		}
		if quickLookText(ql.res) {
			h = add(h, first(in("find")), "search")
			h = add(h, first(in("go_to")), "go to line")
			wrap := "wrap"
			if ql.wrap {
				wrap = "cut lines"
			}
			h = add(h, first(in("wrap")), wrap)
		}
		h = add(h, pair(in, "line_down", "line_up"), "line")
		h = add(h, pair(in, "half_down", "half_up"), "half page")
		h = add(h, pair(in, "start", "end"), "start, end")
		h = add(h, first(in("full_size")), "full size")
		return add(h, closeKey, "close")
	}
	out := reg.GetQuickLookKeys
	h := add(nil, closeKey, "close")
	if quickLookMoves(ql.res) {
		label := "half page"
		if quickLookPaged(ql.res) {
			label = "page"
		}
		h = add(h, pair(out, "seek_down", "seek_up"), label)
		h = add(h, first(out("go_inside")), "go inside")
	}
	h = append(h, overlay.Hint{Key: "↑↓", Label: "other files"})
	return add(h, first(out("full_size")), "full size")
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
