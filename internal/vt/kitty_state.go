package vt

import "sync"

// KittyState is the clear-notification relay between the emulator and the
// passthrough layer: a guest screen clear (ED, mode reset) must also clear
// the images the passthrough painted on the host terminal.
type KittyState struct {
	mu            sync.RWMutex
	clearCallback func()
}

func NewKittyState() *KittyState {
	return &KittyState{}
}

// SetClearCallback registers the passthrough hook fired on guest screen
// clears.
func (s *KittyState) SetClearCallback(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCallback = fn
}

// ClearPlacements reports a guest screen clear to the passthrough layer,
// which owns the placements.
func (s *KittyState) ClearPlacements() {
	s.mu.RLock()
	callback := s.clearCallback
	s.mu.RUnlock()
	if callback != nil {
		callback()
	}
}
