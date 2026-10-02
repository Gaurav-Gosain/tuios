package config

import "strings"

// LauncherConfig is the [launcher] section.
type LauncherConfig struct {
	// GUICommand is a command that starts a graphical desktop entry instead of
	// a new pane. The entry's argv is appended to it. With it empty, every
	// entry runs in a pane, as before.
	//
	// It exists for a Wayland compositor that shows its windows as panes
	// (tuios-wayland launch --), and works as well for one that does not
	// (niri msg action spawn --).
	GUICommand string `toml:"gui_command"`
}

// GUIPrefix is GUICommand split into an argv prefix. Double and single quotes
// group words, so a path with a space can be given. It is nil when the option
// is unset.
func (l LauncherConfig) GUIPrefix() []string {
	return splitWords(l.GUICommand)
}

// splitWords splits s on spaces, keeping quoted runs together. It does no
// expansion: the result is run directly, not by a shell.
func splitWords(s string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		have  bool
	)
	for _, r := range s {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote, have = r, true
		case r == ' ' || r == '\t':
			if have || cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if have || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
