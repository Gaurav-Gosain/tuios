package preview

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

// A text too large to hold whole reads through a line index: the offset of
// every 256th line, found in one pass on a goroutine, so a 1 GB log holds
// about 30 KB of index for 4 million lines. A block of lines is read when
// the view shows it, from the nearest mark, and a few blocks are kept.

// markEvery is how many lines one mark of the index covers.
const markEvery = 256

// lineCut is the most of a line kept for the view.
const lineCut = 4096

// blocksKept bounds the blocks of lines kept, each markEvery lines.
const blocksKept = 48

// TextFile is a large text read a block of lines at a time.
type TextFile struct {
	path string
	size int64
	f    *os.File

	mu     sync.Mutex
	marks  []int64 // marks[k] is where line k*markEvery starts
	lines  int64   // the lines counted so far
	done   bool    // the whole file is counted
	blocks map[int]*textBlock
	order  []int // blocks by last use, oldest first
	asked  map[int]bool
}

type textBlock struct {
	lines []string
	cut   bool // a line in it was longer than lineCut
}

// OpenText opens a large text for its line index.
func OpenText(path string) (*TextFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &TextFile{path: path, size: fi.Size(), f: f, marks: []int64{0}, blocks: map[int]*textBlock{}, asked: map[int]bool{}}, nil
}

// Close lets the file go.
func (t *TextFile) Close() { _ = t.f.Close() }

// Size is the file's size in bytes.
func (t *TextFile) Size() int64 { return t.size }

// Count is the lines counted so far, and whether that is all of them.
func (t *TextFile) Count() (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lines, t.done
}

// Index counts the file's lines, marking every markEvery-th, and calls
// progress now and then as the count grows, and once at the end.
func (t *TextFile) Index(ctx context.Context, progress func()) error {
	buf := make([]byte, 1<<20)
	var off, lines int64
	last := time.Now()
	var marks []int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := t.f.ReadAt(buf, off)
		chunk := buf[:n]
		for len(chunk) > 0 {
			i := bytes.IndexByte(chunk, '\n')
			if i < 0 {
				break
			}
			lines++
			chunk = chunk[i+1:]
			if lines%markEvery == 0 {
				marks = append(marks, off+int64(n-len(chunk)))
			}
		}
		off += int64(n)
		if n == 0 || err != nil {
			break
		}
		if time.Since(last) > 60*time.Millisecond {
			last = time.Now()
			t.mu.Lock()
			t.marks = append(t.marks, marks...)
			t.lines = lines
			t.mu.Unlock()
			marks = marks[:0]
			if progress != nil {
				progress()
			}
		}
	}
	// A last line with no newline is a line too.
	if t.size > 0 {
		var end [1]byte
		if _, err := t.f.ReadAt(end[:], t.size-1); err == nil && end[0] != '\n' {
			lines++
		}
	}
	t.mu.Lock()
	t.marks = append(t.marks, marks...)
	t.lines, t.done = lines, true
	t.mu.Unlock()
	if progress != nil {
		progress()
	}
	return nil
}

// Line is line i (from 0), and whether its block is read. A block not read
// yet is read on a goroutine, which calls ready when it is.
func (t *TextFile) Line(i int64, ready func()) (string, bool) {
	k := int(i / markEvery)
	t.mu.Lock()
	b := t.blocks[k]
	if b != nil {
		t.touch(k)
		t.mu.Unlock()
		j := int(i % markEvery)
		if j < len(b.lines) {
			return b.lines[j], true
		}
		return "", true
	}
	if k >= len(t.marks) || t.asked[k] {
		t.mu.Unlock()
		return "", false
	}
	t.asked[k] = true
	from := t.marks[k]
	to := t.size
	if k+1 < len(t.marks) {
		to = t.marks[k+1]
	}
	t.mu.Unlock()
	go func() {
		b := t.readBlock(from, to)
		t.mu.Lock()
		t.blocks[k] = b
		delete(t.asked, k)
		t.touch(k)
		for len(t.order) > blocksKept {
			delete(t.blocks, t.order[0])
			t.order = t.order[1:]
		}
		t.mu.Unlock()
		if ready != nil {
			ready()
		}
	}()
	return "", false
}

