package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// listWraps reports whether a single step off the end of a list wraps to the
// other end for the move being made now.
//
// The wheel never wraps, whatever the setting says: a wheel is flung rather
// than pressed, and a list that came round to the top under a fling would
// never let the pointer rest on its last row. OverlayMouseWheel marks the
// moves it makes, and every list reads the mark here, so no list has to be
// told twice.
func (m *OS) listWraps() bool {
	return m.Settings.WrapLists && !m.wheelMoving
}

// listStep is listnav.Step under this session's wrap rule.
func (m *OS) listStep(cur, delta, n int) int {
	return listnav.Step(cur, delta, n, m.listWraps())
}

// listStepSkip is listnav.StepSkip under this session's wrap rule.
func (m *OS) listStepSkip(cur, delta, n int, rest func(int) bool) int {
	return listnav.StepSkip(cur, delta, n, m.listWraps(), rest)
}

// listDelta reads a list movement key of [keybindings.lists], with its letter
// keys (k, j, g and G by default) as a list with no filter reads them, and
// returns how many rows it moves: page for a page key, further than any list
// for Home and End. It reports false for any other key. A list that clamps at
// its ends rather than wrapping clamps the delta itself.
func listDelta(keys *listnav.Keymap, key string, page int) (int, bool) {
	motion := keys.Keys(key, true)
	if motion == listnav.None {
		return 0, false
	}
	return listnav.Delta(motion, page), true
}

// composerListKey is composer.Options.ListKey for this session: the list keys
// of [keybindings.lists], letters included, since a paste block is read only.
func (m *OS) composerListKey(key string, page int) (int, bool) {
	return listDelta(m.ListKeys, key, page)
}

// The hint keys a list footer gives for moving the cursor, filled in from the
// session's [keybindings.lists] when the footer is drawn (see listHints). A
// footer that wrote "↑↓" or "j/k" itself named keys the person may have
// unbound. hintListMove is for a list with a filter, which reads no letter
// keys; hintListMoveLetters for one without.
const (
	hintListMove        = "\x00list-move"
	hintListMoveLetters = "\x00list-move-letters"
)

// listMoveKeys is how a footer names the keys that move a list up and down:
// the arrows as ↑↓ ("up/down" in ASCII) when they are the first keys bound,
// otherwise the first up key and the first down key, as "ctrl+k/ctrl+j".
// It is "" when either motion has no key, and the hint is then left out.
func (m *OS) listMoveKeys(letters bool) string {
	up := m.ListKeys.Names(listnav.Up, letters)
	down := m.ListKeys.Names(listnav.Down, letters)
	if len(up) == 0 || len(down) == 0 {
		return ""
	}
	if up[0] == "up" && down[0] == "down" {
		if overlay.UseASCII() {
			return "up/down"
		}
		return "↑↓"
	}
	return up[0] + "/" + down[0]
}

// listHints fills the list movement hint keys in with the keys the session's
// keymap holds, and drops a hint whose motion has no key.
func (m *OS) listHints(hints []overlay.Hint) []overlay.Hint {
	var out []overlay.Hint
	for i, h := range hints {
		switch h.Key {
		case hintListMove, hintListMoveLetters:
		default:
			if out != nil {
				out = append(out, h)
			}
			continue
		}
		if out == nil {
			out = append(make([]overlay.Hint, 0, len(hints)), hints[:i]...)
		}
		if keys := m.listMoveKeys(h.Key == hintListMoveLetters); keys != "" {
			h.Key = keys
			out = append(out, h)
		}
	}
	if out == nil {
		return hints
	}
	return out
}

// composerListKeyHint is composer.Options.ListKeyHint for this session.
func (m *OS) composerListKeyHint() string { return m.listMoveKeys(true) }
