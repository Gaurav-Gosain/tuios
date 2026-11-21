//go:build unix && !linux

package session

// askpassWatch returns a channel that closes when process pid exits, looked
// at every askpassPoll until stop closes.
func askpassWatch(pid int, stop <-chan struct{}) <-chan struct{} {
	gone := make(chan struct{})
	if pid > 0 {
		go pollExit(pid, stop, gone)
	}
	return gone
}
