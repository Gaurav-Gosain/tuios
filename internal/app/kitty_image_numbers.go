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
// translates.
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
	kittyPassthroughLog("resolveImageNumber: window=%s number=%d -> guestID=%d",
		windowID[:min(8, len(windowID))], cmd.ImageNumber, cmd.ImageID)
	return renameKittyImageNumber(rawData, cmd.ImageID), true
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
