package session

import (
	"bytes"
	"fmt"
)

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
