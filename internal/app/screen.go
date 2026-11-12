package app

import (
	"io"
	"os/exec"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
)

// A client with a screen writes its own frames: the program runs without
// Bubble Tea's renderer (WithOwnScreen), compose diffs the canvas into the
// screen, and View hands the screen the modes and the cursor. See
// internal/termio. Without a screen, View returns the frame as a string for
// Bubble Tea's renderer, as it always has.

// UseScreen gives this client a screen that writes to out. environ is the
// client terminal's environment, profile its colour profile, and o how its
// output is carried. The program must take WithOwnScreen(true).
func (m *OS) UseScreen(out io.Writer, environ []string, profile colorprofile.Profile, o termio.Output) {
	m.screen = termio.New(out, environ, profile, o)
}

// CloseScreen puts the client's terminal back once its program has ended.
func (m *OS) CloseScreen() {
	if m.screen != nil {
		m.screen.Close()
	}
}

// screenFrame takes a composed frame into the screen and returns the token
// View compares to decide whether the frame is new: it changes only when the
// frame changed a cell, as the frame string did.
func (m *OS) screenFrame() viewContent {
	canvas, frame := m.composeFrameParts()
	changed := false
	if canvas != nil {
		rows := m.frameRows
		if m.frameRowsAll {
			rows = nil
		}
		changed = m.screen.FrameRows(&canvas.Buffer, rows)
		m.frameRowsAll = false
		clear(m.frameRows)
		// The theme transition and the workspace slide copy the old frame
		// from the canvas (canvasIsFrame), so there is no text to keep.
		frame = ""
	} else {
		changed = m.screen.FrameString(frame, m.GetRenderWidth(), m.GetRenderHeight())
	}
	m.themeFx.lastText, m.themeFx.drawn = frame, true
	if changed {
		m.screenSeq++
	}
	return viewContent("\x00frame" + strconv.FormatUint(m.screenSeq, 10))
}

// screenReleaseMsg hands the terminal to a program execProcess runs, and
// screenBackMsg takes it back when the program ends, carrying the message
// the caller asked for.
type (
	screenReleaseMsg struct{}
	screenBackMsg    struct{ inner tea.Msg }
)

// execProcess is tea.ExecProcess for this client. Bubble Tea gives the
// terminal to the command by stopping its renderer; a client with a screen
// releases the screen first and takes it back after.
func (m *OS) execProcess(c *exec.Cmd, done tea.ExecCallback) tea.Cmd {
	if m.screen == nil {
		return tea.ExecProcess(c, done)
	}
	return tea.Sequence(
		func() tea.Msg { return screenReleaseMsg{} },
		tea.ExecProcess(c, func(err error) tea.Msg { return screenBackMsg{inner: done(err)} }),
	)
}

// takeScreenBack draws the whole screen again after the terminal was another
// program's.
func (m *OS) takeScreenBack() {
	if m.screen == nil {
		return
	}
	m.screen.Restore()
	m.MarkAllDirty()
	m.renderSkipped = false
}

// WatchScreenResize sends the console's new size to send whenever the
// terminal is resized, and returns what stops it. A client with no screen
// leaves this to Bubble Tea's renderer.
func (m *OS) WatchScreenResize(send func(tea.Msg)) (stop func()) {
	if m.screen == nil {
		return func() {}
	}
	return m.screen.WatchResize(send)
}
