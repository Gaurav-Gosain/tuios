package session

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"tuios.dev/vt"
)

// Kitty images named by a path, from a pane this machine runs for another
// machine: the far half. The near half is kitty_link_near.go.
//
// A kitty graphics command can name a file instead of carrying the image: t=f
// a file, t=t a temporary file, t=s a shared memory object. The name means
// something only on the machine it was written on. A hosted pane runs no
// emulator here (see hosted_pane.go), so before this every such command went
// to the owner as it was, and the owner refused it: the path named nothing
// there, or some other file.
//
// So this side reads the path, where it means something, and the owner sends
// the image on as an ordinary direct (t=d) transmission. The work is split so
// that the file's bytes never ride the pane's own stream:
//
//  1. The pump (hosted_resume.go) passes the process's output through
//     kittyLinkFilter. A graphics command with a file medium is taken out of
//     the stream. Everything else passes unchanged.
//  2. The path is checked and the file opened here (openKittyLinkMedium). A
//     refusal is answered to the process at once, in kitty's own words, and
//     nothing goes to the owner.
//  3. A query (a=q) is answered by the owner as a query for direct
//     transmission, so the guest hears what the owner's clients can show.
//  4. An upload puts a marker in the stream in its place: an APC that names
//     the transfer and carries the command's other keys. The pump then waits.
//  5. The owner sees the marker, fetches the bytes with pane-kitty-medium on
//     a bulk stream of its own, and writes the t=d chunks in the marker's
//     place. Once they are sent the pump goes on.
//
// The pump waiting is what keeps the order. The process's later output comes
// after the image, as it would in kitty, and a process that writes more fills
// its pty and waits, as it would on a terminal that is busy reading a file.
// Other panes do not wait: each has its own stream and its own pump, and the
// bytes go on a bulk stream, which takes its turn behind every ordinary frame
// on the link and keeps to a window (internal/federation/bulk.go). A keystroke
// echo in another pane waits behind at most one 64 KiB frame. At most
// kittyLinkSlots reads of a file run at once on this machine, and a slot is
// held for one read, never while the owner is slow.
//
// What may be read, and why. The bytes leave this machine, so the rule is the
// one a file read over the link follows, with kitty's own checks on top:
//
//   - The machine the pane is for must hold the files capability here
//     ([hosts] allow). Without it the filter is off and the owner refuses
//     file media, as before.
//   - kitty's rules: an absolute path, symlinks followed and the checks
//     applied to where they lead, nothing in /proc, /sys or /dev except
//     /dev/shm, a regular file this user owns, at most kittyLinkMaxBytes, and
//     for t=t a name holding tty-graphics-protocol in a temporary folder.
//   - The link's read rules (checkLinkRead): never keys, credentials, shell
//     start files, histories or tuios's own state, only inside
//     files_read_roots when that is set, and never a file with other names.
//
// A path is text the pane printed, so a file cat'ed in the pane can name any
// file. The owner can already read the same files with file-read under the
// same rules, so this gives the link nothing it did not have. files_read_roots
// narrows both at once.

// kittyLinkMarkerPrefix starts the marker that stands for an image in the
// stream. A process cannot print one that reaches the owner: the filter drops
// any APC that starts this way.
const kittyLinkMarkerPrefix = "\x1b_tuios-kitty-medium;"

// kittyLinkMaxBytes is the most one file medium may hold. It is the transmit
// cap the client passthrough keeps for a direct upload.
const kittyLinkMaxBytes = 64 << 20

// kittyLinkMaxCommand bounds a graphics command the filter holds while it
// waits for the rest. A file command is a few keys and a path.
const kittyLinkMaxCommand = 16 << 10

// kittyLinkMaxKeys bounds the control keys of a command before its ';'.
const kittyLinkMaxKeys = 512

// kittyLinkHoldMax is how long the filter holds the start of an escape it
// cannot classify yet. A program writes a graphics command in one write, so
// the rest is already in the pty when the first read ends; this only bounds
// the case of a program that prints a lone ESC and stops.
const kittyLinkHoldMax = 50 * time.Millisecond

// kittyLinkOpenBudget bounds the look at a file. Opening a file can wait on a
// network mount that does not answer.
const kittyLinkOpenBudget = 5 * time.Second

// kittyLinkStartBudget is how long the pump waits for the owner to start
// fetching an image after the marker is sent.
const kittyLinkStartBudget = 30 * time.Second

