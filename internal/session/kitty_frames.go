package session

import (
	"bytes"
	"slices"
	"strconv"
	"sync/atomic"
)

type queuedFrame struct {
	state atomic.Int32
	parts [][]byte
	size  int64
	end   int64
	owner *ptySubscriber

	id      uint32
	alt     bool
	read    uint64
	pinned  bool
	key     string
	follows bool
}

const (
	frameWaiting int32 = iota
	frameTaken
	frameDropped
)

func (f *queuedFrame) take() bool {
	if !f.state.CompareAndSwap(frameWaiting, frameTaken) {
		return false
	}
	f.owner.queued.Add(-f.size)
	f.owner.framesWaiting.Add(-1)
	return true
}

type gfxSeg struct {
	b   []byte
	end int64

	frame   bool
	first   bool
	last    bool
	id      uint32
	alt     bool
	moves   bool
	keep    bool
	key     string
	follows bool

	pins bool
}

const (
	gfxGround = iota
	gfxHeader
	gfxPayload
	gfxPayloadEsc
)

const maxGfxHeader = 1024

type gfxScanner struct {
	state int
	carry []byte

	segs []gfxSeg

	cmdFrame bool
	cmdLast  bool

	inFrame    bool
	otherChunk bool

	spans    [][2]int64
	spanOpen bool
	spanFrom int64

	tail         [maxCUP]byte
	ntail        int
	lastFrameEnd int64

	alt  bool
	mode []byte

	frameAlt bool
	frameKey string

	frameID        uint32
	frameMoves     bool
	frameKeep      bool
	frameIDless    bool
	frameContinues bool
	cmdQuery       bool
	otherQuery     bool
}

const maxCUP = 16

func (s *gfxScanner) scan(data []byte, end int64) (segs []gfxSeg, saw bool) {
	buf := data
	base := end - int64(len(data))
	segs = s.segs[:0]
	defer func() { s.segs = segs[:0] }()
	if len(s.carry) > 0 {
		buf = make([]byte, 0, len(s.carry)+len(data))
		buf = append(buf, s.carry...)
		buf = append(buf, data...)
		base -= int64(len(s.carry))
		s.carry = nil
	}

	if len(s.mode) > 0 {
		s.trackScreen(data, 0)
	}

	segStart := 0
	cur := gfxSeg{frame: (s.state == gfxPayload || s.state == gfxPayloadEsc) && s.cmdFrame}
	if cur.frame {
		cur.id, cur.alt, cur.moves, cur.keep = s.frameID, s.frameAlt, s.frameMoves, s.frameKeep
		cur.key = s.frameKey
	}
	emit := func(to int) {
		if to > segStart {
			cur.b = buf[segStart:to]
			cur.end = base + int64(to)
			segs = append(segs, cur)
		}
		segStart = to
		cur = gfxSeg{frame: cur.frame, id: cur.id, alt: cur.alt, moves: cur.moves, keep: cur.keep, key: cur.key}
	}
	endCmd := func(at int) {
		s.state = gfxGround
		if !s.cmdFrame {
			s.closeSpan(base + int64(at))
			return
		}
		cur.last = s.cmdLast
		emit(at)
		cur.frame = false
		if s.cmdLast {
			s.inFrame = false
			s.closeSpan(base + int64(at))
			s.lastFrameEnd = base + int64(at)
		}
	}

	cmdStart := 0
	i := 0
	for i < len(buf) || s.state == gfxHeader {
		switch s.state {
		case gfxGround:
			j := bytes.IndexByte(buf[i:], 0x1b)
			if j < 0 {
				i = len(buf)
				continue
			}
			k := i + j
			if rest := buf[k:]; len(rest) < 3 {
				if bytes.HasPrefix([]byte("\x1b_G"), rest) {
					emit(k)
					s.carry = append([]byte(nil), rest...)
					s.keepTail(buf[:k])
					return segs, saw
				}
				s.trackScreen(buf, k)
				i = k + 1
				continue
			}
			if buf[k+1] == '_' && buf[k+2] == 'G' {
				s.state = gfxHeader
				cmdStart = k
				i = k + 3
				continue
			}
			s.trackScreen(buf, k)
			i = k + 1

		case gfxHeader:
			j := bytes.IndexAny(buf[i:], ";\x1b")
			if j < 0 && len(buf)-cmdStart <= maxGfxHeader {
				emit(cmdStart)
				s.carry = append([]byte(nil), buf[cmdStart:]...)
				s.state = gfxGround
				s.keepTail(buf[:cmdStart])
				return segs, saw
			}
			s.state = gfxPayload
			if j < 0 || j+i-cmdStart > maxGfxHeader {
				saw = true
				cur.pins = true
				s.cmdFrame = false
				s.otherChunk = false
				if j < 0 {
					i = len(buf)
				} else if buf[i+j] == ';' {
					i += j + 1
				} else {
					i += j
				}
				continue
			}
			k := i + j
			keys := buf[cmdStart+3 : k]
			s.classify(keys)
			if !s.cmdQuery {
				saw = true
			}
			if !s.frameContinues {
				s.openSpan(base + int64(cmdStart))
			}
			if s.cmdFrame {
				emit(cmdStart)
				cur.frame = true
				if !s.frameContinues {
					cur.first = true
					cur.key = ""
					cup, from := s.cupBefore(buf, cmdStart, base)
					cur.follows = cup != nil && from == s.lastFrameEnd
					if s.frameIDless && cup != nil {
						cur.key = string(cup) + "\x00" + string(keys)
					}
					s.frameAlt = s.alt
					s.frameKey = cur.key
				}
				cur.id, cur.alt, cur.moves, cur.keep = s.frameID, s.frameAlt, s.frameMoves, s.frameKeep
				cur.key = s.frameKey
				s.inFrame = true
			} else {
				cur.pins = true
			}
			if buf[k] == ';' {
				i = k + 1
			} else {
				i = k
			}

		case gfxPayload:
			j := bytes.IndexByte(buf[i:], 0x1b)
			if j < 0 {
				i = len(buf)
				continue
			}
			k := i + j
			if k+1 >= len(buf) {
				s.state = gfxPayloadEsc
				i = len(buf)
				continue
			}
			if buf[k+1] == '\\' {
				endCmd(k + 2)
				i = k + 2
				continue
			}
			i = k + 1

		case gfxPayloadEsc:
			if buf[i] == '\\' {
				endCmd(i + 1)
				i++
				continue
			}
			s.state = gfxPayload
		}
	}
	emit(len(buf))
	s.keepTail(buf)
	return segs, saw
}

