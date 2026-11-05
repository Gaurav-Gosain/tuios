package app

import (
	"image"
	"math"
	"time"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// A workspace switch slides. Going to a higher workspace number moves the
// workspace being left out to the left while the new one comes in from the
// right; a lower number goes the other way. Only the pane area moves: the
// rail, the dock and every panel are drawn where they always are.
//
// How it works. At the moment of the switch, before any state changes, the
// pane layers of the workspace being left are composed once more, with no
// chrome, and the pane area of that canvas is kept (slideSnapshot). The switch
// then happens at once, exactly as a cut does: focus, mode, subscriptions and
// keys are the new workspace's from the first frame. While the slide runs,
// every frame is composed as usual, with two changes in composeLayersIn: the
// pane layers are shifted and clipped through the same path a view of a
// larger session uses, and the kept cells of the old workspace are copied
// into the part of the pane area the new one has not reached. The new
// workspace is live while it slides; the old one is the frame it left.
//
// A second switch while one runs keeps what is on screen. Going straight back
// reverses from where the slide is, so nothing jumps. Going on to another
// workspace keeps the half and half pane area as the frame being left and
// slides it out. A jump over several workspaces is one slide.
//
// What it costs. Idle, nothing: no timer, no buffer. The frames come from the
// motion clock (motion.go), which exists only while something moves. A slide
// frame copies the kept cells, a row segment at a time, and blits the pane
// layers from the cells the compositor already holds. The snapshot is one
// compose without the rail or the dock, once per switch.
//
// When it is a cut. appearance.motion = none, calm mode, a tape or a remote
// command (MotionAllows), the screen saver, a first frame not yet drawn, a
// scratch group shown or hidden, and a client too slow to draw the slide: one
// whose frame rate gives the slide fewer than slideMinFrames frames, or whose
// frames take longer to compose, on average, than a slide frame lasts. An SSH or web
// client runs it at slideRemoteFPS, since each frame repaints the pane area
// over the network.

// Slide timing.
const (
	// slideDuration is how long a slide takes at animation_speed 100.
	slideDuration = 200 * time.Millisecond
	// slideRemoteFPS caps the frame rate for a client across a network.
	slideRemoteFPS = 30
	// slideMinFrames is the fewest frames a slide may be drawn in. Fewer
	// reads as a stutter, and a cut is better.
	slideMinFrames = 4
)

// workspaceSlide is this client's running slide. The zero value is idle.
type workspaceSlide struct {
	// dir is +1 when the content moves left (to a higher number), -1 right.
	dir int
	// from and to are the workspaces the slide goes between.
	from, to int
	// start is when it began, dur how long it runs, and at the progress it
	// started from: zero for a new slide, part way for one reversed.
	start time.Time
	dur   time.Duration
	at    float64
	// area is the pane area on screen the kept cells cover. A slide whose
	// pane area moves (a resize, the rail toggled) ends.
	area image.Rectangle
	// old is the pane area of the workspace being left. spare is the buffer
	// the next snapshot is taken into, so a retarget allocates nothing.
	old, spare *uv.Buffer
	// still turns the slide pass off while a snapshot is composed.
	still bool
	// cost is how long a frame takes to compose, smoothed over the last few
	// so one slow frame does not turn the next switch into a cut.
	cost time.Duration
}

// running reports whether a slide is drawing.
func (s *workspaceSlide) running() bool { return s.old != nil }

// end stops the slide and lets the kept cells go.
func (s *workspaceSlide) end() {
	s.old, s.spare = nil, nil
	s.start = time.Time{}
}

// slideEase is ease-out cubic: fast at first, settling onto the end with no
// overshoot.
func slideEase(p float64) float64 {
	p = min(max(p, 0), 1)
	q := 1 - p
	return 1 - q*q*q
}

// progress is how far the slide is at now, from 0 to 1, and whether it is
// done.
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

// slideFrameInterval is the motion clock's interval while a slide runs: 60
// frames a second, the session's own rate when that is lower, and at most
// slideRemoteFPS for a client across a network.
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

// noteFrameCost folds one frame's compose time into the smoothed cost.
func (s *workspaceSlide) noteFrameCost(d time.Duration) {
	s.cost += (d - s.cost) / 4
}

// slideRemote reports whether the client draws across a network.
func (m *OS) slideRemote() bool {
	return m.BrowserClient || (m.RemoteClient && !m.SSHIsLoopback)
}

// slideDur is the slide's length, scaled by appearance.animation_speed.
func (m *OS) slideDur() time.Duration {
	speed := m.Settings.AnimationSpeed
	if speed == 0 {
		speed = config.AnimationSpeedDefault
	}
	speed = min(max(speed, config.AnimationSpeedMin), config.AnimationSpeedMax)
	return slideDuration * config.AnimationSpeedDefault / time.Duration(speed)
}

// slideAllowed reports whether a switch made now slides.
func (m *OS) slideAllowed() bool {
	if !m.Settings.MotionAllows(config.MotionBasic) || m.screensaver.active || m.themeFx.lastFrame == "" {
		return false
	}
	frame := m.slideFrameInterval()
	if m.slideDur() < frame*slideMinFrames {
		return false
	}
	// A client whose frames already take longer than a slide frame would
	// draw the slide as a few jumps.
	return m.wsSlide.cost <= frame
}

// slideArea is the part of the screen the panes are drawn in.
func (m *OS) slideArea() image.Rectangle {
	if m.sessionView.on {
		return m.sessionView.clip
	}
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	area := image.Rect(left, top, left+m.GetContentWidth(), top+m.GetUsableHeight())
	return area.Intersect(image.Rect(0, 0, m.GetRenderWidth(), m.GetRenderHeight()))
}

// beginWorkspaceSlide starts the slide for a switch from one workspace to
// another. It runs before the switch changes any state, since what it keeps
// is the workspace being left.
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
			// Straight back: the workspace on its way in is the one leaving
			// now, from where it is.
			at = 1 - e
		default:
			// On to another: the pane area as it is drawn now leaves.
			mixed = true
		}
	}
	if !m.slideSnapshot(area, mixed) {
		s.end()
		return
	}
	s.dir, s.from, s.to = dir, from, to
	s.start, s.at, s.area = now, at, area
	s.dur = time.Duration(float64(m.slideDur()) * (1 - at))
	m.renderSkipped = false
}