// kittyLinkSlots is how many reads of files for linked panes run at once on
// this machine. A slot is held for one open or one read of the file, never
// while the bytes wait for the owner, so an owner that stops reading holds up
// only its own pane.
var kittyLinkSlots = make(chan struct{}, 2)

// kittyLinkSlotBudget is how long one open or read waits for a slot.
const kittyLinkSlotBudget = 10 * time.Second

// kittyLinkStallBudget is how long a write of an image's bytes to the owner
// may wait. An owner that stops reading for longer ends the transfer, and
// the pane goes on.
const kittyLinkStallBudget = 15 * time.Second

// errKittyLinkBusy is a read that found no slot in kittyLinkSlotBudget.
var errKittyLinkBusy = errors.New("too many images are being read")

// takeKittyLinkSlot waits for a read slot, at most kittyLinkSlotBudget, and
// returns the function that gives it back.
func takeKittyLinkSlot(stop <-chan struct{}) (func(), error) {
	t := time.NewTimer(kittyLinkSlotBudget)
	defer t.Stop()
	select {
	case kittyLinkSlots <- struct{}{}:
		return func() { <-kittyLinkSlots }, nil
	case <-t.C:
		return nil, errKittyLinkBusy
	case <-stop:
		return nil, errors.New("the pane closed")
	}
}

// kittyLinkReadBudget bounds one read of a file. A read on a network or FUSE
// mount that stopped answering never returns, and the transfer cannot wait
// for it.
const kittyLinkReadBudget = 10 * time.Second

// kittyLinkMaxStuck is how many opens and reads may hang past their budget
// before new ones are refused. A hung one keeps its goroutine and its thread
// until the system answers, and a pane that names a hung path again and
// again would pile them up.
const kittyLinkMaxStuck = 4

// kittyLinkStuck counts the opens and reads past their budget that have not
// returned yet.
var kittyLinkStuck atomic.Int32

// errKittyLinkStuck is a read refused because earlier ones still hang.
var errKittyLinkStuck = errors.New("earlier reads of files are still stuck")

// kittyLinkBounded runs fn, at most budget, and counts it as stuck while it
// runs past. It reports false when fn did not finish in time; fn must then
// touch nothing the caller still uses.
func kittyLinkBounded(budget time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		kittyLinkStuck.Add(1)
		go func() {
			<-done
			kittyLinkStuck.Add(-1)
		}()
		return false
	}
}

// kittyLinkSlotReader reads r one slot at a time, each read bounded by
// kittyLinkReadBudget. A read goes into a buffer of its own, so one that
// returns late writes nothing the caller still holds.
type kittyLinkSlotReader struct{ r io.Reader }

func (s kittyLinkSlotReader) Read(p []byte) (int, error) {
	if kittyLinkStuck.Load() >= kittyLinkMaxStuck {
		return 0, errKittyLinkStuck
	}
	done, err := takeKittyLinkSlot(nil)
	if err != nil {
		return 0, err
	}
	defer done()
	buf := make([]byte, len(p))
	var n int
	var rerr error
	if !kittyLinkBounded(kittyLinkReadBudget, func() { n, rerr = s.r.Read(buf) }) {
		return 0, errors.New("a read of the file timed out")
	}
	copy(p, buf[:n])
	return n, rerr
}

// kittyLinkStallWriter gives each write to conn kittyLinkStallBudget.
type kittyLinkStallWriter struct {
	conn interface {
		io.Writer
		SetWriteDeadline(time.Time) error
	}
}

func (w kittyLinkStallWriter) Write(p []byte) (int, error) {
	_ = w.conn.SetWriteDeadline(time.Now().Add(kittyLinkStallBudget))
	return w.conn.Write(p)
}

// kittyLinkFar is a hosted pane's file medium state. Nil on a pane whose
// owner did not ask for it or whose link may not read files.
type kittyLinkFar struct {
	token string
	// check applies the link's read rules to an open file.
	check func(f *os.File, path string) error
	// allowed reports whether the machine the pane is for may still read
	// files here. The config can change while the pane runs, so it is asked
	// again before each open and each delete.
	allowed func() bool

	mu      sync.Mutex
	pending *kittyLinkTransfer
}

// kittyLinkTransfer is one image waiting for the owner to fetch it.
type kittyLinkTransfer struct {
	id       string
	medium   *kittyLinkMedium
	compress bool
	taken    bool
	started  chan struct{}
	done     chan error
}

