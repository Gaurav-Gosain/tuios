// Package transcript reads an agent's own record of its turn from the file the
// agent writes as it runs, and reports nothing else.
//
// A coding agent that keeps a session log on disk is stating what it did rather
// than painting a picture of it, so the log answers "has this turn finished"
// contractually, where a screen rule can only match how one version of one TUI
// happened to look. That is the whole reason this package exists.
//
// # What it will not do
//
// These files hold the entire conversation: the user's prompts, the contents of
// every file the agent read, and the output of every command it ran. This
// package is built so that leaking any of that is not a matter of care.
//
//   - Records decode into [record], a struct of scalar fields. Go's
//     encoding/json walks an unknown field to find where it ends and never
//     allocates a Go value for it, so message.content, toolUseResult,
//     lastPrompt, aiTitle and the file-history snapshots are not merely
//     discarded, they are never constructed. There is no map[string]any and no
//     json.RawMessage anywhere in this package, which is what makes that a
//     property of the types rather than of the code that uses them.
//   - Nothing here logs. A line that fails to parse increments a counter and is
//     dropped; the error, which would quote the line, is not kept. The only
//     identifier that ever escapes is the file path the caller already supplied.
//   - The read buffer is a field of [Reader], zeroed after every parse, so a
//     heap dump or a core file taken later does not hold a stale page of
//     someone's conversation. It is a field rather than a parameter because a Go
//     panic traceback prints argument words, and a slice header passed by value
//     would put a pointer to that page in the trace.
//
// The only things that leave this package are a [Turn], which is one of three
// constants, the offset a read stopped at, which says how far the file grew,
// and a count of the tokens the agent's context held, which is a number the
// API returned and says nothing of what the tokens were.
package transcript

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// Turn is what the transcript says about the agent's current turn. It is
// deliberately the smallest vocabulary the records support: three constants and
// no free text, so the type itself bounds what this package can disclose.
type Turn int

const (
	// TurnUnknown is the answer when the tail carried no record that speaks to
	// the turn at all. It is not a state; the caller asserts nothing.
	TurnUnknown Turn = iota
	// TurnWorking is the agent mid-turn: it has called a tool, or a tool result
	// or a fresh prompt has arrived and it has not finished answering.
	TurnWorking
	// TurnDone is the agent having finished its turn and now waiting for the
	// human. It is the fact the Stop hook reports, read from the file instead.
	TurnDone
)

// String renders a Turn for tests and diagnostics.
func (t Turn) String() string {
	switch t {
	case TurnWorking:
		return "working"
	case TurnDone:
		return "done"
	default:
		return "unknown"
	}
}

type record struct {
	Type         string `json:"type"`
	SessionID    string `json:"sessionId"`
	CWD          string `json:"cwd"`
	Version      string `json:"version"`
	Timestamp    string `json:"timestamp"`
	IsSidechain  bool   `json:"isSidechain"`
	PromptSource string `json:"promptSource"`
	TurnOrigin   string `json:"turnOrigin"`
	Message      struct {
		StopReason string `json:"stop_reason"`
		Usage      struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (r record) contextTokens() int64 {
	if r.Type != "assistant" || r.IsSidechain {
		return 0
	}
	u := r.Message.Usage
	if u.Input < 0 || u.CacheCreation < 0 || u.CacheRead < 0 {
		return 0
	}
	return u.Input + u.CacheCreation + u.CacheRead
}

// Observation is what one read of the tail concluded. Every field is either a
// three-valued enum or a piece of session identity the caller matched the file
// on in the first place, so an Observation crossing a package boundary carries
// no conversation with it.
type Observation struct {
	Turn Turn
	// SessionID, CWD and Version are the record fields a manifest may ask to be
	// verified against the pane. They are the agent's own identity, not the
	// user's content.
	SessionID string
	CWD       string
	Version   string
	// Timestamp is the newest record's own timestamp, as written. It is kept as
	// the string the agent wrote because nothing here compares two of them; the
	// caller uses wall-clock arrival for staleness.
	Timestamp string
	// ContextTokens is the context size the newest main agent response in
	// the read carried (see record.contextTokens), or 0 when the read held
	// none.
	ContextTokens int64
}

const tailWindow = 128 << 10

// ErrNoFile reports that the transcript is gone: deleted, or never there. The
// caller uses it to give up its claim rather than to retry, so it is
// distinguished from any other read failure.
var ErrNoFile = errors.New("transcript: file does not exist")

// Reader tails one transcript. One reader belongs to one joined pane, and it is
// safe for concurrent use: the session reads a pane from more than one goroutine
// (the debounce callback, the read on join, the output-driven fallback), and
// those reads share buf and off, so Read serializes them.
type Reader struct {
	path    string
	mu      sync.Mutex
	off     int64
	buf     []byte
	skipped int
}

// NewReader returns a reader for path. Nothing is opened until Read.
func NewReader(path string) *Reader { return &Reader{path: path} }

// Path returns the file this reader tails.
func (r *Reader) Path() string { return r.path }

// Offset returns where the last read stopped: the end of the last complete
// record it consumed. It says how far the file has grown and nothing about
// what is in it.
func (r *Reader) Offset() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.off
}

// Skipped returns how many lines have failed to parse over this reader's life.
func (r *Reader) Skipped() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skipped
}

