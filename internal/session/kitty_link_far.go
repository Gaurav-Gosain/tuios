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
// kittyLinkSlots files are read at once on this machine.
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

// kittyLinkSlots is how many files the daemon reads for linked panes at once.
var kittyLinkSlots = make(chan struct{}, 2)

// kittyLinkFar is a hosted pane's file medium state. Nil on a pane whose
// owner did not ask for it or whose link may not read files.
type kittyLinkFar struct {
	token string
	// check applies the link's read rules to an open file.
	check func(f *os.File, path string) error

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
	return &kittyLinkFar{
		token: token,
		check: func(f *os.File, path string) error {
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
		f.deliver(h)
	}
}

func (f *kittyLinkFilter) scan(data []byte) {
	start, i := 0, 0
	for {
		j := bytes.IndexByte(data[i:], 0x1b)
		if j < 0 {
			break
		}
		i += j
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
			f.handle(data[i : i+n])
		case escDrop:
			if i > start {
				f.deliver(data[start:i])
			}
		}
		i += n
		start = i
	}
	if start < len(data) {
		f.deliver(data[start:])
	}
}

// arm sends what is held as it is, when nothing more comes soon.
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
		f.deliver(h)
	})
}

type escKind int

const (
	escPass escKind = iota // pass n bytes and scan on
	escMore                // cannot tell yet: hold from here
	escFile                // a graphics command with a file medium, n bytes
	escDrop                // a forged marker, n bytes, dropped
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
			if end < 0 && len(ctrl) <= kittyLinkMaxKeys {
				return escMore, 0
			}
			return escPass, 3
		}
		if !kittyLinkFileKeys(ctrl) {
			return escPass, 3
		}
		if end < 0 {
			if len(b) > kittyLinkMaxCommand {
				return escPass, 3
			}
			return escMore, 0
		}
		if 3+end+1 >= len(b) {
			return escMore, 0
		}
		if b[3+end+1] != '\\' {
			return escPass, 3
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
				return escDrop, len(b)
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

// kittyLinkFileKeys reports whether a graphics command's control keys ask for
// a file medium upload or query this side handles: one command, not chunked.
func kittyLinkFileKeys(ctrl []byte) bool {
	medium, action, more := byte('d'), byte('t'), false
	for len(ctrl) > 0 {
		var pair []byte
		pair, ctrl, _ = bytes.Cut(ctrl, []byte{','})
		if len(pair) < 3 || pair[1] != '=' {
			continue
		}
		switch pair[0] {
		case 't':
			medium = pair[2]
		case 'a':
			action = pair[2]
		case 'm':
			more = pair[2] == '1'
		}
	}
	switch medium {
	case 'f', 't', 's':
	default:
		return false
	}
	switch action {
	case 't', 'T', 'q', 'f':
	default:
		return false
	}
	return !more
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

	select {
	case kittyLinkSlots <- struct{}{}:
	case <-hp.stop:
		return
	}
	defer func() { <-kittyLinkSlots }()

	m, msg := openKittyLinkMediumTimed(cmd, k.check)
	if msg != "" {
		hp.kittyReply(cmd, msg)
		return
	}
	keys := kittyLinkKeys(ctrl)

	if cmd.Action == vt.KittyActionQuery {
		// kitty reads the medium to answer a query, and deletes a
		// temporary file or a shared memory object it read. The owner
		// answers for its clients as it answers a query for t=d.
		m.release(true)
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
		hp.kittyReply(cmd, "EIO:the image did not reach the terminal: "+kittyLinkErrText(result))
	}
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
		if r < 0x20 || r == 0x7f || r == ';' {
			return ' '
		}
		return r
	}, err.Error())
}

// kittyLinkMedium is an open file medium: the file positioned at its first
// byte, and how many bytes it covers.
type kittyLinkMedium struct {
	f      *os.File
	n      int64
	path   string
	medium vt.KittyGraphicsMedium
}

// release closes the file. After a read, a temporary file and a shared memory
// object are deleted, as kitty deletes them, when the name still holds the
// file that was read.
func (m *kittyLinkMedium) release(read bool) {
	if read && (m.medium == vt.KittyMediumTempFile || m.medium == vt.KittyMediumSharedMemory) {
		opened, oerr := m.f.Stat()
		now, nerr := os.Lstat(m.path)
		if oerr == nil && nerr == nil && os.SameFile(opened, now) {
			if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
				debugLog("[DEBUG] kitty file over link: remove %s: %v", m.path, err)
			}
		}
	}
	_ = m.f.Close()
}

// openKittyLinkMediumTimed is openKittyLinkMedium, bounded by
// kittyLinkOpenBudget.
func openKittyLinkMediumTimed(cmd *vt.KittyCommand, check func(*os.File, string) error) (*kittyLinkMedium, string) {
	type res struct {
		m   *kittyLinkMedium
		msg string
	}
	got := make(chan res, 1)
	go func() {
		m, msg := openKittyLinkMedium(cmd, check)
		got <- res{m, msg}
	}()
	t := time.NewTimer(kittyLinkOpenBudget)
	defer t.Stop()
	select {
	case r := <-got:
		return r.m, r.msg
	case <-t.C:
		go func() {
			if r := <-got; r.m != nil {
				r.m.release(false)
			}
		}()
		return nil, "EBADF:Failed to open file for graphics transmission: timed out"
	}
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
			return nil, "EPERM:" + kittyLinkErrText(err)
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
	return &kittyLinkMedium{f: f, n: n, path: resolved, medium: cmd.Medium}, ""
}

func kittyLinkOpenError(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "ENOENT:Failed to open file for graphics transmission: no such file"
	}
	return "EBADF:Failed to open file for graphics transmission: " + kittyLinkErrText(err)
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
		_ = cs.conn.SetWriteDeadline(time.Time{})
		bw := bufio.NewWriterSize(cs.conn, 64<<10)
		var w io.Writer = bw
		var err error
		var n int64
		if t.compress {
			fw := getFlateWriter(bw)
			n, err = io.CopyBuffer(fw, io.LimitReader(m.f, m.n), make([]byte, 64<<10))
			if err == nil {
				err = fw.Close()
			}
			putFlateWriter(fw)
		} else {
			n, err = io.CopyBuffer(w, io.LimitReader(m.f, m.n), make([]byte, 64<<10))
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
