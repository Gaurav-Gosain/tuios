package app

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// imageNumberIDBase is where the ids tuios gives to numbered images start.
// Kitty assigns the id itself when a guest transmits with I= and no i=, and
// the guest may name the image by that id later. Starting high keeps the ids
// clear of the small ones guests choose for themselves.
const imageNumberIDBase uint32 = 1 << 31

// resolveImageNumber turns a command that names its image by number (I=) and
// not by id (i=) into one that names it by id, which is all the rest of the
// passthrough keys on. It returns the command's bytes with I=<n> replaced by
// i=<id>, and false when the command names a number no image was transmitted
// under, which kitty answers with ENOENT and draws nothing for.
//
// Every transmission under a number is a new image with a new id, as in
// kitty, and the number names the newest of them from then on. The image it
// named before keeps its placements, and is freed once it has none left (see
// freeRetiredNumbered), so a guest that sends a new picture under one number
// every frame does not pile up images on the host.
//
// cbirds names every sprite this way. Before this, its transmissions reached
// the host with I= intact, and every placement went out under one host id that
// nothing was ever transmitted under (#644).
func (kp *KittyPassthrough) resolveImageNumber(cmd *vt.KittyCommand, rawData []byte, windowID string) ([]byte, bool) {
	if cmd.ImageNumber == 0 || cmd.ImageID != 0 {
		return rawData, true
	}
	number := cmd.ImageNumber
	numbers := kp.imageNumbers[windowID]

	switch cmd.Action {
	case vt.KittyActionTransmit, vt.KittyActionTransmitPlace:
		if numbers == nil {
			numbers = make(map[uint32]uint32)
			if kp.imageNumbers == nil {
				kp.imageNumbers = make(map[string]map[uint32]uint32)
			}
			kp.imageNumbers[windowID] = numbers
		}
		if older, ok := numbers[number]; ok {
			kp.retireNumbered(windowID, older)
		}
		id := kp.allocateNumberedID(windowID)
		numbers[number] = id
		cmd.ImageID = id

	case vt.KittyActionDelete:
		// Only d=n and d=N name an image by number. Every other target
		// ignores I=.
		if cmd.Delete != vt.KittyDeleteByNumber && cmd.Delete != vt.KittyDeleteByNumberPlacement {
			return rawData, true
		}
		id, ok := numbers[number]
		if !ok {
			return rawData, false
		}
		cmd.ImageID = id
		if cmd.Delete == vt.KittyDeleteByNumber {
			cmd.Delete = vt.KittyDeleteByID
		} else {
			cmd.Delete = vt.KittyDeleteByIDAndPlacement
			// d=N frees the image's data, after which the number names
			// nothing. With p= it deletes that one placement, and the
			// image stays while its other placements remain.
			if cmd.PlacementID == 0 {
				delete(numbers, number)
			}
		}

	case vt.KittyActionQuery:
		return rawData, true

	default:
		id, ok := numbers[number]
		if !ok {
			return rawData, false
		}
		cmd.ImageID = id
	}

	kittyPassthroughLog("resolveImageNumber: window=%s number=%d -> guestID=%d",
		windowID[:min(8, len(windowID))], number, cmd.ImageID)
	return renameKittyImageNumber(rawData, cmd.ImageID), true
}

// allocateNumberedID picks an id for a numbered image that no image of this
// window holds.
func (kp *KittyPassthrough) allocateNumberedID(windowID string) uint32 {
	if kp.nextNumberedID < imageNumberIDBase {
		kp.nextNumberedID = imageNumberIDBase
	}
	for {
		id := kp.nextNumberedID
		kp.nextNumberedID++
		if kp.nextNumberedID == 0 {
			kp.nextNumberedID = imageNumberIDBase
		}
		if _, used := kp.imageIDMap[windowID][id]; !used {
			return id
		}
	}
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

// forgetImageNumbers drops what numbers this window's images were sent under.
func (kp *KittyPassthrough) forgetImageNumbers(windowID string) {
	delete(kp.imageNumbers, windowID)
	delete(kp.retiredNumbered, windowID)
	delete(kp.uploadReplies, windowID)
}

// uploadReply is the answer owed to a transmission that named its image.
type uploadReply struct {
	id, number, placement uint32
	quiet                 int
}

// answerUpload answers a transmission the way kitty does, once its last chunk
// is in: i=<id>, with I=<n> and p=<p> when the guest gave them, and OK. q=1
// and q=2 both suppress an OK. A transmission that names no image gets no
// answer. The id is the one the guest knows the image by, and for an image
// sent by number this answer is the only way the guest learns it.
//
// The host is not asked. Its answer would carry the host's id, and it comes
// back on tuios's input, not the pane's.
func (kp *KittyPassthrough) answerUpload(cmd *vt.KittyCommand, windowID string, ptyInput func([]byte)) {
	var r uploadReply
	if cmd.ImageID != 0 {
		r = uploadReply{id: cmd.ImageID, number: cmd.ImageNumber, placement: cmd.PlacementID, quiet: cmd.Quiet}
		if cmd.More {
			if kp.uploadReplies == nil {
				kp.uploadReplies = make(map[string]uploadReply)
			}
			kp.uploadReplies[windowID] = r
			return
		}
	} else {
		if cmd.More {
			return
		}
		pending, ok := kp.uploadReplies[windowID]
		if !ok {
			return
		}
		r = pending
	}
	delete(kp.uploadReplies, windowID)
	if r.quiet >= 1 || ptyInput == nil {
		return
	}
	reply := fmt.Appendf(nil, "\x1b_Gi=%d", r.id)
	if r.number != 0 {
		reply = fmt.Appendf(reply, ",I=%d", r.number)
	}
	if r.placement != 0 {
		reply = fmt.Appendf(reply, ",p=%d", r.placement)
	}
	reply = append(reply, ";OK\x1b\\"...)
	ptyInput(reply)
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
