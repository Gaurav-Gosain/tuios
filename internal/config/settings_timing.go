package config

import "time"

// WhichKeyDelay is the pause after the leader before which-key opens.
func (s *Settings) WhichKeyDelay() time.Duration {
	return time.Duration(s.WhichKeyDelayMS) * time.Millisecond
}

// TooltipDelay is how long the pointer rests on a control before its tooltip
// shows.
func (s *Settings) TooltipDelay() time.Duration {
	return time.Duration(s.TooltipDelayMS) * time.Millisecond
}

// ZenReveal is how long the pointer may sit still before zen mode (mouse) hides
// the borders again.
func (s *Settings) ZenReveal() time.Duration {
	return time.Duration(s.ZenRevealMS) * time.Millisecond
}

// PastePreviewDuration is how long the paste card stays up with nothing pressed.
func (s *Settings) PastePreviewDuration() time.Duration {
	return time.Duration(s.PastePreviewMS) * time.Millisecond
}

// ChordTimeout is how long the first key of a two-key chord waits for the
// second.
func (s *Settings) ChordTimeout() time.Duration {
	return time.Duration(s.ChordTimeoutMS) * time.Millisecond
}
