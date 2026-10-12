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
// A transmission under a number gets an id here, as kitty gives it one. A
// later transmission under the same number reuses that id, so a guest that
// sends a new picture under one number every frame replaces the image instead
// of piling up new ones on the host. Every other command resolves the number
// to the id of the image last transmitted under it, which is kitty's rule.
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
		id, ok := numbers[number]
		if !ok {
			id = kp.allocateNumberedID(windowID)
			if numbers == nil {
				numbers = make(map[uint32]uint32)
				if kp.imageNumbers == nil {
					kp.imageNumbers = make(map[string]map[uint32]uint32)
				}
				kp.imageNumbers[windowID] = numbers
			}
			numbers[number] = id
		}
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
			// The data is freed, so the number names nothing after this.
			cmd.Delete = vt.KittyDeleteByIDAndPlacement
			delete(numbers, number)
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
	inUse := func(id uint32) bool {
		if _, ok := kp.imageIDMap[windowID][id]; ok {
			return true
		}
		for _, other := range kp.imageNumbers[windowID] {
			if other == id {
				return true
			}
		}
		return false
	}
	id := imageNumberIDBase + uint32(len(kp.imageNumbers[windowID]))
	for id == 0 || inUse(id) {
		id++
	}
	return id
}

// forgetImageNumbers drops what numbers this window's images were sent under.
func (kp *KittyPassthrough) forgetImageNumbers(windowID string) {
	delete(kp.imageNumbers, windowID)
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
