package app

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

const stripTouchPrefix = "\x1b[7503;"

// StripTouchKind is what a StripTouchMsg reports.
type StripTouchKind int

const (
	// StripTouchLeft brings the columns to the left into view.
	StripTouchLeft StripTouchKind = 1
	// StripTouchRight brings the columns to the right into view.
	StripTouchRight StripTouchKind = 2
	// StripTouchLift is the finger leaving the glass.
	StripTouchLift StripTouchKind = 3
)

const stripTouchMaxCells = 500

// StripTouchMsg is a step or the lift of a finger's sideways drag. X and Y
// are the cell the drag started on, 0-based like a mouse message's.
type StripTouchMsg struct {
	Kind  StripTouchKind
	X, Y  int
	Cells int
}

func stripTouchReport(msg tea.Msg) tea.Msg {
	e, ok := msg.(uv.UnknownCsiEvent)
	if !ok {
		return msg
	}
	body, ok := strings.CutPrefix(string(e), stripTouchPrefix)
	if !ok {
		return msg
	}
	body, ok = strings.CutSuffix(body, "~")
	if !ok {
		return msg
	}
	parts := strings.Split(body, ";")
	if len(parts) < 3 || len(parts) > 4 {
		return msg
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return msg
		}
		nums[i] = n
	}
	t := StripTouchMsg{Kind: StripTouchKind(nums[0]), X: max(nums[1]-1, 0), Y: max(nums[2]-1, 0), Cells: 1}
	if len(nums) == 4 {
		t.Cells = min(max(nums[3], 1), stripTouchMaxCells)
	}
	switch t.Kind {
	case StripTouchLeft, StripTouchRight, StripTouchLift:
		return t
	}
	return msg
}
