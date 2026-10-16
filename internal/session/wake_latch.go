package session

import "sync/atomic"

// wakeLatch is a flag that can wake one goroutine asleep on it.
//
// It is how the daemon's background loops sleep at idle instead of polling.
// The side that notices a change calls Set. The loop blocks on C while it has
// nothing to do, and reads and lowers the flag with Take when it does its work.
//
// Set is meant for hot paths, such as every chunk a pane prints. While the
// flag is up it is one atomic load. Only the edge from lowered to raised sends
// a token, and the channel holds one, so a flood of Sets wakes the loop once.
//
// The zero value keeps the flag but wakes nobody: Set drops the token when the
// channel was never made. init makes the channel.
type wakeLatch struct {
	up atomic.Bool
	c  chan struct{}
}

// init makes the channel. It runs before any goroutine shares the latch.
func (l *wakeLatch) init() { l.c = make(chan struct{}, 1) }

// Set raises the flag and wakes the loop if the flag was down.
func (l *wakeLatch) Set() {
	if l.up.Load() {
		return
	}
	if l.up.CompareAndSwap(false, true) {
		select {
		case l.c <- struct{}{}:
		default:
		}
	}
}

// Take lowers the flag and reports whether it was up.
func (l *wakeLatch) Take() bool { return l.up.Swap(false) }

// Up reports whether the flag is up, and leaves it as it is.
func (l *wakeLatch) Up() bool { return l.up.Load() }

// C receives a token after Set raises the flag. A token can outlive the flag:
// a loop that wakes on it reads the flag before it acts.
func (l *wakeLatch) C() <-chan struct{} { return l.c }
