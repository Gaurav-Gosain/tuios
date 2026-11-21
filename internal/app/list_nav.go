package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func (m *OS) listWraps() bool {
	return m.Settings.WrapLists && !m.wheelMoving
}

func (m *OS) listStep(cur, delta, n int) int {
	return listnav.Step(cur, delta, n, m.listWraps())
}

func listDelta(keys *listnav.Keymap, key string, page int) (int, bool) {
	motion := keys.Keys(key, true)
	if motion == listnav.None {
		return 0, false
	}
	return listnav.Delta(motion, page), true
}

func (m *OS) composerListKey(key string, page int) (int, bool) {
	return listDelta(m.ListKeys, key, page)
}

const (
	hintListMove        = "\x00list-move"
	hintListMoveLetters = "\x00list-move-letters"
)

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

func (m *OS) composerListKeyHint() string { return m.listMoveKeys(true) }
