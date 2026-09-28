package session

import "time"

// evidenceNow is the time the detection verbs measure evidence age against.
func (d *Daemon) evidenceNow() time.Time {
	if d.evidenceClock != nil {
		return d.evidenceClock()
	}
	return time.Now()
}

// evidenceStamp is the unix-nano time the last piece of evidence about a
// pane's agent state arrived, or 0 when nothing ever set a state.
//
// For a state the agent or a rule said about itself (a report, a hook, a
// transcript, an OSC sequence, a screen rule), the evidence is the saying, and
// AgentStateAt is when it was said. A title or screen look that only reads
// back the claim it already holds does not restamp it (see lookRepeatsClaim),
// so a spinner left in a title ages like the silence it is.
//
// For a state the daemon inferred, detect or stall, the evidence is the pane
// itself. The detector says working from presence and the silence timer says
// unknown or idle from quiet, and both are measured against the pane's output:
// applyStallHeuristic takes the later of AgentStateAt and the last output
// (stalledAt). The age follows the same rule, so a pane that keeps printing
// reads fresh rather than aging from the moment the detector first saw it.
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

// evidenceAgeMS is how long ago, in milliseconds, stamp was, or nil when
// stamp is 0 (nothing ever set a state). evidenceStamp says what the stamp is.
// It is the answer to "how stale is what this pane says", which confidence
// alone cannot give: a certain report from an hour ago is still an hour old.
//
// A stamp in the future, which a restored or merged state from a machine with
// a faster clock can carry, reads as zero rather than a negative age.
func evidenceAgeMS(stamp int64, now time.Time) any {
	if stamp <= 0 {
		return nil
	}
	return max(now.UnixNano()-stamp, 0) / int64(time.Millisecond)
}
