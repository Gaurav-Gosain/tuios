package mermaid

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// TermSafe is s with every character a terminal would act on written as
// U+FFFD: C0 controls other than tab and newline, DEL, C1 controls, the
// bidirectional overrides, isolates and marks, and the Unicode line and
// paragraph separators. Invalid UTF-8 becomes U+FFFD too.
// Every string that comes from a diagram file, or from an error that quotes
// one, goes through it before it reaches a terminal: a label such as
// "#27;]2;x#7;" decodes to OSC 2, and a source may hold a raw ESC.
func TermSafe(s string) string {
	clean := true
	for _, r := range s {
		if unsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unsafeRune(r) {
			r = '\uFFFD'
		}
		b.WriteRune(r)
	}
	return b.String()
}

func unsafeRune(r rune) bool {
	switch {
	case r == '\n', r == '\t':
		return false
	case r < 0x20, r >= 0x7f && r <= 0x9f, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == '\uFFFD',
		r == 0x200e, r == 0x200f, r == 0x061c, r == 0x2028, r == 0x2029:
		return true
	}
	return false
}

// MaxSource is the most bytes ReadSource reads: 4 MiB, far above the 50,000
// characters sketch draws, and small enough that a stray large file cannot
// fill memory.
const MaxSource = 4 << 20

// ErrNotRegular is returned for a name that is not a regular file, such as a
// FIFO or a device, which a read could wait on forever.
var ErrNotRegular = errors.New("not a regular file")

// ReadSource reads a diagram file, or standard input for "-". It reads only
// regular files, after symlinks, and at most MaxSource bytes.
func ReadSource(name string) ([]byte, error) {
	var r io.Reader
	if name == "-" {
		r = os.Stdin
	} else {
		info, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, ErrNotRegular
		}
		if info.Size() > MaxSource {
			return nil, errfmt.Errorf("the file is larger than %s", units.Bytes(int64(MaxSource)))
		}
		f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
			return nil, ErrNotRegular
		}
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxSource+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSource {
		return nil, errfmt.Errorf("the input is larger than %s", units.Bytes(int64(MaxSource)))
	}
	return data, nil
}
