package session

import (
	"bytes"
	"strconv"
	"sync/atomic"
)

// A kitty graphics stream sends each picture whole: a transmission of image i
// replaces image i, data and all. A client that is behind does not need every
// frame of such a stream, only the newest, so a frame still waiting on a
// client's queue when the next frame of the same image arrives is dropped
// from that client's queue. The slow client skips frames instead of falling
// further behind, and every other client is unaffected.
//
// The pane's output is cut into segments as it is read (gfxScanner.scan): a
// frame is every byte of one transmission, from the ESC that opens its first
// command to the ST that closes its last chunk, and everything else is text.
// Each client gathers a frame's segments until the frame is complete, and only
// then queues it, as one item. A frame is never cut: it goes out whole or not
// at all. Text and every other graphics command go out in order, as before.
//
// A frame is dropped only when nothing between it and its replacement could
// depend on it:
//   - The newer frame names the same image id, which is not zero.
//   - No other graphics command came between them. A placement, a delete or
//     a query may name the image, so any of them keeps every waiting frame.
//   - The older frame did not move the cursor, or the text after it set the
//     cursor to an absolute position before anything else was drawn. An a=T
//     without C=1 or U=1 moves the cursor past the image, and the text
//     after it is drawn from there.

// queuedFrame is one complete frame on a client's queue. Exactly one side
// settles it: the stream goroutine takes it, or broadcast drops it for a newer
// frame. state decides which, so a frame is never half sent.
type queuedFrame struct {
	state atomic.Int32
	// parts are the frame's bytes, in order. Read by the stream goroutine only
	// after it has taken the frame, and cleared by broadcast only after it has
	// dropped it.
	parts [][]byte
	size  int64
	// end is the stream position after the frame's last byte.
	end   int64
	owner *ptySubscriber

	// Read and written by broadcast only.
	id     uint32
	read   uint64 // the read that queued it, from PTY.reads
	moves  bool   // an a=T that moves the cursor past the image
	pinned bool   // something after it depends on it, so it is never dropped
}

const (
	frameWaiting int32 = iota
	frameTaken
	frameDropped
)

// take claims the frame for sending. It returns false when broadcast dropped
// it first.
func (f *queuedFrame) take() bool {
	if !f.state.CompareAndSwap(frameWaiting, frameTaken) {
		return false
	}
	f.owner.queued.Add(-f.size)
	f.owner.framesWaiting.Add(-1)
	return true
}

// gfxSeg is one run of a pane's output: text, or part of a frame.
type gfxSeg struct {
	b   []byte
	end int64 // stream position after the segment's last byte

	frame bool   // part of a frame
	first bool   // holds the frame's first byte
	last  bool   // holds the frame's last byte
	id    uint32 // the frame's image id
	moves bool   // the frame moves the cursor

	// pins marks text that holds a graphics command that is not a frame.
	pins bool
}

// Scanner states.
const (
	gfxGround     = iota
	gfxHeader     // after ESC _ G, before the ';' or ST that ends the keys
	gfxPayload    // after the keys, before ST
	gfxPayloadEsc // an ESC ended the last read inside a payload
)

// maxGfxHeader bounds the control keys of one command. Real ones are under a
// hundred bytes. A command whose keys run past this is treated as text.
const maxGfxHeader = 1024

// gfxScanner cuts a pane's output into segments. It keeps its state across
// reads, so a command split across reads is still found. Only broadcast uses
// it, under streamMu.
type gfxScanner struct {
	state int
	// carry is the start of a command whose keys are not complete yet. It
	// always starts at an ESC, and the next scan starts with it.
	carry []byte

	cmdFrame bool // the command in progress belongs to a frame
	cmdLast  bool // the command in progress ends its frame

	inFrame    bool // a frame's command ended with m=1: the next command continues it
	otherChunk bool // the same, for a transmission that is not a frame

	// The last few spans the scanner closed, and the one open now: a frame
	// from its first byte to its last, or one other graphics command. A
	// client that starts reading inside one would print the rest of it as
	// text. See spanAt.
	spans    [32][2]int64
	nspans   int
	spanOpen bool
	spanFrom int64

	// What classify found in the command in progress.
	frameID        uint32
	frameMoves     bool
	frameContinues bool
}

