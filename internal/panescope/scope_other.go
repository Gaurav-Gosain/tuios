//go:build !linux

package panescope

// serviceSlice: only Linux has systemd.
func serviceSlice() (string, bool) { return "", false }

func place(int, string, func(string, ...any)) {}
