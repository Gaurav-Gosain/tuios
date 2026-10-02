//go:build slim

package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/edition"
)

// tuios-slim has no tape manager, no tape recording and no project tapes.
// These stand in for tapemanager.go, tape_review.go, tape_run.go,
// tape_body.go and tape_detect.go. Single tape commands from run-command and
// the keybindings still run: the executor in os_tape_executor.go is core.

type TapeReviewState struct{}

func (m *OS) projectTapeOnCwdChange(CwdChangedMsg) tea.Cmd { return nil }

func (m *OS) tapeDockBadge() string { return "" }

func (m *OS) OpenTapeReview() {}

// ToggleTapeManager says the tape manager is not in this build.
func (m *OS) ToggleTapeManager() {
	m.ShowNotification(edition.MissingMessage("The tape manager"), "info", m.Settings.NotificationDuration)
}

func (m *OS) RenderTapeManager() string { return "" }

func (m *OS) RenderTapeReview() string { return "" }

func (m *OS) HandleTapeManagerInput(string) bool { m.ShowTapeManager = false; return false }

func (m *OS) HandleTapeReviewInput(string) bool { m.ShowTapeReview = false; return false }

// PlayTapeMsg asks for a tape to play. tuios-slim cannot play one.
type PlayTapeMsg struct {
	Name   string
	Script string
}

// PlayTape says tape playback is not in this build.
func (m *OS) PlayTape(string, string) (tea.Cmd, error) {
	return nil, edition.Missing("Tape playback")
}

// tapeDebounceMsg is never sent: there is no project tape check.
type tapeDebounceMsg struct{ gen uint64 }

func (m *OS) handleTapeDebounce(uint64) {}

func (m *OS) refreshAllPanesAfterTape() {}
