package app

import (
	"image"
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const (
	slideRemoteFPS  = 30
	slideMinFrames  = 4
	slideCostFrames = 5
	slideSlowFrames = 3
)

type workspaceSlide struct {
	dir        int
	from, to   int
	start      time.Time
	dur        time.Duration
	at         float64
	area       image.Rectangle
	old, spare *uv.Buffer
	fade       bool
	still      bool
	costs      [slideCostFrames]time.Duration
	next       int
}

func (s *workspaceSlide) running() bool { return s.old != nil }

func (s *workspaceSlide) end() {
	s.old, s.spare = nil, nil
	s.start = time.Time{}
	s.fade = false
}

func slideEase(p float64) float64 {
	p = min(max(p, 0), 1)
	q := 1 - p
	return 1 - q*q*q
}

func (s *workspaceSlide) progress(now time.Time) (float64, bool) {
	if s.dur <= 0 {
		return 1, true
	}
	p := float64(now.Sub(s.start)) / float64(s.dur)
	if p >= 1 {
		return 1, true
	}
	return s.at + (1-s.at)*slideEase(p), false
}

func (m *OS) slideFrameInterval() time.Duration {
	frame := config.OverlayFadeFrame
	if fps := m.Settings.NormalFPS; fps > 0 {
		frame = max(frame, time.Second/time.Duration(fps))
	}
	if m.slideRemote() {
		frame = max(frame, time.Second/slideRemoteFPS)
	}
	return frame
}

func (s *workspaceSlide) noteFrameCost(d time.Duration) {
	s.costs[s.next] = d
	s.next = (s.next + 1) % slideCostFrames
}

func (s *workspaceSlide) tooSlow(frame time.Duration) bool {
	slow := 0
	for _, d := range s.costs {
		if d > frame {
			slow++
		}
	}
	return slow >= slideSlowFrames
}

func (m *OS) slideRemote() bool {
	return m.BrowserClient || (m.RemoteClient && !m.SSHIsLoopback)
}

func (m *OS) slideDur() time.Duration {
	return m.Settings.WorkspaceTransitionDuration()
}

func (m *OS) slideAllowed() bool {
	if m.Settings.WorkspaceTransitionStyle() == config.WorkspaceTransitionNone || m.screensaver.active || !m.themeFx.drawn {
		return false
	}
	if m.Settings.WorkspaceTransitionStyle() == config.WorkspaceTransitionFade && theme.Depth() == overlay.Depth16 {
		return false
	}
	frame := m.slideFrameInterval()
	if m.slideDur() < frame*slideMinFrames {
		return false
	}
	return !m.wsSlide.tooSlow(frame)
}

func (m *OS) slideArea() image.Rectangle {
	if m.sessionView.on {
		return m.sessionView.clip
	}
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	area := image.Rect(left, top, left+m.GetContentWidth(), top+m.GetUsableHeight())
	return area.Intersect(image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight()))
}

func (m *OS) beginWorkspaceSlide(from, to int) {
	s := &m.wsSlide
	if from == to || session.IsScratchWorkspace(from) || session.IsScratchWorkspace(to) || !m.slideAllowed() {
		s.end()
		return
	}
	area := m.slideArea()
	if area.Dx() < 2 || area.Dy() < 1 {
		s.end()
		return
	}
	now := time.Now()
	dir := 1
	if to < from {
		dir = -1
	}
	at := 0.0
	mixed := false
	if s.running() {
		e, done := s.progress(now)
		switch {
		case done || area != s.area:
		case to == s.from && from == s.to:
			at = 1 - e
		default:
			mixed = true
		}
	}
	if !m.slideSnapshot(area, mixed) {
		s.end()
		return
	}
	s.dir, s.from, s.to = dir, from, to
	s.fade = m.Settings.WorkspaceTransitionStyle() == config.WorkspaceTransitionFade
	s.start, s.at, s.area = now, at, area
	s.dur = time.Duration(float64(m.slideDur()) * (1 - at))
	m.renderSkipped = false
}

