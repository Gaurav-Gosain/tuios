package dirwatch

import "time"

// Settle is how long a burst of changes is left to settle before it is
// reported. A `git checkout` or an `rm -r` is thousands of events, and a
// listing only needs the state they leave behind. It is spent only after a
// change, never while the folder is quiet.
const Settle = 100 * time.Millisecond

// Nudge sends a change on ch without blocking. ch holds one change, so the
// changes nobody has taken yet fold into one. It is the notify function a
// caller of Watch passes, bound to its channel.
func Nudge(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// AfterBurst waits Settle for the burst that started with a change taken from
// ch to end, and then drops the changes the burst left on ch, so one read of
// the folder covers them all. It returns false, early, when stop is closed
// first. A nil stop never closes.
func AfterBurst(ch <-chan struct{}, stop <-chan struct{}) bool {
	t := time.NewTimer(Settle)
	defer t.Stop()
	select {
	case <-t.C:
	case <-stop:
		return false
	}
	select {
	case <-ch:
	default:
	}
	return true
}
