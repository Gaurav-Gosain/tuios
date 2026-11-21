package app

import (
	"bytes"
	"strconv"
	"time"

	"tuios.dev/vt"
)

const (
	heldWindowMaxBytes = maxPendingGraphicsBytes
	heldTotalMaxBytes  = 4 * maxPendingGraphicsBytes
)

type heldUpdate struct {
	serial  uint64
	since   time.Time
	buf     []byte
	ids     map[uint32]bool
	scanned int
	bypass  bool
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

func (kp *KittyPassthrough) beginGuestCapture() {
	kp.guestCapture = true
	kp.captureStart = len(kp.pendingOutput)
}

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

func (kp *KittyPassthrough) unholdWindow(windowID string) {
	h := kp.held[windowID]
	if h == nil {
		return
	}
	kp.releaseHeld(windowID)
	h.bypass = true
	kp.flushToHost()
}

func (kp *KittyPassthrough) releaseDueHeld(refreshing bool) {
	kp.renderPasses++
	now := kp.now()
	for id, h := range kp.held {
		open, serial := false, uint64(0)
		if probe := kp.syncProbes[id]; probe != nil {
			open, serial = probe()
		}
		switch {
		case (!open || serial != h.serial) && !refreshing && len(kp.placements[id]) > 0 &&
			now.Sub(h.since) < kp.holdLimit():
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

func (kp *KittyPassthrough) holdTail(windowID string, hostID uint32, start int, tuiosMoved bool) {
	h := kp.held[windowID]
	if h == nil || h.bypass || start >= len(kp.pendingOutput) || !h.names(hostID) {
		return
	}
	h.buf = append(h.buf, kp.pendingOutput[start:]...)
	kp.heldBytes += len(kp.pendingOutput) - start
	if tuiosMoved {
		return
	}
	kp.pendingOutput = kp.pendingOutput[:start]
}

type heldStreamFrame struct {
	hostID uint32
	serial uint64
	size   int
	cmd    vt.KittyCommand
	path   string
	passes uint64
}

func (kp *KittyPassthrough) noteHeldStreamFrame(windowID string, hostID uint32, serial uint64, size int, cmd *vt.KittyCommand, path string) {
	if kp.heldFrames == nil {
		kp.heldFrames = make(map[string]heldStreamFrame)
	}
	kp.heldFrames[windowID] = heldStreamFrame{hostID: hostID, serial: serial, size: size, cmd: *cmd, path: path, passes: kp.renderPasses}
}

func (kp *KittyPassthrough) replaceHeldStreamFrame(windowID string, hostID uint32, next *vt.KittyCommand) {
	rec, ok := kp.heldFrames[windowID]
	if !ok {
		return
	}
	delete(kp.heldFrames, windowID)
	h := kp.held[windowID]
	if h == nil || h.bypass || rec.hostID != hostID || h.serial != rec.serial ||
		len(h.buf) != rec.size || rec.passes != kp.renderPasses {
		return
	}
	kp.heldBytes -= len(h.buf)
	delete(kp.held, windowID)
	if rec.cmd.FilePath != next.FilePath {
		releaseKittyMedium(&rec.cmd, rec.path)
	}
}

func (h *heldUpdate) names(hostID uint32) bool {
	if h.scanned > len(h.buf) {
		h.ids, h.scanned = nil, 0
	}
	for buf := h.buf[h.scanned:]; ; {
		i := bytes.Index(buf, []byte("\x1b_G"))
		if i < 0 {
			break
		}
		buf = buf[i+3:]
		end := bytes.IndexAny(buf, ";\x1b")
		if end < 0 {
			break
		}
		for field := range bytes.SplitSeq(buf[:end], []byte{','}) {
			if v, ok := bytes.CutPrefix(field, []byte("i=")); ok {
				if id, err := strconv.ParseUint(string(v), 10, 32); err == nil {
					if h.ids == nil {
						h.ids = make(map[uint32]bool)
					}
					h.ids[uint32(id)] = true
				}
			}
		}
		buf = buf[end:]
		h.scanned = len(h.buf) - len(buf)
	}
	return h.ids[hostID]
}

func (kp *KittyPassthrough) dropHeldPlacements(hostID uint32) {
	kp.dropHeldPlacementRange(hostID, 0, 0)
}

func (kp *KittyPassthrough) dropHeldPlacementRange(hostID, first, n uint32) {
	for _, h := range kp.held {
		if len(h.buf) == 0 {
			continue
		}
		before := len(h.buf)
		h.buf = stripKittyPlacements(h.buf, hostID, first, n)
		h.ids, h.scanned = nil, 0
		kp.heldBytes -= before - len(h.buf)
	}
}

func stripKittyPlacements(buf []byte, hostID, first, n uint32) []byte {
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
		match := hasKittyKey(ctl, want) && inPlacementRange(ctl, first, n)
		if !(match && hasKittyKey(ctl, []byte("a=p"))) {
			if match && hasKittyKey(ctl, []byte("a=T")) {
				if at := bytes.Index(seq, []byte("a=T")); at >= 0 {
					seq[at+2] = 't'
				}
			}
			out = append(out, seq...)
		}
		buf = buf[end:]
	}
	return out
}

func inPlacementRange(ctl []byte, first, n uint32) bool {
	if n == 0 {
		return true
	}
	for field := range bytes.SplitSeq(ctl, []byte{','}) {
		if v, ok := bytes.CutPrefix(field, []byte("p=")); ok {
			id, err := strconv.ParseUint(string(v), 10, 32)
			return err == nil && uint32(id) >= first && uint32(id)-first < n
		}
	}
	return false
}

func hasKittyKey(ctl, kv []byte) bool {
	for field := range bytes.SplitSeq(ctl, []byte{','}) {
		if bytes.Equal(field, kv) {
			return true
		}
	}
	return false
}

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

type paneGeom struct {
	x, y, w, h, cw, ch, scroll, z, lx, ly, lw, lh, sw, sh int
	visible                                               bool
}

func geomOf(i *WindowPositionInfo) paneGeom {
	return paneGeom{i.WindowX, i.WindowY, i.Width, i.Height, i.ContentWidth, i.ContentHeight,
		i.ScrollOffset, i.WindowZ, i.LayoutX, i.LayoutY, i.LayoutW, i.LayoutH, i.ScreenWidth, i.ScreenHeight, i.Visible}
}
