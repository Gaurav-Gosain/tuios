package terminal

// guestWaiter is a pty that runs its program in process rather than starting
// Cmd, such as the browser build's in-memory shell (see ptyspawn.NewGuestPty).
// Cmd.Wait would return at once on such a pane, since Cmd was never started,
// and the pane would close as soon as it opened. The pty knows when its
// program ends.
type guestWaiter interface {
	Wait() error
}

// waitProcess waits for the pane's process to exit.
//
// The pty is read before it is waited on: Close nils w.Pty, and the monitor
// goroutine calls this first thing, before anything could close it.
func waitProcess(w *Window) error {
	if w.Cmd.Process == nil {
		w.ioMu.RLock()
		p := w.Pty
		w.ioMu.RUnlock()
		if g, ok := p.(guestWaiter); ok {
			return g.Wait()
		}
	}
	return w.Cmd.Wait()
}
