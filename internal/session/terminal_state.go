package session

import (
	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

// The snapshot of a pane's terminal lives in vt/snapshot. These
// names keep the session API and the wire as they were: gob registers
// TerminalState under its old name (codec.go), so an older peer and saved
// history read it as before.
type (
	TerminalState     = snapshot.State
	CellState         = snapshot.CellState
	StyleState        = snapshot.StyleState
	SavedCursorState  = snapshot.SavedCursorState
	TitleEntryState   = snapshot.TitleEntryState
	GuestColorState   = snapshot.GuestColorState
	PaletteColorState = snapshot.PaletteColorState
)

// DefaultStateScrollback is how many scrollback lines a state request carries
// when it does not ask for a number.
const DefaultStateScrollback = snapshot.DefaultScrollback

// TerminalStateOf is snapshot.Capture.
func TerminalStateOf(t vt.Terminal, width, height, maxScrollback, have int) *TerminalState {
	return snapshot.Capture(t, width, height, maxScrollback, have)
}

// ApplyTerminalState is snapshot.Apply. A part of the snapshot it skips is
// logged.
func ApplyTerminalState(t vt.Terminal, state *TerminalState) {
	if err := snapshot.Apply(t, state); err != nil {
		debugLog("[CLIENT] %v", err)
	}
}

// SnapshotVT is snapshot.Replay.
func SnapshotVT(st *TerminalState) []byte { return snapshot.Replay(st) }

// CellStateOf is snapshot.CellOf.
func CellStateOf(cell *uv.Cell) CellState { return snapshot.CellOf(cell) }
