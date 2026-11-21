package terminal

import (
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
)

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

func guestKittyAnimation() string {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return guestenv.KittyAnimationVar(kittyGraphicsHost && kittyAnimationHost)
}

func guestTermProgram(command []string) string {
	graphicsMu.RLock()
	defer graphicsMu.RUnlock()
	return guestenv.TermProgramFor(command, kittyGraphicsHost, sixelGraphicsHost)
}

func guestBaseEnv() []string {
	return guestenv.WithoutHostMultiplexer(os.Environ())
}

func detectShell() (shell string, integration, login bool) {
	cfg, err := config.LoadUserConfig()
	if err != nil {
		cfg = nil
	}
	return config.ResolveShell(cfg), cfg == nil || config.ShellIntegrationOn(cfg.Shell.Integration), cfg != nil && cfg.Shell.Login
}

func getTerminalEnv() (termType, colorTerm string) {
	localEnvOnce.Do(func() {
		localTermType, localColorTerm = guestenv.DetectTerm()
		if localTermType == "dumb" && !term.IsTerminal(int(os.Stdout.Fd())) {
			if d := headlessGuestTerm.Load(); d != nil {
				localTermType, localColorTerm = d.term, d.colorTerm
			}
		}
		localTermType = guestenv.PaneTerm(localTermType)
	})
	return localTermType, localColorTerm
}

type guestTermDefault struct {
	term, colorTerm string
}

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

func (w *Window) enableTerminalFeatures() {
	if w.Pty == nil {
		return
	}
}

func (w *Window) disableTerminalFeatures() {
	if w.Pty == nil {
		return
	}
}
