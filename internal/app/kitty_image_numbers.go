package app

import (
	"bytes"
	"fmt"
	"strings"

	"tuios.dev/vt"
)

func (kp *KittyPassthrough) resolveImageNumber(cmd *vt.KittyCommand, rawData []byte, windowID string) ([]byte, bool) {
	if cmd.ImageNumber == 0 || cmd.Action == vt.KittyActionQuery {
		return rawData, true
	}
	if cmd.ImageID == 0 {
		if cmd.Action == vt.KittyActionDelete &&
			cmd.Delete != vt.KittyDeleteByNumber && cmd.Delete != vt.KittyDeleteByNumberFree {
			return rawData, true
		}
		return rawData, false
	}
	kittyPassthroughLog("resolveImageNumber: window=%s number=%d -> guestID=%d",
		windowID[:min(8, len(windowID))], cmd.ImageNumber, cmd.ImageID)
	return renameKittyImageNumber(rawData, cmd.ImageID), true
}

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
