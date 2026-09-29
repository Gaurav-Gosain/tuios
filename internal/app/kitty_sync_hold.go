package app

import (
	"bytes"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A guest that animates an image replaces it every frame, and says so with
// commands that only make sense together: delete the old image, transmit the
// new one, place it. It wraps each frame in a synchronized update (DEC 2026)
// so its terminal presents the three at once. OpenTUI's image renderer does
// exactly this, about a megabyte of pixels per frame.
//
// The commands reach the passthrough one at a time as the pane's emulator
// parses them, and the render loop drains the queue whenever it ticks. A tick
// that fell between the delete and the placement used to ship the delete on
// its own, so the host presented the pane without its image until a later
// tick carried the next placement. At video rates that is a flicker.
//
// So what a guest sends inside an open update is kept aside, per window,
// until the guest closes that update. Only that window waits:
//
//   - Other windows' output and tuios's own commands stay in pendingOutput
//     and go out on the next drain as before.
//   - tuios turns a guest's a=T into a transmit and a placement that the
//     refresh pass emits. For a held window that placement joins the held
//     frame, behind its transmit, so the frame goes out with its placement.
//     Re-transmitting an image id removes its placements on the host, so a
//     transmit released without the placement shows an empty pane.
//     Closed updates are released at the start of the refresh pass for the
//     same reason: the placements it computes then follow them.
//   - When tuios hides a placement of a held window, the hide goes out at
//     once and the held placements of that image are taken out, so a release
//     cannot show it again.
//   - A write that bypasses the queue for a window (the video paths) first
//     releases what that window holds, and the rest of that update is not
//     held, so the direct write cannot overtake a held delete.
//   - A hold ends after vt.SyncMaxHold whatever the guest says, and a window
//     whose held bytes pass heldWindowMaxBytes, or a total past
//     heldTotalMaxBytes, is released early. Every captured command emits whole
//     kitty sequences (chunked transmissions are joined before they are
//     emitted), so a release never splits a transmission.

const (
	// heldWindowMaxBytes bounds what one window may hold.
	heldWindowMaxBytes = maxPendingGraphicsBytes
	// heldTotalMaxBytes bounds what all windows together may hold.
	heldTotalMaxBytes = 4 * maxPendingGraphicsBytes
)

// heldUpdate is what one window's guest has sent inside its open update.
type heldUpdate struct {
	serial uint64
	since  time.Time
	buf    []byte
	// bypass is set when the window stops being held for the rest of this
	// update: after a direct write, the time limit, or the size limit.
	bypass bool
}

// SetGuestSyncProbe tells the passthrough how to ask whether a window's guest
// has an open synchronized update. A nil probe forgets the window and
// releases what it held.
func (kp *KittyPassthrough) SetGuestSyncProbe(windowID string, probe func() (open bool, serial uint64)) {
	kp.mu.Lock()
	defer kp.mu.Unlock()
	if probe == nil {
		kp.releaseHeld(windowID)
		delete(kp.syncProbes, windowID)
		return
	}
	if kp.syncProbes == nil {
		kp.syncProbes = make(map[string]func() (bool, uint64))
	}
	kp.syncProbes[windowID] = probe
}

// beginGuestCapture starts collecting what a window's guest adds to
// pendingOutput. Callers hold kp.mu and call endGuestCapture after.
func (kp *KittyPassthrough) beginGuestCapture() {
	kp.guestCapture = true
	kp.captureStart = len(kp.pendingOutput)
}

// endGuestCapture moves what the guest added since beginGuestCapture into
// the window's held update when the guest is inside one. Callers hold kp.mu.
func (kp *KittyPassthrough) endGuestCapture(windowID string) {
	kp.guestCapture = false
	start := min(kp.captureStart, len(kp.pendingOutput))
	kp.captureStart = 0

	h := kp.held[windowID]
	open, serial := false, uint64(0)
	if probe := kp.syncProbes[windowID]; probe != nil {
		open, serial = probe()
	}
	if h != nil && (!open || h.serial != serial) {
		// The update h belongs to has closed, so it is complete. It goes
		// ahead of what this command added.
		h.bypass = false
		tail := bytes.Clone(kp.pendingOutput[start:])
		kp.pendingOutput = kp.pendingOutput[:start]
		kp.releaseHeld(windowID)
		delete(kp.held, windowID)
		start = len(kp.pendingOutput)
		kp.pendingOutput = append(kp.pendingOutput, tail...)
		h = nil
	}
	if !open {
		return
	}
	if h == nil {
		if kp.held == nil {
			kp.held = make(map[string]*heldUpdate)
		}
		h = &heldUpdate{serial: serial, since: kp.now()}
		kp.held[windowID] = h
	}
	if h.bypass || start == len(kp.pendingOutput) {
		return
	}
	h.buf = append(h.buf, kp.pendingOutput[start:]...)
	kp.heldBytes += len(kp.pendingOutput) - start
	kp.pendingOutput = kp.pendingOutput[:start]

	if len(h.buf) > heldWindowMaxBytes {
		kittyPassthroughLog("sync hold: window %s holds %d bytes, releasing early", windowID, len(h.buf))
		kp.releaseHeld(windowID)
		h.bypass = true
	}
	for kp.heldBytes > heldTotalMaxBytes {
		kp.releaseLargestHeld()
	}
}

// releaseHeld appends what a window holds to pendingOutput. The entry stays,
// so a window released early is not held again for the same update.
// Callers hold kp.mu.
func (kp *KittyPassthrough) releaseHeld(windowID string) {
	h := kp.held[windowID]
	if h == nil {
		return
	}
	kp.pendingOutput = append(kp.pendingOutput, h.buf...)
	kp.heldBytes -= len(h.buf)
	h.buf = nil
	if !h.bypass {
		delete(kp.held, windowID)
	}
}

// releaseLargestHeld releases the window holding the most, and stops holding
// it for the rest of its update. Callers hold kp.mu.
func (kp *KittyPassthrough) releaseLargestHeld() {
	var id string
	most := -1
	for w, h := range kp.held {
		if len(h.buf) > most {
			id, most = w, len(h.buf)
		}
	}
	if most <= 0 {
		kp.heldBytes = 0
		return
	}
	kp.releaseHeld(id)
	kp.held[id].bypass = true
}

// unholdWindow releases what a window holds, sends the queue now, and stops
// holding that window for the rest of its update. It is called before a
// write that goes to the host without the queue. Callers hold kp.mu.
func (kp *KittyPassthrough) unholdWindow(windowID string) {
	h := kp.held[windowID]
	if h == nil {
		return
	}
	kp.releaseHeld(windowID)
	h.bypass = true
	kp.flushToHost()
}

// releaseDueHeld releases every window whose update has closed, changed or
// run past the time limit. Callers hold kp.mu.
//
// A closed update of a window with tracked placements is left for the refresh
// pass unless refreshing is set: the refresh pass may owe that frame a
// placement, and it releases the frame first and places after it. Releasing
// it from a drain that follows the pass would send the frame a tick ahead of
// its placement.
func (kp *KittyPassthrough) releaseDueHeld(refreshing bool) {
	now := kp.now()
	for id, h := range kp.held {
		open, serial := false, uint64(0)
		if probe := kp.syncProbes[id]; probe != nil {
			open, serial = probe()
		}
		switch {
		case (!open || serial != h.serial) && !refreshing && len(kp.placements[id]) > 0 &&
			now.Sub(h.since) < kp.holdLimit():
			// The next refresh pass releases it.
		case !open || serial != h.serial:
			h.bypass = false
			kp.releaseHeld(id)
			delete(kp.held, id)
		case !h.bypass && now.Sub(h.since) >= kp.holdLimit():
			kittyPassthroughLog("sync hold: window %s held past the limit, releasing", id)
			kp.releaseHeld(id)
			h.bypass = true
		}
	}
}

// holdTail moves what the refresh pass appended to pendingOutput since start,
// the placement of one host image, into the window's held update when the
// held frame names that image. Such a placement shows the frame's new data or
// position and belongs to it. A placement the frame does not touch only
// follows tuios moving or uncovering the pane, and goes out at once.
// Callers hold kp.mu.
func (kp *KittyPassthrough) holdTail(windowID string, hostID uint32, start int) {
	h := kp.held[windowID]
	if h == nil || h.bypass || start >= len(kp.pendingOutput) || !heldNames(h.buf, hostID) {
		return
	}
	h.buf = append(h.buf, kp.pendingOutput[start:]...)
	kp.heldBytes += len(kp.pendingOutput) - start
	kp.pendingOutput = kp.pendingOutput[:start]
}

// heldNames reports whether any kitty command in buf names host image id.
func heldNames(buf []byte, hostID uint32) bool {
	var id [16]byte
	want := strconv.AppendUint(append(id[:0], "i="...), uint64(hostID), 10)
	for len(buf) > 0 {
		i := bytes.Index(buf, []byte("\x1b_G"))
		if i < 0 {
			return false
		}
		buf = buf[i+3:]
		end := bytes.IndexAny(buf, ";\x1b")
		if end < 0 {
			end = len(buf)
		}
		if hasKittyKey(buf[:end], want) {
			return true
		}
		buf = buf[end:]
	}
	return false
}

// dropHeldPlacements takes every placement of a host image out of what the
// windows hold. tuios calls it when it hides that image itself, so a later
// release does not show the image again. A placement taken out is undone by
// the hide anyway, whichever order the host saw them in. Callers hold kp.mu.
func (kp *KittyPassthrough) dropHeldPlacements(hostID uint32) {
	for _, h := range kp.held {
		if len(h.buf) == 0 {
			continue
		}
		before := len(h.buf)
		h.buf = stripKittyPlacements(h.buf, hostID)
		kp.heldBytes -= before - len(h.buf)
	}
}

// stripKittyPlacements removes the a=p commands naming a host image from buf,
// in place.
func stripKittyPlacements(buf []byte, hostID uint32) []byte {
	var id [16]byte
	want := strconv.AppendUint(append(id[:0], "i="...), uint64(hostID), 10)
	out := buf[:0]
	for len(buf) > 0 {
		i := bytes.Index(buf, []byte("\x1b_G"))
		if i < 0 {
			out = append(out, buf...)
			break
		}
		end := bytes.Index(buf[i:], []byte("\x1b\\"))
		if end < 0 {
			out = append(out, buf...)
			break
		}
		end += i + 2
		seq := buf[i:end]
		ctl := seq[3:]
		if semi := bytes.IndexByte(ctl, ';'); semi >= 0 {
			ctl = ctl[:semi]
		} else {
			ctl = ctl[:len(ctl)-2]
		}
		out = append(out, buf[:i]...)
		if !(hasKittyKey(ctl, []byte("a=p")) && hasKittyKey(ctl, want)) {
			out = append(out, seq...)
		}
		buf = buf[end:]
	}
	return out
}

// hasKittyKey reports whether a kitty control string has key=value kv.
func hasKittyKey(ctl, kv []byte) bool {
	for field := range bytes.SplitSeq(ctl, []byte{','}) {
		if bytes.Equal(field, kv) {
			return true
		}
	}
	return false
}

// takePending releases what is due and hands over pendingOutput. Callers
// hold kp.mu.
func (kp *KittyPassthrough) takePending() []byte {
	kp.releaseDueHeld(false)
	out := kp.pendingOutput
	kp.pendingOutput = nil
	kp.captureStart = 0
	return out
}

func (kp *KittyPassthrough) holdLimit() time.Duration {
	if kp.syncHoldLimit > 0 {
		return kp.syncHoldLimit
	}
	return vt.SyncMaxHold
}

func (kp *KittyPassthrough) now() time.Time {
	if kp.clock != nil {
		return kp.clock()
	}
	return time.Now()
}
