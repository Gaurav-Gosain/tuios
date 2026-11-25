package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// takeFrames sets up a pane the daemon may send as frames of its screen in
// place of its bytes (session.FeaturePTYFrames): the handler that applies a
// frame, and the report that the window is behind on the pane. A pane whose
// stream a tap records gets bytes only, because the tap needs every byte.
func (m *OS) takeFrames(window *terminal.Window, ptyID string, tapped bool) {
	client := m.DaemonClient
	if tapped || !client.HasFeature(session.FeaturePTYFrames) || !client.HasFeature(session.FeatureRemoteHistory) {
		client.OnPTYFrame(ptyID, nil)
		window.SetLagReporter(nil)
		return
	}
	client.OnPTYFrame(ptyID, func(state *session.TerminalState) {
		applyPTYFrame(window, state)
	})
	window.SetLagReporter(func() { client.ReportPTYLag(ptyID) })
}

// applyPTYFrame puts a frame of a pane into the window's emulator. It runs
// on the client's read loop, in order with the pane's output, so everything
// queued for the emulator was sent before the frame and is dropped.
func applyPTYFrame(w *terminal.Window, state *session.TerminalState) {
	w.DiscardPendingOutput()
	w.ResizeEmulatorToSnapshot(state.Width, state.Height)
	w.LockIO()
	if w.Terminal != nil {
		// A frame carries no history. The emulator's goes, and the window
		// reads the lines above the screen from the daemon, by the numbers
		// the frame gives.
		w.Terminal.ClearScrollback()
		session.ApplyTerminalState(w.Terminal, state)
		w.NoteDaemonHistory(state.ScrollbackBase, state.ScrollbackEpoch, state.ScrollbackNumbered)
	}
	w.UnlockIO()
	w.SetAltScreen(state.IsAltScreen)
	w.FrameApplied()
}
