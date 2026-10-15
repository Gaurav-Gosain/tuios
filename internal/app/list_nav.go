package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
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

// listKeymap is the keys [keybindings.lists] gives each list motion. An action
// the section does not name keeps its default keys, and one set to [] has none.
func listKeymap(cfg *config.UserConfig) *listnav.Keymap {
	if cfg == nil {
		return nil
	}
	lists := cfg.Keybindings.Lists
	bindings := map[listnav.Motion][]string{}
	for action, motion := range map[string]listnav.Motion{
		config.ActionListUp:       listnav.Up,
		config.ActionListDown:     listnav.Down,
		config.ActionListPageUp:   listnav.PageUp,
		config.ActionListPageDown: listnav.PageDown,
		config.ActionListFirst:    listnav.Home,
		config.ActionListLast:     listnav.End,
	} {
		if keys, ok := lists[action]; ok {
			bindings[motion] = keys
		}
	}
	return listnav.NewKeymap(bindings)
}
