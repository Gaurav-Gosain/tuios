// Package transcriptview decodes an agent's transcript into a conversation a
// person can read: prompts, answers, thinking, tool calls with their results,
// diffs of edits, plans and todo lists.
//
// It is the opposite of internal/transcript, and it is kept apart from it on
// purpose. That package reads a transcript to learn one of three turn states
// and is built so it cannot see anything else. This one reads the content,
// because showing the content to the person is its whole job. So the care
// moves from "decode nothing" to "decode only for the person, and only on
// request":
//
//   - It is reachable from one place, the agent-transcript verb, which runs
//     only for a caller that holds the person's live human_nonce. Nothing
//     reads a transcript here in the background, and nothing here keeps what
//     it read: every call opens the file, decodes, and returns. CursorAt, for
//     the daemon's transcript event, hashes the start of the first record to
//     name the file and returns nothing of it.
//   - Every string that leaves goes through the caller's Clean function, which
//     strips control characters and masks likely secrets, and is cut to a
//     bound. A whole page has a byte bound too.
//   - Nothing here logs. A line that fails to decode is skipped and counted.
//     The read buffers are zeroed before Read returns.
//   - The path comes from the daemon's join table, never from the caller, so a
//     caller cannot point this at another file.
package transcriptview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Bounds on what one page carries.
const (
	// DefaultLimit is how many entries a call without a limit gets.
	DefaultLimit = 200
	// MaxLimit is the most entries one call returns.
	MaxLimit = 1000
	// TextMax bounds the text, thinking and plan of one entry, in bytes.
	TextMax = 8 << 10
	// ResultLines is how many lines of a tool result are kept.
	ResultLines = 20
	// DiffLinesMax bounds the diff lines of one entry.
	DiffLinesMax = 400
	// diffLineMax bounds one diff line, in bytes. A minified file is one line.
	diffLineMax = 1 << 10
	// targetMax bounds a target, in bytes.
	targetMax = 512
	// toolMax bounds a tool name, in bytes.
	toolMax = 64
	// todosMax bounds the items of one todo list.
	todosMax = 100
	// DefaultMaxBytes is the bound on one page's entries as JSON.
	DefaultMaxBytes = 2 << 20
	// pageOverhead is held back from MaxBytes for the reply around the
	// entries.
	pageOverhead = 4 << 10
	// scanMax bounds how much of the file one call reads. A newest-entries
	// read grows its window from the end up to this, and a read after a
	// cursor stops here and reports more.
	scanMax = 32 << 20
	// scanFirst is the first window of a newest-entries read.
	scanFirst = 1 << 20
	// lineMax bounds one record. A longer line is skipped.
	lineMax = 16 << 20
	// maxLineDiffOps bounds the longest common subsequence table of one edit.
	// Two texts larger than that are shown as all removed and all added.
	maxLineDiffOps = 1 << 22
	// headMax is how many bytes of the first record go into the file's
	// identity.
	headMax = 256
)

// Entry roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Entry kinds.
const (
	KindText       = "text"
	KindThinking   = "thinking"
	KindToolCall   = "tool_call"
	KindToolResult = "tool_result"
	KindPlan       = "plan"
	KindTodos      = "todos"
)

// Tool call statuses.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusRunning = "running"
)

// Entry is one item of the conversation.
type Entry struct {
	// ID is stable: the same record gives the same id on every read.
	ID   string `json:"id"`
	At   int64  `json:"at,omitzero"`
	Role string `json:"role"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	// Truncated says text, plan or the result was cut.
	Truncated bool   `json:"truncated,omitzero"`
	Tool      string `json:"tool,omitempty"`
	Target    string `json:"target,omitempty"`
	Status    string `json:"status,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	Diff      *Diff  `json:"diff,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Todos     []Todo `json:"todos,omitempty"`

	// line is the byte offset of the record the entry came from, and sub its
	// place among that record's entries. They place a page's cursor and are
	// never sent.
	line int64
	sub  int
}