const maxMode = 32

func (s *gfxScanner) trackScreen(buf []byte, at int) {
	var arr [maxMode]byte
	seq := append(arr[:0], s.mode...)
	if len(seq) == 0 {
		seq = append(seq, buf[at])
		at++
	}
	s.mode = s.mode[:0]
	for i := at; i < len(buf); i++ {
		c := buf[i]
		switch {
		case len(seq) == 1:
			if c == 'c' {
				s.alt = false
				return
			}
			if c != '[' {
				return
			}
		case len(seq) == 2:
			if c != '?' {
				return
			}
		case c >= '0' && c <= '9', c == ';':
			if len(seq) >= maxMode {
				return
			}
		case c == 'h', c == 'l':
			params := seq[3:]
			for len(params) > 0 {
				param := params
				if j := bytes.IndexByte(params, ';'); j >= 0 {
					param, params = params[:j], params[j+1:]
				} else {
					params = nil
				}
				switch string(param) {
				case "47", "1047", "1049":
					s.alt = c == 'h'
				}
			}
			return
		default:
			return
		}
		seq = append(seq, c)
	}
	s.mode = append(s.mode, seq...)
}

func (s *gfxScanner) keepTail(b []byte) {
	if len(b) >= maxCUP {
		s.ntail = copy(s.tail[:], b[len(b)-maxCUP:])
		return
	}
	keep := min(s.ntail, maxCUP-len(b))
	copy(s.tail[:], s.tail[s.ntail-keep:s.ntail])
	s.ntail = keep + copy(s.tail[keep:], b)
}

func (s *gfxScanner) cupBefore(buf []byte, at int, base int64) (cup []byte, from int64) {
	var w []byte
	if at >= maxCUP {
		w = buf[at-maxCUP : at]
	} else {
		w = make([]byte, 0, maxCUP)
		w = append(w, s.tail[max(0, s.ntail-(maxCUP-at)):s.ntail]...)
		w = append(w, buf[:at]...)
	}
	n := len(w)
	if n < 3 || (w[n-1] != 'H' && w[n-1] != 'f') {
		return nil, 0
	}
	i := n - 2
	for i >= 0 && (w[i] >= '0' && w[i] <= '9' || w[i] == ';') {
		i--
	}
	if i < 1 || w[i] != '[' || w[i-1] != 0x1b {
		return nil, 0
	}
	cup = w[i-1:]
	return cup, base + int64(at) - int64(len(cup))
}

