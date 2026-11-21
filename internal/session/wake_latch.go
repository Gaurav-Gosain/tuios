package session

import "sync/atomic"

type wakeLatch struct {
	up atomic.Bool
	c  chan struct{}
}

func (l *wakeLatch) init() { l.c = make(chan struct{}, 1) }

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

func (l *wakeLatch) Take() bool { return l.up.Swap(false) }

func (l *wakeLatch) Up() bool { return l.up.Load() }

func (l *wakeLatch) C() <-chan struct{} { return l.c }
