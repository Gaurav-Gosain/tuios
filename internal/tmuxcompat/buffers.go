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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/vt"
)

const maxBufferBytes = 16 << 20

type buffer struct {
	name    string
	data    string
	at      time.Time
	size    int
	sample  string
	sampled bool
	auto    bool
}

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

func (s *Shim) bufferDir() string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, "buffers")
}

func bufferFile(dir, name string) string {
	return filepath.Join(dir, hex.EncodeToString([]byte(name)))
}

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

func withoutBuffer(list []buffer, name string) []buffer {
	out := list[:0]
	for _, b := range list {
		if b.name != name {
			out = append(out, b)
		}
	}
	return out
}

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

func (s *Shim) writeBuffer(name, data string) error {
	if data == "" {
		return nil
	}
	if len(data) > maxBufferBytes {
		return errfmt.Errorf("buffer is too large: %d bytes, the limit is %d", len(data), maxBufferBytes)
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

func newestAfter(list []buffer) time.Time {
	stamp := time.Now().Round(0)
	for _, b := range list {
		if !stamp.After(b.at) {
			stamp = b.at.Add(time.Microsecond)
		}
	}
	return stamp
}

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

func (s *Shim) newBufferName() (string, error) {
	if s.daemonBuffers() {
		return "", nil
	}
	list, err := s.buffers()
	if err != nil {
		return "", err
	}
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

const bufferCountFile = "next"

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
			return OutcomeError, detail, errfmt.Errorf("%s: %w", p.Args[0], errors.Unwrap(err))
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBufferBytes+1))
	if err != nil {
		return OutcomeError, detail, errfmt.Errorf("load-buffer: %w", err)
	}
	buf, ok := p.Value('b')
	if !ok {
		if buf, err = s.newBufferName(); err != nil {
			return OutcomeError, detail, err
		}
	}
	if err := s.writeBuffer(buf, string(data)); err != nil {
		return OutcomeError, detail, errfmt.Errorf("load-buffer: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

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
	newBuf := buf == "" && err == nil
	if newBuf {
		buf, err = s.newBufferName()
	}
	if err != nil {
		return OutcomeError, detail, err
	}
	if p.Has('a') && !newBuf {
		if s.daemonBuffers() {
			if err := s.daemonBufferWrite(buf, data, true); err != nil {
				return OutcomeError, detail, errfmt.Errorf("set-buffer: %w", err)
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
		return OutcomeError, detail, errfmt.Errorf("set-buffer: %w", err)
	}
	return outcomeFor(detail), detail, nil
}

func pasteText(data string, raw bool, sep string) string {
	if !raw {
		data = strings.ReplaceAll(data, "\n", sep)
	}
	return vt.SanitizePaste(data)
}

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
			return OutcomeOK, nil, nil
		}
	}
	data, found, err := s.readBuffer(buf)
	if err != nil {
		return OutcomeError, nil, err
	}
	if !found {
		return OutcomeError, nil, errfmt.Errorf("no buffer %s", buf)
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
			delete(params, "paste")
			detail = append(detail, "paste-buffer -p: the daemon does not bracket pastes. Restart it (tuios kill-server) to run this build")
			_, err = s.Caller.Call("send-text", params)
		}
		if err != nil {
			return OutcomeError, detail, err
		}
	}
	if p.Has('d') {
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
		return OutcomeError, nil, errfmt.Errorf("no buffer %s", buf)
	}
	if err := s.removeBuffer(buf); err != nil {
		return OutcomeError, nil, err
	}
	return OutcomeOK, nil, nil
}
