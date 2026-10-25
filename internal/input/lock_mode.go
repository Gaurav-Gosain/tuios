package input

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
)

// Lock mode's routing. See app/lock_mode.go for the model.
//
// The locked path itself is one check in HandleTerminalModeKey, after the
// overlays, copy mode, the leader and the menus have had the key, and before
// any terminal-mode key, global key or workspace chord could: a locked key
// goes straight to the pane. The two keys outside that function that act in
// terminal mode, the hold key and the prefix repeat window, check Locked
// where they are read.
//
// What is here is the bookkeeping that keeps a menu open after a repeatable
// action, which every menu handler shares.

// menuKey is what a menu handler remembers about the menu before it closes
// it to handle one key.
type menuKey struct {
	before app.MenuState
	held   bool
}

// startMenuKey closes the open menu for one key and returns what it was. A
// handler calls it in place of clearing its own flag.
func startMenuKey(o *app.OS) menuKey {
	k := menuKey{before: o.MenuState(), held: o.LockHeld}
	o.LockHeld = false
	o.SetMenuState(app.MenuState{})
	return k
}

// leaves reports whether msg ends a menu that lock mode holds open: esc and
// ctrl+c close it and lock again, and go nowhere.
func (k menuKey) leaves(msg tea.KeyPressMsg) bool {
	if !k.held {
		return false
	}
	s := msg.String()
	return s == "esc" || s == "ctrl+c"
}

// refuses reports whether a menu lock mode holds open turns down action. A
// held menu takes only the repeatable actions, as a tmux key table entered
// after a -r binding does: its other keys, most of them one press from
// closing or killing something, must not stay live behind a run of moves.
// The caller closes the menu and gives the key to the pane.
func (k menuKey) refuses(o *app.OS, action string) bool {
	return k.held && !prefixRepeats(o, action)
}

// ran finishes a key that ran action. In lock mode a repeatable action keeps
// the menu it ran in open until esc, unless the action opened a menu of its
// own. It reports whether it held the menu, in which case the timed repeat
// window has no work to do.
func (k menuKey) ran(o *app.OS, action string) bool {
	if !o.Settings.Lock || o.Mode != app.TerminalMode || o.MenuOpen() || !prefixRepeats(o, action) {
		return false
	}
	o.SetMenuState(k.before)
	o.LockHeld = true
	o.ClearPrefixRepeat()
	return true
}

// unbound finishes a key the menu does not bind, or one a held menu refuses.
// A held menu closes and the key goes to the pane, as a sticky menu does, so
// a letter typed after a run of moves is not lost. It reports whether it forwarded the key.
func (k menuKey) unbound(msg tea.KeyPressMsg, o *app.OS) bool {
	if !k.held || o.Mode != app.TerminalMode {
		return false
	}
	// typeIntoPane, so the key reaches the pane the user types into (a rail
	// session pane too), the multifocus peers and a TML window, and is
	// recorded and noted like any typed key.
	typeIntoPane(msg, o, false)
	return true
}

// leaderWhileHeld closes a menu lock mode holds open when the leader is
// pressed in it, so the leader opens a fresh leader menu rather than sending
// itself to the pane as the leader pressed two times does.
func leaderWhileHeld(o *app.OS) {
	if o.LockHeld {
		o.LockHeld = false
		o.SetMenuState(app.MenuState{})
	}
}
