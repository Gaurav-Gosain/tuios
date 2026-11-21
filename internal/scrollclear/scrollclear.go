// Package scrollclear decides what ED 3 (CSI 3 J, erase saved lines) does to
// a pane's history.
//
// clear sends ED 3 after ED 2 wherever terminfo has the E3 capability, so a
// terminal that honours ED 3 loses its whole scrollback on every clear. tmux
// with its usual TERM never sees ED 3 from clear, and people who come from
// tmux expect the history to stay. tuios keeps it by default, and
// appearance.scrollback_keep_on_clear = false honours ED 3 again. The
// clear_scrollback key and tuios clear-history still drop the history on
// purpose, because they call the emulator directly, not through ED 3.
//
// The setting is one per process: the daemon and a client each read it from
// the config file, and every pane emulator of that process follows it, the
// ones made before a reload too.
package scrollclear

import (
	"sync/atomic"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
)

var drop atomic.Bool

// SetKeep sets whether ED 3 keeps the history.
func SetKeep(keep bool) { drop.Store(!keep) }

// Drop is ED 3 that tuios writes into a pane's stream itself, for
// clear-history, the clear_scrollback key and a resync. Its second parameter
// tells it apart from the ED 3 a program sends: Apply lets it through
// whatever the setting, and every emulator reads only the first parameter of
// ED, so it drops the history there too, in a client of an older release as
// well.
const Drop = "\x1b[3;0J"

// Apply makes t follow the setting. The pure Go emulator runs the handler
// registered last first, so this one sees ED 3 before the emulator's own and
// swallows it while the history is kept. Every other ED goes on to the
// emulator, Drop among them. The libghostty-vt backend erases inside the library, which no
// handler reaches, so there ED 3 always drops the history.
func Apply(t vt.Terminal) {
	e, ok := t.(*vt.Emulator)
	if !ok {
		return
	}
	e.RegisterCsiHandler('J', func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 0)
		return n == 3 && len(params) == 1 && !drop.Load()
	})
}
