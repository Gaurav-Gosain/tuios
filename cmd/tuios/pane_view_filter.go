package main

import (
	"bytes"
	"strconv"
	"strings"
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
//   - Output that is not a request dropped. Each rule names one exact request
//     form, by its final byte, its prefix and its intermediates, and checks
//     its parameters: a title ending in ";?" and CSI > 4 n (a
//     modifyOtherKeys reset) pass as written. XTSMGRAPHICS is dropped
//     whatever its action, because a terminal answers a set as it answers a
//     read.
//   - A request let through. The list covers the requests xterm, kitty and
//     the DEC terminals answer, OSC 21 and the DEC page and checksum reports
//     included. A kitty graphics command always goes out with q=2.
//   - Output held back. A string sequence (OSC, DCS, APC) ends at its
//     terminator, and is abandoned at the next ESC that does not start ST, or
//     at CAN or SUB, as a terminal abandons it. Only a short head is held
//     while a string is decided; past it the rest streams through.
//   - Bytes inside a string read as sequences. A string's payload is never
//     scanned: a CSI inside an OSC title is part of the title.
//   - TSP (Tern's surface protocol) reaching the outer terminal without the
//     program having negotiated it with that terminal. It is dropped.
type queryFilter struct {
	state filterState
	// head holds the sequence being read until it is decided.
	head []byte
	// esc is set while a string sequence has just read ESC.
	esc bool
}

type filterState int

const (
	fsGround filterState = iota
	fsEsc                // read ESC
	fsCSI                // ESC [ ...
	fsString             // OSC, DCS or APC, head held until decided
	fsPass               // a decided string, passed through to its end
	fsDrop               // a decided string, dropped to its end
)

// stringHeadMax is how much of a string sequence is held before it is
// decided. Every request form fits in it; a string still undecided past it is
// not a request.
const stringHeadMax = 256

// csiMax bounds a held CSI sequence. A longer one is passed as written.
const csiMax = 128

const (
	esc = 0x1b
	can = 0x18
	sub = 0x1a
	bel = 0x07
)

// Write filters b and returns what should reach the terminal. The result may
// hold bytes of earlier calls, and may hold back the end of b until the
// sequence it starts is complete.
func (f *queryFilter) Write(b []byte) []byte {
	out := make([]byte, 0, len(b)+16)
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch f.state {
		case fsGround:
			if c == esc {
				f.state = fsEsc
				f.head = append(f.head[:0], c)
				continue
			}
			out = append(out, c)
		case fsEsc:
			switch c {
			case '[':
				f.head = append(f.head, c)
				f.state = fsCSI
			case ']', 'P', '_':
				f.head = append(f.head, c)
				f.state = fsString
				f.esc = false
			case esc:
				// ESC ESC: the first is a lone escape.
				out = append(out, esc)
			default:
				out = append(out, f.head...)
				out = append(out, c)
				f.head = f.head[:0]
				f.state = fsGround
			}
		case fsCSI:
			switch {
			case c == esc:
				// An ESC cancels the CSI and starts a new sequence.
				out = append(out, f.head...)
				f.head = append(f.head[:0], c)
				f.state = fsEsc
			case c == can || c == sub:
				out = append(out, f.head...)
				out = append(out, c)
				f.head = f.head[:0]
				f.state = fsGround
			case c >= 0x40 && c <= 0x7e:
				f.head = append(f.head, c)
				if !isCSIRequest(f.head) {
					out = append(out, f.head...)
				}
				f.head = f.head[:0]
				f.state = fsGround
			default:
				f.head = append(f.head, c)
				if len(f.head) > csiMax {
					out = append(out, f.head...)
					f.head = f.head[:0]
					f.state = fsGround
				}
			}
		case fsString, fsPass, fsDrop:
			out = f.stringByte(out, c)
		}
	}
	return out
}

