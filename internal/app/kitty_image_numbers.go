package app

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// resolveImageNumber finishes, for the host, what the emulator did for a
// command that names its image by number (I=). The emulator has already
// given the image an id (see vt.KittyGuestImages), so the rest of the
// passthrough keys on that id like on any other. What is left is the bytes:
// I=<n> becomes i=<id>, so the host files the image under the id tuios
// translates. An image the number named before is retired, to be freed once
// it has no placement left.
//
// It returns false for a command that still names a number with no id. Only
// a caller that skipped the emulator can send one, and it names nothing.
//
// cbirds names every sprite this way. Before this, its transmissions reached
// the host with I= intact, and every placement went out under one host id that
// nothing was ever transmitted under (#644).
func (kp *KittyPassthrough) resolveImageNumber(cmd *vt.KittyCommand, rawData []byte, windowID string) ([]byte, bool) {
	if cmd.ImageNumber == 0 || cmd.Action == vt.KittyActionQuery {
		return rawData, true
	}
	if cmd.ImageID == 0 {
		if cmd.Action == vt.KittyActionDelete &&
			cmd.Delete != vt.KittyDeleteByNumber && cmd.Delete != vt.KittyDeleteByNumberPlacement {
			return rawData, true
		}
		return rawData, false
	}
	if cmd.ReplacedImageID != 0 {
		kp.retireNumbered(windowID, cmd.ReplacedImageID)
	}
	kittyPassthroughLog("resolveImageNumber: window=%s number=%d -> guestID=%d",
		windowID[:min(8, len(windowID))], cmd.ImageNumber, cmd.ImageID)
	return renameKittyImageNumber(rawData, cmd.ImageID), true
}

// retireNumbered marks an image that its number no longer names. It is freed
// once it has no placement left.
func (kp *KittyPassthrough) retireNumbered(windowID string, guestID uint32) {
	if kp.retiredNumbered == nil {
		kp.retiredNumbered = make(map[string]map[uint32]struct{})
	}
	if kp.retiredNumbered[windowID] == nil {
		kp.retiredNumbered[windowID] = make(map[uint32]struct{})
	}
	kp.retiredNumbered[windowID][guestID] = struct{}{}
}

// freeRetiredNumbered frees the host images of this window that their number
// no longer names and that have no placement left. Nothing but their id can
// name them now, and a guest that sends a new picture under one number every
// frame would otherwise fill the host with old ones.
func (kp *KittyPassthrough) freeRetiredNumbered(windowID string) {
	for guestID := range kp.retiredNumbered[windowID] {
		hostID, ok := kp.imageIDMap[windowID][guestID]
		if !ok {
			delete(kp.retiredNumbered[windowID], guestID)
			continue
		}
		if kp.placements[windowID][hostID] != nil {
			continue
		}
		kittyPassthroughLog("freeRetiredNumbered: window=%s guestID=%d hostID=%d",
			windowID[:min(8, len(windowID))], guestID, hostID)
		kp.releaseHostID(windowID, hostID)
		delete(kp.imageIDMap[windowID], guestID)
		kp.forgetImagePixels(windowID, guestID)
		delete(kp.retiredNumbered[windowID], guestID)
	}
}

// forgetImageNumbers drops the numbered images of this window waiting to
// be freed.
func (kp *KittyPassthrough) forgetImageNumbers(windowID string) {
	delete(kp.retiredNumbered, windowID)
}

// renameKittyImageNumber returns an APC sequence with its I= parameter
// replaced by i=<id>. The payload and every other parameter stay
// byte-identical. Anything that is not a whole sequence, such as the nil a
// header-only parse hands over, comes back as it was.
func renameKittyImageNumber(rawData []byte, id uint32) []byte {
	const prefix = "\x1b_G"
	if !bytes.HasPrefix(rawData, []byte(prefix)) {
		return rawData
	}
	body := rawData[len(prefix):]
	semi := bytes.IndexByte(body, ';')
	if semi < 0 {
		semi = len(body)
		if end := bytes.Index(body, []byte("\x1b\\")); end >= 0 {
			semi = end
		}
	}

	var out bytes.Buffer
	out.WriteString(prefix)
	for i, pair := range strings.Split(string(body[:semi]), ",") {
		if i > 0 {
			out.WriteByte(',')
		}
		if strings.HasPrefix(pair, "I=") {
			fmt.Fprintf(&out, "i=%d", id)
			continue
		}
		out.WriteString(pair)
	}
	out.Write(body[semi:])
	return out.Bytes()
}
