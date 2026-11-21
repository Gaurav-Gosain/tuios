package app

import (
	"context"
	"reflect"
	"sync/atomic"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/memtrim"
	"github.com/Gaurav-Gosain/tuios/internal/refreshrate"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type frameRate struct {
	program             *tea.Program
	applied             int
	detecting, detected bool
	idle                bool
	lastFrame           time.Time
	burst               *atomic.Bool
	lastCompose         time.Time
	dueArmed            bool
	lastAnswered        time.Time
	lastCursor          tea.Cursor
	kickWanted          bool
}

func cursorState(c *tea.Cursor) tea.Cursor {
	if c == nil {
		return tea.Cursor{}
	}
	return *c
}

const answerWindow = 50 * time.Millisecond

func (m *OS) noteAnsweredInput(msg tea.Msg) {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
		m.frameRate.lastAnswered = time.Now()
	}
}

type frameDueMsg struct{}

const idleTickerAfter = 500 * time.Millisecond

const (
	wakeTick  = time.Millisecond
	wakeBurst = 4 * time.Millisecond
)

type displayRateMsg struct{ hz int }

// BindProgram ties the model to the program that runs it, which is what lets
// the frame rate change while it runs. Call it after tea.NewProgram and before
// Run.
//
// Bubble Tea fixes its frame ticker at NewProgram and clamps it to 120. Here
// the ticker is set to the session's NormalFPS past that clamp, and again each
// time NormalFPS changes: a config reload, the settings row, or the display
// rate arriving for "auto". Before this, raising max_fps took effect only at
// the next start.
func (m *OS) BindProgram(p *tea.Program) {
	m.frameRate.program = p
	m.applyFrameRate()
	m.detectDisplayRate(false)
	dumpTickStatsOnSignal(p)
}

func (m *OS) applyFrameRate() {
	fps := m.Settings.NormalFPS
	m.setPaneFrameInterval()
	if m.frameRate.program == nil || fps <= 0 || fps == m.frameRate.applied {
		return
	}
	if setProgramFPS(m.frameRate.program, fps) {
		m.frameRate.applied = fps
		m.frameRate.idle = false
		m.frameRate.lastFrame = time.Now()
	}
}

func (m *OS) idleFrameTicker(now time.Time) {
	fr := &m.frameRate
	if fr.idle || fr.program == nil || now.Sub(fr.lastFrame) < idleTickerAfter {
		return
	}
	ticker := programTicker(fr.program)
	if ticker == nil {
		fr.idle = true
		return
	}
	if fr.burst != nil {
		fr.burst.Store(true)
		fr.burst = nil
	}
	ticker.Stop()
	sendTick(ticker)
	fr.idle = true
	m.tickStats.TickerSleeps++
	memtrim.Request()
}

func (m *OS) rendererRestarted(msg tea.Msg) {
	switch msg.(type) {
	case tea.ResumeMsg, hostTerminalBackMsg:
		m.frameRate.idle = false
		m.frameRate.lastFrame = time.Now()
	}
	if _, ok := msg.(tea.ResumeMsg); ok {
		m.takeScreenBack()
	}
}

func (m *OS) flushWhileIdle(msg tea.Msg) bool {
	if !m.frameRate.idle {
		return false
	}
	if _, tick := msg.(TickerMsg); tick && m.renderSkipped {
		return m.frameRate.kickWanted
	}
	return true
}

func (m *OS) noteFrame() {
	fr := &m.frameRate
	if fr.program == nil {
		return
	}
	fr.lastFrame = time.Now()
	if !fr.idle {
		return
	}
	fr.idle = false
	fps := fr.applied
	if !setProgramFPS(fr.program, fps) {
		return
	}
	m.tickStats.TickerWakes++
	ticker := programTicker(fr.program)
	if ticker == nil {
		fr.idle = true
		return
	}
	ticker.Reset(wakeTick)
	cancel := new(atomic.Bool)
	fr.burst = cancel
	time.AfterFunc(wakeBurst, func() {
		if !cancel.Load() {
			ticker.Reset(time.Second / time.Duration(fps))
		}
	})
}

func (m *OS) setPaneFrameInterval() {
	if m.frameRate.program == nil {
		return
	}
	period := m.framePeriod()
	for _, w := range m.Windows {
		if w != nil {
			w.SetFrameInterval(period)
		}
	}
}

func (m *OS) framePeriod() time.Duration {
	fps := m.Settings.NormalFPS
	if fps <= 0 {
		fps = config.DefaultFPS
	}
	return time.Second / time.Duration(fps)
}

