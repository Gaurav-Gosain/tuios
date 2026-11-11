//go:build windows || js

package termio

import tea "charm.land/bubbletea/v2"

// WatchResize does nothing here: the local cell frame is not offered on
// Windows, and the browser build is told its size by the page.
func (s *Screen) WatchResize(func(tea.Msg)) (stop func()) { return func() {} }
