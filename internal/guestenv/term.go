package guestenv

import (
	"os"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// DetectTerm returns the TERM and COLORTERM values to hand a guest, from this
// process's own terminal and environment. colorTerm may be empty.
//
// The standalone window path and the client that hands the daemon its hello
// both call it, so a pane is told the same thing whichever side spawns it.
// It is not cached here; the standalone path caches it per process.
func DetectTerm() (termType, colorTerm string) {
	envTerm := os.Getenv("TERM")
	envColorTerm := os.Getenv("COLORTERM")
	if envColorTerm == "truecolor" && envTerm != "" && envTerm != "dumb" {
		return envTerm, envColorTerm
	}

	return ProfileToEnv(colorprofile.Detect(os.Stdout, os.Environ()), envTerm)
}

// ProfileToEnv converts a color profile to TERM and COLORTERM values, keeping
// parentTerm where it already describes the profile. colorTerm may be empty.
func ProfileToEnv(profile colorprofile.Profile, parentTerm string) (termType, colorTerm string) {
	switch profile {
	case colorprofile.TrueColor:
		if parentTerm != "" {
			return parentTerm, "truecolor"
		}
		return "xterm-256color", "truecolor"

	case colorprofile.ANSI256:
		switch {
		case parentTerm != "" && strings.Contains(parentTerm, "256color"):
			return parentTerm, ""
		case strings.HasPrefix(parentTerm, "screen"):
			return "screen-256color", ""
		case strings.HasPrefix(parentTerm, "tmux"):
			return "tmux-256color", ""
		default:
			return "xterm-256color", ""
		}

	case colorprofile.ANSI:
		if parentTerm != "" && parentTerm != "dumb" {
			return parentTerm, ""
		}
		return "xterm", ""

	case colorprofile.Ascii, colorprofile.NoTTY:
		return "dumb", ""

	default:
		return "xterm-256color", ""
	}
}
