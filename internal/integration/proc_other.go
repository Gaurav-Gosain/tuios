//go:build !linux && !darwin

package integration

// Other platforms have no session id a pane's shell leads and no portable
// parent read, so only the immediate parent is offered.
func selfSID() int { return 0 }

// processName has no portable source here, so every name is unknown and
// HarnessPID takes the nearest ancestor.
func processName(int) string { return "" }
