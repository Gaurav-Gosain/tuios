//go:build !ghostty

package vt

// Backend names the emulator implementation compiled into this binary. Both
// builds install under the same `tuios` name, so the binary has to be able to
// say which one it is; deriving it from the build tag keeps it honest, where a
// string injected at link time can be forgotten or set wrong.
const Backend = "pure-Go"