// Diff is the change an edit made to one file.
type Diff struct {
	File    string `json:"file"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Hunks   []Hunk `json:"hunks"`
	// Truncated says lines past DiffLinesMax were left out. Added and
	// Removed still count every line.
	Truncated bool `json:"truncated,omitzero"`
}

// Hunk is one run of changed lines with their context.
type Hunk struct {
	OldStart int        `json:"old_start"`
	NewStart int        `json:"new_start"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is one line of a hunk. Op is " ", "+" or "-".
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Todo is one item of a todo list.
type Todo struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Options says what to read.
type Options struct {
	// After is the cursor of an earlier page. Empty reads the newest Limit
	// entries.
	After string
	// Limit is the most entries to return, DefaultLimit when 0.
	Limit int
	// MaxBytes bounds the page's entries as JSON, DefaultMaxBytes when 0.
	MaxBytes int
	// Clean makes a string safe to show. It is applied to every string that
	// leaves this package, before it is cut to its bound. Nil leaves strings
	// as they are, which only a test should do.
	Clean func(string) string
}

// Page is one read.
type Page struct {
	Entries []Entry
	// Cursor is where the next read with After starts.
	Cursor string
	// Reset says After was not a cursor into this file as it is now, so the
	// page is a fresh read of the newest entries.
	Reset bool
	// More says the read after a cursor stopped before the end of the file.
	More bool
	// Skipped counts lines that did not decode.
	Skipped int
}

// ErrNoFile reports the transcript is gone.
var ErrNoFile = errors.New("transcriptview: file does not exist")

// Read reads one page of the transcript at path, as Claude Code writes it.
func Read(path string, opts Options) (Page, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	opts.Limit = min(opts.Limit, MaxLimit)
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	f, err := os.Open(path) //nolint:gosec // the path is the daemon's join, never the caller's
	if err != nil {
		if os.IsNotExist(err) {
			return Page{}, ErrNoFile
		}
		return Page{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Page{}, err
	}
	size := info.Size()
	id, err := fileID(f, path, size)
	if err != nil {
		return Page{}, err
	}
	r := &reader{f: f, size: size, opts: opts}
	defer r.zero()

	if opts.After != "" {
		if at, ok := parseCursor(opts.After, id); ok && at.off <= size && r.atBoundary(at.off) {
			return r.forward(at, id)
		}
		page, err := r.newest(id)
		page.Reset = true
		return page, err
	}
	return r.newest(id)
}

// reader is one call's read. Its buffers are zeroed when the call ends.
type reader struct {
	f       *os.File
	size    int64
	opts    Options
	bufs    [][]byte
	skipped int
}

func (r *reader) zero() {
	for _, b := range r.bufs {
		clear(b)
	}
}

// readAt reads [from, to) of the file into a buffer zeroed when the call ends.
func (r *reader) readAt(from, to int64) ([]byte, error) {
	buf := make([]byte, to-from)
	r.bufs = append(r.bufs, buf)
	n, err := r.f.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

// atBoundary reports whether off is where a record starts.
func (r *reader) atBoundary(off int64) bool {
	if off == 0 {
		return true
	}
	var b [1]byte
	if _, err := r.f.ReadAt(b[:], off-1); err != nil {
		return false
	}
	return b[0] == '\n'
}

// newest reads the newest Limit entries, from a window at the end of the file
// that grows until it holds them or reaches scanMax.
func (r *reader) newest(id string) (Page, error) {
	window := int64(scanFirst)
	for {
		start := max(r.size-window, 0)
		buf, err := r.readAt(start, r.size)
		if err != nil {
			return Page{}, err
		}
		end := bytes.LastIndexByte(buf, '\n') + 1
		lines := buf[:end]
		base := start
		if start > 0 {
			// The window begins inside a record. Skip to the next one.
			i := bytes.IndexByte(lines, '\n')
			if i < 0 {
				lines = nil
			} else {
				lines = lines[i+1:]
				base += int64(i + 1)
			}
		}
		r.skipped = 0
		entries := r.decode(lines, base)
		if len(entries) >= r.opts.Limit || start == 0 || window >= scanMax {
			entries = foldResults(entries)
			// Keep the newest whole entries that fit both bounds.
			keep := len(entries) - min(len(entries), r.opts.Limit)
			total := 0
			for i := len(entries) - 1; i >= keep; i-- {
				n := entrySize(entries[i])
				if total+n > r.opts.MaxBytes-pageOverhead {
					keep = i + 1
					break
				}
				total += n
			}
			return Page{
				Entries: entries[keep:],
				Cursor:  makeCursor(id, cursorPos{off: start + int64(end)}),
				Skipped: r.skipped,
			}, nil
		}
		// Not enough yet: drop this window's buffer and read a larger one.
		clear(buf)
		window *= 4
	}
}

// forward reads the entries after a cursor, at most Limit and MaxBytes of
// them. The cursor is a record's offset and how many of that record's entries
// were already returned, so a page can end inside a record that gives several
// entries and the next page goes on from there.
func (r *reader) forward(at cursorPos, id string) (Page, error) {
	to := min(r.size, at.off+scanMax)
	buf, err := r.readAt(at.off, to)
	if err != nil {
		return Page{}, err
	}
	end := bytes.LastIndexByte(buf, '\n') + 1
	if end == 0 && to < r.size {
		// One record longer than the scan bound. Step over it.
		if next := r.skipLongLine(to); next > 0 {
			return Page{Cursor: makeCursor(id, cursorPos{off: next}), More: true, Skipped: 1}, nil
		}
	}
	more := to < r.size
	entries := r.decode(buf[:end], at.off)
	// Drop what the last page already returned of the first record.
	skip := 0
	for skip < len(entries) && entries[skip].line == at.off && entries[skip].sub < at.sub {
		skip++
	}
	entries = entries[skip:]
	next := cursorPos{off: at.off + int64(end)}

	total := 0
	for i, e := range entries {
		n := entrySize(e)
		if i > 0 && (i >= r.opts.Limit || total+n > r.opts.MaxBytes-pageOverhead) {
			// The page ends before this entry, which the next one starts at.
			next = cursorPos{off: e.line, sub: e.sub}
			entries = entries[:i]
			more = true
			break
		}
		total += n
	}
	return Page{
		Entries: foldResults(entries),
		Cursor:  makeCursor(id, next),
		More:    more,
		Skipped: r.skipped,
	}, nil
}

// skipLongLine finds the end of a record that starts before from and runs
// past the scan bound, and returns the offset after it, or 0 when the file
// ends first.
func (r *reader) skipLongLine(from int64) int64 {
	chunk := make([]byte, 64<<10)
	defer clear(chunk)
	for off := from; off < r.size; {
		n, err := r.f.ReadAt(chunk, off)
		if i := bytes.IndexByte(chunk[:n], '\n'); i >= 0 {
			return off + int64(i) + 1
		}
		if err != nil {
			return 0
		}
		off += int64(n)
	}
	return 0
}

// decode turns complete lines starting at file offset base into entries.
func (r *reader) decode(lines []byte, base int64) []Entry {
	var out []Entry
	off := base
	for len(lines) > 0 {
		line := lines
		if i := bytes.IndexByte(lines, '\n'); i >= 0 {
			line, lines = lines[:i], lines[i+1:]
		} else {
			lines = nil
		}
		start := off
		off += int64(len(line)) + 1
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(line) > lineMax {
			r.skipped++
			continue
		}
		var rec ccRecord
		if err := unmarshalRecord(line, &rec); err != nil {
			r.skipped++
			continue
		}
		for i, e := range r.recordEntries(&rec, start) {
			e.line, e.sub = start, i
			out = append(out, e)
		}
	}
	return out
}

// entrySize is an entry's size as JSON.
func entrySize(e Entry) int {
	b, err := json.Marshal(e)
	if err != nil {
		return 0
	}
	return len(b) + 1
}

// foldResults gives each tool call the status of its result on the page, or
// running when the page has none. A result's file diff, which carries the
// file's real line numbers, replaces the hunks worked out from the call.
func foldResults(entries []Entry) []Entry {
	results := map[string]int{}
	for i, e := range entries {
		if e.Kind == KindToolResult && e.ToolID != "" {
			results[e.ToolID] = i
		}
	}
	for i := range entries {
		e := &entries[i]
		if e.Kind == KindToolResult || e.ToolID == "" || e.Role != RoleAssistant {
			continue
		}
		j, ok := results[e.ToolID]
		if !ok {
			e.Status = StatusRunning
			continue
		}
		res := entries[j]
		e.Status = res.Status
		if e.Diff != nil && res.Diff != nil {
			file := e.Diff.File
			d := *res.Diff
			d.File = file
			e.Diff = &d
		}
	}
	return entries
}

// --- Cursors.

// fileID names the file as it is now: its path and the start of its first
// record. A file replaced at the same path has another first record. A first
// record not yet finished gives the empty head, and the cursor it makes is at
// offset 0, so the change when it finishes costs nothing.
func fileID(f *os.File, path string, size int64) (string, error) {
	head := make([]byte, min(size, headMax))
	defer clear(head)
	n, err := f.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	head = head[:n]
	switch i := bytes.IndexByte(head, '\n'); {
	case i >= 0:
		head = head[:i]
	case size <= headMax:
		// The first record is not finished, so it is not a record yet.
		head = nil
	}
	// Otherwise the first record is longer than headMax, and its first
	// headMax bytes name it.
	h := sha256.New()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(head)
	return hex.EncodeToString(h.Sum(nil)[:8]), nil
}

// CursorAt is the cursor of offset off in the file at path, the one a read
// that ends there returns. The daemon puts it in its transcript event. It
// reads the start of the first record to name the file, and returns nothing
// of it.
func CursorAt(path string, off int64) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the daemon's join, never the caller's
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	id, err := fileID(f, path, info.Size())
	if err != nil {
		return "", err
	}
	return makeCursor(id, cursorPos{off: off}), nil
}

// cursorPos is where a page ends: the offset of a record, and how many of
// its entries were returned already.
type cursorPos struct {
	off int64
	sub int
}

// makeCursor encodes a position in the file id names.
func makeCursor(id string, at cursorPos) string {
	c := "t1." + id + "." + strconv.FormatInt(at.off, 36)
	if at.sub > 0 {
		c += "." + strconv.Itoa(at.sub)
	}
	return c
}

// parseCursor returns the position of a cursor made for the file id names.
func parseCursor(c, id string) (cursorPos, bool) {
	parts := strings.Split(c, ".")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "t1" || parts[1] != id {
		return cursorPos{}, false
	}
	off, err := strconv.ParseInt(parts[2], 36, 64)
	if err != nil || off < 0 {
		return cursorPos{}, false
	}
	at := cursorPos{off: off}
	if len(parts) == 4 {
		sub, err := strconv.Atoi(parts[3])
		if err != nil || sub <= 0 {
			return cursorPos{}, false
		}
		at.sub = sub
	}
	return at, true
}

// --- Strings.

// clean applies the caller's Clean and cuts to limit bytes, reporting whether
// it cut.
func (r *reader) clean(s string, limit int) (string, bool) {
	if r.opts.Clean != nil {
		s = r.opts.Clean(s)
	}
	return cut(s, limit)
}

// cut cuts s to limit bytes on a rune boundary.
func cut(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	i := limit
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], true
}

// oneLine is the first non-empty line of s.
func oneLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// headLines keeps the first n lines of s, reporting whether it dropped any.
func headLines(s string, n int) (string, bool) {
	s = strings.TrimRight(s, "\n")
	idx := 0
	for range n {
		i := strings.IndexByte(s[idx:], '\n')
		if i < 0 {
			return s, false
		}
		idx += i + 1
	}
	return s[:idx-1], true
}
