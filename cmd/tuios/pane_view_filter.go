package main

import (
	"bytes"
	"strconv"
)

// queryFilter takes a pane's output on its way to the terminal pane-view runs
// in, and drops the requests that terminal would answer.
//
// The daemon's own emulator already answers every request a program in the
// pane makes: DA, cursor position, XTVERSION, colour queries, mode reports.
// pane-view also passes the bytes on to the outer terminal, which answers them
// too, and pane-view types everything it reads from the outer terminal into
// the pane. Without the filter the program gets each answer twice, and the
// second one arrives as typed input: "^[[?62;22c" at a shell prompt, or a
// stray key in an editor.
//
// How it could go wrong, and what holds it:
//   - A sequence split across two output frames. The filter keeps its state
//     between writes, so a request cut anywhere is still recognised.
//   - A long string sequence (a kitty image, a sixel) held whole in memory.
//     Only a short head is held; the rest streams through or is dropped as it
//     comes.
//   - A request dropped that the program needed answered by the outer
//     terminal. None is: the daemon answers them all, before pane-view sees
//     the bytes.
//   - A sequence that is not a request dropped. Only the exact request forms
//     are dropped; anything else, malformed ones included, passes as written.
//   - A kitty graphics command answered twice. The daemon answers it, so the
//     filter asks the outer terminal for quiet (q=2) when the program did not
//     choose, and drops a query action (a=q) outright.
//   - TSP (Tern's surface protocol) reaching the outer terminal without the
//     program having negotiated it with that terminal. It is dropped.
type queryFilter struct {
	state filterState
	// head holds the sequence being read until it is decided.
	head []byte
	// esc is set while a string sequence has just read ESC, which may start
	// its ST terminator.
	esc bool
}

type filterState int

const (
	fsGround filterState = iota
	fsEsc                // read ESC
	fsCSI                // ESC [ ...
	fsString             // OSC, DCS or APC: head held until decided
	fsPass               // a decided string sequence, passed through to its end
	fsDrop               // a decided string sequence, dropped to its end
)

// stringHeadMax is how much of a string sequence is held before it is
// decided. Every request form fits in it; a sequence still undecided past it
// is not a request.
const stringHeadMax = 512

// csiMax bounds a held CSI sequence. A longer one is passed as written.
const csiMax = 256

// Write filters b and returns what should reach the terminal. The result may
// hold bytes of earlier calls, and may hold back the end of b until the
// sequence it starts is complete.
func (f *queryFilter) Write(b []byte) []byte {
	out := make([]byte, 0, len(b)+16)
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch f.state {
		case fsGround:
			if c == 0x1b {
				f.state = fsEsc
				f.head = append(f.head[:0], c)
				continue
			}
			out = append(out, c)
		case fsEsc:
			f.head = append(f.head, c)
			switch c {
			case '[':
				f.state = fsCSI
			case ']', 'P', '_':
				f.state = fsString
				f.esc = false
			case 0x1b:
				// ESC ESC: the first is a lone escape.
				out = append(out, 0x1b)
				f.head = f.head[:1]
			default:
				out = append(out, f.head...)
				f.head = f.head[:0]
				f.state = fsGround
			}
		case fsCSI:
			if c == 0x1b {
				// An ESC cancels the CSI and starts a new sequence.
				out = append(out, f.head...)
				f.head = append(f.head[:0], c)
				f.state = fsEsc
				continue
			}
			f.head = append(f.head, c)
			if c >= 0x40 && c <= 0x7e {
				if !isCSIRequest(f.head) {
					out = append(out, f.head...)
				}
				f.head = f.head[:0]
				f.state = fsGround
			} else if len(f.head) > csiMax || c < 0x20 {
				// Not a CSI the filter knows; pass it as written. A control
				// inside a CSI is executed by terminals, so it passes too.
				out = append(out, f.head...)
				f.head = f.head[:0]
				f.state = fsGround
			}
		case fsString:
			end := f.stringEnds(c)
			f.head = append(f.head, c)
			if end {
				if keep, rewritten := decideString(f.head, true); keep {
					out = append(out, rewritten...)
				}
				f.head = f.head[:0]
				f.state = fsGround
				continue
			}
			if decided, keep, rewritten := decideEarly(f.head); decided || len(f.head) > stringHeadMax {
				if !decided {
					keep, rewritten = true, f.head
				}
				if keep {
					out = append(out, rewritten...)
					f.state = fsPass
				} else {
					f.state = fsDrop
				}
				f.head = f.head[:0]
			}
		case fsPass, fsDrop:
			end := f.stringEnds(c)
			if f.state == fsPass {
				out = append(out, c)
			}
			if end {
				f.state = fsGround
			}
		}
	}
	return out
}

// stringEnds reports whether c ends the string sequence being read: BEL, or
// the backslash of ESC \.
func (f *queryFilter) stringEnds(c byte) bool {
	if c == 0x07 {
		f.esc = false
		return true
	}
	if f.esc && c == '\\' {
		f.esc = false
		return true
	}
	f.esc = c == 0x1b
	return false
}

