package tmuxcompat

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Paste buffers: load-buffer, set-buffer, paste-buffer and delete-buffer.
//
// tmux keeps its buffers in the server. A daemon with the paste buffer verbs
// is that server, and holds them (buffers_daemon.go). With an older daemon
// the shim has no server, so a buffer is a file in the shim's runtime
// directory, which only the user can read.
// That lets `tmux load-buffer x` and a later `tmux paste-buffer` work across
// two calls, as they do with tmux. A shim with no runtime directory keeps its
// buffers for the one call.
//
// A paste goes to the pane through send-text, the verb every typed write
// takes, so the daemon holds it to the caller's pane grants: a pane without
// write cannot paste into another pane. The text is sanitized the way every
// tuios paste is (vt.SanitizePaste), and with -p the daemon wraps it in the
// bracketed paste delimiters when the pane's program turned bracketed paste
// on.

// maxBufferBytes caps one buffer. tmux has no cap, but a buffer is typed into
// a pane, and nothing typed is this long.
const maxBufferBytes = 16 << 20

// buffer is one paste buffer.
type buffer struct {
	name string
	data string
	at   time.Time
	// size and sample come from the daemon's listing, so a listing reads no
	// buffer's whole content. sampled says they are set.
	size    int
	sample  string
	sampled bool
	// auto is the daemon's word that it named the buffer. A buffer in a
	// file is automatic when its name is one tmux would give.
	auto bool
}

// automatic reports whether the buffer is one a set without -b made.
func (b buffer) automatic() bool {
	if b.sampled {
		return b.auto
	}
	n, ok := strings.CutPrefix(b.name, "buffer")
	if !ok || n == "" {
		return false
	}
	_, err := strconv.Atoi(n)
	return err == nil
}

// bufferDir is where buffers live, "" when the shim keeps them in memory.
func (s *Shim) bufferDir() string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "buffers")
}

// bufferFile is the file of buffer name: the name in hex, so any name is a
// safe file name.
func bufferFile(dir, name string) string {
	return filepath.Join(dir, hex.EncodeToString([]byte(name)))
}

// buffers lists the buffers, in no order. See topBuffer for the newest.
func (s *Shim) buffers() ([]buffer, error) {
	if s.daemonBuffers() {
		return s.daemonBufferList()
	}
	dir := s.bufferDir()
	if dir == "" {
		return s.memBuffers, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []buffer
	for _, e := range entries {
		raw, err := hex.DecodeString(e.Name())
		if err != nil || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, buffer{name: string(raw), at: info.ModTime()})
	}
	return out, nil
}

// withoutBuffer is list without the buffer called name. It is a plain loop,
// not slices.DeleteFunc, since each generic instance costs binary size.
func withoutBuffer(list []buffer, name string) []buffer {
	out := list[:0]
	for _, b := range list {
		if b.name != name {
			out = append(out, b)
		}
	}
	return out
}

