package main

import (
	"bytes"
	"strconv"
	"strings"
)

type queryFilter struct {
	dropStatus bool
	state      filterState
	head       []byte
	esc        bool
}

type filterState int

const (
	fsGround filterState = iota
	fsEsc
	fsCSI
	fsString
	fsPass
	fsDrop
)

const stringHeadMax = 256

const csiMax = 128

const (
	esc = 0x1b
	can = 0x18
	sub = 0x1a
	bel = 0x07
)

func (f *queryFilter) Write(b []byte) []byte {
	out := make([]byte, 0, len(b)+16)
	for i := range b {
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

func (f *queryFilter) stringByte(out []byte, c byte) []byte {
	if f.esc {
		f.esc = false
		if c == '\\' {
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
		if f.state == fsString {
			out = append(out, f.head...)
		}
		f.head = append(f.head[:0], esc)
		f.state = fsEsc
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
	if f.dropStatus && bytes.Equal(f.head, []byte("\x1b]7501;")) {
		f.state = fsDrop
		f.head = f.head[:0]
		return out
	}
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

func (f *queryFilter) Write2(out []byte, c byte) []byte {
	return append(out, f.Write([]byte{c})...)
}

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
		return (prefix == 0 || prefix == '>' || prefix == '=') && (p == "" || p == "0")
	case final == 'n' && inter == "":
		if prefix == 0 {
			return p == "5" || p == "6"
		}
		return prefix == '?' && isNumber(p)
	case final == 'q' && inter == "":
		return prefix == '>' && (p == "" || p == "0")
	case final == 'p' && inter == "$":
		return (prefix == 0 || prefix == '?') && isNumber(p)
	case final == 'u' && inter == "":
		return prefix == '?' && p == ""
	case final == 'S' && inter == "":
		return prefix == '?' && len(nums) >= 2 && isNumber(nums[0])
	case final == 'm' && inter == "":
		return prefix == '?' && isNumber(p)
	case final == 't' && inter == "" && prefix == 0:
		switch first {
		case "11", "13", "14", "15", "16", "18", "19", "20", "21":
			return true
		}
	case final == 'x' && inter == "" && prefix == 0:
		return p == "" || p == "0" || p == "1"
	case final == 'w' && inter == "$":
		return prefix == 0 && (p == "1" || p == "2")
	case final == 'u' && inter == "$":
		return prefix == 0
	case final == 'y' && inter == "*":
		return prefix == 0
	case final == 'v' && inter == "\"":
		return prefix == 0
	case final == 'u' && inter == "&":
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

func decideEarly(seq []byte) (decided, keep bool, out []byte) {
	if len(seq) < 3 {
		return false, false, nil
	}
	body := seq[2:]
	switch seq[1] {
	case 'P':
		if len(body) < 2 {
			return false, false, nil
		}
		if bytes.HasPrefix(body, []byte("+q")) || bytes.HasPrefix(body, []byte("$q")) {
			return true, false, nil
		}
		return true, true, seq
	case '_':
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
	return false, false, nil
}

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

func isOSCQuery(body string) bool {
	cmd, args, _ := strings.Cut(body, ";")
	switch cmd {
	case "4", "5":
		parts := strings.Split(args, ";")
		for i := 1; i < len(parts); i += 2 {
			if parts[i] == "?" {
				return true
			}
		}
	case "10", "11", "12", "13", "14", "15", "16", "17", "18", "19":
		for p := range strings.SplitSeq(args, ";") {
			if p == "?" {
				return true
			}
		}
	case "21":
		for kv := range strings.SplitSeq(args, ";") {
			if strings.HasSuffix(kv, "=?") || kv == "?" {
				return true
			}
		}
	case "52":
		_, data, _ := strings.Cut(args, ";")
		return data == "?"
	}
	return false
}
