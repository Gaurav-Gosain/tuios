package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The position in a film in quick look.
//
// ql.res holds the frame on the screen, at res.atMs, and filmWant the position the
// keys asked for. Update asks quickLookFilmRead after every message, so one read runs
// at a time and a held key or a drag reads where it ended. A read nobody wants now
// is cancelled on this machine. A frame is not kept when the position moves, because
// a seek is one run of ffmpeg and a film has no pages to come back to.
//
// t shows the filmstrip: seven small frames across the film, read one at a time
// after the main frame. They stay for as long as the file does.
//
// There is no playback. A host that cannot draw a frame gets no reads at all: the
// position moves and the panel shows the film's facts.
const (
	quickLookStripFrames  = 7
	quickLookStripPx      = 240
	quickLookStripMinBody = 22
	quickLookFilmPercent  = 5
	quickLookWheelPercent = 1
	// quickLookFilmEndMs is how far before the end G goes, where a frame still is.
	quickLookFilmEndMs = 500
	// quickLookFilmStrip marks a read that is for the filmstrip, in filmOut.
	quickLookFilmStrip = -1
)

// quickLookFilmMsg carries a finished frame back to the loop. strip is the filmstrip
// frame it is for, or quickLookFilmStrip for the main frame.
type quickLookFilmMsg struct {
	target    quickLookTarget
	atMs      int64
	strip     int
	res       *quickLookResult
	cancelled bool
}

// quickLookFilm reports whether r is a film with a position to move.
func quickLookFilm(r *quickLookResult) bool {
	return r != nil && r.kind == "video" && r.durationMs > 0
}

// quickLookHasBar reports whether r draws the bar on the last row of the body.
func quickLookHasBar(r *quickLookResult) bool { return quickLookPaged(r) || quickLookFilm(r) }

// quickLookFilmTo moves to a position, held to the film.
func (m *OS) quickLookFilmTo(atMs int64) bool {
	ql := &m.quickLook
	if !ql.open || !quickLookFilm(ql.res) {
		return false
	}
	if ql.res.older {
		ql.status, ql.statusLine = "This machine runs an older tuios. It cannot move in a film.", true
		return true
	}
	ql.filmWant = max(min(atMs, ql.res.durationMs-quickLookFilmEndMs), 0)
	ql.status = ""
	return true
}

// quickLookFilmPercentBy moves by a share of the film.
func (m *OS) quickLookFilmPercentBy(percent int64, forward bool) bool {
	ql := &m.quickLook
	d := ql.res.durationMs * percent / 100
	if !forward {
		d = -d
	}
	return m.quickLookFilmTo(ql.filmWant + d)
}

// quickLookClock is a time in a film: 0:42, or 1:02:03 from an hour on.
func quickLookClock(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return strconv.FormatInt(s/3600, 10) + ":" + pad2(s/60%60) + ":" + pad2(s%60)
	}
	return strconv.FormatInt(s/60, 10) + ":" + pad2(s%60)
}

// quickLookFilmRead starts the next frame read. Update asks after every message. It
// is nil unless a read is due, and only one read runs at a time.
func (m *OS) quickLookFilmRead() tea.Cmd {
	ql := &m.quickLook
	r := ql.res
	if !ql.open || !quickLookFilm(r) || r.target != ql.target || r.older ||
		m.replyPreviewer().Mode() == composer.PreviewText {
		return nil
	}
	want := ql.filmWant
	if ql.fetching {
		if r.atMs != want && ql.filmOut != want && ql.pageCancel != nil {
			ql.pageCancel()
		}
		return nil
	}
	if r.atMs != want {
		return m.quickLookReadFrame(want, quickLookFilmStrip, m.quickLookPagePx())
	}
	if !ql.stripOn {
		return nil
	}
	for i := range quickLookStripFrames {
		if ql.strip[i] == nil && !ql.stripFailed[i] {
			return m.quickLookReadFrame(r.durationMs*int64(2*i+1)/(2*quickLookStripFrames), i, quickLookStripPx)
		}
	}
	return nil
}

// quickLookReadFrame reads one frame off the loop.
func (m *OS) quickLookReadFrame(atMs int64, strip, maxPx int) tea.Cmd {
	ql := &m.quickLook
	t := ql.target
	ctx, cancel := context.WithTimeout(context.Background(), quickLookTimeout)
	var call func(host, verb string, params map[string]any) (json.RawMessage, error)
	if t.host != "" {
		call = m.verbOnHostCaller()
	}
	ql.fetching, ql.pageCancel = true, cancel
	ql.filmOut = atMs
	if strip != quickLookFilmStrip {
		ql.filmOut = quickLookFilmStrip
	}
	return func() tea.Msg {
		defer cancel()
		want := session.PreviewWant{MaxPx: maxPx, Seek: true, AtMs: atMs, DurationKnown: true}
		raw, err := quickLookWantReply(ctx, call, t, want)
		msg := quickLookFilmMsg{target: t, atMs: atMs, strip: strip, cancelled: errors.Is(ctx.Err(), context.Canceled)}
		if !msg.cancelled {
			msg.res = quickLookDecode(t, raw, err, false)
		}
		return msg
	}
}

