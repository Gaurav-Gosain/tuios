package terminal

import (
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
)

// Graphics capabilities of the host terminal, set by the app once passthrough
// has been initialised and read when building a guest shell's environment.
// Guarded because windows can be created from the update loop while the
// capabilities are refreshed on reattach.
var (
	graphicsMu         sync.RWMutex
	kittyGraphicsHost  bool
	sixelGraphicsHost  bool
	kittyAnimationHost bool
)

// SetGraphicsCapabilities records which graphics protocols tuios can forward to
// the host terminal. Windows created afterwards advertise a matching terminal
// identity to their shell (see guestenv.TermProgram) and are told whether
// frame edits get through (see guestenv.KittyAnimationVar).
func SetGraphicsCapabilities(kitty, sixel, animation bool) {
	graphicsMu.Lock()
	defer graphicsMu.Unlock()
	kittyGraphicsHost = kitty
	sixelGraphicsHost = sixel
	kittyAnimationHost = animation
}

// guestKittyAnimation returns the TUIOS_KITTY_ANIMATION value for a newly
// spawned shell.
func guestKittyAnimation() string {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return guestenv.KittyAnimationVar(kittyGraphicsHost && kittyAnimationHost)
}

// guestTermProgram returns the TERM_PROGRAM value for a newly spawned shell.
func guestTermProgram() string {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return guestenv.TermProgram(kittyGraphicsHost, sixelGraphicsHost)
}

// detectShell returns the shell a standalone pane runs, honouring
// appearance.preferred_shell. The config is read on every call so an edit
// applies to the next pane.
func detectShell() string {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		cfg = nil
	}
	return config.ResolveShell(cfg)
}

// getTerminalEnv returns TERM and COLORTERM values for the current environment.
// It is detected once per process and cached.
func getTerminalEnv() (termType, colorTerm string) {
	localEnvOnce.Do(func() {
		localTermType, localColorTerm = guestenv.DetectTerm()
		// A server with no terminal of its own (a service manager, nohup, a
		// log file) detects NoTTY, which makes every pane TERM=dumb even though
		// the panes are drawn on a client's real terminal. A real terminal that
		// answers dumb keeps its answer.
		if localTermType == "dumb" && !term.IsTerminal(int(os.Stdout.Fd())) {
			if d := headlessGuestTerm.Load(); d != nil {
				localTermType, localColorTerm = d.term, d.colorTerm
			}
		}
	})
	return localTermType, localColorTerm
}

// guestTermDefault is a TERM and COLORTERM pair for panes.
type guestTermDefault struct {
	term, colorTerm string
}

// headlessGuestTerm is what SetHeadlessGuestTerm installed, or nil.
var headlessGuestTerm atomic.Pointer[guestTermDefault]

// SetHeadlessGuestTerm sets the TERM and COLORTERM a pane gets when this
// process's stdout is not a terminal, in place of the TERM=dumb that detection
// answers there. A server calls it because its panes are drawn on a client's
// terminal, not on its own stdout. With a real terminal on stdout detection
// still runs, and a TERM and COLORTERM=truecolor already in the environment
// still win.
//
// Detection runs once, at the first window, so this must be called before any
// window is created.
func SetHeadlessGuestTerm(termType, colorTerm string) {
	headlessGuestTerm.Store(&guestTermDefault{term: termType, colorTerm: colorTerm})
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
