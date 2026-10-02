//go:build slim

package app

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// tuios-slim has no screen saver and no effect picker, and does not link the
// effects engine. These stand in for screensaver.go, effect_picker.go and
// render_effect_picker.go. The [screensaver] config section still loads, and
// nothing reads it.

// screensaverState keeps the one field the input path writes.
type screensaverState struct {
	active    bool
	frame     string
	lastInput time.Time
}

// effectPreview keeps the fields the overlay path reads. It never runs.
type effectPreview struct {
	capturing bool
	frame     string
}

func (m *OS) armScreensaver() tea.Cmd      { return nil }
func (m *OS) dismissScreensaver() tea.Cmd  { return nil }
func (m *OS) StartScreensaverNow() tea.Cmd { return nil }
func (m *OS) OpenEffectPicker() tea.Cmd    { return nil }

// ScreensaverActive is always false: there is no screen saver.
func (m *OS) ScreensaverActive() bool { return false }

func (m *OS) EffectPickerMove(int) tea.Cmd        { return nil }
func (m *OS) EffectPickerApplySelection() tea.Cmd { return nil }
func (m *OS) CancelEffectPicker()                 { m.ShowEffectPicker = false }
func (m *OS) renderEffectPicker() (string, overlay.Geometry, []overlayRowHit) {
	return "", overlay.Geometry{}, nil
}

// The saver's and the picker's messages. Nothing sends them in tuios-slim.
type (
	screensaverArmMsg     struct{}
	screensaverFrameMsg   struct{}
	effectPreviewFrameMsg struct{}
)

func (m *OS) handleScreensaverArm() tea.Cmd                          { return nil }
func (m *OS) handleScreensaverFrame() tea.Cmd                        { return nil }
func (m *OS) handleEffectPreviewFrame(effectPreviewFrameMsg) tea.Cmd { return nil }
