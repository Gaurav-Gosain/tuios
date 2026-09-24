package app

import "github.com/Gaurav-Gosain/tuios/internal/listnav"

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
