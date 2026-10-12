package session

import (
	"bytes"
	"fmt"
)

// escapeC1 is a JSON line with DEL and the C1 control characters (U+0080 to
// U+009F) written as \u escapes. encoding/json escapes the C0 controls and
// leaves these raw, and a terminal reads U+009B as CSI and U+009D as OSC. A
// name from another machine reaches the daemon's replies and events (a copy's
// file names, its errors), and a script may print those lines as they come,
// so the daemon escapes them before any consumer sees them. Outside a JSON
// string these runes never occur, so the line stays valid JSON.
func escapeC1(line []byte) []byte {
	if bytes.IndexByte(line, 0x7f) < 0 && bytes.IndexByte(line, 0xc2) < 0 {
		return line
	}
	out := make([]byte, 0, len(line)+16)
	for i := 0; i < len(line); i++ {
		b := line[i]
		switch {
		case b == 0x7f:
			out = append(out, `\u007f`...)
		case b == 0xc2 && i+1 < len(line) && line[i+1] >= 0x80 && line[i+1] <= 0x9f:
			out = fmt.Appendf(out, `\u%04x`, line[i+1])
			i++
		default:
			out = append(out, b)
		}
	}
	return out
}
