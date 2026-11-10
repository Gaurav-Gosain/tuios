package client

import (
	"bytes"
	"strconv"
	"unicode/utf8"

	"tuios.dev/vt"
)

// Images across a snapshot.
//
// A snapshot paints the pane on a reset terminal, and the reset takes the
// terminal's images with it. The daemon keeps no pixels: where a picture was,
// its grid holds reserved cells, sixel marker cells under id 0, for a sixel
// image (vt.Emulator.placeSixel) and for a kitty image that moved the cursor
// (vt.Emulator.ReserveKittyPlacement). Those cells scroll and reflow with the
// text, so the snapshot shows where each picture is now.
//
// So the stream keeps the pictures it passed on, in the order they came, as
// the kitty commands that draw them again without moving the cursor. At a
// snapshot it finds the first cell of each reserved block, matches the blocks
// to the kept pictures from the newest back, and writes each picture's
// commands just before its first cell: the cursor is on that cell then, and
// the picture is drawn there. A picture whose block is gone (scrolled out of
// the snapshot, cleared) is let go.

// Bounds on what one pane keeps. A picture past keepImageMax is not kept,
// and past keepTotalMax the oldest go first.
const (
	keepImageMax = 16 << 20
	keepTotalMax = 48 << 20
)

// keptImage is one picture: the kitty commands that upload and place it at
// the cursor with C=1 and q=2, and its width in cells, 0 when unknown.
type keptImage struct {
	data []byte
	cols int
	// id is the image id the program gave (i=), 0 for none, so a delete
	// of that id lets the picture go.
	id uint64
}

// imageKeep follows the kitty commands in a pane's output and keeps the
// pictures. It sees the output in order, the sixel images as the kitty
// images sixelConv made of them.
type imageKeep struct {
	list  []*keptImage
	total int
	// apc is the body of a command still arriving ("G..."), and inAPC says
	// one is. esc says the last byte seen was an ESC: outside a command it
	// may start one, inside it may end it.
	apc   []byte
	inAPC bool
	esc   bool
	// skip says the command arriving is too large to keep.
	skip bool
	// cur is the chunked upload being kept; it is kept once its last chunk
	// came.
	cur *keptImage
}

// reset drops what is half seen, for a snapshot. The pictures stay.
func (k *imageKeep) reset() {
	k.apc, k.inAPC, k.esc, k.skip, k.cur = nil, false, false, false, nil
}

// observe reads output passed on to the terminal.
func (k *imageKeep) observe(b []byte, cellW int) {
	for len(b) > 0 {
		if !k.inAPC {
			if k.esc {
				k.esc = false
				if b[0] == '_' {
					k.inAPC, k.apc, k.skip = true, k.apc[:0], false
					b = b[1:]
					continue
				}
			}
			i := bytes.IndexByte(b, 0x1b)
			if i < 0 {
				return
			}
			b = b[i+1:]
			k.esc = true
			continue
		}
		if k.esc {
			k.esc = false
			if b[0] == '\\' {
				k.inAPC = false
				if !k.skip {
					k.command(k.apc, cellW)
				}
				k.apc = nil
				b = b[1:]
				continue
			}
			// An ESC that is not ST ends the command, and starts another
			// sequence.
			k.inAPC, k.apc = false, nil
			k.esc = true
			continue
		}
		i := bytes.IndexByte(b, 0x1b)
		part := b
		if i >= 0 {
			part = b[:i]
		}
		if !k.skip {
			if len(k.apc) == 0 && len(part) > 0 && part[0] != 'G' || len(k.apc)+len(part) > keepImageMax {
				k.skip, k.apc = true, nil
			} else {
				k.apc = append(k.apc, part...)
			}
		}
		if i < 0 {
			return
		}
		b = b[i+1:]
		k.esc = true
	}
}

// kittyKeys reads a command's control keys.
func kittyKeys(ctrl []byte) map[byte][]byte {
	keys := map[byte][]byte{}
	for kv := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(kv) >= 2 && kv[1] == '=' {
			keys[kv[0]] = kv[2:]
		}
	}
	return keys
}

