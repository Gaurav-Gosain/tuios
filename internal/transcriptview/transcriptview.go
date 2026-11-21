// Package transcriptview decodes an agent's transcript into a conversation a
// person can read: prompts, answers, thinking, tool calls with their results,
// diffs of edits, plans, todo lists and errors.
//
// Each harness keeps its own format, and each format has a decoder of its own
// (claude.go, codex.go, gemini.go, qwen.go, copilot.go, amp.go). They all
// decode into the same Entry, so the thread draws every agent the same way.
// A format is one JSON record per line, or, for a harness that rewrites one
// JSON document whole (format.doc), a document this package turns into one
// record per line in memory before it reads it the same way.
//
// It is the opposite of internal/transcript, and it is kept apart from it on
// purpose. That package reads a transcript to learn one of three turn states
// and is built so it cannot see anything else. This one reads the content,
// because showing the content to the person is its whole job. So the care
// moves from "decode nothing" to "decode only for the person, and only on
// request":
//
//   - It is reachable from two places. The agent-transcript verb runs only
//     for a caller that holds the person's live human_nonce. delegate reads
//     the newest page of a child agent's transcript once, when the child
//     finishes the task its parent gave it, for the answer it mails to that
//     parent; the parent chose to hand the child the task, and could read
//     the child's screen anyway. Nothing else reads a transcript here in the
//     background, and nothing here keeps what it read, with one bounded
//     exception: the records made from a document
//     format are kept a short time (docCache), so a person reading a large
//     document page by page does not have it parsed for each page. Every
//     other call opens the file, decodes, and returns. CursorAt, for
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
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
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
	diffLineMax  = 1 << 10
	targetMax    = 512
	toolMax      = 64
	todosMax     = 100
	// DefaultMaxBytes is the bound on one page's entries as JSON.
	DefaultMaxBytes = 2 << 20
	pageOverhead    = 4 << 10
	scanMax         = 32 << 20
	scanFirst       = 1 << 20
	lineMax         = 16 << 20
	// LCSCellsPerCall bounds the longest common subsequence work of one call:
	// the cells of every table the call's diffs fill, together. A cell is a
	// uint16, so the largest table is 8 MiB, and it is freed when its diff
	// is done. A diff whose table would pass what is left is shown as one
	// replace of the changed lines, all removed and then all added, and says
	// so with WholeReplace. The page's newest diffs are worked out first
	// (oldest first for an after read), so they are the ones matched.
	LCSCellsPerCall = 1 << 22
	rawTextMax      = 4 * TextMax
	headMax         = 256
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
	// KindError is an error the harness wrote into the conversation: a
	// failed request, a refused model, a turn cut short. Its Text says what.
	KindError = "error"
	// KindInterrupt marks where the person stopped the agent. It has no
	// text: Claude Code writes the stop into the conversation as the
	// person's words, and they are not a prompt.
	KindInterrupt = "interrupt"
)

// Tool call statuses.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusRunning = "running"
	// StatusDeclined says the person declined the call. The result's Text
	// is what they said with it, or empty.
	StatusDeclined = "declined"
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
	// Image is set on the "[image]" text entry of an image a client can
	// fetch with ReadImage, by the entry's ID. The data stays in the
	// transcript, so a page carries only its media type. An image a tool
	// result holds, such as a Read of a PNG, is an entry of role tool after
	// the result, with the result's ToolID.
	Image *Image `json:"image,omitempty"`
	// Calls are the calls to other agents or to the person a tool call
	// made through tuios, such as a send-agent-message in a shell command.
	// See agentcall.go.
	Calls []AgentCall `json:"calls,omitempty"`

	line int64
	sub  int
	key  string
	rec  cursorPos

	raw *rawEntry
}

type rawEntry struct {
	text    string
	textCut bool
	tool    string
	target  string
	plan    string
	todos   []Todo
	diff    *diffSrc
	calls   []AgentCall
}

type diffSrc struct {
	file   string
	pairs  [][2]string
	patch  []ccPatch
	shared *diffSrc
	built  *Diff
}

func (p cursorPos) before(at cursorPos) bool {
	return p.off < at.off || p.off == at.off && p.sub < at.sub
}

func (e *Entry) pos() cursorPos { return cursorPos{off: e.line, sub: e.sub} }

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