func (s *gfxScanner) classify(keys []byte) {
	var action, medium, format byte = 't', 'd', 0
	var id uint64
	more, cursorStays, cupOnly, otherKeys := false, false, false, false
	for len(keys) > 0 {
		kv := keys
		if c := bytes.IndexByte(keys, ','); c >= 0 {
			kv, keys = keys[:c], keys[c+1:]
		} else {
			keys = nil
		}
		if len(kv) < 3 || kv[1] != '=' {
			otherKeys = true
			continue
		}
		v := kv[2:]
		switch kv[0] {
		case 'a':
			action = v[0]
		case 'i':
			id, _ = strconv.ParseUint(string(v), 10, 32)
		case 't':
			medium = v[0]
		case 'f':
			if string(v) == "24" {
				format = 24
			}
		case 'm':
			more = string(v) == "1"
		case 'C':
			if string(v) == "1" {
				cursorStays, cupOnly = true, true
			}
		case 'U':
			if string(v) == "1" {
				cursorStays = true
			}
		case 's', 'v', 'q', 'o', 'S', 'O', 'c', 'r', 'x', 'y', 'w', 'h', 'X', 'Y', 'z':
		default:
			otherKeys = true
		}
	}

	s.frameContinues = false
	switch {
	case s.inFrame:
		s.cmdFrame, s.frameContinues = true, true
		s.cmdLast = !more
		s.cmdQuery = false
	case s.otherChunk:
		s.cmdFrame = false
		s.otherChunk = more
		s.cmdQuery = s.otherQuery
	case (action == 't' || action == 'T') && id > 0:
		s.cmdFrame = true
		s.cmdLast = !more
		s.cmdQuery = false
		s.frameID = uint32(id)
		s.frameMoves = action == 'T' && !cursorStays
		s.frameKeep = s.frameMoves || medium == 's' || medium == 't'
		s.frameIDless = false
	case action == 'T' && id == 0 && cupOnly && medium == 'd' && format == 24 && !otherKeys:
		s.cmdFrame = true
		s.cmdLast = !more
		s.cmdQuery = false
		s.frameID = 0
		s.frameMoves, s.frameKeep = false, false
		s.frameIDless = true
	default:
		s.cmdFrame = false
		s.cmdQuery = action == 'q'
		s.otherChunk = (action == 't' || action == 'T' || action == 'q') && more
		s.otherQuery = s.cmdQuery
	}
}

func idless(f, older *queuedFrame) bool {
	return f.id == 0 && older.id == 0 && f.key != "" && f.follows && f.key == older.key && f.alt == older.alt
}

type imageKey struct {
	id  uint32
	alt bool
}

var maxOpenFrame = maxSubscriberQueue / 2

const maxLatestFrames = 64

func (p *PTY) route(clientID string, sub *ptySubscriber, segs []gfxSeg) {
	for _, sg := range segs {
		if sub.gapped.Load() {
			sub.forgetFrames()
			return
		}
		if sg.end <= sub.seen {
			continue
		}
		b := sg.b
		plain := false
		if start := sg.end - int64(len(b)); start < sub.seen {
			b = b[sub.seen-start:]
			if sg.frame {
				plain = true
				sub.passFrame = !sg.last
			}
		}
		sub.seen = sg.end

		if !sg.frame {
			if sub.open != nil {
				p.flushOpen(clientID, sub)
			}
			if sub.skipSpan {
				end, closed := p.gfx.spanEnd(sub.skipFrom)
				if !closed || end >= sg.end {
					sub.skipSpan = !closed || end > sg.end
					sub.sent.Store(sg.end)
					continue
				}
				sub.skipSpan = false
				if start := sg.end - int64(len(b)); end > start {
					b = b[end-start:]
				}
			}
			if sg.pins {
				sub.pinAll()
			}
			p.enqueue(clientID, sub, ptyChunk{data: b}, sg.end)
			continue
		}
		sub.skipSpan = false

		if sub.skipFrame {
			if sg.first {
				sub.skipFrame = false
			} else {
				if sg.last {
					sub.skipFrame = false
				}
				sub.sent.Store(sg.end)
				continue
			}
		}

		if sg.first && !plain {
			sub.passFrame = false
		}
		if plain || sub.passFrame || (sub.open == nil && !sg.first) {
			if sg.last {
				sub.passFrame = false
			}
			sub.lastFrame = nil
			p.enqueue(clientID, sub, ptyChunk{data: b}, sg.end)
			continue
		}
		if sg.first {
			if sub.open != nil {
				p.flushOpen(clientID, sub)
				sub.passFrame = false
			}
			sub.open = &queuedFrame{id: sg.id, alt: sg.alt, pinned: sg.keep, key: sg.key, follows: sg.follows, owner: sub}
		}
		f := sub.open
		f.parts = append(f.parts, b)
		f.size += int64(len(b))
		f.end = sg.end
		switch {
		case sg.last:
			sub.open = nil
			p.commitFrame(clientID, sub, f)
		case f.size > maxOpenFrame:
			p.flushOpen(clientID, sub)
		}
	}
}

