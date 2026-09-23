//go:build !js

package terminal

// waitProcess waits for the pane's process to exit.
func waitProcess(w *Window) error { return w.Cmd.Wait() }
