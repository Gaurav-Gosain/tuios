//go:build !linux && !darwin

package integration

func processName(int) string { return "" }