// touch marks block k the last used. The lock is held.
func (t *TextFile) touch(k int) {
	for i, o := range t.order {
		if o == k {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
	t.order = append(t.order, k)
}

// readBlock reads the lines between two offsets, at most markEvery. A
// block at the end of a file not counted yet holds what lines there are.
func (t *TextFile) readBlock(from, to int64) *textBlock {
	// A block holds markEvery lines; one past the index read so far ends
	// where its lines do, at most 4 MiB on.
	limit := min(to-from, 4<<20)
	buf := make([]byte, limit)
	n, _ := t.f.ReadAt(buf, from)
	buf = buf[:n]
	b := &textBlock{}
	for len(buf) > 0 && len(b.lines) < markEvery {
		i := bytes.IndexByte(buf, '\n')
		line := buf
		if i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			buf = nil
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) > lineCut {
			line, b.cut = line[:lineCut], true
		}
		b.lines = append(b.lines, strings.ToValidUTF8(strings.ReplaceAll(string(line), "\t", "    "), "\uFFFD"))
	}
	return b
}

// lineOffset is where line i starts: from its mark, reading forward.
func (t *TextFile) lineOffset(i int64) (int64, bool) {
	t.mu.Lock()
	k := int(i / markEvery)
	if k >= len(t.marks) {
		t.mu.Unlock()
		return 0, false
	}
	off := t.marks[k]
	t.mu.Unlock()
	skip := i % markEvery
	buf := make([]byte, 64<<10)
	for skip > 0 {
		n, _ := t.f.ReadAt(buf, off)
		if n == 0 {
			break // past the end: the last line's start is as far as it goes
		}
		chunk := buf[:n]
		used := 0
		for skip > 0 {
			j := bytes.IndexByte(chunk[used:], '\n')
			if j < 0 {
				break
			}
			used += j + 1
			skip--
		}
		if skip > 0 {
			off += int64(n)
			continue
		}
		off += int64(used)
	}
	return off, true
}

// ErrNotFound is a search that reached the end of the file.
var ErrNotFound = errors.New("not found")

// Find looks for needle from the start of line from, forward or back, and
// returns the line it is on. Lower case ignores case, as the TUI's search
// does; a capital in it does not. The index must be done.
func (t *TextFile) Find(ctx context.Context, needle string, from int64, back bool) (int64, error) {
	if needle == "" {
		return 0, ErrNotFound
	}
	if _, done := t.Count(); !done {
		return 0, ErrCounting
	}
	fold := !strings.ContainsFunc(needle, unicode.IsUpper)
	nb := []byte(needle)
	if back {
		return t.findBack(ctx, nb, fold, from)
	}
	start, ok := t.lineOffset(from)
	if !ok {
		return 0, ErrNotFound
	}
	const chunk = 4 << 20
	buf := make([]byte, chunk)
	line := from
	off := start
	for off < t.size {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		n, _ := t.f.ReadAt(buf, off)
		if n == 0 {
			break
		}
		data := buf[:n]
		// Whole lines only, so a match and its line count agree.
		if off+int64(n) < t.size {
			if j := bytes.LastIndexByte(data, '\n'); j >= 0 {
				data = data[:j+1]
			}
		}
		hay := data
		if fold {
			hay = bytes.ToLower(data)
		}
		if j := bytes.Index(hay, nb); j >= 0 {
			return line + int64(bytes.Count(data[:j], []byte{'\n'})), nil
		}
		line += int64(bytes.Count(data, []byte{'\n'}))
		off += int64(len(data))
	}
	return 0, ErrNotFound
}

// findBack looks for the last match before line from, a block at a time
// back from its mark.
func (t *TextFile) findBack(ctx context.Context, nb []byte, fold bool, from int64) (int64, error) {
	k := int(from / markEvery)
	for ; k >= 0; k-- {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		t.mu.Lock()
		if k >= len(t.marks) {
			k = len(t.marks) - 1
		}
		lo := t.marks[k]
		hi := t.size
		if k+1 < len(t.marks) {
			hi = t.marks[k+1]
		}
		t.mu.Unlock()
		b := t.readBlock(lo, hi)
		for j := len(b.lines) - 1; j >= 0; j-- {
			ln := int64(k)*markEvery + int64(j)
			if ln >= from {
				continue
			}
			hay := b.lines[j]
			if fold {
				hay = strings.ToLower(hay)
			}
			if strings.Contains(hay, string(nb)) {
				return ln, nil
			}
		}
	}
	return 0, ErrNotFound
}
