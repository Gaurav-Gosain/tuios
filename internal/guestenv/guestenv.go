// Package guestenv derives environment values tuios exports to the processes
// it spawns in its windows. Both the local terminal path and the daemon's PTY
// path build a guest environment, and they must agree on what they advertise.
package guestenv

import "strings"

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

// TermProgramFor is TermProgram for a pane that starts argv, nil for the
// user's shell. It differs from TermProgram in one case: Codex started
// directly on a host with neither graphics protocol.
//
// Codex sends its desktop notifications as OSC 9 only to a terminal it names
// from TERM_PROGRAM (Ghostty, iTerm2, kitty, Warp and WezTerm), and rings the
// bell for every other name, TUIOS included
// (codex-rs/tui/src/notifications/mod.rs and codex-rs/terminal-detection in
// github.com/openai/codex). tuios shows an OSC 9 notification with its text
// and a bell only as "bell", so a Codex pane on a plain host lost what the
// notification said. With kitty graphics or sixel the pane is already told
// ghostty or WezTerm, and Codex sends OSC 9.
//
// Such a pane is told WarpTerminal. Of the names Codex sends OSC 9 to, it is
// the only one Codex treats like an unknown terminal in every other respect:
// it picks no image protocol for it, no keyboard workaround, the same resize
// limits and the same link style. The other names would have Codex, and the
// image tools it runs, emit graphics this host cannot show. Only Codex's own
// pane is told this, never a shell: chafa, for one, reads WarpTerminal as a
// kitty graphics terminal, and a shell prompt may set itself up differently
// for Warp.
func TermProgramFor(argv []string, kittyGraphics, sixelGraphics bool) string {
	name := TermProgram(kittyGraphics, sixelGraphics)
	if name == "TUIOS" && IsCodex(argv) {
		return "WarpTerminal"
	}
	return name
}

// IsCodex reports whether argv starts Codex directly: its program's base
// name is codex, with or without an .exe suffix.
func IsCodex(argv []string) bool {
	return programName(argv) == "codex"
}

// programName is the base name of argv[0], lower-cased, without an .exe
// suffix, or "" for no argv.
func programName(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := argv[0]
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(name)
	return strings.TrimSuffix(name, ".exe")
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

// hostMultiplexerVars are the variables an enclosing tmux sets for its own
// panes. A tuios started from inside tmux inherits them, and a pane that
// inherits them in turn believes it is a tmux pane: Codex wraps its OSC 9
// notifications in tmux DCS passthrough, which tuios drops, and an agent that
// splits panes through tmux reaches the outer tmux instead of the pane it is
// in. The pane is a tuios pane, so these are removed.
var hostMultiplexerVars = []string{"TMUX", "TMUX_PANE"}

// WithoutHostMultiplexer returns env with every assignment of TMUX and
// TMUX_PANE removed. The slice is filtered in place, so callers pass a copy
// they own, such as the fresh one os.Environ returns.
func WithoutHostMultiplexer(env []string) []string {
	kept := env[:0]
	for _, kv := range env {
		if isHostMultiplexerVar(kv) {
			continue
		}
		kept = append(kept, kv)
	}
	return kept
}

func isHostMultiplexerVar(kv string) bool {
	for _, name := range hostMultiplexerVars {
		if len(kv) > len(name) && kv[len(name)] == '=' && kv[:len(name)] == name {
			return true
		}
	}
	return false
}
