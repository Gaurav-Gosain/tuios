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

	"github.com/Gaurav-Gosain/tuios/internal/transcript"
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
	// LCSCellsPerCall bounds the longest common subsequence work of one call:
	// the cells of every table the call's diffs fill, together. A cell is a
	// uint16, so the largest table is 8 MiB, and it is freed when its diff
	// is done. A diff whose table would pass what is left is shown as one
	// replace of the changed lines, all removed and then all added, and says
	// so with WholeReplace. The page's newest diffs are worked out first
	// (oldest first for an after read), so they are the ones matched.
	LCSCellsPerCall = 1 << 22
	// rawTextMax bounds the text of one entry as decoded, before Clean. It
	// is more than TextMax because Clean removes escapes and so shortens.
	rawTextMax = 4 * TextMax
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

	// raw is what the record said, before Clean and before any diff is
	// worked out. finish turns it into the fields above, only for an entry
	// that a page is about to carry.
	raw *rawEntry
}

// rawEntry is an entry as decoded. Its strings are cut to a bound but not
// cleaned.
type rawEntry struct {
	text    string
	textCut bool
	tool    string
	target  string
	plan    string
	todos   []Todo
	diff    *diffSrc
}

// diffSrc is what a diff is worked out from: an edit's old and new texts, or
// a result's structuredPatch. built holds the diff once it is.
type diffSrc struct {
	file  string
	pairs [][2]string
	patch []ccPatch
	// shared is the result's source, for a call that takes the diff of a
	// result about another file name. The call shows it under its own name.
	shared *diffSrc
	built  *Diff
}

// pos is the place of an entry, as a cursor names it.
func (e *Entry) pos() cursorPos { return cursorPos{off: e.line, sub: e.sub} }

// before reports whether the entry comes before position at.
func (e *Entry) before(at cursorPos) bool {
	return e.line < at.off || e.line == at.off && e.sub < at.sub
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

	// Plain says the reply reached its budget for colours before this
	// diff, so its lines have words but no spans.
	Plain bool `json:"plain,omitzero"`

	// WholeReplace says the lines were not matched: the call reached
	// LCSCellsPerCall, so each hunk shows every old line removed and then
	// every new line added. Added and Removed then count the changed lines
	// of each side.
	WholeReplace bool `json:"whole_replace,omitzero"`

	// styled says style has added the spans and words.
	styled bool
}

