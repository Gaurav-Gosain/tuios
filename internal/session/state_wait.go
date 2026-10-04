package session

import "time"

// noteStateChangeLocked records a change to the session's state: the
// resurrection saver writes it, every WaitState wakes to look at it, and the
// workspace trail notes the workspace on screen. The caller holds stateMu for
// writing.
func (s *Session) noteStateChangeLocked() {
	s.noteWorkspaceLocked()
	s.stateDirty.Store(true)
	s.wakeStateWaiters()
}

// wakeStateWaiters wakes every goroutine in WaitState or waitChange. It
// closes the current channel, and the next waiter makes a new one.
func (s *Session) wakeStateWaiters() {
	s.stateWakeMu.Lock()
	if s.stateWake != nil {
		close(s.stateWake)
		s.stateWake = nil
	}
	s.stateWakeMu.Unlock()
}

// stateWakeChan is the channel the next wakeStateWaiters closes.
func (s *Session) stateWakeChan() <-chan struct{} {
	s.stateWakeMu.Lock()
	defer s.stateWakeMu.Unlock()
	if s.stateWake == nil {
		s.stateWake = make(chan struct{})
	}
	return s.stateWake
}

// waitChange waits up to limit for cond to hold, looking again each time the
// session wakes its waiters, and reports whether it held. cond must take no
// lock that is held while a waiter is woken.
func (s *Session) waitChange(limit time.Duration, cond func() bool) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		// The channel is taken before the look, so a change that lands
		// between the two still wakes this loop.
		wake := s.stateWakeChan()
		if cond() {
			return true
		}
		select {
		case <-wake:
		case <-timer.C:
			return cond()
		}
	}
}

// WaitState waits up to limit for pred to hold of the session's state, and
// reports whether it did. pred sees the stored state under the read lock,
// without the live facts GetState adds. It must not keep or change it.
func (s *Session) WaitState(limit time.Duration, pred func(*SessionState) bool) bool {
	return s.waitChange(limit, func() bool {
		s.stateMu.RLock()
		defer s.stateMu.RUnlock()
		return pred(s.state)
	})
}
