package webshell

import (
	"strings"
	"sync"
)

// screenLog keeps what the shell printed on the main screen, so the shell
// can draw it again for a new pane width. A terminal rewraps text it already
// holds when the pane narrows, and that breaks anything laid out for the old
// width: two panes side by side cut the fastfetch logo into fragments and
// drew its info lines over them. Output a command laid out for the width is
// kept as the function that lays it out, and drawn again by calling it with
// the new width. Everything else is kept as the bytes it was, which the
// terminal wraps as a real one would.
//
// What a full-screen program draws on the alternate screen is not kept: the
// main screen is back as it was when the program exits. A clear of the
// screen drops everything kept before it.
type screenLog struct {
	mu     sync.Mutex
	blocks []logBlock
	size   int // bytes held, roughly, to keep the log bounded
	alt    bool
}

// logBlock is one run of output: raw bytes, or a layout for a width.
type logBlock struct {
	raw string
	lay func(cols int) []string
}

// maxLogBytes bounds the log. The oldest blocks go first. The emulator's own
// scrollback is 10,000 lines, and a guided tour prints far less than this.
const maxLogBytes = 256 << 10

// layBytes is what a layout block counts towards maxLogBytes.
const layBytes = 2 << 10

// rawChunk is how far one raw block grows before the next write starts a
// new one. Output with no layout between would otherwise pile up in one
// block that trim, which drops whole blocks, could never shorten, and every
// write would copy it.
const rawChunk = 4 << 10

// The sequences the log reads in what it keeps.
const (
	seqAltOn  = "\x1b[?1049h"
	seqAltOff = "\x1b[?1049l"
	seqClear  = "\x1b[2J"
)

// write keeps s.
func (l *screenLog) write(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for s != "" {
		i, seq := firstOf(s, seqAltOn, seqAltOff, seqClear)
		if i < 0 {
			l.appendRaw(s)
			return
		}
		l.appendRaw(s[:i])
		switch seq {
		case seqAltOn:
			l.alt = true
		case seqAltOff:
			l.alt = false
		case seqClear:
			if !l.alt {
				l.blocks, l.size = nil, 0
			}
		}
		s = s[i+len(seq):]
	}
}

// layout keeps a layout block.
func (l *screenLog) layout(f func(cols int) []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.alt {
		return
	}
	l.blocks = append(l.blocks, logBlock{lay: f})
	l.size += layBytes
	l.trim()
}

func (l *screenLog) appendRaw(s string) {
	if s == "" || l.alt {
		return
	}
	if n := len(l.blocks); n > 0 && l.blocks[n-1].lay == nil && len(l.blocks[n-1].raw) < rawChunk {
		l.blocks[n-1].raw += s
	} else {
		l.blocks = append(l.blocks, logBlock{raw: s})
	}
	l.size += len(s)
	l.trim()
}

func (l *screenLog) trim() {
	for l.size > maxLogBytes && len(l.blocks) > 1 {
		b := l.blocks[0]
		l.blocks[0] = logBlock{} // let the dropped block be collected
		l.blocks = l.blocks[1:]
		if b.lay != nil {
			l.size -= layBytes
		} else {
			l.size -= len(b.raw)
		}
	}
	// One write bigger than the bound is kept from its last whole line.
	if b := &l.blocks[0]; l.size > maxLogBytes && b.lay == nil {
		cut := len(b.raw) - maxLogBytes
		if i := strings.IndexByte(b.raw[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		b.raw = strings.Clone(b.raw[cut:])
		l.size = len(b.raw)
	}
}

// replay draws everything kept for a pane cols cells wide.
func (l *screenLog) replay(cols int) string {
	l.mu.Lock()
	blocks := append([]logBlock(nil), l.blocks...)
	l.mu.Unlock()
	var b strings.Builder
	for _, blk := range blocks {
		if blk.lay == nil {
			b.WriteString(blk.raw)
			continue
		}
		for _, line := range blk.lay(cols) {
			b.WriteString(line + "\r\n")
		}
	}
	return b.String()
}

// firstOf returns the index of the first of seqs in s, and which one it is,
// or -1.
func firstOf(s string, seqs ...string) (int, string) {
	at, which := -1, ""
	for _, q := range seqs {
		if i := strings.Index(s, q); i >= 0 && (at < 0 || i < at) {
			at, which = i, q
		}
	}
	return at, which
}