// slideSnapshot composes the pane layers as they are now, with no chrome,
// and keeps the cells of area as the frame being left. mixed keeps the slide
// pass on, so a slide still running is kept as it is drawn.
func (m *OS) slideSnapshot(area image.Rectangle, mixed bool) bool {
	s := &m.wsSlide
	// The off-render compose clears the rail's hit rectangles and shimmer
	// spans, which the frame on screen still shows. Nothing is appended to
	// them on that path, so the slices are put back as they were.
	hits, rail := m.SidebarHits, m.motion.rail
	s.still = !mixed
	m.keepLayerCells = true
	canvas := m.GetCanvas(false)
	m.keepLayerCells = false
	s.still = false
	m.SidebarHits, m.motion.rail = hits, rail
	// The canvas no longer holds the frame on screen.
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

// slideFrame is one frame's slide: how far the new workspace's panes are
// shifted, the part of the pane area they show, and the part the old one
// keeps.
type slideFrame struct {
	area      image.Rectangle
	newShift  int
	newClip   image.Rectangle
	oldClip   image.Rectangle
	oldOrigin int
}

// slideAt works out the slide for a frame composed at now. It ends a slide
// that has run its course, or whose pane area moved, and then reports false:
// that frame is the new workspace whole.
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
	moved := int(math.Round(e * float64(w)))
	if moved >= w {
		s.end()
		return slideFrame{}, false
	}
	f := slideFrame{area: a}
	if s.dir > 0 {
		// The old frame leaves to the left: its columns from moved on are
		// at the left edge. The new one comes in behind it.
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

// drawSlideOld copies the kept cells of the old workspace into the part of
// the pane area it still holds, and repairs the wide glyphs cut at the seam.
func (m *OS) drawSlideOld(canvas *frameCanvas, f slideFrame) {
	old := m.wsSlide.old
	r := f.oldClip
	for y := r.Min.Y; y < r.Max.Y && y < len(canvas.Lines); y++ {
		line := canvas.Lines[y]
		src := old.Lines[y-f.area.Min.Y]
		copy(line[r.Min.X:r.Max.X], src[f.oldOrigin:f.oldOrigin+r.Dx()])
		// Both edges of the old part can cut a wide glyph: the seam with
		// the new workspace, and the pane area's own edge, where the old
		// frame's columns were cut.
		fixWideEdges(line, r.Min.X, r.Min.X)
		fixWideEdges(line, r.Max.X, r.Max.X)
	}
}

// expireWorkspaceSlide ends a slide that has run its course without its last
// frame being composed. It reports whether a slide was running, which asks
// the motion clock for a frame: the one at the end is the new workspace whole.
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
