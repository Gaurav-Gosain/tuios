//go:build !js

package session

func verbKnown(verb string) bool {
	_, ok := verbRegistry()[verb]
	return ok
}
