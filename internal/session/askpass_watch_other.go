//go:build !unix

package session

// askpassWatch never closes here: a prompt ends with its answer, a cancel or
// its timeout.
func askpassWatch(int, <-chan struct{}) <-chan struct{} { return make(chan struct{}) }