// readBuffer returns the data of buffer name.
func (s *Shim) readBuffer(name string) (string, bool, error) {
	if s.daemonBuffers() {
		return s.daemonBufferRead(name)
	}
	dir := s.bufferDir()
	if dir == "" {
		for _, b := range s.memBuffers {
			if b.name == name {
				return b.data, true, nil
			}
		}
		return "", false, nil
	}
	data, err := os.ReadFile(bufferFile(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// writeBuffer stores data as buffer name and puts it on top. A name of ""
// is a new buffer the daemon names, and comes only from newBufferName when
// the daemon holds the buffers.
func (s *Shim) writeBuffer(name, data string) error {
	if data == "" {
		// tmux stores nothing for empty data, and says nothing.
		return nil
	}
	if len(data) > maxBufferBytes {
		return fmt.Errorf("buffer is too large: %d bytes, the limit is %d", len(data), maxBufferBytes)
	}
	if s.daemonBuffers() {
		return s.daemonBufferWrite(name, data, false)
	}
	dir := s.bufferDir()
	if dir == "" {
		s.memBuffers = append(withoutBuffer(s.memBuffers, name), buffer{name: name, data: data, at: newestAfter(s.memBuffers)})
		return nil
	}
	list, err := s.buffers()
	if err != nil {
		return err
	}
	stamp := newestAfter(list)
	if err := EnsureDir(s.Dir); err != nil {
		return err
	}
	if err := EnsureDir(dir); err != nil {
		return err
	}
	path := bufferFile(dir, name)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chtimes(tmp.Name(), stamp, stamp); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// newestAfter is the time to stamp a new buffer with: now, or just after the
// newest buffer in list when the clock has not moved past it. The kernel
// stamps a file with a clock that ticks every few milliseconds, so two
// buffers written in one tick would otherwise tie, and the top of the stack
// would fall to the name order. The step is a microsecond, which NTFS's
// 100ns mtime keeps.
func newestAfter(list []buffer) time.Time {
	stamp := time.Now().Round(0)
	for _, b := range list {
		if !stamp.After(b.at) {
			stamp = b.at.Add(time.Microsecond)
		}
	}
	return stamp
}

// removeBuffer deletes buffer name.
func (s *Shim) removeBuffer(name string) error {
	if s.daemonBuffers() {
		return s.daemonBufferRemove(name, 0)
	}
	dir := s.bufferDir()
	if dir == "" {
		s.memBuffers = withoutBuffer(s.memBuffers, name)
		return nil
	}
	err := os.Remove(bufferFile(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// newBufferName is the name tmux gives a buffer made without -b:
// bufferN, one past the highest in use. With the daemon holding the
// buffers it is "", and the daemon names the buffer as it names a yank.
func (s *Shim) newBufferName() (string, error) {
	if s.daemonBuffers() {
		return "", nil
	}
	list, err := s.buffers()
	if err != nil {
		return "", err
	}
	// The number only goes up, as in tmux, so a deleted buffer's name is not
	// given again. The count is kept beside the buffers, since every call is
	// a process of its own.
	next := s.loadBufferCount()
	for _, b := range list {
		if n, ok := strings.CutPrefix(b.name, "buffer"); ok {
			if i, err := strconv.Atoi(n); err == nil && i >= next {
				next = i + 1
			}
		}
	}
	s.saveBufferCount(next + 1)
	return fmt.Sprintf("buffer%d", next), nil
}

// bufferCountFile holds the number of the next buffer name. Its name is not
// hex, so buffers never takes it for a buffer.
const bufferCountFile = "next"

// loadBufferCount is the number the next buffer name starts from.
func (s *Shim) loadBufferCount() int {
	dir := s.bufferDir()
	if dir == "" {
		return s.memNext
	}
	raw, err := os.ReadFile(filepath.Join(dir, bufferCountFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// saveBufferCount keeps the number the next buffer name starts from. It is
// best effort: losing it only lets a name come back after a delete.
func (s *Shim) saveBufferCount(n int) {
	dir := s.bufferDir()
	if dir == "" {
		s.memNext = n
		return
	}
	if EnsureDir(s.Dir) != nil || EnsureDir(dir) != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, bufferCountFile), []byte(strconv.Itoa(n)), 0o600)
}

// topBuffer is the name of the newest automatic buffer, "" when there is
// none: the top of tmux's buffer stack, which a command with no -b takes. A
// buffer someone named is not on it. Of two written at one time, the later
// name wins.
func (s *Shim) topBuffer() (string, error) {
	list, err := s.buffers()
	if err != nil {
		return "", err
	}
	var top buffer
	for _, b := range list {
		if !b.automatic() {
			continue
		}
		if top.name == "" || b.at.After(top.at) || b.at.Equal(top.at) && b.name > top.name {
			top = b
		}
	}
	return top.name, nil
}

// loadBuffer reads a file, or stdin for "-", into a buffer.
func (s *Shim) loadBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("load-buffer: give one path, or - for the standard input")
	}
	var detail []string
	if p.Has('w') {
		detail = append(detail, "load-buffer -w (copy to the clipboard) ignored")
	}
	var r io.Reader
	if p.Args[0] == "-" {
		if s.Stdin == nil {
			return OutcomeError, detail, errors.New("load-buffer: no standard input to read")
		}
		r = s.Stdin
	} else {
		path := p.Args[0]
		if !filepath.IsAbs(path) && s.Cwd != "" {
			path = filepath.Join(s.Cwd, path)
		}
		f, err := os.Open(path)
		if err != nil {
			return OutcomeError, detail, fmt.Errorf("%s: %w", p.Args[0], errors.Unwrap(err))
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBufferBytes+1))
	if err != nil {
		return OutcomeError, detail, fmt.Errorf("load-buffer: %w", err)
	}
	buf, ok := p.Value('b')
	if !ok {
		if buf, err = s.newBufferName(); err != nil {
			return OutcomeError, detail, err
		}
	}
	if err := s.writeBuffer(buf, string(data)); err != nil {
		return OutcomeError, detail, fmt.Errorf("load-buffer: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

// setBuffer sets a buffer's data, or appends to it with -a.
func (s *Shim) setBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	var detail []string
	if p.Has('w') {
		detail = append(detail, "set-buffer -w (copy to the clipboard) ignored")
	}
	if len(p.Args) != 1 {
		return OutcomeError, detail, errors.New("set-buffer: give the data as one argument")
	}
	data := p.Args[0]
	buf, _ := p.Value('b')
	// set-buffer -a with no -b makes a new buffer, as in tmux.
	newBuf := buf == "" && err == nil
	if newBuf {
		buf, err = s.newBufferName()
	}
	if err != nil {
		return OutcomeError, detail, err
	}
	if p.Has('a') && !newBuf {
		if s.daemonBuffers() {
			// The daemon appends in one step, so nothing set between a
			// read and a write here is lost.
			if err := s.daemonBufferWrite(buf, data, true); err != nil {
				return OutcomeError, detail, fmt.Errorf("set-buffer: %w", err)
			}
			return outcomeFor(detail), detail, nil
		}
		old, _, err := s.readBuffer(buf)
		if err != nil {
			return OutcomeError, detail, err
		}
		data = old + data
	}
	if err := s.writeBuffer(buf, data); err != nil {
		return OutcomeError, detail, fmt.Errorf("set-buffer: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

// pasteText is buffer data as paste-buffer types it: each line feed replaced
// by sep unless raw, then sanitized as every tuios paste is.
func pasteText(data string, raw bool, sep string) string {
	if !raw {
		data = strings.ReplaceAll(data, "\n", sep)
	}
	return vt.SanitizePaste(data)
}

// pasteBuffer types a buffer into a pane as a paste.
func (s *Shim) pasteBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	buf, named := p.Value('b')
	if !named {
		if buf, err = s.topBuffer(); err != nil {
			return OutcomeError, nil, err
		}
		if buf == "" {
			// tmux pastes nothing when there is no buffer and none was named.
			return OutcomeOK, nil, nil
		}
	}
	data, found, err := s.readBuffer(buf)
	if err != nil {
		return OutcomeError, nil, err
	}
	if !found {
		return OutcomeError, nil, fmt.Errorf("no buffer %s", buf)
	}
	v, err := s.loadView()
	if err != nil {
		return OutcomeError, nil, err
	}
	tv, _ := p.Value('t')
	target, err := v.resolvePane(tv, s.callerPane(v))
	if err != nil {
		return OutcomeError, nil, err
	}
	sep, ok := p.Value('s')
	if !ok {
		sep = "\r"
	}
	var detail []string
	if text := pasteText(data, p.Has('r'), sep); text != "" {
		params := map[string]any{"session": target.sess.name, "window": target.ID, "text": text}
		if p.Has('p') {
			params["paste"] = true
		}
		_, err := s.Caller.Call("send-text", params)
		var coded interface{ ErrorCode() string }
		if err != nil && params["paste"] == true && errors.As(err, &coded) && coded.ErrorCode() == "invalid_params" {
			// A daemon from before send-text took paste refuses it. The text
			// is sanitized already, so it goes without the brackets.
			delete(params, "paste")
			detail = append(detail, "paste-buffer -p: the daemon does not bracket pastes. Restart it (tuios kill-server) to run this build")
			_, err = s.Caller.Call("send-text", params)
		}
		if err != nil {
			return OutcomeError, detail, err
		}
	}
	if p.Has('d') {
		// Only the text that was pasted goes: a buffer set again meanwhile
		// stays.
		remove := s.removeBuffer
		if s.daemonBuffers() {
			version := s.readVersion
			remove = func(name string) error { return s.daemonBufferRemove(name, version) }
		}
		if err := remove(buf); err != nil {
			return OutcomeError, detail, err
		}
	}
	return outcomeFor(detail), detail, nil
}

// deleteBuffer deletes the named buffer, or the newest.
func (s *Shim) deleteBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	buf, named := p.Value('b')
	if !named {
		if buf, err = s.topBuffer(); err != nil {
			return OutcomeError, nil, err
		}
		if buf == "" {
			return OutcomeError, nil, errors.New("no buffer")
		}
	}
	if _, found, err := s.readBuffer(buf); err != nil {
		return OutcomeError, nil, err
	} else if !found {
		return OutcomeError, nil, fmt.Errorf("no buffer %s", buf)
	}
	if err := s.removeBuffer(buf); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}
