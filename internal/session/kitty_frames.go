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
//   - The newer frame names the same image id, which is not zero, on the
//     same screen. A terminal keeps one image store for the main screen and
//     one for the alternate screen, so image 1 on one is not image 1 on the
//     other. Or both frames have no id, and the newer one is drawn over the
//     older one exactly: see idless.
//   - No other graphics command came between them. A placement, a delete or
//     a query may name the image, so any of them keeps every waiting frame.
//   - The older frame did not move the cursor. An a=T without C=1 or U=1
//     moves the cursor past the image, and at the bottom row it scrolls the
//     screen. A cursor move after it does not undo the scroll, so such a
//     frame is never dropped.
//   - The older frame did not name a shared memory object or a temporary
//     file (t=s, t=t). The terminal that reads one deletes it, so a dropped
//     one would stay on disk.

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
	id      uint32
	alt     bool   // sent while the pane was on the alternate screen
	read    uint64 // the read that queued it, from PTY.reads
	pinned  bool   // something after it depends on it, so it is never dropped
	key     string // an id-less frame's place and keys; see idless
	follows bool   // only a cursor move came between it and the frame before
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
	alt   bool   // the frame was sent on the alternate screen
	moves bool   // the frame moves the cursor
	keep  bool   // the frame is never dropped: it moves the cursor or names a file
	key   string // see queuedFrame
	// follows is set on a frame's first segment when only a cursor move came
	// between it and the end of the frame before it.
	follows bool

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

	// The spans the scanner closed that the ring may still hold, oldest
	// first, and the one open now: a frame from its first byte to its last,
	// or one other graphics command. A client that starts reading inside one
	// would print the rest of it as text. See spanAt.
	spans    [][2]int64
	spanOpen bool
	spanFrom int64

	// tail is the last bytes before the scan position, so a cursor move just
	// in front of a frame is found wherever the reads fall. lastFrameEnd is
	// the stream position after the last frame's last byte.
	tail         [maxCUP]byte
	ntail        int
	lastFrameEnd int64

	// alt is whether the pane is on the alternate screen, from the private
	// modes 47, 1047 and 1049 the scanner has seen. mode holds a private mode
	// sequence that a read ended inside: see trackScreen.
	alt  bool
	mode []byte

	// frameAlt is alt as it was at the first chunk of the frame in progress,
	// and frameKey is the key its first chunk got. Every segment of a frame
	// carries both, whichever read the segment came in.
	frameAlt bool
	frameKey string

	// What classify found in the command in progress.
	frameID        uint32
	frameMoves     bool
	frameKeep      bool
	frameIDless    bool
	frameContinues bool
	cmdQuery       bool // a query, which is not graphics output
	otherQuery     bool // the chunked transmission in progress is a query
}

// maxCUP bounds the cursor move scan looks for in front of a frame.
const maxCUP = 16

// scan cuts data, which ends at stream position end, into segments. The
// segments cover every byte except a trailing carry. saw reports a kitty
// graphics command other than a query in this read.
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

	if len(s.mode) > 0 {
		// A private mode sequence the last read ended inside.
		s.trackScreen(data, 0)
	}

	segStart := 0
	cur := gfxSeg{frame: (s.state == gfxPayload || s.state == gfxPayloadEsc) && s.cmdFrame}
	if cur.frame {
		// A payload the last read ended inside. Its segments describe the
		// same frame as the ones before them did.
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
				// Keys this long are not a command anyone sends. Pass it
				// through as text that keeps every waiting frame. The test is
				// the same whether or not the keys end in this read, so where
				// the reads fall does not change the answer.
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

// maxMode bounds a private mode sequence trackScreen reads.
const maxMode = 32

// trackScreen reads the escape sequence that starts at buf[at] (an ESC), or
// for at 0 with s.mode set, the rest of one the last read ended inside. It
// follows the screen switches: CSI ? 47, 1047 or 1049 with h or l, and RIS
// (ESC c), which returns to the main screen. Everything else is ignored.
func (s *gfxScanner) trackScreen(buf []byte, at int) {
	seq := s.mode
	if len(seq) == 0 {
		seq = buf[at : at+1]
		at++
	}
	s.mode = nil
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
			for _, param := range bytes.Split(seq[3:], []byte{';'}) {
				switch string(param) {
				case "47", "1047", "1049":
					s.alt = c == 'h'
				}
			}
			return
		default:
			return
		}
		seq = append(seq[:len(seq):len(seq)], c)
	}
	// The read ended inside the sequence: keep it for the next one.
	s.mode = append([]byte(nil), seq...)
}

