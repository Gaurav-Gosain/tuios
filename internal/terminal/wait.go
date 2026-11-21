package terminal

type guestWaiter interface {
	Wait() error
}

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