func (m *OS) slideSnapshot(area image.Rectangle, mixed bool) bool {
	s := &m.wsSlide
	hits, rail := m.SidebarHits, m.motion.rail
	s.still = !mixed
	m.keepLayerCells = true
	canvas := m.GetCanvas(false)
	m.keepLayerCells = false
	s.still = false
	m.SidebarHits, m.motion.rail = hits, rail
	m.canvasIsFrame = false
	if !area.In(canvas.Bounds()) {
		return false
	}
	buf := s.spare
	if buf == nil {
		buf = uv.NewBuffer(area.Dx(), area.Dy())
	} else {
		buf.Resize(area.Dx(), area.Dy())
	}
	for y := range area.Dy() {
		copy(buf.Lines[y], canvas.Lines[area.Min.Y+y][area.Min.X:area.Max.X])
	}
	s.spare, s.old = s.old, buf
	return true
}

type slideFrame struct {
	area      image.Rectangle
	newShift  int
	newClip   image.Rectangle
	oldClip   image.Rectangle
	oldOrigin int
	fade      uint8
	isFade    bool
}

func (m *OS) slideAt(now time.Time) (slideFrame, bool) {
	s := &m.wsSlide
	if !s.running() || s.still {
		return slideFrame{}, false
	}
	e, done := s.progress(now)
	if done || m.slideArea() != s.area {
		s.end()
		return slideFrame{}, false
	}
	a := s.area
	w := a.Dx()
	if s.fade {
		level := uint8(math.Round(e * themeMixLevels))
		if level >= themeMixLevels {
			s.end()
			return slideFrame{}, false
		}
		return slideFrame{area: a, newClip: a, fade: level, isFade: true}, true
	}
	moved := int(math.Round(e * float64(w)))
	if moved >= w {
		s.end()
		return slideFrame{}, false
	}
	f := slideFrame{area: a}
	if s.dir > 0 {
		f.oldClip = image.Rect(a.Min.X, a.Min.Y, a.Max.X-moved, a.Max.Y)
		f.oldOrigin = moved
		f.newShift = w - moved
		f.newClip = image.Rect(a.Max.X-moved, a.Min.Y, a.Max.X, a.Max.Y)
	} else {
		f.oldClip = image.Rect(a.Min.X+moved, a.Min.Y, a.Max.X, a.Max.Y)
		f.oldOrigin = 0
		f.newShift = -(w - moved)
		f.newClip = image.Rect(a.Min.X, a.Min.Y, a.Min.X+moved, a.Max.Y)
	}
	return f, true
}

func (m *OS) drawSlideOld(canvas *frameCanvas, f slideFrame) {
	old := m.wsSlide.old
	if f.isFade {
		a := f.area
		for y := a.Min.Y; y < a.Max.Y && y < len(canvas.Lines); y++ {
			line := canvas.Lines[y][a.Min.X:a.Max.X]
			src := old.Lines[y-a.Min.Y]
			for x := range line {
				m.themeBlendCell(&line[x], &src[x], f.fade)
			}
			fixWideEdges(line, 0, len(line))
		}
		return
	}
	r := f.oldClip
	for y := r.Min.Y; y < r.Max.Y && y < len(canvas.Lines); y++ {
		line := canvas.Lines[y]
		src := old.Lines[y-f.area.Min.Y]
		copy(line[r.Min.X:r.Max.X], src[f.oldOrigin:f.oldOrigin+r.Dx()])
		fixWideEdges(line, r.Min.X, r.Min.X)
		fixWideEdges(line, r.Max.X, r.Max.X)
	}
}

func (m *OS) expireWorkspaceSlide(now time.Time) bool {
	s := &m.wsSlide
	if !s.running() {
		return false
	}
	if now.Sub(s.start) >= s.dur {
		s.end()
	}
	return true
}
