//go:build js

package session

// verbKnown reports false: the browser build runs no daemon and talks to
// none, so no answer it gets can come from an older one. Asking the verb
// table would link it, and with it every verb handler the daemon has, about
// 3 MB of the browser build.
func verbKnown(string) bool { return false }
