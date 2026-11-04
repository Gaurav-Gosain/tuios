// Package clilink puts the kero command in a directory: a link to the
// app's executable, which is the CLI too.
package clilink

// Target returns the app's executable that the command in bin runs, ""
// when there is none.
func Target(bin string) string { return target(bin) }

// Link makes bin's kero run exe, unless it does already.
func Link(exe, bin string) error { return link(exe, bin) }