func (p *PTY) flushOpen(clientID string, sub *ptySubscriber) {
	f := sub.open
	sub.open = nil
	f.pinned = true
	sub.passFrame = true
	sub.lastFrame = nil
	p.enqueueFrame(clientID, sub, f)
}

func (p *PTY) commitFrame(clientID string, sub *ptySubscriber, f *queuedFrame) {
	old := sub.lastFrame
	if f.id != 0 {
		old = sub.latest[imageKey{f.id, f.alt}]
	} else if old != nil && !idless(f, old) {
		old = nil
	}
	if old != nil && !old.pinned && old.read < p.reads && old.state.CompareAndSwap(frameWaiting, frameDropped) {
		sub.queued.Add(-old.size)
		sub.framesWaiting.Add(-1)
		old.parts = nil
		sub.skipped.Add(1)
		p.framesSkipped.Add(1)
	}
	sub.lastFrame = nil
	if !p.enqueueFrame(clientID, sub, f) {
		return
	}
	sub.lastFrame = f
	if f.id == 0 {
		return
	}
	if sub.latest == nil || len(sub.latest) >= maxLatestFrames {
		sub.latest = make(map[imageKey]*queuedFrame)
	}
	sub.latest[imageKey{f.id, f.alt}] = f
}

func (p *PTY) enqueueFrame(clientID string, sub *ptySubscriber, f *queuedFrame) bool {
	f.read = p.reads
	if !p.enqueue(clientID, sub, ptyChunk{frame: f}, f.end) {
		return false
	}
	sub.framesWaiting.Add(1)
	return true
}

func (p *PTY) enqueue(clientID string, sub *ptySubscriber, c ptyChunk, end int64) bool {
	n := c.size()
	if sub.queued.Load()+n > maxSubscriberQueue {
		sub.gapped.Store(true)
		p.streamsCut.Add(1)
		sub.forgetFrames()
		if p.debug {
			debugLog("[DEBUG] PTY %s: %s holds %d bytes unread, gapped", p.ID[:8], clientID, sub.queued.Load())
		}
		return false
	}
	sub.queued.Add(n)
	c.end = end
	if sub.out.push(c) {
		sub.sent.Store(end)
		return true
	}
	sub.queued.Add(-n)
	sub.gapped.Store(true)
	p.streamsCut.Add(1)
	sub.forgetFrames()
	if p.debug {
		debugLog("[DEBUG] PTY %s: queue full for %s, gapped", p.ID[:8], clientID)
	}
	return false
}

func (sub *ptySubscriber) forgetFrames() {
	sub.open = nil
	sub.passFrame = false
	sub.latest = nil
	sub.lastFrame = nil
}

func (sub *ptySubscriber) pinAll() {
	for _, f := range sub.latest {
		f.pinned = true
	}
	sub.latest = nil
	if sub.lastFrame != nil {
		sub.lastFrame.pinned = true
		sub.lastFrame = nil
	}
}

func (s *gfxScanner) openSpan(at int64) {
	s.spanOpen, s.spanFrom = true, at
}

func (s *gfxScanner) closeSpan(at int64) {
	if !s.spanOpen {
		return
	}
	s.spanOpen = false
	s.spans = append(s.spans, [2]int64{s.spanFrom, at})
}

func (s *gfxScanner) pruneSpans(ringStart int64) {
	n := 0
	for n < len(s.spans) && s.spans[n][1] <= ringStart {
		n++
	}
	if n == 0 {
		return
	}
	if n == len(s.spans) {
		s.spans = s.spans[:0]
		return
	}
	s.spans = s.spans[n:]
}

func (s *gfxScanner) spanAt(pos int64) (inside bool, end int64) {
	if s.spanOpen && pos > s.spanFrom {
		return true, -1
	}
	for _, sp := range s.spans {
		if pos > sp[0] && pos < sp[1] {
			return true, sp[1]
		}
	}
	return false, 0
}

func (s *gfxScanner) spanEnd(from int64) (end int64, closed bool) {
	if s.spanOpen && s.spanFrom == from {
		return 0, false
	}
	for _, v := range slices.Backward(s.spans) {
		if v[0] == from {
			return v[1], true
		}
	}
	return from, true
}
