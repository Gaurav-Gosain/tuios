package session

import "time"

// evidenceNow is the time the detection verbs measure evidence age against.
func (d *Daemon) evidenceNow() time.Time {
	if d.evidenceClock != nil {
		return d.evidenceClock()
	}
	return time.Now()
}

// evidenceAgeMS is how long ago, in milliseconds, the last piece of evidence
// about a pane's agent state arrived, or nil when nothing ever set one.
//
// The last evidence is AgentStateAt. Every source that says something new
// stamps it: a report, a hook, the detector promoting or clearing the pane,
// the silence timer. A title or screen look that only reads back the claim it
// already holds does not (see lookRepeatsClaim), so a spinner left in a title
// ages like the silence it is. That makes the age the answer to "how stale is
// what this pane says", which confidence alone cannot give: a certain report
// from an hour ago is still an hour old.
//
// A stamp in the future, which a restored or merged state from a machine with
// a faster clock can carry, reads as zero rather than a negative age.
func evidenceAgeMS(stateAt int64, now time.Time) any {
	if stateAt <= 0 {
		return nil
	}
	return max(now.UnixNano()-stateAt, 0) / int64(time.Millisecond)
}