func (m *OS) paneFrameWait(now time.Time) time.Duration {
	last := m.frameRate.lastCompose
	if last.IsZero() || now.Sub(m.frameRate.lastAnswered) < answerWindow {
		return 0
	}
	period := m.framePeriod()
	slack := terminal.FrameSlack(period)
	wait := last.Add(period).Sub(now)
	if wait <= slack {
		return 0
	}
	return wait - slack
}

func (m *OS) takePaneOutput(now time.Time) (open, changed bool, due tea.Cmd) {
	if wait := m.paneFrameWait(now); wait > 0 {
		if m.anyPaneOutput() {
			due = m.armFrameDue(wait)
		}
		return false, false, due
	}
	changed = m.MarkTerminalsWithNewContent()
	if changed {
		m.frameRate.lastCompose = terminal.NextFrameTime(m.frameRate.lastCompose, m.framePeriod(), now)
	}
	return true, changed, nil
}

func (m *OS) anyPaneOutput() bool {
	if m.sidebarSessionOutputPending() {
		return true
	}
	for _, w := range m.Windows {
		if w != nil && (w.HasNewOutput.Load() || w.HasGraphicsOutput.Load()) {
			return true
		}
	}
	return false
}

func (m *OS) armFrameDue(wait time.Duration) tea.Cmd {
	if m.frameRate.dueArmed {
		return nil
	}
	m.frameRate.dueArmed = true
	return tea.Tick(wait, func(time.Time) tea.Msg { return frameDueMsg{} })
}

func (m *OS) kickFlush() {
	if m.frameRate.program != nil {
		m.frameRate.kickWanted = true
	}
}

// RequestFlush asks for the next frame to be written now, for a host model
// that draws around this one: tuios tutor's card can change while the panes
// do not, and a bound program writes no frame nobody asked for. Return the
// command from the host's Update, and pass the message it makes back to
// this model's Update.
func (m *OS) RequestFlush() tea.Cmd {
	m.kickFlush()
	return flushCmd
}

type flushMsg struct{}

func flushCmd() tea.Msg { return flushMsg{} }

func (m *OS) handleFlush() {
	if m.screen != nil {
		m.screen.Commit()
	}
	if !m.frameRate.kickWanted && !m.frameRate.idle {
		return
	}
	m.frameRate.kickWanted = false
	if ticker := programTicker(m.frameRate.program); ticker != nil {
		sendTick(ticker)
	}
}

func sendTick(ticker *time.Ticker) {
	// #nosec G103 - a timer channel is a buffered chan time.Time; its
	ch := *(*chan time.Time)(unsafe.Pointer(&ticker.C))
	select {
	case ch <- time.Now():
	default:
	}
}

func (m *OS) detectsDisplay() bool {
	return m.Settings.MaxFPSAuto && m.Client == ClientLocal && !m.LearnMode
}

func (m *OS) detectDisplayRate(again bool) {
	p := m.frameRate.program
	if p == nil || !m.detectsDisplay() || m.frameRate.detecting {
		return
	}
	if m.frameRate.detected && !again {
		return
	}
	probe := refreshrate.System()
	m.frameRate.detecting = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), refreshrate.Timeout(probe.GOOS))
		hz := probe.Detect(ctx)
		cancel()
		p.Send(displayRateMsg{hz: hz})
	}()
}

func (m *OS) handleDisplayRate(msg displayRateMsg) {
	m.frameRate.detecting = false
	m.frameRate.detected = true
	m.Settings.DisplayFPS = msg.hz
	if m.Settings.MaxFPSAuto {
		m.Settings.NormalFPS = config.AutoFPS(msg.hz)
	}
	m.applyFrameRate()
	m.MarkAllDirty()
}

func setProgramFPS(p *tea.Program, fps int) bool {
	if p == nil || fps <= 0 {
		return false
	}
	v := reflect.ValueOf(p).Elem()
	rate := v.FieldByName("fps")
	if !rate.IsValid() || rate.Kind() != reflect.Int {
		return false
	}
	// #nosec G103 - the field's own address, checked above to be an int.
	*(*int)(unsafe.Pointer(rate.UnsafeAddr())) = fps
	if ticker := programTicker(p); ticker != nil {
		ticker.Reset(time.Second / time.Duration(fps))
	}
	return true
}

func programTicker(p *tea.Program) *time.Ticker {
	tick := reflect.ValueOf(p).Elem().FieldByName("ticker")
	if !tick.IsValid() || tick.Type() != reflect.TypeFor[*time.Ticker]() {
		return nil
	}
	// #nosec G103 - the field's own address, checked above to be a *time.Ticker.
	return *(**time.Ticker)(unsafe.Pointer(tick.UnsafeAddr()))
}