// newKittyLinkFar turns the feature on for a pane opened over cs, when the
// machine asking may read files here.
func (d *Daemon) newKittyLinkFar(cs *connState) *kittyLinkFar {
	if cs != nil && cs.viaLink && !d.linkPolicy(cs).Allows(config.LinkAllowFiles) {
		return nil
	}
	token := newHostedCallsToken()
	if token == "" {
		return nil
	}
	allowed := func() bool {
		return cs == nil || !cs.viaLink || d.linkPolicy(cs).Allows(config.LinkAllowFiles)
	}
	return &kittyLinkFar{
		token:   token,
		allowed: allowed,
		check: func(f *os.File, path string) error {
			if !allowed() {
				return errors.New("this machine no longer lets the other one read files")
			}
			if verr := d.checkLinkRead(cs, path); verr != nil {
				return verr
			}
			if verr := d.checkOpenedForLink(cs, f, path); verr != nil {
				return verr
			}
			return nil
		},
	}
}

// kittyLinkFilter takes graphics commands with a file medium out of a hosted
// pane's output. Everything else passes through deliver unchanged.
type kittyLinkFilter struct {
	mu      sync.Mutex
	held    []byte
	gen     uint64
	deliver func([]byte)
	handle  func(apc []byte)
	// refuse answers the program for a file command that came apart, from
	// its control keys, when it is dropped.
	refuse func(ctrl []byte)
	// skip drops the output up to the next ESC: the rest of a command that
	// could name a file, or of a marker, that came apart. See arm.
	skip bool
	// escSent is set when a lone ESC went out on the hold timeout, so an
	// APC that the next read opens with '_' is skipped.
	escSent bool
	// keys holds the control keys of a graphics command whose start went
	// out on the hold timeout before its ';'. The rest of its keys are
	// checked as they come, so a t=f added after the pause cannot follow.
	keys     []byte
	keysOpen bool
	// chunks drops the later chunks of a chunked file command, which is
	// refused whole.
	chunks bool
}

// feed passes one read of the process's output through the filter.
func (f *kittyLinkFilter) feed(chunk []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	data := chunk
	if len(f.held) > 0 {
		data = append(f.held, chunk...)
		f.held = nil
	}
	f.scan(data)
}

// flush sends whatever the filter holds.
func (f *kittyLinkFilter) flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	if len(f.held) > 0 {
		h := f.held
		f.held = nil
		if kittyLinkHeldSafe(h) {
			f.deliver(h)
		}
	}
}

// closeOpenEscape sends a backslash after an ESC the owner was already sent
// alone, when what follows that ESC is skipped. The owner sees ESC \, an ST
// that does nothing, instead of the ESC joining its next byte (ESC X opens a
// string that swallows output, ESC D eats a D).
func (f *kittyLinkFilter) closeOpenEscape() {
	f.deliver([]byte{'\\'})
}

func (f *kittyLinkFilter) scan(data []byte) {
	start, i := 0, 0
	if f.escSent {
		f.escSent = false
		if len(data) > 0 && data[0] == '_' {
			// The ESC went out alone and this opens an APC after it. The
			// owner gets ESC \ and the APC is skipped.
			f.closeOpenEscape()
			f.skip = true
		}
	}
	if f.keysOpen {
		i = f.continueKeys(data)
		start = i
	}
	for {
		if f.skip {
			j := bytes.IndexByte(data[i:], 0x1b)
			if j < 0 {
				return
			}
			i += j
			f.skip = false
			if i+1 < len(data) && data[i+1] == '\\' {
				i += 2
			}
			start = i
			continue
		}
		j := bytes.IndexByte(data[i:], 0x1b)
		if j < 0 {
			break
		}
		i += j
		if f.chunks {
			if n, more, ok := kittyLinkContinuation(data[i:]); ok {
				if i > start {
					f.deliver(data[start:i])
				}
				f.chunks = more
				i += n
				start = i
				continue
			} else if n == 0 && !more {
				// Not a chunk at all: the chunked command is over.
				f.chunks = false
			} else {
				// Cannot tell yet.
				if i > start {
					f.deliver(data[start:i])
				}
				f.held = append([]byte(nil), data[i:]...)
				f.arm()
				return
			}
		}
		kind, n := classifyKittyLinkEscape(data[i:])
		switch kind {
		case escPass:
			i += n
			continue
		case escMore:
			if i > start {
				f.deliver(data[start:i])
			}
			f.held = append([]byte(nil), data[i:]...)
			f.arm()
			return
		case escFile:
			if i > start {
				f.deliver(data[start:i])
			}
			apc := data[i : i+n]
			if kittyLinkChunked(apc) {
				f.chunks = true
			}
			f.handle(apc)
		case escDrop:
			if i > start {
				f.deliver(data[start:i])
			}
		case escSkip:
			if i > start {
				f.deliver(data[start:i])
			}
			f.skip = true
			start = len(data)
			i = len(data)
			continue
		}
		i += n
		start = i
	}
	if start < len(data) {
		f.deliver(data[start:])
	}
}