// Image is an image in the conversation.
type Image struct {
	MediaType string `json:"media_type"`
	// Width and Height are the image's size in pixels, when the transcript
	// says it.
	Width  int `json:"width,omitzero"`
	Height int `json:"height,omitzero"`
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
	// Format names the transcript's format, the reader its harness's
	// manifest names. Empty is FormatClaude.
	Format string
	// Session names the session in a format whose store holds many
	// (FormatOpenCode), as the harness's plugin reported it.
	Session string
	// Leaf selects a Pi tree entry. Nil uses the last complete entry; a
	// present empty string selects the empty branch before its first entry.
	Leaf *string
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

// ErrFormat reports a format this build has no decoder for.
var ErrFormat = errors.New("transcriptview: unknown transcript format")

// ErrTooLarge reports a document format whose file is larger than one call
// may read.
var ErrTooLarge = errors.New("transcriptview: transcript too large to read whole")

// Read reads one page of the transcript at path, in the format opts names.
func Read(path string, opts Options) (Page, error) {
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	opts.Limit = min(opts.Limit, MaxLimit)
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.Format == FormatOpenCode {
		return opencodeRead(path, opts)
	}
	fm, ok := lookupFormat(opts.Format)
	if !ok {
		return Page{}, ErrFormat
	}
	if fm.tree {
		return piRead(path, opts)
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
	r := &reader{src: f, size: size, opts: opts, fm: fm, lcsLeft: LCSCellsPerCall}
	defer r.zero()
	if fm.doc != nil && fm.isDoc(path) {
		if size > scanMax {
			return Page{}, ErrTooLarge
		}
		key := docKey{path: path, size: size, mod: info.ModTime()}
		var lines []byte
		if e, ok := docs.get(key); ok {
			defer docs.release(e)
			lines = e.lines
		} else {
			data, err := r.readAt(0, size)
			if err != nil {
				return Page{}, err
			}
			lines = fm.doc(data)
			if e := docs.put(key, lines); e != nil {
				defer docs.release(e)
			} else {
				r.bufs = append(r.bufs, lines)
			}
		}
		r.src, r.size = bytes.NewReader(lines), int64(len(lines))
	}
	id, err := fileID(r.src, path, r.size)
	if err != nil {
		return Page{}, err
	}

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

type reader struct {
	src     io.ReaderAt
	size    int64
	opts    Options
	fm      format
	bufs    [][]byte
	skipped int
	lexed   int
	lcsLeft int
	rewound bool
	trimmed bool
}

func (r *reader) zero() {
	for _, b := range r.bufs {
		clear(b)
	}
}

func (r *reader) readAt(from, to int64) ([]byte, error) {
	buf := make([]byte, to-from)
	r.bufs = append(r.bufs, buf)
	n, err := r.src.ReadAt(buf, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}

func (r *reader) atBoundary(off int64) bool {
	if off == 0 {
		return true
	}
	var b [1]byte
	if _, err := r.src.ReadAt(b[:], off-1); err != nil {
		return false
	}
	return b[0] == '\n'
}

func (r *reader) newest(id string) (Page, error) {
	return r.back(cursorPos{off: r.size}, id, true)
}

func (r *reader) back(to cursorPos, id string, atEnd bool) (Page, error) {
	stop := to.off
	if to.sub > 0 {
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
		r.trimmed = false
		entries := r.decode(lines, base, decodeBounds{before: before, keepLast: r.opts.Limit + 1})
		if len(entries) >= r.opts.Limit || start == 0 || window >= scanMax {
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
			for keep > 0 && keep < len(entries) && entries[keep].key != "" && entries[keep-1].key == entries[keep].key {
				keep--
				r.finish(&entries[keep])
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
				if base > 0 || keep > 0 || r.trimmed {
					older := entries[keep].pos()
					if entries[keep].key != "" {
						older.sub = 0
					}
					page.Older = makeCursor(id, older)
				}
			case base > 0:
				page.Older = makeCursor(id, cursorPos{off: base})
			}
			return page, nil
		}
		clear(buf)
		window = min(window*4, scanMax)
	}
}

func (r *reader) forward(at cursorPos, id string) (Page, error) {
	to := min(r.size, at.off+scanMax)
	buf, err := r.readAt(at.off, to)
	if err != nil {
		return Page{}, err
	}
	end := bytes.LastIndexByte(buf, '\n') + 1
	if end == 0 && to < r.size {
		if next := r.skipLongLine(to); next > 0 {
			return Page{Cursor: makeCursor(id, cursorPos{off: next}), More: true, Skipped: 1}, nil
		}
	}
	more := to < r.size
	entries := r.decode(buf[:end], at.off, decodeBounds{from: &at, maxCount: r.opts.Limit + 1})
	if r.rewound {
		r.rewound = false
		page, err := r.newest(id)
		page.Reset = true
		return page, err
	}
	next := cursorPos{off: at.off + int64(end)}
	if len(entries) > r.opts.Limit {
		e := entries[r.opts.Limit]
		next = cursorPos{off: e.line, sub: e.sub}
		entries = entries[:r.opts.Limit]
		more = true
	}
	foldResults(entries)
	total := 0
	for i := range entries {
		n := r.entrySize(&entries[i])
		if i > 0 && total+n > r.opts.MaxBytes-pageOverhead {
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
	if at.off > 0 || at.sub > 0 {
		first := at
		if len(page.Entries) > 0 {
			first = page.Entries[0].pos()
		}
		page.Older = makeCursor(id, first)
	}
	return page, nil
}

func (r *reader) skipLongLine(from int64) int64 {
	chunk := make([]byte, 64<<10)
	defer clear(chunk)
	for off := from; off < r.size; {
		n, err := r.src.ReadAt(chunk, off)
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

type decodeBounds struct {
	from     *cursorPos
	before   *cursorPos
	keepLast int
	maxCount int
}

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
		dec, ok := r.fm.line(r, line, start)
		if !ok {
			r.skipped++
			continue
		}
		at, place := len(out), cursorPos{off: -1}
		if b.before == nil || start < b.before.off || start == b.before.off && b.before.sub > 0 {
			if dec.rewind != "" {
				out = dropRewound(out, dec.rewind)
				r.rewound = true
			}
			out, at, place = dropKey(out, dec.key)
		}
		var add []Entry
		for i, e := range dec.entries {
			e.line, e.sub, e.key = start, i, dec.key
			e.rec = cursorPos{off: start, sub: i}
			if place.off >= 0 {
				e.line, e.sub = place.off, place.sub+i
			}
			if b.from != nil && e.rec.before(*b.from) {
				continue
			}
			if b.before != nil && !e.before(*b.before) {
				continue
			}
			add = append(add, e)
		}
		n := len(out)
		out = append(out, add...)
		if at < n {
			copy(out[at+len(add):], out[at:n])
			copy(out[at:], add)
		}
		if b.keepLast > 0 && len(out) >= 2*b.keepLast {
			cut := len(out) - b.keepLast
			for cut > 0 && out[cut].key != "" && out[cut-1].key == out[cut].key {
				cut--
			}
			n := copy(out, out[cut:])
			r.trimmed = true
			clear(out[n:])
			out = out[:n]
		}
		if b.maxCount > 0 && len(out) >= b.maxCount {
			break
		}
	}
	return out
}

func (r *reader) entrySize(e *Entry) int {
	r.finish(e)
	b, err := jsonx.Marshal(e)
	if err != nil {
		return 0
	}
	return len(b) + 1
}

func (r *reader) finish(e *Entry) {
	raw := e.raw
	if raw == nil {
		return
	}
	e.raw = nil
	switch e.Kind {
	case KindText, KindThinking, KindError:
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
		target := raw.target
		if ShellTool(raw.tool) {
			target = ControlPictures(target)
		}
		e.Target, _ = r.clean(target, targetMax)
	}
	for _, t := range raw.todos {
		text, _ := r.clean(t.Text, targetMax)
		status, _ := r.clean(t.Status, toolMax)
		e.Todos = append(e.Todos, Todo{Text: text, Status: status})
	}
	if raw.diff != nil {
		e.Diff = r.buildDiff(raw.diff)
	}
	for _, c := range raw.calls {
		e.Calls = append(e.Calls, r.cleanCall(c))
	}
}

func (r *reader) cleanCall(c AgentCall) AgentCall {
	line := func(s string) string {
		s, _ = r.clean(oneLine(s), targetMax)
		return s
	}
	c.Session, c.To, c.Select, c.Subject = line(c.Session), line(c.To), line(c.Select), line(c.Subject)
	c.Harness, c.Name = line(c.Harness), line(c.Name)
	c.Text, _ = r.clean(c.Text, TextMax)
	opts := c.Options[:min(len(c.Options), 9)]
	c.Options = nil
	for _, o := range opts {
		c.Options = append(c.Options, line(o))
	}
	return c
}

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
			e.raw.diff = res.raw.diff
			continue
		}
		e.raw.diff = &diffSrc{file: e.raw.diff.file, shared: res.raw.diff}
	}
}

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

func fileID(f io.ReaderAt, path string, size int64) (string, error) {
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
		head = nil
	}
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

type cursorPos struct {
	off int64
	sub int
}

func makeCursor(id string, at cursorPos) string {
	c := "t1." + id + "." + strconv.FormatInt(at.off, 36)
	if at.sub > 0 {
		c += "." + strconv.Itoa(at.sub)
	}
	return c
}

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

func (r *reader) clean(s string, limit int) (string, bool) {
	if r.opts.Clean != nil {
		s = r.opts.Clean(s)
	}
	return cutString(s, limit)
}

//go:noinline
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

//go:noinline
func oneLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

//go:noinline
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
