package main

import (
	"io"
	"os"
	"runtime"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/termio"
)

// localScreen gives the local client a screen of its own when
// appearance.cell_frames asks for it (all, or TUIOS_CELL_FRAME=1): its frames
// are written as diffed cells and Bubble Tea runs without its renderer. It
// returns the program options that go with it, what to call once the program
// exists, and what to call when it has ended. Not offered on Windows.
func localScreen(m *app.OS, out io.Writer) (opts []app.ProgramOption, started func(*tea.Program), ended func()) {
	none := func(*tea.Program) {}
	if runtime.GOOS == "windows" || !m.Settings.CellFramesFor(false) ||
		!term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return nil, none, func() {}
	}
	w, h, err := term.GetSize(os.Stdout.Fd())
	if err != nil {
		return nil, none, func() {}
	}
	env := os.Environ()
	m.UseScreen(out, env, colorprofile.Detect(os.Stdout, env), termio.Output{Raw: true, Console: os.Stdin})
	stop := func() {}
	return []app.ProgramOption{app.WithOwnScreen(true), app.WithStartSize(w, h)},
		func(p *tea.Program) { stop = m.WatchScreenResize(p.Send) },
		func() {
			stop()
			m.CloseScreen()
		}
}
