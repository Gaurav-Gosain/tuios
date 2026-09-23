//go:build js

package terminal

// waitProcess waits for the pane's guest to exit. In the browser build the
// exec.Cmd was never started (there is no process to start), so Cmd.Wait would
// return at once and close the pane. The guest pty knows when its program ends.
//
// The pty is captured before it is waited on: Close nils w.Pty, and the
// monitor goroutine calls this first thing, before anything could close it.
func waitProcess(w *Window) error {
	w.ioMu.RLock()
	p := w.Pty
	w.ioMu.RUnlock()
	if waiter, ok := p.(interface{ Wait() error }); ok {
		return waiter.Wait()
	}
	return nil
}