// continueKeys takes the rest of the control keys of a command whose start
// went out on the hold timeout, and returns where scanning goes on. Keys
// that turn out to name a file are refused: what follows them is dropped and
// the owner gets ESC \ to close the command it was sent the start of.
func (f *kittyLinkFilter) continueKeys(data []byte) int {
	end := bytes.IndexAny(data, ";\x1b")
	part := data
	if end >= 0 {
		part = data[:end]
	}
	f.keys = append(f.keys, part...)
	if kittyLinkFileMedium(f.keys) || len(f.keys) > kittyLinkMaxKeys {
		f.keysOpen = false
		if f.refuse != nil {
			f.refuse(f.keys)
		}
		f.keys = nil
		f.deliver([]byte("\x1b\\"))
		f.skip = true
		return 0
	}
	if end < 0 {
		f.deliver(data)
		return len(data)
	}
	f.keysOpen, f.keys = false, nil
	return 0
}

// arm settles what is held when nothing more comes soon:
//
//   - a lone ESC goes out;
//   - a graphics command that names no file goes out, also when its keys
//     are not all in yet (continueKeys checks the rest);
//   - a command that names a file is refused, with an answer to the
//     program, and dropped with the rest of it;
//   - anything else, the start of an APC or of a marker, is dropped with the
//     rest of it.
//
// Sent on as it is, a path would reach the owner and the terminal there,
// and a marker prefix would confuse the owner's scan.
func (f *kittyLinkFilter) arm() {
	gen := f.gen
	time.AfterFunc(kittyLinkHoldMax, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.gen != gen || len(f.held) == 0 {
			return
		}
		h := f.held
		f.held = nil
		f.gen++
		switch {
		case len(h) == 1:
			f.deliver(h)
			f.escSent = true
		case kittyLinkHeldSafe(h):
			f.deliver(h)
		case len(h) >= 3 && h[1] == '_' && h[2] == 'G':
			ctrl, _, semi := bytes.Cut(h[3:], []byte{';'})
			switch {
			case kittyLinkFileMedium(ctrl):
				if f.refuse != nil {
					f.refuse(ctrl)
				}
				f.skip = true
			case !semi && !f.chunks:
				f.deliver(h)
				f.keys = append([]byte(nil), ctrl...)
				f.keysOpen = true
			default:
				f.skip = true
			}
		default:
			f.skip = true
		}
	})
}

// kittyLinkHeldSafe reports whether held output, an incomplete escape, is a
// graphics command whose keys are complete and name no file.
func kittyLinkHeldSafe(h []byte) bool {
	if len(h) < 3 || h[1] != '_' || h[2] != 'G' {
		return false
	}
	ctrl, _, semi := bytes.Cut(h[3:], []byte{';'})
	return semi && !kittyLinkFileMedium(ctrl)
}

// kittyLinkContinuation looks for a later chunk of a chunked command at the
// start of b: a graphics command whose keys are only m and q. It returns the
// chunk's length and whether more follow. ok is false with n 0 and more false
// when b is something else, and false with more true when b is too short to
// tell.
func kittyLinkContinuation(b []byte) (n int, more, ok bool) {
	if len(b) < 3 {
		return 0, true, false
	}
	if b[1] != '_' || b[2] != 'G' {
		return 0, false, false
	}
	end := bytes.IndexByte(b[3:], 0x1b)
	body := b[3:]
	if end >= 0 {
		body = body[:end]
	}
	ctrl, _, semi := bytes.Cut(body, []byte{';'})
	for pair := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(pair) < 2 || pair[1] != '=' || (pair[0] != 'm' && pair[0] != 'q') {
			if !semi && end < 0 {
				return 0, true, false
			}
			return 0, false, false
		}
		if pair[0] == 'm' {
			more = len(pair) > 2 && pair[2] == '1'
		}
	}
	if end < 0 || 3+end+1 >= len(b) {
		return 0, true, false
	}
	n = 3 + end + 1
	if b[n] == '\\' {
		n++
	}
	return n, more, true
}

