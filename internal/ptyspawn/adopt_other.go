//go:build windows || js

package ptyspawn

// ControlMaster reports false: a ConPTY and a guest pty have no master
// descriptor to wait on.
func ControlMaster(_ any, _ func(fd int)) bool { return false }