// stringByte takes one byte of a string sequence.
func (f *queryFilter) stringByte(out []byte, c byte) []byte {
	if f.esc {
		f.esc = false
		if c == '\\' {
			// ST: the string is complete.
			switch f.state {
			case fsString:
				f.head = append(f.head, esc, c)
				if keep, seq := decideString(f.head); keep {
					out = append(out, seq...)
				}
			case fsPass:
				out = append(out, esc, c)
			}
			f.head = f.head[:0]
			f.state = fsGround
			return out
		}
		// Any other byte after ESC abandons the string, as a terminal does,
		// and the ESC starts a new sequence. A held string is let through
		// so the outer terminal abandons it too.
		if f.state == fsString {
			out = append(out, f.head...)
		}
		f.head = append(f.head[:0], esc)
		f.state = fsEsc
		// Reprocess c as the byte after that ESC.
		return f.Write2(out, c)
	}
	switch c {
	case esc:
		f.esc = true
		return out
	case bel:
		switch f.state {
		case fsString:
			f.head = append(f.head, c)
			if keep, seq := decideString(f.head); keep {
				out = append(out, seq...)
			}
		case fsPass:
			out = append(out, c)
		}
		f.head = f.head[:0]
		f.state = fsGround
		return out
	case can, sub:
		if f.state == fsString {
			out = append(out, f.head...)
		}
		if f.state != fsDrop {
			out = append(out, c)
		}
		f.head = f.head[:0]
		f.state = fsGround
		return out
	}
	switch f.state {
	case fsPass:
		return append(out, c)
	case fsDrop:
		return out
	}
	f.head = append(f.head, c)
	if decided, keep, seq := decideEarly(f.head); decided {
		if keep {
			out = append(out, seq...)
			f.state = fsPass
		} else {
			f.state = fsDrop
		}
		f.head = f.head[:0]
	} else if len(f.head) > stringHeadMax {
		out = append(out, f.head...)
		f.head = f.head[:0]
		f.state = fsPass
	}
	return out
}

// Write2 runs Write on one byte, appending to out. It is how an abandoned
// string hands the byte after its ESC back to the parser.
func (f *queryFilter) Write2(out []byte, c byte) []byte {
	return append(out, f.Write([]byte{c})...)
}

// isCSIRequest reports whether a complete CSI sequence (ESC [ ... final) is
// one of the requests a terminal answers.
func isCSIRequest(seq []byte) bool {
	body := seq[2 : len(seq)-1]
	final := seq[len(seq)-1]
	prefix := byte(0)
	if len(body) > 0 && body[0] >= 0x3c && body[0] <= 0x3f {
		prefix = body[0]
		body = body[1:]
	}
	params, inter := body, ""
	for j, c := range body {
		if c >= 0x20 && c <= 0x2f {
			params, inter = body[:j], string(body[j:])
			break
		}
	}
	p := string(params)
	nums := strings.Split(p, ";")
	first := nums[0]
	switch {
	case final == 'c' && inter == "":
		// DA1 (CSI c), DA2 (CSI > c), DA3 (CSI = c).
		return (prefix == 0 || prefix == '>' || prefix == '=') && (p == "" || p == "0")
	case final == 'n' && inter == "":
		// DSR: status (5), cursor position (6), and the DEC private reports
		// (CSI ? Ps n). CSI > Ps n is a modifyOtherKeys reset, not a request.
		if prefix == 0 {
			return p == "5" || p == "6"
		}
		return prefix == '?' && isNumber(p)
	case final == 'q' && inter == "":
		// XTVERSION. CSI SP q is the cursor shape.
		return prefix == '>' && (p == "" || p == "0")
	case final == 'p' && inter == "$":
		// DECRQM, ANSI and DEC private.
		return (prefix == 0 || prefix == '?') && isNumber(p)
	case final == 'u' && inter == "":
		// The kitty keyboard flags query.
		return prefix == '?' && p == ""
	case final == 'S' && inter == "":
		// XTSMGRAPHICS. Every action is answered, a set and a reset too:
		// xterm replies with the status and the value now in force. The
		// daemon's emulator answers it, so the outer terminal's reply would
		// be typed into the pane a second time.
		return prefix == '?' && len(nums) >= 2 && isNumber(nums[0])
	case final == 'm' && inter == "":
		// XTQMODKEYS. CSI > Pp m sets a resource.
		return prefix == '?' && isNumber(p)
	case final == 't' && inter == "" && prefix == 0:
		// XTWINOPS reports.
		switch first {
		case "11", "13", "14", "15", "16", "18", "19", "20", "21":
			return true
		}
	case final == 'x' && inter == "" && prefix == 0:
		// DECREQTPARM.
		return p == "" || p == "0" || p == "1"
	case final == 'w' && inter == "$":
		// DECRQPSR: the cursor or tab stop report.
		return prefix == 0 && (p == "1" || p == "2")
	case final == 'u' && inter == "$":
		// DECRQTSR.
		return prefix == 0
	case final == 'y' && inter == "*":
		// DECRQCRA, the checksum of a rectangle.
		return prefix == 0
	case final == 'v' && inter == "\"":
		// DECRQDE, the displayed extent.
		return prefix == 0
	case final == 'u' && inter == "&":
		// DECRQUPSS, the user preferred supplemental set.
		return prefix == 0
	}
	return false
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.Atoi(s)
	return err == nil
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
		// XTGETTCAP (DCS + q) and DECRQSS (DCS $ q).
		if bytes.HasPrefix(body, []byte("+q")) || bytes.HasPrefix(body, []byte("$q")) {
			return true, false, nil
		}
		return true, true, seq
	case '_': // APC
		if len(body) < 3 && bytes.HasPrefix([]byte("tsp"), body) {
			return false, false, nil
		}
		if bytes.HasPrefix(body, []byte("tsp")) {
			return true, false, nil
		}
		if body[0] != 'G' {
			return true, true, seq
		}
		semi := bytes.IndexByte(body, ';')
		if semi < 0 {
			return false, false, nil
		}
		keep, out := kittyCommand(body[1:semi], body[semi:])
		return true, keep, out
	}
	// OSC: decided at its end.
	return false, false, nil
}

