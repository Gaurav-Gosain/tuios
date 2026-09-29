package app

import "bytes"

// A guest that animates an image replaces it every frame, and says so with
// commands that only make sense together: delete the old image, transmit the
// new one, place it. It wraps each frame in a synchronized update (DEC 2026)
// so its terminal presents the three at once. OpenTUI's image renderer does
// exactly this, about a megabyte of pixels per frame.
//
// The commands reach the passthrough one at a time as the pane's emulator
// parses them, and the render loop drains the queue whenever it ticks. A tick
// that fell between the delete and the placement used to ship the delete on
// its own, inside tuios's own synchronized update, so the host presented an
// empty pane until the next tick carried the new bitmap. At video rates that
// is a flicker, and with enough ticks landing there the image is gone more
// often than it is shown.
//
// So the queue is released only up to the start of the oldest update that is
// still open. Everything after it stays queued, in order, until the guest
// closes that update or the emulator stops honouring it (syncMaxHold in the
// vt package). Positions in the queue rather than per-window buffers keep the
// host seeing commands in the order they were produced, including the
// refresh pass's re-placements and other windows' output that land in
// between.

// guestSyncMark is where in pendingOutput a window's open synchronized update
// began, and the serial of that update.
type guestSyncMark struct {
	serial uint64
	at     int
}

// SetGuestSyncProbe tells the passthrough how to ask whether a window's guest
// has an open synchronized update. A nil probe forgets the window.
func (kp *KittyPassthrough) SetGuestSyncProbe(windowID string, probe func() (open bool, serial uint64)) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if probe == nil {
		delete(kp.syncProbes, windowID)
		delete(kp.syncMarks, windowID)
		return
	}
	if kp.syncProbes == nil {
		kp.syncProbes = make(map[string]func() (bool, uint64))
	}
	kp.syncProbes[windowID] = probe
}

// forgetGuestSync drops a window's probe and mark, releasing what it held.
// Callers hold kp.mu.
func (kp *KittyPassthrough) forgetGuestSync(windowID string) {
	delete(kp.syncProbes, windowID)
	delete(kp.syncMarks, windowID)
}

// noteGuestSync is called before a window's guest adds to pendingOutput. If
// the guest is inside a synchronized update that has not queued anything yet,
// this records where its output starts. Callers hold kp.mu.
func (kp *KittyPassthrough) noteGuestSync(windowID string) {
	probe := kp.syncProbes[windowID]
	if probe == nil {
		return
	}
	open, serial := probe()
	if !open {
		delete(kp.syncMarks, windowID)
		return
	}
	if mark, ok := kp.syncMarks[windowID]; ok && mark.serial == serial {
		return
	}
	// A different serial means the update the old mark belonged to has
	// closed, so what it held is complete and may go.
	if kp.syncMarks == nil {
		kp.syncMarks = make(map[string]guestSyncMark)
	}
	kp.syncMarks[windowID] = guestSyncMark{serial: serial, at: len(kp.pendingOutput)}
}

// releasableLen is how much of pendingOutput may go to the host now: all of
// it, or up to the start of the oldest synchronized update still open. Marks
// whose update has closed are dropped. Callers hold kp.mu.
func (kp *KittyPassthrough) releasableLen() int {
	cut := len(kp.pendingOutput)
	for id, mark := range kp.syncMarks {
		probe := kp.syncProbes[id]
		if probe == nil {
			delete(kp.syncMarks, id)
			continue
		}
		if open, serial := probe(); !open || serial != mark.serial {
			delete(kp.syncMarks, id)
			continue
		}
		cut = min(cut, mark.at)
	}
	return cut
}

// takeReleasable removes and returns the part of pendingOutput that may go to
// the host now. The returned slice is the caller's. Callers hold kp.mu.
func (kp *KittyPassthrough) takeReleasable() []byte {
	cut := kp.releasableLen()
	if cut == 0 {
		return nil
	}
	if cut == len(kp.pendingOutput) {
		out := kp.pendingOutput
		kp.pendingOutput = nil
		kp.shiftSyncMarks(cut)
		return out
	}
	out := bytes.Clone(kp.pendingOutput[:cut])
	kp.dropReleased(cut)
	return out
}

// dropReleased removes the first cut bytes of pendingOutput, which the caller
// has sent, keeping the held rest. Callers hold kp.mu.
func (kp *KittyPassthrough) dropReleased(cut int) {
	n := copy(kp.pendingOutput, kp.pendingOutput[cut:])
	kp.pendingOutput = kp.pendingOutput[:n]
	kp.shiftSyncMarks(cut)
}

// shiftSyncMarks moves every mark back by the cut bytes released ahead of
// it. Callers hold kp.mu.
func (kp *KittyPassthrough) shiftSyncMarks(cut int) {
	for id, mark := range kp.syncMarks {
		mark.at -= cut
		kp.syncMarks[id] = mark
	}
}
