//go:build !slim

package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// registerTapeActions registers the tape manager, tape recording and the
// tape prefix (leader, T, ...).
func registerTapeActions(d *ActionDispatcher) {
	d.Register("toggle_tape_manager", handleToggleTapeManager)
	d.Register("stop_recording", handleStopRecording)
	d.Register("prefix_tape", makeSubPrefixHandler(func(o *app.OS) { o.TapePrefixActive = true }))
	d.Register("tape_prefix_manager", handleToggleTapeManager)
	d.Register("tape_prefix_review", handleTapeReview)
	d.Register("tape_prefix_record", handleTapeRecord)
	d.Register("tape_prefix_stop", handleTapeStop)
	d.Register("tape_prefix_cancel", handlePrefixCancel)
}

func handleToggleTapeManager(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.ToggleTapeManager()
	return o, nil
}

func handleStopRecording(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.TapeManagerStopRecording()
	}
	return o, nil
}

// ============================================================================
// Scrolling Tiling Action Handlers (niri-like)
// ============================================================================

// handleTapeReview opens the project-tape review/trust dialog for the tape in
// the focused window's current directory. It is the deliberate action that lets
// the user read a detected tape and choose to run or trust it.
func handleTapeReview(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	o.OpenTapeReview()
	return o, nil
}

func handleTapeRecord(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.ShowNotification("Already recording", "warning", o.Settings.NotificationDuration)
		return o, nil
	}
	o.TapeManagerStartRecording()
	o.ShowTapeManager = true // Show the UI for naming
	return o, nil
}

func handleTapeStop(_ tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if o.TapeRecorder != nil && o.TapeRecorder.IsRecording() {
		o.TapeManagerStopRecording()
	} else {
		o.ShowNotification("Not recording", "warning", o.Settings.NotificationDuration)
	}
	return o, nil
}

// ============================================================================
// Terminal mode direct binds
// ============================================================================
