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

const kittyLinkMarkerPrefix = "\x1b_tuios-kitty-medium;"

const kittyLinkMaxBytes = 64 << 20

const kittyLinkMaxCommand = 16 << 10

const kittyLinkMaxKeys = 512

const kittyLinkHoldMax = 50 * time.Millisecond

const kittyLinkOpenBudget = 5 * time.Second

const kittyLinkStartBudget = 30 * time.Second

var kittyLinkSlots = make(chan struct{}, 2)

const kittyLinkSlotBudget = 10 * time.Second

const kittyLinkStallBudget = 15 * time.Second

var errKittyLinkBusy = errors.New("too many images are being read")

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

const kittyLinkReadBudget = 10 * time.Second

const kittyLinkMaxStuck = 4

var kittyLinkStuck atomic.Int32

var errKittyLinkStuck = errors.New("earlier reads of files are still stuck")

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

type kittyLinkFar struct {
	token   string
	check   func(f *os.File, path string) error
	allowed func() bool

	mu      sync.Mutex
	pending *kittyLinkTransfer
}

type kittyLinkTransfer struct {
	id       string
	medium   *kittyLinkMedium
	compress bool
	taken    bool
	started  chan struct{}
	done     chan error
}

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

type kittyLinkFilter struct {
	mu       sync.Mutex
	held     []byte
	gen      uint64
	deliver  func([]byte)
	handle   func(apc []byte)
	refuse   func(ctrl []byte)
	skip     bool
	escSent  bool
	keys     []byte
	keysOpen bool
	chunks   bool
}

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

func (f *kittyLinkFilter) closeOpenEscape() {
	f.deliver([]byte{'\\'})
}

func (f *kittyLinkFilter) scan(data []byte) {
	start, i := 0, 0
	if f.escSent {
		f.escSent = false
		if len(data) > 0 && data[0] == '_' {
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
				f.chunks = false
			} else {
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

func kittyLinkHeldSafe(h []byte) bool {
	if len(h) < 3 || h[1] != '_' || h[2] != 'G' {
		return false
	}
	ctrl, _, semi := bytes.Cut(h[3:], []byte{';'})
	return semi && !kittyLinkFileMedium(ctrl)
}

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
	escPass escKind = iota
	escMore
	escFile
	escDrop
	escSkip
)

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
				return escPass, 3
			case len(ctrl) <= kittyLinkMaxKeys:
				return escMore, 0
			}
			return escSkip, len(b)
		}
		if !kittyLinkFileMedium(ctrl) {
			return escPass, 3
		}
		if end < 0 {
			if len(b) > kittyLinkMaxCommand {
				return escSkip, len(b)
			}
			return escMore, 0
		}
		if 3+end+1 >= len(b) {
			return escMore, 0
		}
		if b[3+end+1] != '\\' {
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

func kittyLinkFileMedium(ctrl []byte) bool {
	medium := byte('d')
	for pair := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(pair) >= 3 && pair[0] == 't' && pair[1] == '=' {
			medium = pair[2]
		}
	}
	return medium == 'f' || medium == 't' || medium == 's'
}

func kittyLinkChunked(apc []byte) bool {
	ctrl, _, _ := bytes.Cut(apc[3:], []byte{';'})
	for pair := range bytes.SplitSeq(ctrl, []byte{','}) {
		if len(pair) >= 3 && pair[0] == 'm' && pair[1] == '=' {
			return pair[2] == '1'
		}
	}
	return false
}

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
		m.release(false)
		hp.deliver([]byte("\x1b_G" + keys + "\x1b\\"))
		return
	}

	t := &kittyLinkTransfer{
		id:       newHostedCallsToken(),
		medium:   m,
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
		m.release(false)
	}
	if result != nil {
		debugLog("[DEBUG] kitty file over link: %v", result)
		hp.kittyReply(cmd, "EIO:the image did not reach the terminal")
	}
}

func (hp *hostedPane) kittyRefuseParts(ctrl []byte) {
	cmd, err := vt.ParseKittyCommand(ctrl)
	if err != nil || cmd == nil {
		return
	}
	hp.kittyReply(cmd, "EINVAL:the command came in parts too far apart")
}

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

type kittyLinkMedium struct {
	f       *os.File
	n       int64
	name    string
	cmd     *vt.KittyCommand
	allowed func() bool
}

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
			m.release(false)
		}
		return nil, "EBADF:Failed to open file for graphics transmission: timed out"
	}
	return m, msg
}

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
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return "EBADF:Failed to open file for graphics transmission: " + kittyLinkErrText(errno)
	}
	return "EBADF:Failed to open file for graphics transmission"
}

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
