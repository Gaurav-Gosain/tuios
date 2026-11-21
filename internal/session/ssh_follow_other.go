//go:build !linux && !darwin

package session

func readParentAndOwner(int) (int, int, bool) { return 0, 0, false }

func readArgvExact(int) []string { return nil }
