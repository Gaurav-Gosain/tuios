//go:build !js

package session

// verbKnown reports whether this build has a handler for verb.
func verbKnown(verb string) bool {
	_, ok := verbRegistry()[verb]
	return ok
}
