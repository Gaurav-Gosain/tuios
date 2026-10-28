package app

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// A finger's sideways drag on tuios-web.
//
// tuios-web's touch layer (cmd/tuios-web/strip_touch.js) takes a finger
// dragged sideways on the worker engine's canvas and writes it as a private
// report of its own, which no terminal and no keyboard sends:
//
//	CSI 7503 ; kind ; col ; row ; cells ~
//
// kind is 1 for a step that brings the columns to the left into view, 2 for
// one that brings the columns to the right, and 3 for the lift. col and row
// are the cell the drag started on, 1-based as in a mouse report, and cells is
// how many cells the finger crossed (1 when left out). The input decoder
// hands it on as an unknown CSI sequence with its bytes, and FilterMouseMotion
// turns it into a StripTouchMsg before anything else reads it.
//
// A report of its own, and not a wheel with modifiers, because every wheel a
// terminal can send is someone's input: Ctrl and Alt with a sideways wheel is
// a trackpad's drift with those keys held, or AltGr on Windows. The page
// cannot see which layout is up, so it sends the report anywhere and tuios
// decides: in the scrolling layout it moves the strip, and anywhere else it
// reaches nothing.

// stripTouchPrefix is the start of the report, up to its kind.
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

// stripTouchMaxCells bounds the cells one report moves the strip. A report
// is sent per pointer move, and no finger crosses more cells than this
// between two of them.
const stripTouchMaxCells = 500

// StripTouchMsg is a step or the lift of a finger's sideways drag. X and Y
// are the cell the drag started on, 0-based like a mouse message's.
type StripTouchMsg struct {
	Kind  StripTouchKind
	X, Y  int
	Cells int
}

// stripTouchReport decodes a finger's report, or returns msg unchanged.
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