// scan cuts data, which ends at stream position end, into segments. The
// segments cover every byte except a trailing carry. saw reports a kitty
// graphics command in this read.
func (s *gfxScanner) scan(data []byte, end int64) (segs []gfxSeg, saw bool) {
	buf := data
	base := end - int64(len(data))
	if len(s.carry) > 0 {
		buf = make([]byte, 0, len(s.carry)+len(data))
		buf = append(buf, s.carry...)
		buf = append(buf, data...)
		base -= int64(len(s.carry))
		s.carry = nil
	}

	segStart := 0
	cur := gfxSeg{frame: (s.state == gfxPayload || s.state == gfxPayloadEsc) && s.cmdFrame}
	emit := func(to int) {
		if to > segStart {
			cur.b = buf[segStart:to]
			cur.end = base + int64(to)
			segs = append(segs, cur)
		}
		segStart = to
		cur = gfxSeg{frame: cur.frame, id: cur.id, moves: cur.moves}
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
		}
	}

	cmdStart := 0
	i := 0
	// A header that reaches the end of the buffer is carried, so the header
	// case runs once more with nothing left to read.
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
					return segs, saw
				}
				i = k + 1
				continue
			}
			if buf[k+1] == '_' && buf[k+2] == 'G' {
				s.state = gfxHeader
				cmdStart = k
				i = k + 3
				continue
			}
			i = k + 1

		case gfxHeader:
			j := bytes.IndexAny(buf[i:], ";\x1b")
			if j < 0 && len(buf)-cmdStart <= maxGfxHeader {
				emit(cmdStart)
				s.carry = append([]byte(nil), buf[cmdStart:]...)
				s.state = gfxGround
				return segs, saw
			}
			saw = true
			s.state = gfxPayload
			if j < 0 || j+i-cmdStart > maxGfxHeader {
				// Keys this long are not a command anyone sends. Pass it
				// through as text that keeps every waiting frame. The test is
				// the same whether or not the keys end in this read, so where
				// the reads fall does not change the answer.
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
			s.classify(buf[cmdStart+3 : k])
			if !s.frameContinues {
				s.openSpan(base + int64(cmdStart))
			}
			if s.cmdFrame {
				emit(cmdStart)
				cur.frame = true
				if !s.frameContinues {
					cur.first = true
				}
				cur.id, cur.moves = s.frameID, s.frameMoves
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
	return segs, saw
}

// classify reads one command's keys and decides whether it belongs to a frame.
func (s *gfxScanner) classify(keys []byte) {
	var action byte = 't'
	var id uint64
	more, cursorStays := false, false
	for len(keys) > 0 {
		kv := keys
		if c := bytes.IndexByte(keys, ','); c >= 0 {
			kv, keys = keys[:c], keys[c+1:]
		} else {
			keys = nil
		}
		if len(kv) < 3 || kv[1] != '=' {
			continue
		}
		v := kv[2:]
		switch kv[0] {
		case 'a':
			action = v[0]
		case 'i':
			id, _ = strconv.ParseUint(string(v), 10, 32)
		case 'm':
			more = string(v) == "1"
		case 'C', 'U':
			if string(v) == "1" {
				cursorStays = true
			}
		}
	}

	s.frameContinues = false
	switch {
	case s.inFrame:
		// The next chunk of the frame in progress. Chunks carry m and q only.
		s.cmdFrame, s.frameContinues = true, true
		s.cmdLast = !more
	case s.otherChunk:
		s.cmdFrame = false
		s.otherChunk = more
	case (action == 't' || action == 'T') && id > 0:
		s.cmdFrame = true
		s.cmdLast = !more
		s.frameID = uint32(id)
		s.frameMoves = action == 'T' && !cursorStays
	default:
		s.cmdFrame = false
		s.otherChunk = (action == 't' || action == 'T') && more
	}
}

// maxOpenFrame bounds what one client gathers of a frame that is not complete
// yet. A larger frame goes out as it arrives and is never dropped. A variable
// so a test can lower it.
var maxOpenFrame = maxSubscriberQueue / 2

// maxLatestFrames bounds how many image ids one client remembers a waiting
// frame for. A stream that uses a new id for every frame never replaces one,
// and past this the record is cleared rather than grown.
const maxLatestFrames = 64

// route hands one read's segments to one client. Called by broadcast only.
func (p *PTY) route(clientID string, sub *ptySubscriber, segs []gfxSeg) {
	for _, sg := range segs {
		if sub.gapped.Load() {
			sub.forgetFrames()
			return
		}
		if sg.end <= sub.seen {
			continue // the catch-up handed this client these bytes already
		}
		b := sg.b
		plain := false
		if start := sg.end - int64(len(b)); start < sub.seen {
			// Begun in the catch-up: hand over the rest only. A frame that
			// began there is not this stream's to gather.
			b = b[sub.seen-start:]
			if sg.frame {
				plain = true
				sub.passFrame = !sg.last
			}
		}
		sub.seen = sg.end

		if !sg.frame {
			if sub.open != nil {
				// Text inside a chunked transmission. The frame is sent as it
				// stands, and its other chunks go out as they arrive.
				p.flushOpen(clientID, sub)
			}
			if sg.pins {
				sub.pinAll()
			}
			if f := sub.cursorFrame; f != nil {
				if !setsCursor(b) {
					f.pinned = true
				}
				sub.cursorFrame = nil
			}
			p.enqueue(clientID, sub, ptyChunk{data: b}, sg.end)
			continue
		}

		if sub.skipFrame {
			// The rest of a frame whose start this client never got.
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
			p.enqueue(clientID, sub, ptyChunk{data: b}, sg.end)
			continue
		}
		if sg.first {
			if sub.open != nil {
				p.flushOpen(clientID, sub)
				sub.passFrame = false
			}
			if f := sub.cursorFrame; f != nil {
				// A frame straight after one that moved the cursor is placed
				// where that one left it.
				f.pinned = true
				sub.cursorFrame = nil
			}
			sub.open = &queuedFrame{id: sg.id, moves: sg.moves, owner: sub}
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

// flushOpen queues the frame a client is gathering as it stands, kept, and
// sends the rest of that frame as it arrives.
func (p *PTY) flushOpen(clientID string, sub *ptySubscriber) {
	f := sub.open
	sub.open = nil
	f.pinned = true
	sub.passFrame = true
	p.enqueueFrame(clientID, sub, f)
}

// commitFrame queues a complete frame, and drops the waiting frame of the same
// image that it replaces.
func (p *PTY) commitFrame(clientID string, sub *ptySubscriber, f *queuedFrame) {
	// Only a frame queued by an earlier read: one queued by this read has not
	// had a chance to be taken, and its client is not behind.
	if old := sub.latest[f.id]; old != nil && !old.pinned && old.read < p.reads && old.state.CompareAndSwap(frameWaiting, frameDropped) {
		sub.queued.Add(-old.size)
		sub.framesWaiting.Add(-1)
		old.parts = nil
		sub.skipped.Add(1)
		p.framesSkipped.Add(1)
	}
	if !p.enqueueFrame(clientID, sub, f) {
		return
	}
	if sub.latest == nil || len(sub.latest) >= maxLatestFrames {
		sub.latest = make(map[uint32]*queuedFrame)
	}
	sub.latest[f.id] = f
	if f.moves {
		sub.cursorFrame = f
	}
}

// enqueueFrame puts a frame on a client's queue as one item.
func (p *PTY) enqueueFrame(clientID string, sub *ptySubscriber, f *queuedFrame) bool {
	f.read = p.reads
	if !p.enqueue(clientID, sub, ptyChunk{frame: f}, f.end) {
		return false
	}
	sub.framesWaiting.Add(1)
	return true
}

// enqueue puts one item on a client's queue, or gaps the stream when the queue
// is full. It reports whether the item was queued.
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
	// Counted before the send, so the stream goroutine can never take more
	// than was counted.
	sub.queued.Add(n)
	select {
	case sub.ch <- c:
		// Only an item that was queued counts as reached: a client dropped
		// here resumes from the gap rather than past it.
		sub.sent.Store(end)
		return true
	default:
		sub.queued.Add(-n)
		sub.gapped.Store(true)
		p.streamsCut.Add(1)
		sub.forgetFrames()
		if p.debug {
			debugLog("[DEBUG] PTY %s: channel full for %s, gapped", p.ID[:8], clientID)
		}
		return false
	}
}

// forgetFrames drops what a client was gathering and every frame it could
// still replace. Its stream is gapped or rebuilt.
func (sub *ptySubscriber) forgetFrames() {
	sub.open = nil
	sub.passFrame = false
	sub.latest = nil
	sub.cursorFrame = nil
}

// pinAll keeps every frame a client has waiting.
func (sub *ptySubscriber) pinAll() {
	for _, f := range sub.latest {
		f.pinned = true
	}
	sub.latest = nil
	if sub.cursorFrame != nil {
		sub.cursorFrame.pinned = true
		sub.cursorFrame = nil
	}
}

// setsCursor reports whether b starts by moving the cursor to an absolute
// position (CUP, ESC [ row ; col H or f), which makes where an earlier image
// left the cursor irrelevant.
func setsCursor(b []byte) bool {
	if len(b) < 3 || b[0] != 0x1b || b[1] != '[' {
		return false
	}
	for _, c := range b[2:] {
		switch {
		case c >= '0' && c <= '9', c == ';':
		case c == 'H', c == 'f':
			return true
		default:
			return false
		}
	}
	return false
}

func (s *gfxScanner) openSpan(at int64) {
	s.spanOpen, s.spanFrom = true, at
}

func (s *gfxScanner) closeSpan(at int64) {
	if !s.spanOpen {
		return
	}
	s.spanOpen = false
	s.spans[s.nspans%len(s.spans)] = [2]int64{s.spanFrom, at}
	s.nspans++
}

// spanAt reports whether stream position pos falls inside a graphics command
// or frame, so that a stream starting there would begin in the middle of one.
// end is where that span ends, or -1 when it has not ended yet.
func (s *gfxScanner) spanAt(pos int64) (inside bool, end int64) {
	if s.spanOpen && pos > s.spanFrom {
		return true, -1
	}
	for i := range min(s.nspans, len(s.spans)) {
		if sp := s.spans[i]; pos > sp[0] && pos < sp[1] {
			return true, sp[1]
		}
	}
	return false, 0
}
