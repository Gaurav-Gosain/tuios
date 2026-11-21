//go:build !linux

package panescope

func serviceSlice() (string, bool) { return "", false }

func place(int, string, func(string, ...any)) {}
