package session

import "time"

func (s *Session) noteStateChangeLocked() {
	s.changeSeq++
	s.noteWorkspaceLocked()
	s.stateDirty.Set()
	s.noteIdleStateChange()
	s.wakeStateWaiters()
}

func (s *Session) wakeStateWaiters() {
	s.stateWakeMu.Lock()
	if s.stateWake != nil {
		close(s.stateWake)
		s.stateWake = nil
	}
	s.stateWakeMu.Unlock()
}

func (s *Session) stateWakeChan() <-chan struct{} {
	s.stateWakeMu.Lock()
	defer s.stateWakeMu.Unlock()
	if s.stateWake == nil {
		s.stateWake = make(chan struct{})
	}
	return s.stateWake
}

func (s *Session) waitChange(limit time.Duration, cond func() bool) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
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
