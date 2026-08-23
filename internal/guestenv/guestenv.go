// Package guestenv derives environment values tuios exports to the processes
// it spawns in its windows. Both the local terminal path and the daemon's PTY
// path build a guest environment, and they must agree on what they advertise.
package guestenv

// TermProgram returns the TERM_PROGRAM value for a guest process, given the
// graphics capabilities tuios can actually forward to the host terminal.
//
// Tools that draw images (chafa, yazi, kitten icat) pick their output format
// from the environment rather than by querying the terminal, and none of them
// know the name "TUIOS", so advertising it made every guest fall back to
// unicode block art even when tuios was forwarding kitty graphics to a capable
// host. Naming a terminal the tools do know makes them emit the protocol tuios
// passes through: ghostty for kitty graphics, WezTerm for sixel. TERM is left
// alone so no guest needs a terminfo entry that may not be installed, and
// tuios remains identifiable through TUIOS_SESSION and TUIOS_WINDOW_ID.
func TermProgram(kittyGraphics, sixelGraphics bool) string {
	switch {
	case kittyGraphics:
		return "ghostty"
	case sixelGraphics:
		return "WezTerm"
	default:
		return "TUIOS"
	}
}

// KittyAnimationVar returns the TUIOS_KITTY_ANIMATION assignment tuios exports
// to a guest, given whether kitty animation frames survive the trip to the
// host terminal.
//
// A guest cannot find this out for itself. An a=f frame edit is answered by
// the terminal that applies it, and tuios does not relay that answer back into
// the pane, so a guest that sends one and waits hears nothing whether it
// worked or not. Guessing from the environment is worse: TERM and
// KITTY_WINDOW_ID are inherited straight through a pane, so they name the host
// terminal and say nothing about what the pane in front of it will carry.
//
// So tuios says. It has already probed the host, and this is that answer,
// which is also why the variable is the same one that overrides the probe: a
// tuios running inside a tuios pane should believe the pane it is in.
//
// Only the local terminal path sets it so far. The daemon builds its own guest
// environment in internal/session and does not, so a pane under the daemon is
// silent on the question and a guest reading this falls back to whatever is
// safe everywhere. Silence is the safe answer, so that is a gap and not a bug.
func KittyAnimationVar(supported bool) string {
	if supported {
		return "TUIOS_KITTY_ANIMATION=1"
	}
	return "TUIOS_KITTY_ANIMATION=0"
}
