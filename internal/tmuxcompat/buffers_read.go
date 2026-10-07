package tmuxcompat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// show-buffer, save-buffer and list-buffers: the paste buffers read back.
//
// save-buffer writes a file as the caller, from the caller's own process, so
// it reaches only what the caller could already write. It refuses a path in
// the shim's runtime directory, where the buffers and the pane holders'
// sockets are, so a buffer cannot be written over the shim's own state.

// bufferOrError reads the buffer -b names, or the newest, with tmux's errors.
func (s *Shim) bufferOrError(p Parsed) (string, error) {
	buf, named := p.Value('b')
	if !named {
		top, err := s.topBuffer()
		if err != nil {
			return "", err
		}
		if top == "" {
			return "", errors.New("no buffers")
		}
		buf = top
	}
	data, found, err := s.readBuffer(buf)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no buffer %s", buf)
	}
	return data, nil
}

// showBuffer prints a buffer as it is, with no line feed added, as tmux does.
func (s *Shim) showBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	data, err := s.bufferOrError(p)
	if err != nil {
		return OutcomeError, nil, err
	}
	_, _ = s.Stdout.Write([]byte(data))
	return OutcomeOK, nil, nil
}

// saveBuffer writes a buffer to a file, or to standard output for "-". -a
// appends.
func (s *Shim) saveBuffer(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	if len(p.Args) != 1 {
		return OutcomeError, nil, errors.New("save-buffer: give one path, or - for the standard output")
	}
	data, err := s.bufferOrError(p)
	if err != nil {
		return OutcomeError, nil, err
	}
	path := p.Args[0]
	if path == "-" {
		_, _ = s.Stdout.Write([]byte(data))
		return OutcomeOK, nil, nil
	}
	if !filepath.IsAbs(path) && s.Cwd != "" {
		path = filepath.Join(s.Cwd, path)
	}
	if s.Dir != "" && (InShimDir(path, s.Dir) || canonPath(path) == canonPath(s.Dir)) {
		return OutcomeError, nil, fmt.Errorf("%s: refused, it is in the tmux shim's own directory", p.Args[0])
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if p.Has('a') {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return OutcomeError, nil, fmt.Errorf("%s: %w", p.Args[0], errors.Unwrap(err))
	}
	if _, err := f.WriteString(data); err != nil {
		_ = f.Close()
		return OutcomeError, nil, fmt.Errorf("%s: %w", p.Args[0], err)
	}
	if err := f.Close(); err != nil {
		return OutcomeError, nil, fmt.Errorf("%s: %w", p.Args[0], err)
	}
	return OutcomeOK, nil, nil
}

// listBuffers prints the buffers, newest first. -f keeps those whose filter
// format expands true.
func (s *Shim) listBuffers(name string, args []string) (string, []string, error) {
	p, err := parseFlags(name, specs[name], args)
	if err != nil {
		return OutcomeUnsupported, nil, err
	}
	list, err := s.buffers()
	if err != nil {
		return OutcomeError, nil, err
	}
	slices.SortFunc(list, func(a, b buffer) int {
		if c := b.at.Compare(a.at); c != 0 {
			return c
		}
		return strings.Compare(b.name, a.name)
	})
	format := cmpOr(valueOr(p, 'F'), `#{buffer_name}: #{buffer_size} bytes: "#{buffer_sample}"`)
	filter := valueOr(p, 'f')
	var detail []string
	for _, b := range list {
		size, sample := b.size, b.sample
		if !b.sampled {
			data, found, err := s.readBuffer(b.name)
			if err != nil || !found {
				continue
			}
			size, sample = len(data), bufferSample(data)
		}
		vars := s.sessionVars(nil)
		vars["buffer_name"] = b.name
		vars["buffer_size"] = strconv.Itoa(size)
		vars["buffer_sample"] = sample
		vars["buffer_created"] = strconv.FormatInt(b.at.Unix(), 10)
		if filter != "" {
			ok, d := s.expand(filter, vars)
			detail = mergeDetail(detail, d)
			if !formatTrue(ok) {
				continue
			}
		}
		out, d := s.expand(format, vars)
		detail = mergeDetail(detail, d)
		s.println(out)
	}
	return outcomeFor(detail), detail, nil
}

// bufferSample is tmux's paste_make_sample: the start of the buffer with
// backslashes, tabs, line feeds and other unprintable bytes written as C
// escapes, cut at 200 bytes with "...".
func bufferSample(data string) string {
	const width = 200
	cut := len(data) > width
	if cut {
		data = data[:width]
	}
	var b strings.Builder
	for i := 0; i < len(data); i++ {
		switch c := data[i]; {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < ' ' || c == 0x7f:
			fmt.Fprintf(&b, `\%03o`, c)
		default:
			b.WriteByte(c)
		}
	}
	if cut {
		b.WriteString("...")
	}
	return b.String()
}
