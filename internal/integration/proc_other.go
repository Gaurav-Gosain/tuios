//go:build !linux && !darwin

package integration

// processName has no portable source here, so every name is unknown and
// HarnessPID takes the nearest ancestor.
func processName(int) string { return "" }