// isCSIRequest reports whether a complete CSI sequence (ESC [ ... final) asks
// the terminal for an answer.
func isCSIRequest(seq []byte) bool {
	body := seq[2 : len(seq)-1]
	final := seq[len(seq)-1]
	params, inter := body, []byte(nil)
	for j, c := range body {
		if c >= 0x20 && c <= 0x2f {
			params, inter = body[:j], body[j:]
			break
		}
	}
	p := string(params)
	switch final {
	case 'c': // DA1, DA2, DA3
		return len(inter) == 0 && (p == "" || p == "0" || p == ">" || p == ">0" || p == "=" || p == "=0")
	case 'n': // DSR and its private forms: status, cursor position, colour scheme
		return len(inter) == 0
	case 'q': // XTVERSION is CSI > q; CSI SP q is the cursor shape
		return len(inter) == 0 && (p == ">" || p == ">0")
	case 'p': // DECRQM
		return string(inter) == "$"
	case 'u': // kitty keyboard flags query
		return len(inter) == 0 && p == "?"
	case 'S': // XTSMGRAPHICS
		return len(inter) == 0 && len(p) > 0 && p[0] == '?'
	case 'm': // XTQMODKEYS
		return len(inter) == 0 && len(p) > 0 && p[0] == '?'
	case 't': // XTWINOPS reports
		if len(inter) != 0 {
			return false
		}
		first, _, _ := bytes.Cut(params, []byte{';'})
		n, err := strconv.Atoi(string(first))
		if err != nil {
			return false
		}
		switch n {
		case 11, 13, 14, 15, 16, 18, 19, 20, 21:
			return true
		}
	}
	return false
}

// decideEarly decides a string sequence from its head, before its end, when
// the head is enough. seq starts with ESC and the introducer.
func decideEarly(seq []byte) (decided, keep bool, out []byte) {
	if len(seq) < 3 {
		return false, false, nil
	}
	body := seq[2:]
	switch seq[1] {
	case 'P': // DCS
		if len(body) < 2 {
			return false, false, nil
		}
		if bytes.HasPrefix(body, []byte("+q")) || bytes.HasPrefix(body, []byte("$q")) {
			return true, false, nil
		}
		return true, true, seq
	case '_': // APC
		if bytes.HasPrefix(body, []byte("tsp")) {
			return true, false, nil
		}
		if len(body) < 3 && bytes.HasPrefix([]byte("tsp"), body) {
			return false, false, nil
		}
		if body[0] != 'G' {
			return true, true, seq
		}
		semi := bytes.IndexByte(body, ';')
		if semi < 0 {
			return false, false, nil
		}
		keep, out := kittyCommand(seq, semi+2)
		return true, keep, out
	}
	// OSC: decided at its end, where a query shows as a trailing ?.
	return false, false, nil
}

// decideString decides a complete string sequence.
func decideString(seq []byte, complete bool) (bool, []byte) {
	if decided, keep, out := decideEarly(seq); decided {
		return keep, out
	}
	if seq[1] == '_' && len(seq) > 2 && seq[2] == 'G' {
		// A kitty command with no payload: its keys end at the terminator.
		end := len(seq) - 1
		if seq[end] == '\\' {
			end--
		}
		keep, out := kittyCommand(seq, end)
		return keep, out
	}
	if seq[1] == ']' && complete && isOSCQuery(seq) {
		return false, nil
	}
	return true, seq
}

// kittyCommand decides a kitty graphics command whose keys end at keysEnd:
// a query is dropped, and a command that did not choose its replies is asked
// for none, since the daemon answers it.
func kittyCommand(seq []byte, keysEnd int) (bool, []byte) {
	keys := seq[3:keysEnd]
	hasQ := false
	for kv := range bytes.SplitSeq(keys, []byte{','}) {
		if bytes.Equal(kv, []byte("a=q")) {
			return false, nil
		}
		if bytes.HasPrefix(kv, []byte("q=")) {
			hasQ = true
		}
	}
	if hasQ {
		return true, seq
	}
	out := make([]byte, 0, len(seq)+4)
	out = append(out, seq[:3]...)
	out = append(out, "q=2"...)
	if len(keys) > 0 {
		out = append(out, ',')
	}
	out = append(out, seq[3:]...)
	return true, out
}

// isOSCQuery reports whether a complete OSC sequence asks for a value: its
// last parameter is "?" (OSC 10/11/12 ; ?, OSC 4 ; n ; ?, OSC 52 ; c ; ?).
func isOSCQuery(seq []byte) bool {
	body := seq[2:]
	if n := len(body); n > 0 && body[n-1] == 0x07 {
		body = body[:n-1]
	} else if n >= 2 && body[n-2] == 0x1b && body[n-1] == '\\' {
		body = body[:n-2]
	}
	return bytes.HasSuffix(body, []byte(";?"))
}
