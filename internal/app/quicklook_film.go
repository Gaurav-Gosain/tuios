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

const (
	quickLookStripFrames  = 7
	quickLookStripPx      = 240
	quickLookStripMinBody = 22
	quickLookFilmPercent  = 5
	quickLookWheelPercent = 1
	quickLookFilmEndMs    = 500
	quickLookFilmStrip    = -1
)

type quickLookFilmMsg struct {
	target    quickLookTarget
	atMs      int64
	strip     int
	res       *quickLookResult
	cancelled bool
}

func quickLookFilm(r *quickLookResult) bool {
	return r != nil && r.kind == "video" && r.durationMs > 0
}

func quickLookHasBar(r *quickLookResult) bool { return quickLookPaged(r) || quickLookFilm(r) }

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

func (m *OS) quickLookFilmPercentBy(percent int64, forward bool) bool {
	ql := &m.quickLook
	d := ql.res.durationMs * percent / 100
	if !forward {
		d = -d
	}
	return m.quickLookFilmTo(ql.filmWant + d)
}

func quickLookClock(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return strconv.FormatInt(s/3600, 10) + ":" + pad2(s/60%60) + ":" + pad2(s%60)
	}
	return strconv.FormatInt(s/60, 10) + ":" + pad2(s%60)
}

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

func (m *OS) quickLookStripShows(bodyH int) bool {
	ql := &m.quickLook
	return ql.stripOn && quickLookFilm(ql.res) && bodyH >= quickLookStripMinBody &&
		m.replyPreviewer().Mode() != composer.PreviewText
}

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
