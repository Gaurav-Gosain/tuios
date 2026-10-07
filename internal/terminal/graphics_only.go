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
//
// ground says the parser was between sequences when b began. A write that
// starts while a character, a sequence or a string (a sixel DCS, say) is half
// read finishes it, and finishing it can draw.
func graphicsOnly(ground bool, b []byte) bool {
	if !ground {
		return false
	}
	if len(b) < 4 || b[0] != 0x1b || !bytes.HasSuffix(b, []byte("\x1b\\")) {
		return false
	}
	// open is set while a chunked transmission that started in this write
	// is waiting for its last chunk, and keeps says whether its first chunk
	// leaves the cursor alone. A chunk with no action belongs to the
	// command its first chunk started, and only that first chunk says what
	// the last one does: an a=T without C=1 places the image, and moves the
	// cursor, when its last chunk arrives. A chunk whose first chunk came in
	// an earlier write is therefore refused.
	open, keeps := false, false
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
			// A control or a byte past ASCII ends the string early in the
			// parser (CAN, SUB, ESC), and what follows is printed. Kitty
			// commands are ASCII: keys, numbers and base64.
			for _, c := range body {
				if c < 0x20 || c > 0x7e {
					return false
				}
			}
			head, _, _ := bytes.Cut(body, []byte(";"))
			k := parseKittyHead(head)
			if k.bad {
				return false
			}
			if k.action == 0 && k.more != 0 {
				// A chunk of a transmission.
				if !open || !keeps {
					return false
				}
				open = k.more == '1'
			} else {
				if open || !k.keepsCursor() {
					return false
				}
				open, keeps = k.more == '1', true
			}
			i += 3 + end + 2
		default:
			return false
		}
	}
	return !open
}

// kittyHead is the part of a kitty command's control keys graphicsOnly reads.
type kittyHead struct {
	action      byte // 0 when the command names none
	more        byte // the m= value, 0 when absent
	cursorStays bool // C=1
	virtual     bool // U=1
	bad         bool // a key graphicsOnly cannot read
}

// parseKittyHead reads the control keys of a kitty graphics command.
func parseKittyHead(head []byte) kittyHead {
	var k kittyHead
	for len(head) > 0 {
		var kv []byte
		kv, head, _ = bytes.Cut(head, []byte(","))
		if len(kv) < 2 || kv[1] != '=' {
			continue
		}
		switch kv[0] {
		case 'a':
			if len(kv) != 3 {
				k.bad = true
				return k
			}
			k.action = kv[2]
		case 'm':
			if len(kv) != 3 || (kv[2] != '0' && kv[2] != '1') {
				k.bad = true
				return k
			}
			k.more = kv[2]
		case 'C':
			k.cursorStays = string(kv[2:]) == "1"
		case 'U':
			k.virtual = true
		}
	}
	return k
}

// keepsCursor reports whether a command with these keys leaves the cursor and
// the cells alone: one that places nothing, or one that places with C=1.
func (k kittyHead) keepsCursor() bool {
	if k.virtual {
		// A virtual placement is drawn through placeholder cells.
		return false
	}
	action := k.action
	if action == 0 {
		action = 't'
	}
	switch action {
	case 't', 'f', 'd', 'a', 'c':
		return true
	case 'T', 'p':
		return k.cursorStays
	}
	return false
}
