package app

import (
	"context"
	"reflect"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/refreshrate"
)

// frameRate is what the model keeps to drive the program's frame ticker and
// to find the display's refresh rate for max_fps = "auto".
type frameRate struct {
	// program is the Bubble Tea program running this model, nil until
	// BindProgram. Every program in tuios is bound; a test model is not, and
	// for it everything here does nothing.
	program *tea.Program
	// applied is the rate the program's ticker was last set to, so a change
	// that leaves NormalFPS where it was does not touch the ticker.
	applied int
	// detecting is set while a detection is running, and detected once one
	// has returned. Only the Update goroutine reads or writes them.
	detecting, detected bool
}

// displayRateMsg carries the refresh rate a detection found, 0 for none.
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
}

// applyFrameRate sets the program's ticker to NormalFPS when it moved.
func (m *OS) applyFrameRate() {
	fps := m.Settings.NormalFPS
	if m.frameRate.program == nil || fps <= 0 || fps == m.frameRate.applied {
		return
	}
	if setProgramFPS(m.frameRate.program, fps) {
		m.frameRate.applied = fps
	}
}

// detectsDisplay reports whether this client may look for the display's
// refresh rate: a terminal on this machine, with max_fps set to auto. A served
// client's displays are on the far side of the connection.
func (m *OS) detectsDisplay() bool {
	return m.Settings.MaxFPSAuto && m.Client == ClientLocal && !m.LearnMode
}

// detectDisplayRate starts a detection in the background. again forces a new
// one even after a result came back, which only a config reload asks for: the
// person may have moved the window to another screen or changed its mode.
// Otherwise the first result is kept for the life of the client.
//
// It never runs on the Update goroutine and never holds up a frame: the answer
// arrives as a displayRateMsg, and until it does auto draws at the default.
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

// handleDisplayRate records a detection's answer and moves the frame rate to
// it when max_fps is still auto.
func (m *OS) handleDisplayRate(msg displayRateMsg) {
	m.frameRate.detecting = false
	m.frameRate.detected = true
	// Not logged: the client's log is the terminal unless --debug sent it to
	// a file, and the settings row already says what auto picked.
	m.Settings.DisplayFPS = msg.hz
	if m.Settings.MaxFPSAuto {
		m.Settings.NormalFPS = config.AutoFPS(msg.hz)
	}
	m.applyFrameRate()
	// The settings row shows the rate auto picked.
	m.MarkAllDirty()
}

// setProgramFPS sets the rate of a Bubble Tea program's frame ticker, which
// Bubble Tea keeps unexported and clamps to 120.
//
// It reaches the two fields by name: fps, which the ticker is started from,
// and ticker, which is nil until Run. A Bubble Tea release that renames
// either makes this return false and the program keeps the rate it was given
// at NewProgram, which is the rate tuios had before. TestSetProgramFPS fails on
// that release so the loss is not silent.
//
// Safe to call before Run, and from the Update goroutine during it: Run starts
// the ticker on that goroutine before the first Update, and a time.Ticker
// may be reset while another goroutine receives from it.
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
	tick := v.FieldByName("ticker")
	if tick.IsValid() && tick.Type() == reflect.TypeFor[*time.Ticker]() {
		// #nosec G103 - the field's own address, checked above to be a *time.Ticker.
		if ticker := *(**time.Ticker)(unsafe.Pointer(tick.UnsafeAddr())); ticker != nil {
			ticker.Reset(time.Second / time.Duration(fps))
		}
	}
	return true
}
