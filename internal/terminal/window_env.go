package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/colorprofile"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
)

// Graphics capabilities of the host terminal, set by the app once passthrough
// has been initialised and read when building a guest shell's environment.
// Guarded because windows can be created from the update loop while the
// capabilities are refreshed on reattach.
var (
	graphicsMu        sync.RWMutex
	kittyGraphicsHost bool
	sixelGraphicsHost bool
)

// SetGraphicsCapabilities records which graphics protocols tuios can forward to
// the host terminal. Windows created afterwards advertise a matching terminal
// identity to their shell (see guestenv.TermProgram).
func SetGraphicsCapabilities(kitty, sixel bool) {
	graphicsMu.Lock()
	defer graphicsMu.Unlock()
	kittyGraphicsHost = kitty
	sixelGraphicsHost = sixel
}

// guestTermProgram returns the TERM_PROGRAM value for a newly spawned shell.
func guestTermProgram() string {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return guestenv.TermProgram(kittyGraphicsHost, sixelGraphicsHost)
}

func detectShell() string {
	// Check user configuration first
	if cfg, err := config.LoadUserConfig(); err == nil && cfg.Appearance.PreferredShell != "" {
		preferredShell := cfg.Appearance.PreferredShell

		// just do a check in case
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(preferredShell), ".exe") {
			preferredShell += ".exe"
		}

		shellExists := false
		if runtime.GOOS == "windows" {
			_, err = exec.LookPath(preferredShell)
			shellExists = err == nil
		} else {
			_, err = os.Stat(preferredShell)
			shellExists = err == nil
		}

		if shellExists {
			return preferredShell
		}
		fmt.Fprintf(os.Stderr, "Warning: Configured shell '%s' not found. Falling back to defaults.\n", preferredShell)
	}

	// Check environment variable
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}

	// Check if we're on Windows
	if runtime.GOOS == "windows" {
		// Check for PowerShell or CMD
		shells := []string{
			"powershell.exe",
			"pwsh.exe", // PowerShell Core/7+
			"cmd.exe",
		}
		for _, shell := range shells {
			if _, err := exec.LookPath(shell); err == nil {
				return shell
			}
		}
		// Windows fallback
		return "cmd.exe"
	}

	// Unix/Linux/macOS shells
	shells := []string{"/bin/bash", "/bin/zsh", "/bin/fish", "/bin/sh"}
	for _, shell := range shells {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	// Unix fallback
	return "/bin/sh"
}

// getTerminalEnv returns TERM and COLORTERM values for the current environment.
// For local sessions, this is cached after first detection.
// The environment is detected from os.Environ() which includes SSH forwarded vars.
func getTerminalEnv() (termType, colorTerm string) {
	// Use sync.Once to cache local terminal detection
	// This runs once per process lifetime for efficiency
	localEnvOnce.Do(func() {
		// First check if TERM/COLORTERM are already set in the environment
		// This handles the case where tuios-web sets them explicitly because
		// os.Stdout is not a TTY in web mode
		envTerm := os.Getenv("TERM")
		envColorTerm := os.Getenv("COLORTERM")

		// If COLORTERM=truecolor is set, trust the environment
		// This is the case for web sessions where we explicitly set these
		if envColorTerm == "truecolor" && envTerm != "" && envTerm != "dumb" {
			localTermType = envTerm
			localColorTerm = envColorTerm
			return
		}

		// Detect terminal capabilities using colorprofile (from charm)
		// This handles TERM, COLORTERM, NO_COLOR, CLICOLOR, terminfo, and tmux detection
		// For SSH sessions, os.Environ() will include the SSH client's environment
		profile := colorprofile.Detect(os.Stdout, os.Environ())
		localTermType, localColorTerm = profileToEnv(profile)
	})
	return localTermType, localColorTerm
}

// profileToEnv converts a colorprofile.Profile to TERM and COLORTERM environment variables.
// Returns (termType, colorTerm) where colorTerm may be empty string.
func profileToEnv(profile colorprofile.Profile) (termType, colorTerm string) {
	// Get parent TERM for preserving specific terminal types
	parentTerm := os.Getenv("TERM")

	switch profile {
	case colorprofile.TrueColor:
		// Prefer parent TERM, fallback to xterm-256color
		// Note: We support XTWINOPS but xterm-256color terminfo doesn't advertise it
		// Applications must query the terminal directly (which works via our CSI 't' handler)
		if parentTerm != "" {
			termType = parentTerm
		} else {
			termType = "xterm-256color"
		}
		colorTerm = "truecolor"

	case colorprofile.ANSI256:
		// 256 color support
		if parentTerm != "" && strings.Contains(parentTerm, "256color") {
			termType = parentTerm
		} else if strings.HasPrefix(parentTerm, "screen") {
			termType = "screen-256color"
		} else if strings.HasPrefix(parentTerm, "tmux") {
			termType = "tmux-256color"
		} else {
			termType = "xterm-256color"
		}
		colorTerm = "" // Don't set COLORTERM for 256 color

	case colorprofile.ANSI:
		// Basic 16 color support
		if parentTerm != "" && parentTerm != "dumb" {
			termType = parentTerm
		} else {
			termType = "xterm"
		}
		colorTerm = ""

	case colorprofile.Ascii, colorprofile.NoTTY:
		// No color support or not a TTY
		termType = "dumb"
		colorTerm = ""

	default:
		// Fallback to sensible default
		termType = "xterm-256color"
		colorTerm = ""
	}

	return termType, colorTerm
}

// enableTerminalFeatures enables advanced terminal features
func (w *Window) enableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// Bracketed paste mode is handled by wrapping paste content with escape sequences
	// when pasting (see input.go handleClipboardPaste). We don't need to enable it
	// via the PTY as that sends the sequence to the shell's stdin, which can cause
	// the escape codes to be echoed back and appear as garbage in the terminal.
	// The shell/application running in the PTY will handle bracketed paste mode
	// if it supports it, based on receiving the wrapped paste content.

	// Don't enable mouse modes automatically - let applications request them
	// Applications like vim, less, htop will enable mouse support themselves
	// by sending the appropriate escape sequences
}

// disableTerminalFeatures disables advanced terminal features before closing
func (w *Window) disableTerminalFeatures() {
	if w.Pty == nil {
		return
	}

	// No terminal features to explicitly disable
	// Bracketed paste is handled at the application level
	// Mouse tracking is managed by applications themselves
}
