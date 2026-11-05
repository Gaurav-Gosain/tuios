package composer

import "time"

// TypingHoldFor is how long after the last key a hold lasts.
const TypingHoldFor = 1500 * time.Millisecond

// TypingHold is the rule that keeps a key typed into a draft from answering
// something that appeared while the person typed. A host that offers answers
// beside the composer, such as an approval or a question from an agent,
// records each key with Key and asks Held before an answer acts and before it
// draws the answer.
//
// The rule:
//
//   - Something that arrived, or changed, during the current run of typing is
//     held. A run starts at the first key after a pause of TypingHoldFor or
//     more, and ends TypingHoldFor after its last key.
//   - Something that was there before the run started is not held: the person
//     saw it before they started to type.
//   - Nothing is held while the composer does not have the keyboard. A host
//     that closes the composer, sends the draft or moves the keyboard away
//     passes focused as false, or calls Reset.
//
// A hold always ends TypingHoldFor after the last key, so it can never hold
// forever. The type keeps two times and has no timer: the host schedules one
// redraw at the release time Held returns, and only while something is held.
//
// The zero value holds nothing.
type TypingHold struct {
	// Start is the first key of the current run of typing.
	Start time.Time
	// Last is the last key typed.
	Last time.Time
}

// Key records a key typed at now.
func (h *TypingHold) Key(now time.Time) {
	if h.Last.IsZero() || now.Sub(h.Last) >= TypingHoldFor || now.Before(h.Last) {
		h.Start = now
	}
	h.Last = now
}

// Reset ends the run of typing: the draft was sent or closed.
func (h *TypingHold) Reset() { *h = TypingHold{} }

// Held reports whether something that arrived at arrived is held at now,
// with the composer focused or not. release is when the hold ends, zero when
// nothing is held. A zero arrived is something that was there before the
// composer opened, and is never held.
func (h TypingHold) Held(arrived, now time.Time, focused bool) (held bool, release time.Time) {
	if !focused || h.Last.IsZero() || arrived.IsZero() || arrived.Before(h.Start) {
		return false, time.Time{}
	}
	release = h.Last.Add(TypingHoldFor)
	if !now.Before(release) {
		return false, time.Time{}
	}
	return true, release
}
