package terminal

import "bytes"

func graphicsOnly(ground bool, b []byte) bool {
	if !ground {
		return false
	}
	if len(b) < 4 || b[0] != 0x1b || !bytes.HasSuffix(b, []byte("\x1b\\")) {
		return false
	}
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

type kittyHead struct {
	action      byte
	more        byte
	cursorStays bool
	virtual     bool
	bad         bool
}

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

func (k kittyHead) keepsCursor() bool {
	if k.virtual {
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
