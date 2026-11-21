package session

import "time"

func (d *Daemon) evidenceNow() time.Time {
	if d.evidenceClock != nil {
		return d.evidenceClock()
	}
	return time.Now()
}

func (s *Session) evidenceStamp(w WindowState, claim agentClaim) int64 {
	if w.AgentStateAt <= 0 {
		return 0
	}
	switch claim.source {
	case AgentSourceDetect, AgentSourceStall:
	default:
		return w.AgentStateAt
	}
	if w.PTYID == "" {
		return w.AgentStateAt
	}
	if pty := s.GetPTY(w.PTYID); pty != nil {
		return max(w.AgentStateAt, pty.LastOutput())
	}
	return w.AgentStateAt
}

func evidenceAgeMS(stamp int64, now time.Time) any {
	if stamp <= 0 {
		return nil
	}
	return max(now.UnixNano()-stamp, 0) / int64(time.Millisecond)
}