// Hunk is one run of changed lines with their context.
type Hunk struct {
	OldStart int        `json:"old_start"`
	NewStart int        `json:"new_start"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is one line of a hunk. Op is " ", "+" or "-". Spans colour its
// code and Words mark the part of a changed line that changed, both in
// UTF-16 code units of Text (style.go).
type DiffLine struct {
	Op    string  `json:"op"`
	Text  string  `json:"text"`
	Spans []Span  `json:"spans,omitempty"`
	Words []Range `json:"words,omitempty"`
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
	// Before is a cursor, and the page is the newest Limit entries before
	// it. Older of an earlier page is one. After and Before are not given
	// together.
	Before string
	// Limit is the most entries to return, DefaultLimit when 0.
	Limit int
	// MaxBytes bounds the page's entries as JSON, DefaultMaxBytes when 0.
	MaxBytes int
	// Clean makes a string safe to show. It is applied to every string that
	// leaves this package, before it is cut to its bound. Nil leaves strings
	// as they are, which only a test should do.
	Clean func(string) string
	// CleanLines masks, in place, what spans lines of a diff hunk once
	// Clean has run on each line, such as a private key. Nil does nothing.
	CleanLines func([]string) bool
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
	// Older is the cursor to read the page before this one with Before. It
	// is empty when the page starts at the first entry of the file.
	Older string
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
	f, err := transcript.OpenRegular(path)
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
	r := &reader{f: f, size: size, opts: opts, lcsLeft: LCSCellsPerCall}
	defer r.zero()

	if opts.After != "" {
		if at, ok := parseCursor(opts.After, id); ok && at.off <= size && r.atBoundary(at.off) {
			return r.forward(at, id)
		}
		page, err := r.newest(id)
		page.Reset = true
		return page, err
	}
	if opts.Before != "" {
		if at, ok := parseCursor(opts.Before, id); ok && at.off <= size && r.atBoundary(at.off) {
			return r.back(at, id, false)
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
	// lexed counts the bytes of code the lexer read for this call, against
	// spanBudget.
	lexed int
	// lcsLeft is what is left of LCSCellsPerCall.
	lcsLeft int
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

// newest reads the newest Limit entries.
func (r *reader) newest(id string) (Page, error) {
	return r.back(cursorPos{off: r.size}, id, true)
}

// back reads the newest Limit entries before position to, from a window that
// ends there and grows back until it holds them or reaches scanMax. The
// window is all one call reads, so a page far back in a large file costs
// what a page at its end costs. When atEnd, to is the end of the file, which
// may end inside a record that is not finished, and the page's cursor is
// the end of the last whole record. Otherwise to is a cursor, and the
// page's cursor is to itself.
func (r *reader) back(to cursorPos, id string, atEnd bool) (Page, error) {
	stop := to.off
	if to.sub > 0 {
		// The record at to gave some of its entries to a newer page, and the
		// ones before them belong to this one.
		if next := r.skipLongLine(to.off); next > 0 {
			stop = next
		} else {
			to.sub = 0
		}
	}
	window := int64(scanFirst)
	for {
		start := max(stop-window, 0)
		buf, err := r.readAt(start, stop)
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
				base = start + int64(end)
			} else {
				lines = lines[i+1:]
				base += int64(i + 1)
			}
		}
		r.skipped = 0
		var before *cursorPos
		if !atEnd {
			before = &to
		}
		entries := r.decode(lines, base, decodeBounds{before: before, keepLast: r.opts.Limit + 1})
		if len(entries) >= r.opts.Limit || start == 0 || window >= scanMax {
			// Only the newest Limit can be on the page. Their calls fold in
			// their results, and then each is finished, newest first, until
			// the page is full. An entry the page does not carry is never
			// cleaned and its diff never worked out.
			keep := len(entries) - min(len(entries), r.opts.Limit)
			foldResults(entries[keep:])
			total := 0
			for i := len(entries) - 1; i >= keep; i-- {
				n := r.entrySize(&entries[i])
				if total+n > r.opts.MaxBytes-pageOverhead {
					keep = i + 1
					break
				}
				total += n
			}
			page := Page{
				Entries: entries[keep:],
				Skipped: r.skipped,
			}
			if atEnd {
				page.Cursor = makeCursor(id, cursorPos{off: start + int64(end)})
			} else {
				page.Cursor = makeCursor(id, to)
			}
			switch {
			case keep < len(entries):
				if base > 0 || keep > 0 {
					page.Older = makeCursor(id, entries[keep].pos())
				}
			case base > 0:
				// The window held no entry. The next page starts where it did.
				page.Older = makeCursor(id, cursorPos{off: base})
			}
			return page, nil
		}
		// Not enough yet: drop this window's buffer and read a larger one.
		clear(buf)
		window = min(window*4, scanMax)
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
	// Decoding stops once it has one entry past the page, which is where
	// the next page starts.
	entries := r.decode(buf[:end], at.off, decodeBounds{from: &at, maxCount: r.opts.Limit + 1})
	next := cursorPos{off: at.off + int64(end)}
	if len(entries) > r.opts.Limit {
		e := entries[r.opts.Limit]
		next = cursorPos{off: e.line, sub: e.sub}
		entries = entries[:r.opts.Limit]
		more = true
	}
	// The calls fold in their results among the first Limit, and each entry
	// is finished in order until the page is full.
	foldResults(entries)
	total := 0
	for i := range entries {
		n := r.entrySize(&entries[i])
		if i > 0 && total+n > r.opts.MaxBytes-pageOverhead {
			// The page ends before this entry, which the next one starts at.
			e := entries[i]
			next = cursorPos{off: e.line, sub: e.sub}
			entries = entries[:i]
			more = true
			unfoldPast(entries)
			break
		}
		total += n
	}
	page := Page{
		Entries: entries,
		Cursor:  makeCursor(id, next),
		More:    more,
		Skipped: r.skipped,
	}
	// The page starts at its first entry, or at the cursor when it has none.
	// A read from the start of the file has nothing before it.
	if at.off > 0 || at.sub > 0 {
		first := at
		if len(page.Entries) > 0 {
			first = page.Entries[0].pos()
		}
		page.Older = makeCursor(id, first)
	}
	return page, nil
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

// decodeBounds limits what decode keeps, so one call holds no more entries
// than its page can use.
type decodeBounds struct {
	// from drops the entries before it: those a page after a cursor has
	// already returned.
	from *cursorPos
	// before drops the entries at it and after it.
	before *cursorPos
	// keepLast, when set, keeps only about the newest keepLast entries: the
	// older ones are dropped whenever twice that many are held.
	keepLast int
	// maxCount, when set, stops decoding at the first record that brings
	// the entries to it.
	maxCount int
}

// decode turns complete lines starting at file offset base into entries, as
// far as b allows.
func (r *reader) decode(lines []byte, base int64, b decodeBounds) []Entry {
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
			if b.from != nil && e.before(*b.from) {
				continue
			}
			if b.before != nil && !e.before(*b.before) {
				continue
			}
			out = append(out, e)
		}
		if b.keepLast > 0 && len(out) >= 2*b.keepLast {
			n := copy(out, out[len(out)-b.keepLast:])
			clear(out[n:])
			out = out[:n]
		}
		if b.maxCount > 0 && len(out) >= b.maxCount {
			break
		}
	}
	return out
}

// entrySize finishes an entry and returns its size as JSON, with the
// colours of its diff.
func (r *reader) entrySize(e *Entry) int {
	r.finish(e)
	b, err := json.Marshal(e)
	if err != nil {
		return 0
	}
	return len(b) + 1
}

// finish cleans an entry's strings and works out its diff, once. It runs
// only for an entry a page is about to carry.
func (r *reader) finish(e *Entry) {
	raw := e.raw
	if raw == nil {
		return
	}
	e.raw = nil
	switch e.Kind {
	case KindText, KindThinking:
		var cut bool
		e.Text, cut = r.clean(raw.text, TextMax)
		e.Truncated = raw.textCut || cut
	case KindToolResult:
		text := raw.text
		if r.opts.Clean != nil {
			text = r.opts.Clean(text)
		}
		var cut bool
		e.Text, cut = cutString(text, TextMax)
		e.Truncated = raw.textCut || cut
	case KindPlan:
		var cut bool
		e.Plan, cut = r.clean(raw.plan, TextMax)
		e.Truncated = raw.textCut || cut
	}
	if raw.tool != "" {
		e.Tool, _ = r.clean(raw.tool, toolMax)
	}
	if raw.target != "" {
		e.Target, _ = r.clean(raw.target, targetMax)
	}
	for _, t := range raw.todos {
		text, _ := r.clean(t.Text, targetMax)
		status, _ := r.clean(t.Status, toolMax)
		e.Todos = append(e.Todos, Todo{Text: text, Status: status})
	}
	if raw.diff != nil {
		e.Diff = r.buildDiff(raw.diff)
	}
}

// buildDiff works out the diff of src once, with its colours, and returns
// it.
func (r *reader) buildDiff(src *diffSrc) *Diff {
	if src.built != nil {
		return src.built
	}
	var d *Diff
	switch {
	case src.shared != nil:
		cp := *r.buildDiff(src.shared)
		cp.File, _ = r.clean(oneLine(src.file), targetMax)
		d = &cp
	case src.patch != nil:
		d = r.patchDiff(src.file, src.patch)
	default:
		d = r.editDiff(src.file, src.pairs)
	}
	r.style(d)
	src.built = d
	return d
}

// foldResults gives each tool call the status of its result on the page, or
// running when the page has none. A result's file diff, which carries the
// file's real line numbers, replaces the hunks worked out from the call. It
// works on the entries as decoded, before finish.
func foldResults(entries []Entry) {
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
		res := &entries[j]
		e.Status = res.Status
		if e.raw == nil || res.raw == nil || e.raw.diff == nil || res.raw.diff == nil {
			continue
		}
		if res.raw.diff.file == e.raw.diff.file {
			// One diff for both, so it is worked out once.
			e.raw.diff = res.raw.diff
			continue
		}
		e.raw.diff = &diffSrc{file: e.raw.diff.file, shared: res.raw.diff}
	}
}

// unfoldPast marks running each call on a page whose result was folded in
// but did not fit on it. The call keeps the result's diff.
func unfoldPast(entries []Entry) {
	on := map[string]bool{}
	for _, e := range entries {
		if e.Kind == KindToolResult && e.ToolID != "" {
			on[e.ToolID] = true
		}
	}
	for i := range entries {
		e := &entries[i]
		if e.Kind != KindToolResult && e.ToolID != "" && e.Role == RoleAssistant && !on[e.ToolID] {
			e.Status = StatusRunning
		}
	}
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
	f, err := transcript.OpenRegular(path)
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
	return cutString(s, limit)
}

// cutString cuts s to limit bytes on a rune boundary.
func cutString(s string, limit int) (string, bool) {
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
