package input

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// handleReviewMouseClick handles a press while the review is open. A left
// press on a key hint in the footer is that key, run through the review's
// own keys, so a click does what the hint says. Any other left press goes to
// the review's places (app.ReviewMouseClick).
func handleReviewMouseClick(msg tea.MouseClickMsg, o *app.OS) (*app.OS, tea.Cmd) {
	if msg.Button != tea.MouseLeft {
		return o, nil
	}
	if key := o.ReviewHintKeyAt(msg.X, msg.Y); key != "" {
		if press, ok := reviewKeyPress(key); ok {
			return handleReviewInput(press, o)
		}
		return o, nil
	}
	o.ReviewMouseClick(msg.X, msg.Y)
	return o, nil
}

// reviewKeyNames are the named keys a review hint can show.
var reviewKeyNames = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab,
	"space": tea.KeySpace, "backspace": tea.KeyBackspace,
	"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
	"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
}

// reviewKeyPress is the key press a hint's key names: one character, or a
// named key, after any ctrl+, alt+ and shift+. ok is false for a key it
// cannot name.
func reviewKeyPress(key string) (tea.KeyPressMsg, bool) {
	var mod tea.KeyMod
	for {
		switch {
		case strings.HasPrefix(key, "ctrl+") && len(key) > len("ctrl+"):
			mod |= tea.ModCtrl
			key = key[len("ctrl+"):]
			continue
		case strings.HasPrefix(key, "alt+") && len(key) > len("alt+"):
			mod |= tea.ModAlt
			key = key[len("alt+"):]
			continue
		case strings.HasPrefix(key, "shift+") && len(key) > len("shift+"):
			mod |= tea.ModShift
			key = key[len("shift+"):]
			continue
		}
		break
	}
	if code, ok := reviewKeyNames[key]; ok {
		press := tea.KeyPressMsg{Code: code, Mod: mod}
		if code == tea.KeySpace && mod == 0 {
			press.Text = " "
		}
		return press, true
	}
	r, size := utf8.DecodeRuneInString(key)
	if r == utf8.RuneError || size != len(key) {
		return tea.KeyPressMsg{}, false
	}
	press := tea.KeyPressMsg{Code: r, Mod: mod}
	if mod&^tea.ModShift == 0 {
		press.Text = key
	}
	return press, true
}