type escKind int

const (
	escPass escKind = iota // pass n bytes and scan on
	escMore                // cannot tell yet: hold from here
	escFile                // a graphics command with a file medium, n bytes
	escDrop                // a forged marker, n bytes, dropped
	escSkip                // drop n bytes and the rest up to the next ESC
)

// classifyKittyLinkEscape looks at an escape sequence at the start of b.
func classifyKittyLinkEscape(b []byte) (escKind, int) {
	if len(b) < 2 {
		return escMore, 0
	}
	if b[1] != '_' {
		return escPass, 1
	}
	if len(b) < 3 {
		return escMore, 0
	}
	switch b[2] {
	case 'G':
		body := b[3:]
		end := bytes.IndexByte(body, 0x1b)
		if end >= 0 {
			body = body[:end]
		}
		ctrl, _, semi := bytes.Cut(body, []byte{';'})
		if !semi {
			switch {
			case end >= 0:
				// Keys and no payload: no path to read.
				return escPass, 3
			case len(ctrl) <= kittyLinkMaxKeys:
				return escMore, 0
			}
			// Keys longer than any real command: dropped, so no path
			// after them is sent on.
			return escSkip, len(b)
		}
		if !kittyLinkFileMedium(ctrl) {
			return escPass, 3
		}
		if end < 0 {
			if len(b) > kittyLinkMaxCommand {
				// Too long for a path: dropped, never sent on.
				return escSkip, len(b)
			}
			return escMore, 0
		}
		if 3+end+1 >= len(b) {
			return escMore, 0
		}
		if b[3+end+1] != '\\' {
			// Cut off by another escape: dropped.
			return escDrop, 3 + end
		}
		return escFile, 3 + end + 2
	case 't':
		p := kittyLinkMarkerPrefix
		k := min(len(b), len(p))
		if string(b[:k]) != p[:k] {
			return escPass, 2
		}
		if len(b) < len(p) {
			return escMore, 0
		}
		end := bytes.IndexByte(b[len(p):], 0x1b)
		if end < 0 {
			if len(b) > kittyLinkMaxCommand {
				return escSkip, len(b)
			}
			return escMore, 0
		}
		n := len(p) + end + 1
		if n >= len(b) {
			return escMore, 0
		}
		if b[n] == '\\' {
			n++
		}
		return escDrop, n
	}
	return escPass, 2
}

// kittyLinkFileMedium reports whether a graphics command's control keys name
// a file medium (t=f, t=t or t=s), whatever the action and m are. Every such
// command is this side's to handle: none goes on to the owner, where the
// path would reach the terminal.
func kittyLinkFileMedium(ctrl []byte) bool {
	medium := byte('d')
	for pair := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(pair) >= 3 && pair[0] == 't' && pair[1] == '=' {
			medium = pair[2]
		}
	}
	return medium == 'f' || medium == 't' || medium == 's'
}

// kittyLinkChunked reports whether a graphics command says more chunks
// follow (m=1).
func kittyLinkChunked(apc []byte) bool {
	ctrl, _, _ := bytes.Cut(apc[3:], []byte{';'})
	for pair := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(pair) >= 3 && pair[0] == 'm' && pair[1] == '=' {
			return pair[2] == '1'
		}
	}
	return false
}

// kittyLinkKeys is a file command's control keys rewritten for direct
// transmission: t=d, and no O, S or m.
func kittyLinkKeys(ctrl []byte) string {
	var b strings.Builder
	b.WriteString("t=d")
	for len(ctrl) > 0 {
		var pair []byte
		pair, ctrl, _ = bytes.Cut(ctrl, []byte{','})
		if len(pair) == 0 {
			continue
		}
		if len(pair) >= 2 && pair[1] == '=' {
			switch pair[0] {
			case 't', 'O', 'S', 'm':
				continue
			}
		}
		b.WriteByte(',')
		b.Write(pair)
	}
	return b.String()
}