// keepTail records the last bytes the scan has passed.
func (s *gfxScanner) keepTail(b []byte) {
	if len(b) >= maxCUP {
		s.ntail = copy(s.tail[:], b[len(b)-maxCUP:])
		return
	}
	keep := min(s.ntail, maxCUP-len(b))
	copy(s.tail[:], s.tail[s.ntail-keep:s.ntail])
	s.ntail = keep + copy(s.tail[keep:], b)
}

// cupBefore returns the cursor move (CUP, ESC [ row ; col H or f) that ends
// right where buf[at] starts, and the stream position it starts at. It returns
// nil when the bytes in front of at are anything else.
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

// classify reads one command's keys and decides whether it belongs to a frame.
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
		// The next chunk of the frame in progress. Chunks carry m and q only.
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
		// A frame with no id, as mpv sends: see idless.
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

// idless reports whether a frame with no image id may replace older, the
// frame with no id before it on the same client's queue. Such a frame is a
// new image each time, and the terminal keeps every one of them, so a newer
// one replaces an older one only where it is drawn exactly over it:
//   - Only a cursor move came between the two frames, and the same cursor
//     move came right in front of each. Both are placed at the same cell.
//   - Their keys are the same byte for byte: the same size and the same cells.
//   - Both are f=24, which has no alpha, so nothing of the older one shows
//     through.
//   - Both keep the cursor where it is (C=1), and send their data in the
//     escape code (t=d).
//
// mpv's --vo=kitty sends every frame this way.
func idless(f, older *queuedFrame) bool {
	return f.id == 0 && older.id == 0 && f.key != "" && f.follows && f.key == older.key && f.alt == older.alt
}

// imageKey names one image: its id, on one screen.
type imageKey struct {
	id  uint32
	alt bool
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
			if sub.skipSpan {
				// The catch-up began inside a graphics command that is not
				// a frame: skip the rest of it.
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
		// A frame starts only after any command the catch-up began in ended.
		sub.skipSpan = false

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

// flushOpen queues the frame a client is gathering as it stands, kept, and
// sends the rest of that frame as it arrives.
func (p *PTY) flushOpen(clientID string, sub *ptySubscriber) {
	f := sub.open
	sub.open = nil
	f.pinned = true
	sub.passFrame = true
	sub.lastFrame = nil
	p.enqueueFrame(clientID, sub, f)
}

// commitFrame queues a complete frame, and drops the waiting frame it
// replaces: the one of the same image, or for a frame with no id the one
// before it that it is drawn exactly over (idless).
func (p *PTY) commitFrame(clientID string, sub *ptySubscriber, f *queuedFrame) {
	old := sub.lastFrame
	if f.id != 0 {
		old = sub.latest[imageKey{f.id, f.alt}]
	} else if old != nil && !idless(f, old) {
		old = nil
	}
	// Only a frame queued by an earlier read: one queued by this read has not
	// had a chance to be taken, and its client is not behind.
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
	c.end = end
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
	sub.lastFrame = nil
}

// pinAll keeps every frame a client has waiting.
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

// pruneSpans forgets the spans that end at or before ringStart, which no
// catch-up can start inside any more. The ring holds 64 KiB and a span is at
// least five bytes, so what is left stays bounded.
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

// spanAt reports whether stream position pos falls inside a graphics command
// or frame, so that a stream starting there would begin in the middle of one.
// end is where that span ends, or -1 when it has not ended yet.
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

// spanEnd returns where the span that starts at from ends, and false while
// it is still open.
func (s *gfxScanner) spanEnd(from int64) (end int64, closed bool) {
	if s.spanOpen && s.spanFrom == from {
		return 0, false
	}
	for i := len(s.spans) - 1; i >= 0; i-- {
		if s.spans[i][0] == from {
			return s.spans[i][1], true
		}
	}
	// Pruned: it ended before the ring's start, so long ago.
	return from, true
}