// decideString decides a complete string sequence, terminator included.
func decideString(seq []byte) (bool, []byte) {
	end := len(seq) - 1
	if seq[end] == '\\' {
		end--
	}
	body := seq[2:end]
	switch seq[1] {
	case ']':
		if isOSCQuery(string(body)) {
			return false, nil
		}
		return true, seq
	case '_':
		if bytes.HasPrefix(body, []byte("tsp")) {
			return false, nil
		}
		if len(body) > 0 && body[0] == 'G' {
			// A kitty command with no payload: its keys end at the
			// terminator.
			keep, out := kittyCommand(body[1:], seq[end:])
			return keep, out
		}
	case 'P':
		if bytes.HasPrefix(body, []byte("+q")) || bytes.HasPrefix(body, []byte("$q")) {
			return false, nil
		}
	}
	return true, seq
}

// kittyCommand decides a kitty graphics command: a query (a=q) is dropped,
// and any other command goes out with q=2, whatever it asked, since the
// daemon answers it. rest is everything after the keys, from the ';' or the
// terminator on.
func kittyCommand(keys, rest []byte) (bool, []byte) {
	kept := make([]string, 0, 8)
	for kv := range strings.SplitSeq(string(keys), ",") {
		if kv == "a=q" {
			return false, nil
		}
		if strings.HasPrefix(kv, "q=") || kv == "" {
			continue
		}
		kept = append(kept, kv)
	}
	out := make([]byte, 0, len(keys)+len(rest)+8)
	out = append(out, esc, '_', 'G')
	out = append(out, "q=2"...)
	for _, kv := range kept {
		out = append(out, ',')
		out = append(out, kv...)
	}
	return true, append(out, rest...)
}

// isOSCQuery reports whether an OSC body (no introducer, no terminator) asks
// the terminal for a value, by the exact query form of its command.
func isOSCQuery(body string) bool {
	cmd, args, _ := strings.Cut(body, ";")
	switch cmd {
	case "4", "5":
		// OSC 4 ; index ; ? (pairs): a request when any spec is ?.
		parts := strings.Split(args, ";")
		for i := 1; i < len(parts); i += 2 {
			if parts[i] == "?" {
				return true
			}
		}
	case "10", "11", "12", "13", "14", "15", "16", "17", "18", "19":
		// Dynamic colours: each ? asks for one.
		for p := range strings.SplitSeq(args, ";") {
			if p == "?" {
				return true
			}
		}
	case "21":
		// kitty's colour protocol: key=? asks for that key.
		for kv := range strings.SplitSeq(args, ";") {
			if strings.HasSuffix(kv, "=?") || kv == "?" {
				return true
			}
		}
	case "52":
		// OSC 52 ; selection ; ? reads the clipboard.
		_, data, _ := strings.Cut(args, ";")
		return data == "?"
	}
	return false
}