// kittyFileCommand handles one graphics command with a file medium, apc being
// the whole sequence. It runs on the pump, so the pane's output waits for it.
func (hp *hostedPane) kittyFileCommand(apc []byte) {
	k := hp.kittyLink
	body := apc[3 : len(apc)-2]
	ctrl, _, _ := bytes.Cut(body, []byte{';'})
	cmd, err := vt.ParseKittyCommand(body)
	if err != nil || cmd == nil {
		return
	}
	if cmd.PayloadErr != nil {
		hp.kittyReply(cmd, "EINVAL:the path is not valid base64")
		return
	}
	if cmd.More {
		// A path in chunks: kitty sends one in one command, and nothing
		// here joins the chunks. Refused whole; the filter drops the
		// later chunks.
		hp.kittyReply(cmd, "EINVAL:a file transmission must be one command, not chunked")
		return
	}
	switch cmd.Action {
	case vt.KittyActionTransmit, vt.KittyActionTransmitPlace, vt.KittyActionQuery, vt.KittyActionFrame:
	default:
		hp.kittyReply(cmd, "EINVAL:this action does not take a file medium")
		return
	}

	done, err := takeKittyLinkSlot(hp.stop)
	if err != nil {
		if errors.Is(err, errKittyLinkBusy) {
			hp.kittyReply(cmd, "EBUSY:too many images are being read")
		}
		return
	}
	m, msg := openKittyLinkMediumTimed(cmd, k.check)
	done()
	if msg != "" {
		hp.kittyReply(cmd, msg)
		return
	}
	keys := kittyLinkKeys(ctrl)

	m.allowed = k.allowed
	if cmd.Action == vt.KittyActionQuery {
		// The owner answers for its clients as it answers a query for t=d.
		// Nothing is deleted for a query: a query proves nothing about the
		// object it names, and a printed text file can hold one.
		m.release(false)
		hp.deliver([]byte("\x1b_G" + keys + "\x1b\\"))
		return
	}

	t := &kittyLinkTransfer{
		id:     newHostedCallsToken(),
		medium: m,
		// Raw pixels compress well and cross the link in a fraction of
		// their size. A PNG does not.
		compress: cmd.Format != vt.KittyFormatPNG && cmd.Compression == vt.KittyCompressionNone,
		started:  make(chan struct{}),
		done:     make(chan error, 1),
	}
	k.mu.Lock()
	k.pending = t
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		if k.pending == t {
			k.pending = nil
		}
		k.mu.Unlock()
	}()

	hp.deliver([]byte(kittyLinkMarkerPrefix + t.id + ";" + keys + "\x1b\\"))

	var result error
	timer := time.NewTimer(kittyLinkStartBudget)
	select {
	case <-t.started:
		timer.Stop()
		select {
		case result = <-t.done:
		case <-hp.stop:
			result = errors.New("the pane closed")
		}
	case <-timer.C:
		result = errors.New("the owner did not fetch the image")
	case <-hp.stop:
		timer.Stop()
		result = errors.New("the pane closed")
	}
	k.mu.Lock()
	taken := t.taken
	if !taken {
		t.taken = true
	}
	k.mu.Unlock()
	if !taken {
		// Nobody fetched it, so nothing else holds the file.
		m.release(false)
	}
	if result != nil {
		debugLog("[DEBUG] kitty file over link: %v", result)
		// The reason stays in the log: it names sockets and paths, and the
		// reply goes into the pane's input.
		hp.kittyReply(cmd, "EIO:the image did not reach the terminal")
	}
}

// kittyRefuseParts answers a file command that came apart and was dropped,
// from its control keys.
func (hp *hostedPane) kittyRefuseParts(ctrl []byte) {
	cmd, err := vt.ParseKittyCommand(ctrl)
	if err != nil || cmd == nil {
		return
	}
	hp.kittyReply(cmd, "EINVAL:the command came in parts too far apart")
}

// kittyReply answers the process in the pane, as kitty answers an upload,
// unless q says not to.
func (hp *hostedPane) kittyReply(cmd *vt.KittyCommand, msg string) {
	if cmd.Quiet >= 2 || (cmd.Quiet >= 1 && msg == "OK") {
		return
	}
	var b strings.Builder
	b.WriteString("\x1b_G")
	sep := ""
	if cmd.ImageID != 0 {
		fmt.Fprintf(&b, "i=%d", cmd.ImageID)
		sep = ","
	}
	if cmd.ImageNumber != 0 {
		fmt.Fprintf(&b, "%sI=%d", sep, cmd.ImageNumber)
		sep = ","
	}
	if cmd.PlacementID != 0 {
		fmt.Fprintf(&b, "%sp=%d", sep, cmd.PlacementID)
	}
	b.WriteByte(';')
	b.WriteString(msg)
	b.WriteString("\x1b\\")
	hp.writeInput([]byte(b.String()))
}

