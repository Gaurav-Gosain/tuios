package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// helpOwnsKey reports whether a key is help mode's to read. Help is a view,
// not a mode: it takes the keys it acts on only while nothing else owns the
// keyboard, and every other key reaches the app as if it were not up. A
// leader chord part-way through, a panel above it, the pointer's labels or a
// capture belong to the app, and a key the leader itself is bound to is the
// leader's first.
func helpOwnsKey(msg tea.KeyPressMsg, o *app.OS) bool {
	return viewerTakesKey(msg, o) && !o.PanelOwnsKeyboard() &&
		!o.HintsOpen() && !o.PaneLabelsOpen() && !o.CaptureActive() && !o.ReviewOpen()
}

// helpOwnsPointer reports whether the pointer is help mode's to read. A panel
// above it, a capture or the review takes the pointer the way it takes the
// keys, so help does not scroll or re-target under them.
func helpOwnsPointer(o *app.OS) bool {
	return !o.PanelOwnsKeyboard() && !o.CaptureActive() && !o.ReviewOpen()
}

// handleHelpModeKey runs the keys help mode binds and reports whether it did.
// A key it does not bind is not help's: the caller lets it through, so the
// rail's own chords, a workspace switch and the pane itself keep working while
// the explanation is up. The pane follows whatever layout they leave on the
// next frame, which is what makes the listed chords worth pressing.
func handleHelpModeKey(msg tea.KeyPressMsg, o *app.OS) (*app.OS, tea.Cmd, bool) {
	switch msg.String() {
	case "esc", "shift+f1":
		o.ExitHelpMode()
	case "tab":
		o.HelpNext(1)
	case "shift+tab":
		o.HelpNext(-1)
	case "down":
		o.HelpDeeper()
	case "up":
		o.HelpShallower()
	case "left":
		o.HelpNext(-1)
	case "right":
		o.HelpNext(1)
	case "h":
		o.HelpToggleKeys()
	default:
		return o, nil, false
	}
	return o, nil, true
}
