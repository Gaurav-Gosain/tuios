package input

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// promptTextKeys is what a rail text prompt does with the keys every text field
// shares: backspace, the word chords, ctrl+u, space and typed text. Each prompt
// handles its own esc, enter and any keys of its own first, then hands the rest
// here, so the file dialog and the worktree dialog edit text the same way.
type promptTextKeys struct {
	Backspace  func()
	DeleteWord func()
	Clear      func()
	Type       func(string)
}

// handle edits the field for msg. Typed text is the terminal's composed text
// when the event carries one, a single printable byte when it does not; any
// other key is ignored.
func (p promptTextKeys) handle(msg tea.KeyPressMsg) {
	key := msg.String()
	switch {
	case key == "backspace":
		p.Backspace()
	case textedit.IsWordDeleteKey(key):
		// The word chords of a shell, the rename dialog and the filtering
		// overlays. Option+Backspace arrives as alt+backspace when the
		// terminal sends Option as Alt.
		p.DeleteWord()
	case key == "ctrl+u":
		p.Clear()
	case key == "space":
		p.Type(" ")
	case msg.Text != "":
		p.Type(msg.Text)
	case len(key) == 1 && key[0] >= 32 && key[0] <= 126:
		p.Type(key)
	}
}