// kittyLinkErrText is an error's text fit for a reply: no path, no control
// characters and no ';'.
func kittyLinkErrText(err error) string {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		err = pathErr.Err
	}
	if verr, ok := errors.AsType[*verbError](err); ok {
		err = errors.New(verr.Message)
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == ';' || r == 0xfffd {
			return ' '
		}
		return r
	}, err.Error())
}

// kittyLinkMedium is an open file medium: the file positioned at its first
// byte, and how many bytes it covers.
type kittyLinkMedium struct {
	f *os.File
	n int64
	// name is the path the command gave, before symlinks; cmd is the command.
	name string
	cmd  *vt.KittyCommand
	// allowed is asked again before a delete. Nil allows.
	allowed func() bool
}

// release closes the file. After an upload is read, a temporary file or a
// shared memory object is deleted, as kitty deletes them, under the rules the
// daemon keeps for a frame no client reads (kitty_medium.go): only for a=t or
// a=T, only the name as given, which must be the very object read and not a
// symlink, and only when it is the frame the command describes
// (vt.KittyMediumIsFrame). The name is text the pane printed, and it can name
// an object another program still uses.
func (m *kittyLinkMedium) release(read bool) {
	defer func() { _ = m.f.Close() }()
	cmd := m.cmd
	if !read || cmd == nil ||
		(cmd.Medium != vt.KittyMediumTempFile && cmd.Medium != vt.KittyMediumSharedMemory) ||
		(cmd.Action != vt.KittyActionTransmit && cmd.Action != vt.KittyActionTransmitPlace) {
		return
	}
	if m.allowed != nil && !m.allowed() {
		return
	}
	info, err := os.Lstat(m.name)
	if err != nil || !vt.KittyMediumIsFrame(cmd, info) {
		return
	}
	if opened, err := m.f.Stat(); err != nil || !os.SameFile(opened, info) {
		return
	}
	if err := os.Remove(m.name); err != nil && !os.IsNotExist(err) {
		debugLog("[DEBUG] kitty file over link: remove: %v", err)
	}
}

// openKittyLinkMediumTimed is openKittyLinkMedium, bounded by
// kittyLinkOpenBudget.
func openKittyLinkMediumTimed(cmd *vt.KittyCommand, check func(*os.File, string) error) (*kittyLinkMedium, string) {
	if kittyLinkStuck.Load() >= kittyLinkMaxStuck {
		return nil, "EBUSY:earlier reads of files are still stuck"
	}
	var (
		mu   sync.Mutex
		late bool
		m    *kittyLinkMedium
		msg  string
	)
	ok := kittyLinkBounded(kittyLinkOpenBudget, func() {
		got, gotMsg := openKittyLinkMedium(cmd, check)
		mu.Lock()
		defer mu.Unlock()
		if late {
			// The caller gave up on this open: nothing else closes it.
			if got != nil {
				got.release(false)
			}
			return
		}
		m, msg = got, gotMsg
	})
	mu.Lock()
	defer mu.Unlock()
	if !ok {
		late = true
		if m != nil {
			// It finished as the budget ran out.
			m.release(false)
		}
		return nil, "EBADF:Failed to open file for graphics transmission: timed out"
	}
	return m, msg
}

