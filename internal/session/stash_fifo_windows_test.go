//go:build windows

package session

import "errors"

// makeFIFO has no Windows equivalent worth writing for one test, so the case
// that uses it skips there.
func makeFIFO(string) error { return errors.New("no fifos on windows") }
