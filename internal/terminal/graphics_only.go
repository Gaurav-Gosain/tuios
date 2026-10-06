package terminal

import "bytes"

// graphicsOnly reports whether b is nothing but kitty graphics commands that
// leave the cursor where it is, with cursor moves (CUP, HVP, DECSC, DECRC)
// between them: the whole of a frame a guest that streams images writes.
//
// Such a write changes no cell. The image goes to the host through the kitty
// passthrough, on its own path, so a compose of the screen for it draws the
// frame that is already there. A pane streaming 240 frames a second spent 1.4
// ms of the client's time on each of those composes.
//
// It is conservative. It answers false for anything it does not recognise, for
// a command that can move the cursor (a placement without C=1, which scrolls
// at the bottom row), and for a command split across two writes, which the
// usual path then handles as before.
func graphicsOnly(b []byte) bool {
	if len(b) < 4 || b[0] != 0x1b || !bytes.HasSuffix(b, []byte("\x1b\\")) {
		return false
	}
	for i := 0; i < len(b); {
		if b[i] != 0x1b || i+1 >= len(b) {
			return false
		}
		switch b[i+1] {
		case '7', '8':
			i += 2
		case '[':
			j := i + 2
			for j < len(b) && (b[j] >= '0' && b[j] <= '9' || b[j] == ';') {
				j++
			}
			if j >= len(b) || (b[j] != 'H' && b[j] != 'f') {
				return false
			}
			i = j + 1
		case '_':
			if i+2 >= len(b) || b[i+2] != 'G' {
				return false
			}
			end := bytes.Index(b[i+3:], []byte("\x1b\\"))
			if end < 0 {
				return false
			}
			body := b[i+3 : i+3+end]
			head, _, _ := bytes.Cut(body, []byte(";"))
			if !kittyKeepsCursor(head) {
				return false
			}
			i += 3 + end + 2
		default:
			return false
		}
	}
	return true
}

// kittyKeepsCursor reports whether a kitty graphics command with these control
// keys leaves the cursor and the cells alone: one that places nothing, or one
// that places with C=1.
func kittyKeepsCursor(head []byte) bool {
	action := byte('t')
	cursorStays := false
	for len(head) > 0 {
		var kv []byte
		kv, head, _ = bytes.Cut(head, []byte(","))
		if len(kv) < 2 || kv[1] != '=' {
			continue
		}
		switch kv[0] {
		case 'a':
			if len(kv) == 3 {
				action = kv[2]
			} else {
				return false
			}
		case 'C':
			cursorStays = string(kv[2:]) == "1"
		case 'U':
			// A virtual placement is drawn through placeholder cells.
			return false
		}
	}
	switch action {
	case 't', 'f', 'd', 'a', 'c':
		return true
	case 'T', 'p':
		return cursorStays
	}
	return false
}
