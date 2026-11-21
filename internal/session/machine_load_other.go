//go:build !linux && !darwin

package session

func readMachineLoad() (load1, memPct float64, ok bool) { return 0, 0, false }
