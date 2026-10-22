//go:build !linux && !darwin

package session

// readMachineLoad has no load to read on this system.
func readMachineLoad() (load1, memPct float64, ok bool) { return 0, 0, false }