// openKittyLinkMedium checks and opens the file a file medium names. The
// message is kitty's answer when it may not be read. See the top of the file
// for the rules.
func openKittyLinkMedium(cmd *vt.KittyCommand, check func(*os.File, string) error) (*kittyLinkMedium, string) {
	name := cmd.FilePath
	if name == "" || strings.ContainsRune(name, 0) {
		return nil, "EINVAL:the file path is empty"
	}
	var path string
	switch cmd.Medium {
	case vt.KittyMediumSharedMemory:
		base := strings.TrimPrefix(name, "/")
		if base == "" || base == "." || base == ".." || strings.ContainsRune(base, '/') {
			return nil, "EINVAL:a shared memory name is one name"
		}
		if runtime.GOOS != "linux" {
			return nil, "ENOTSUPPORTED:shared memory is not a file on this machine"
		}
		path = "/dev/shm/" + base
	default:
		if !filepath.IsAbs(name) {
			return nil, "EINVAL:the file path is not an absolute path"
		}
		path = filepath.Clean(name)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, kittyLinkOpenError(err)
	}
	if cmd.Medium == vt.KittyMediumSharedMemory {
		if filepath.Dir(resolved) != "/dev/shm" {
			return nil, "EPERM:a shared memory name must stay in /dev/shm"
		}
	} else if vt.KittySensitivePath(resolved) {
		return nil, "EPERM:the file is in a place no image may be read from"
	}
	if cmd.Medium == vt.KittyMediumTempFile &&
		(!strings.Contains(filepath.Base(resolved), vt.KittyTempMarker) || !vt.KittyInTempDir(resolved)) {
		return nil, "EPERM:a temporary file must be in a temporary directory and name tty-graphics-protocol"
	}
	// No symlink in the last part, and no wait on a named pipe swapped in
	// since the path was resolved.
	f, err := os.OpenFile(resolved, os.O_RDONLY|oNonBlock|oNoFollow, 0) // #nosec G304 - checked above
	if err != nil {
		return nil, kittyLinkOpenError(err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !fileOwnedByDaemonUser(info) {
		_ = f.Close()
		return nil, "EINVAL:the file is not a regular file of this user"
	}
	if check != nil {
		if err := check(f, resolved); err != nil {
			_ = f.Close()
			debugLog("[DEBUG] kitty file over link: refused by the link's read rules: %v", err)
			return nil, "EPERM:the file may not be read over this link"
		}
	}
	offset := int64(max(cmd.Offset, 0))
	n := info.Size() - offset
	if cmd.Size > 0 {
		n = min(n, int64(cmd.Size))
	}
	if n <= 0 {
		_ = f.Close()
		return nil, "EINVAL:the offset and size are outside the file"
	}
	if n > kittyLinkMaxBytes {
		_ = f.Close()
		return nil, "EFBIG:the image is larger than 64 MiB"
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return nil, kittyLinkOpenError(err)
		}
	}
	return &kittyLinkMedium{f: f, n: n, name: path, cmd: cmd}, ""
}

func kittyLinkOpenError(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "ENOENT:Failed to open file for graphics transmission: no such file"
	}
	// Only the system's error name goes into the reply, never a path.
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return "EBADF:Failed to open file for graphics transmission: " + kittyLinkErrText(errno)
	}
	return "EBADF:Failed to open file for graphics transmission"
}

// verbPaneKittyMedium turns this connection into the bytes of an image a
// hosted pane named by path. Only the owner holds the token.
func (d *Daemon) verbPaneKittyMedium(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Pane  string `json:"pane"`
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	hp := d.lookupHostedPane(p.Pane)
	if hp == nil {
		return nil, newVerbError(ErrVerbUnknownPane, "this machine is not running a pane called "+echoName(p.Pane)+".")
	}
	k := hp.kittyLink
	if k == nil || subtle.ConstantTimeCompare([]byte(k.token), []byte(p.Token)) != 1 {
		return nil, newVerbError(ErrVerbForbidden, "pane-kitty-medium needs the token the open-pane reply carried, and this is not it")
	}
	k.mu.Lock()
	t := k.pending
	if t == nil || t.taken || subtle.ConstantTimeCompare([]byte(t.id), []byte(p.ID)) != 1 {
		k.mu.Unlock()
		return nil, newVerbError(ErrVerbNoFile, "the pane has no image waiting under that id")
	}
	t.taken = true
	k.mu.Unlock()
	close(t.started)

	m := t.medium
	cs.takeover = func(_ *bufio.Reader) {
		defer m.release(true)
		// Each write gets kittyLinkStallBudget and each read of the file
		// takes a slot only for that read, so an owner that stops reading
		// frees the slots at once and ends the transfer soon after.
		bw := bufio.NewWriterSize(kittyLinkStallWriter{cs.conn}, 64<<10)
		body := io.LimitReader(kittyLinkSlotReader{m.f}, m.n)
		var err error
		var n int64
		if t.compress {
			fw := getFlateWriter(bw)
			n, err = io.CopyBuffer(fw, body, make([]byte, 64<<10))
			if err == nil {
				err = fw.Close()
			}
			putFlateWriter(fw)
		} else {
			n, err = io.CopyBuffer(bw, body, make([]byte, 64<<10))
		}
		if err == nil && n < m.n {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			err = bw.Flush()
		}
		_ = cs.conn.Close()
		t.done <- err
	}
	return map[string]any{"pane": p.Pane, "size": m.n, "compressed": t.compress}, nil
}