// command takes one kitty graphics command ("G" control ";" payload).
func (k *imageKeep) command(body []byte, cellW int) {
	if len(body) < 1 || body[0] != 'G' {
		return
	}
	ctrl, _, _ := bytes.Cut(body[1:], []byte{';'})
	keys := kittyKeys(ctrl)
	a, hasA := keys['a']
	more := string(keys['m']) == "1"
	if k.cur != nil {
		if !hasA {
			// The next chunk of the upload kept.
			k.cur.data = appendAPC(k.cur.data, body)
			if !more {
				k.add(k.cur)
				k.cur = nil
			}
			return
		}
		k.cur = nil
	}
	switch string(a) {
	case "T":
		if string(keys['C']) == "1" || string(keys['U']) == "1" {
			return // the daemon reserved nothing for it
		}
		// The same command, with the cursor kept and no reply: the
		// program asked once, and the picture is only drawn again.
		var hdr []byte
		hdr = append(hdr, 'G')
		for kv := range bytes.SplitSeq(ctrl, []byte{','}) {
			if len(kv) >= 2 && (kv[0] == 'C' || kv[0] == 'q') && kv[1] == '=' {
				continue
			}
			hdr = append(hdr, kv...)
			hdr = append(hdr, ',')
		}
		hdr = append(hdr, "C=1,q=2"...)
		if _, payload, ok := bytes.Cut(body[1:], []byte{';'}); ok {
			hdr = append(hdr, ';')
			hdr = append(hdr, payload...)
		}
		img := &keptImage{data: appendAPC(nil, hdr)}
		img.id, _ = strconv.ParseUint(string(keys['i']), 10, 32)
		if c, err := strconv.Atoi(string(keys['c'])); err == nil && c > 0 {
			img.cols = c
		} else if s, err := strconv.Atoi(string(keys['s'])); err == nil && s > 0 && cellW > 0 && keys['r'] == nil {
			img.cols = (s + cellW - 1) / cellW
		}
		if more {
			k.cur = img
		} else {
			k.add(img)
		}
	case "d":
		d := keys['d']
		switch {
		case len(d) == 0 || d[0] == 'a' || d[0] == 'A':
			k.list, k.total = nil, 0
		case d[0] == 'i' || d[0] == 'I':
			id, err := strconv.ParseUint(string(keys['i']), 10, 32)
			if err != nil {
				return
			}
			k.list = dropID(k.list, id)
			k.total = 0
			for _, img := range k.list {
				k.total += len(img.data)
			}
		}
	}
}

func dropID(list []*keptImage, id uint64) []*keptImage {
	out := list[:0]
	for _, img := range list {
		if img.id != id {
			out = append(out, img)
		}
	}
	return out
}

// appendAPC appends an APC with body.
func appendAPC(out, body []byte) []byte {
	out = append(out, "\x1b_"...)
	out = append(out, body...)
	return append(out, "\x1b\\"...)
}

// keepSixel keeps a sixel image's kitty commands (C=1 already), cols wide.
func (k *imageKeep) keepSixel(apc []byte, cols int) {
	k.add(&keptImage{data: bytes.Clone(apc), cols: cols})
}

func (k *imageKeep) add(img *keptImage) {
	if len(img.data) > keepImageMax {
		return
	}
	k.list = append(k.list, img)
	k.total += len(img.data)
	for k.total > keepTotalMax && len(k.list) > 1 {
		k.total -= len(k.list[0].data)
		k.list[0] = nil
		k.list = k.list[1:]
	}
}

// marker is one reserved cell found in a snapshot.
type marker struct {
	at, end  int
	row, col int
}

// replay rewrites a snapshot's body: each reserved cell becomes a space,
// and each kept picture whose block is in the body is drawn at its first
// cell. The pictures with no block are let go.
func (k *imageKeep) replay(body []byte) []byte {
	lead := []byte(vt.SixelMarkerLead)
	if !bytes.Contains(body, lead) {
		k.list, k.total = nil, 0
		return body
	}
	var marks []marker
	for i := 0; ; {
		j := bytes.Index(body[i:], lead)
		if j < 0 {
			break
		}
		at := i + j
		end := at + len(lead)
		for n := 0; n < 4 && end < len(body); n++ {
			_, size := utf8.DecodeRune(body[end:])
			end += size
		}
		if _, row, col, ok := vt.ParseSixelMarker(string(body[at:end])); ok {
			marks = append(marks, marker{at: at, end: end, row: row, col: col})
			i = end
		} else {
			i = at + len(lead)
		}
	}
	// The first cell of each block, and how many cells its first row has
	// in a run.
	type block struct{ mark, width int }
	var blocks []block
	for m := range marks {
		if marks[m].row != 0 || marks[m].col != 0 {
			continue
		}
		w := 1
		for n := m + 1; n < len(marks) && marks[n].row == 0 && marks[n].col == w && adjacent(body[marks[n-1].end:marks[n].at]); n++ {
			w++
		}
		blocks = append(blocks, block{m, w})
	}
	// From the newest back: a block takes the newest picture not yet taken
	// that is at least as wide (a narrow pane cuts a block at its edge).
	draw := map[int]*keptImage{}
	var kept []*keptImage
	i := len(k.list) - 1
	for b := len(blocks) - 1; b >= 0 && i >= 0; b-- {
		for i >= 0 && k.list[i].cols > 0 && blocks[b].width > k.list[i].cols {
			i--
		}
		if i < 0 {
			break
		}
		draw[blocks[b].mark] = k.list[i]
		kept = append(kept, k.list[i])
		i--
	}
	// kept is newest first.
	for l, r := 0, len(kept)-1; l < r; l, r = l+1, r-1 {
		kept[l], kept[r] = kept[r], kept[l]
	}
	k.list, k.total = kept, 0
	for _, img := range kept {
		k.total += len(img.data)
	}
	out := make([]byte, 0, len(body)+k.total)
	prev := 0
	for m, mk := range marks {
		out = append(out, body[prev:mk.at]...)
		if img := draw[m]; img != nil {
			out = append(out, img.data...)
		}
		out = append(out, ' ')
		prev = mk.end
	}
	out = append(out, body[prev:]...)
	return out
}

// adjacent reports whether the bytes between two cells hold no text: none,
// or only escape sequences such as a colour change.
func adjacent(gap []byte) bool {
	if len(gap) == 0 {
		return true
	}
	if gap[0] != 0x1b || len(gap) > 64 {
		return false
	}
	return !bytes.ContainsAny(gap, "\r\n")
}
