package config

import (
	"os"
	"strings"
)

// HostTerminal names the terminal emulator tuios is running inside, as far as the
// environment gives it away.
type HostTerminal string

// The hosts tuios can recognise. Unknown is not a failure: it only means the
// advice has to stay generic.
const (
	HostUnknown       HostTerminal = ""
	HostAppleTerminal HostTerminal = "Terminal.app"
	HostITerm2        HostTerminal = "iTerm2"
	HostGhostty       HostTerminal = "Ghostty"
	HostKitty         HostTerminal = "kitty"
	HostWezTerm       HostTerminal = "WezTerm"
	HostAlacritty     HostTerminal = "Alacritty"
	HostVTE           HostTerminal = "VTE"
	HostVSCode        HostTerminal = "VS Code"
	HostRio           HostTerminal = "Rio"
	HostWarp          HostTerminal = "Warp"
)

// DetectHostTerminal identifies the host terminal from the environment.
//
// TERM_PROGRAM is checked before TERM because a multiplexer rewrites TERM to
// screen/tmux while leaving TERM_PROGRAM alone, and the Option-key setting that
// matters lives in the outer terminal either way.
func DetectHostTerminal() HostTerminal {
	return detectHostTerminal(os.Getenv)
}

func detectHostTerminal(getenv func(string) string) HostTerminal {
	switch prog := getenv("TERM_PROGRAM"); {
	case prog == "Apple_Terminal":
		return HostAppleTerminal
	case prog == "iTerm.app":
		return HostITerm2
	case prog == "WezTerm":
		return HostWezTerm
	case prog == "ghostty":
		return HostGhostty
	case prog == "vscode":
		return HostVSCode
	case prog == "WarpTerminal":
		return HostWarp
	}
	if getenv("KITTY_WINDOW_ID") != "" {
		return HostKitty
	}
	if getenv("GHOSTTY_RESOURCES_DIR") != "" || getenv("GHOSTTY_BIN_DIR") != "" {
		return HostGhostty
	}
	if getenv("ALACRITTY_WINDOW_ID") != "" || getenv("ALACRITTY_SOCKET") != "" {
		return HostAlacritty
	}
	if getenv("WEZTERM_EXECUTABLE") != "" {
		return HostWezTerm
	}
	switch term := getenv("TERM"); {
	case strings.Contains(term, "kitty"):
		return HostKitty
	case strings.Contains(term, "ghostty"):
		return HostGhostty
	case strings.Contains(term, "alacritty"):
		return HostAlacritty
	case strings.Contains(term, "rio"):
		return HostRio
	}
	// VTE-based terminals (GNOME Terminal, Ptyxis, Tilix) set VTE_VERSION.
	// This is the marker the native clipboard fallback keys off: VTE never
	// implements OSC 52, so the host needs the system tool instead.
	if getenv("VTE_VERSION") != "" {
		return HostVTE
	}
	return HostUnknown
}

// InsideMultiplexer reports whether another multiplexer sits between tuios and
// the terminal. It swallows keys of its own and has to be told to pass the
// Option chords through, so the advice has to mention it.
func InsideMultiplexer() bool {
	return insideMultiplexer(os.Getenv)
}

func insideMultiplexer(getenv func(string) string) bool {
	if getenv("TMUX") != "" {
		return true
	}
	term := getenv("TERM")
	return strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux")
}

// MacOptionAdvice returns the one thing the user has to change so their terminal
// sends Alt for Option instead of composing a character, named for the terminal
// they are actually in. chord is the binding that just failed to fire, e.g.
// "alt+n".
//
// Every one of these settings makes the terminal send the ESC-prefixed Meta
// encoding, which tuios already reads, so the advice is complete on its own: no
// tuios-side setting is involved.
func MacOptionAdvice(host HostTerminal, chord string) string {
	var fix string
	switch host {
	case HostAppleTerminal:
		fix = "Terminal → Settings → Profiles → Keyboard → tick \"Use Option as Meta Key\""
	case HostITerm2:
		fix = "iTerm2 → Settings → Profiles → Keys → set Left Option key to \"Esc+\""
	case HostGhostty:
		fix = "add macos-option-as-alt = true to ~/.config/ghostty/config"
	case HostKitty:
		fix = "add macos_option_as_alt yes to ~/.config/kitty/kitty.conf"
	case HostWezTerm:
		fix = "set send_composed_key_when_left_alt_is_pressed = false in ~/.wezterm.lua"
	case HostAlacritty:
		fix = "set option_as_alt = \"Both\" under [window] in alacritty.toml"
	case HostVSCode:
		fix = "turn on terminal.integrated.macOptionIsMeta in VS Code settings"
	case HostWarp, HostRio, HostUnknown:
		fix = "turn on your terminal's \"Option as Meta/Alt\" setting"
	default:
		fix = "turn on your terminal's \"Option as Meta/Alt\" setting"
	}
	advice := "Option is composing a character, so " + chord + " never reaches tuios: " + fix
	if InsideMultiplexer() {
		advice += " (and let the outer multiplexer pass the chord through)"
	}
	return advice
}

// GhosttyAltArrowAdvice is the extra step Ghostty needs for the alt+arrow binds.
// Ghostty ships keybinds that rewrite alt+left/alt+right into the readline word
// motions ESC b and ESC f before any encoding happens, so those two chords never
// reach tuios even with macos-option-as-alt on and the Kitty protocol negotiated.
const GhosttyAltArrowAdvice = "Ghostty rewrites alt+left/alt+right to word motions: " +
	"add keybind = alt+left=unbind and keybind = alt+right=unbind to unbind them"

// HostIsVTE reports whether the outer terminal is built on VTE (GNOME Terminal,
// Ptyxis, Tilix...). VTE terminals never implement OSC 52, so tuios needs the
// native system clipboard path to reach the user's clipboard there. Everywhere
// else, OSC 52 is the preferred channel and a spawned clipboard tool would only
// duplicate entries in the clipboard manager.
func HostIsVTE() bool {
	return detectHostTerminal(os.Getenv) == HostVTE
}