// Read consumes whatever has been appended since the last call and reports what
// the newest usable record says.
//
// ok is false when the file grew by nothing, or grew only by records that say
// nothing about the turn. That is not an error: an append that only wrote an
// ai-title is a real event with no answer in it, and the caller keeps whatever
// it already believed.
//
// Two things about a live append are handled here rather than left to the
// caller, because getting either wrong produces a confident wrong answer:
//
//   - The last line may be half written. Everything after the final newline is
//     dropped, and off advances only to that newline, so the rest of the record
//     is read on the next call when it is complete.
//   - A read that begins mid-file begins mid-record. Everything up to and
//     including the first newline is dropped in that case.
func (r *Reader) Read() (Observation, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, err := OpenRegular(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return Observation{}, false, ErrNoFile
		}
		return Observation{}, false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return Observation{}, false, err
	}
	size := info.Size()
	if size < r.off {
		r.off = 0
	}
	if size == r.off {
		return Observation{}, false, nil
	}

	start := r.off
	if size-start > tailWindow {
		start = size - tailWindow
	}
	n := int(size - start)
	if cap(r.buf) < n {
		r.buf = make([]byte, n)
	}
	buf := r.buf[:n]
	read, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		clear(buf)
		return Observation{}, false, err
	}
	buf = buf[:read]

	obs, ok, consumed := r.scan(buf, start == r.off)
	clear(r.buf[:read])
	if consumed >= 0 {
		r.off = start + int64(consumed)
	}
	return obs, ok, nil
}

func (r *Reader) scan(buf []byte, atBoundary bool) (Observation, bool, int) {
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return Observation{}, false, -1
	}
	consumed := end + 1
	lines := buf[:end]

	if !atBoundary {
		if i := bytes.IndexByte(lines, '\n'); i >= 0 {
			lines = lines[i+1:]
		} else {
			lines = nil
		}
	}

	var obs Observation
	found := false
	var tokens int64
	for len(lines) > 0 {
		line := lines
		if i := bytes.IndexByte(lines, '\n'); i >= 0 {
			line, lines = lines[:i], lines[i+1:]
		} else {
			lines = nil
		}
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := jsonx.Unmarshal(line, &rec); err != nil {
			r.skipped++
			continue
		}
		if n := rec.contextTokens(); n > 0 {
			tokens = n
		}
		turn, speaks := rec.turn()
		if !speaks {
			continue
		}
		obs = Observation{
			Turn:      turn,
			SessionID: rec.SessionID,
			CWD:       rec.CWD,
			Version:   rec.Version,
			Timestamp: rec.Timestamp,
		}
		found = true
	}
	obs.ContextTokens = tokens
	return obs, found, consumed
}

func (r record) turn() (Turn, bool) {
	if r.IsSidechain {
		return TurnUnknown, false
	}
	switch r.Type {
	case "assistant":
		switch r.Message.StopReason {
		case "end_turn":
			return TurnDone, true
		case "":
			return TurnWorking, true
		default:
			return TurnWorking, true
		}
	case "user":
		if r.PromptSource == "" && r.TurnOrigin == "" {
			return TurnUnknown, false
		}
		return TurnWorking, true
	default:
		return TurnUnknown, false
	}
}

const firstRecordWindow = 64 << 10

// FirstRecordTime is the timestamp of the first record in the file that has
// one, which is when the agent that writes the file began it. ok is false when
// the first window of the file holds no record with a timestamp that parses.
// It reads only the timestamp field.
func FirstRecordTime(path string) (time.Time, bool) {
	f, err := os.Open(path) //nolint:gosec // a transcript path the harness manifest names
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, firstRecordWindow)
	n, _ := io.ReadFull(f, buf)
	for line := range bytes.SplitSeq(buf[:n], []byte{'\n'}) {
		var rec struct {
			Timestamp string `json:"timestamp"`
		}
		if jsonx.Unmarshal(line, &rec) != nil || rec.Timestamp == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// LastRecordBefore is the timestamp of the newest record written before t,
// read from the file's last tailWindow bytes, and its first record when the
// tail holds none from before t. It is when the conversation was last used
// before a process started at t went on with it. ok is false when no record
// before t is found.
func LastRecordBefore(path string, t time.Time) (time.Time, bool) {
	f, err := os.Open(path) //nolint:gosec // a transcript path the harness manifest names
	if err != nil {
		return time.Time{}, false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return time.Time{}, false
	}
	start := max(st.Size()-tailWindow, 0)
	buf := make([]byte, st.Size()-start)
	n, _ := f.ReadAt(buf, start)
	var best time.Time
	for line := range bytes.SplitSeq(buf[:n], []byte{'\n'}) {
		var rec struct {
			Timestamp string `json:"timestamp"`
		}
		if jsonx.Unmarshal(line, &rec) != nil || rec.Timestamp == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
		if err == nil && at.Before(t) && at.After(best) {
			best = at
		}
	}
	if !best.IsZero() {
		return best, true
	}
	if first, ok := FirstRecordTime(path); ok && first.Before(t) {
		return first, true
	}
	return time.Time{}, false
}