// applyQuickLookFilm files a finished frame away, or asks for the row the cursor
// moved to while it ran.
func (m *OS) applyQuickLookFilm(msg quickLookFilmMsg) tea.Cmd {
	ql := &m.quickLook
	ql.fetching, ql.pageCancel = false, nil
	if !ql.open {
		return nil
	}
	if msg.target != ql.target {
		return m.quickLookShow(ql.target)
	}
	r := ql.res
	if msg.cancelled || msg.res == nil || !quickLookFilm(r) {
		return nil
	}
	if msg.strip != quickLookFilmStrip {
		ql.strip[msg.strip] = msg.res.img
		ql.stripFailed[msg.strip] = msg.res.img == nil
		return nil
	}
	n := *r
	n.atMs = msg.atMs
	if msg.res.img == nil {
		ql.status, ql.statusLine = "No frame could be read here.", true
	} else {
		n.img = msg.res.img
	}
	ql.res = &n
	return nil
}

// QuickLookFilmstrip shows the filmstrip, or hides it. A file that is not a film, or
// a host that cannot draw a frame, has none.
func (m *OS) QuickLookFilmstrip() {
	ql := &m.quickLook
	if !ql.open || !quickLookFilm(ql.res) || m.replyPreviewer().Mode() == composer.PreviewText {
		return
	}
	if _, bodyH := m.quickLookSize(); !ql.stripOn && bodyH < quickLookStripMinBody {
		ql.status, ql.statusLine = "The preview is too small for the filmstrip. Press f for full size.", true
		return
	}
	ql.stripOn = !ql.stripOn
	ql.status = ""
}

// quickLookStripShows reports whether the panel gives rows to the filmstrip now. A
// panel made smaller after t goes back to the main frame alone.
func (m *OS) quickLookStripShows(bodyH int) bool {
	ql := &m.quickLook
	return ql.stripOn && quickLookFilm(ql.res) && bodyH >= quickLookStripMinBody &&
		m.replyPreviewer().Mode() != composer.PreviewText
}

// quickLookStripBox is the cells of one filmstrip frame.
func (m *OS) quickLookStripBox(innerW int) (cols, rows int) {
	cw, ch := m.hostCellSize()
	if cw <= 0 || ch <= 0 {
		cw, ch = 8, 16
	}
	imgW, imgH := 16, 9
	if img := m.quickLook.res.img; img != nil && img.Width > 0 && img.Height > 0 {
		imgW, imgH = img.Width, img.Height
	}
	cols = max((innerW-2-(quickLookStripFrames-1))/quickLookStripFrames, 1)
	rows = max(min((cols*cw*imgH+imgW*ch/2)/(imgW*ch), 10), 1)
	return cols, rows
}

// drawQuickLookStrip draws the filmstrip on the rows above the bar.
func (m *OS) drawQuickLookStrip(c *ckCanvas, w, h int) {
	ql := &m.quickLook
	p := m.replyPreviewer()
	cols, rows := m.quickLookStripBox(w)
	top := h - 2 - rows
	for i, img := range ql.strip {
		if img == nil {
			continue
		}
		x := 1 + i*(cols+1)
		key := pixelsKey(img, cols, rows)
		if p.Mode() == composer.PreviewCells {
			key = quickLookCellsKey(p, img, cols, rows)
			p.mu.Lock()
			pic := p.pics[key]
			p.mu.Unlock()
			if pic != nil {
				for j, s := range pic.lines {
					c.draw(x, top+j, cols, s)
				}
			}
		}
		ql.stripPics = append(ql.stripPics, quickLookPic{ok: true, key: key, x: x, y: top, cols: cols, rows: rows})
	}
}

// quickLookFilmFacts is what the panel says of a film with no frame to draw.
func quickLookFilmFacts(r *quickLookReply) string {
	if r.VideoCodec == "" {
		return ""
	}
	s := quickLookCodecName(r.VideoCodec)
	if r.Width > 0 && r.Height > 0 {
		s += ", " + strconv.Itoa(r.Width) + " x " + strconv.Itoa(r.Height)
	}
	if r.FPS > 0 {
		s += ", " + strconv.FormatFloat(r.FPS, 'f', -1, 64) + " frames each second"
	}
	if r.AudioCodec == "" {
		return s + ". Audio: none."
	}
	return s + ". Audio: " + quickLookCodecName(r.AudioCodec) + "."
}

// quickLookCodecName is a codec as people say it.
func quickLookCodecName(codec string) string {
	switch codec {
	case "h264":
		return "H.264"
	case "hevc":
		return "H.265"
	case "aac", "av1", "vp8", "vp9", "mp3", "opus", "flac":
		return map[string]string{"aac": "AAC", "av1": "AV1", "vp8": "VP8", "vp9": "VP9", "mp3": "MP3", "opus": "Opus", "flac": "FLAC"}[codec]
	}
	return codec
}
