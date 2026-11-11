package app

import (
	"io"
	"os"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/termio"
	"github.com/charmbracelet/colorprofile"
)

// A client with a screen writes its own frames: the program runs without
// Bubble Tea's renderer (WithOwnScreen), compose diffs the canvas into the
// screen, and View hands the screen the modes and the cursor. See
// internal/termio. Without a screen, View returns the frame as a string for
// Bubble Tea's renderer, as it always has.

// OwnScreenWanted reports whether a served client draws through a screen of
// its own. TUIOS_CELL_FRAME=0 keeps Bubble Tea's renderer.
func OwnScreenWanted() bool { return os.Getenv("TUIOS_CELL_FRAME") != "0" }

// UseScreen gives this client a screen that writes to out. environ is the
// client terminal's environment and profile its colour profile. The program
// must take WithOwnScreen(true).
func (m *OS) UseScreen(out io.Writer, environ []string, profile colorprofile.Profile) {
	m.screen = termio.New(out, environ, profile)
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
func (m *OS) screenFrame() string {
	canvas, frame := m.composeFrameParts()
	changed := false
	if canvas != nil {
		changed = m.screen.Frame(&canvas.Buffer)
		// The theme transition and the workspace slide copy the old frame
		// from the canvas (canvasIsFrame); the string is only their way of
		// asking whether anything was drawn yet.
		frame = "canvas"
	} else {
		changed = m.screen.FrameString(frame, m.GetRenderWidth(), m.GetRenderHeight())
	}
	m.themeFx.lastFrame = frame
	if changed {
		m.screenSeq++
	}
	return "\x00frame" + strconv.FormatUint(m.screenSeq, 10)
}
