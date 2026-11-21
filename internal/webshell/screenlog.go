package webshell

import (
	"strings"
	"sync"
)

type screenLog struct {
	mu     sync.Mutex
	blocks []logBlock
	size   int
	alt    bool
}

type logBlock struct {
	raw string
	lay func(cols int) []string
}

const maxLogBytes = 256 << 10

const layBytes = 2 << 10

const rawChunk = 4 << 10

const (
	seqAltOn  = "\x1b[?1049h"
	seqAltOff = "\x1b[?1049l"
	seqClear  = "\x1b[2J"
)

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
		l.blocks[0] = logBlock{}
		l.blocks = l.blocks[1:]
		if b.lay != nil {
			l.size -= layBytes
		} else {
			l.size -= len(b.raw)
		}
	}
	if b := &l.blocks[0]; l.size > maxLogBytes && b.lay == nil {
		cut := len(b.raw) - maxLogBytes
		if i := strings.IndexByte(b.raw[cut:], '\n'); i >= 0 {
			cut += i + 1
		}
		b.raw = strings.Clone(b.raw[cut:])
		l.size = len(b.raw)
	}
}

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
			b.WriteString(line)
			b.WriteString("\r\n")
		}
	}
	return b.String()
}

func firstOf(s string, seqs ...string) (int, string) {
	at, which := -1, ""
	for _, q := range seqs {
		if i := strings.Index(s, q); i >= 0 && (at < 0 || i < at) {
			at, which = i, q
		}
	}
	return at, which
}
