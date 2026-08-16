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
//   - Records decode into [record], a struct with six scalar fields. Go's
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
// The only thing that leaves this package is a [Turn], which is one of three
// constants.
package transcript

import (
	"encoding/json"
	"errors"
	"io"
	"os"
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

// record is every field this package will look at, and the reason no other
// field can be looked at by accident.
//
// stop_reason is the one thing taken from message. Nesting a struct with a
// single scalar in it is what lets the decoder step into the message object,
// find that field, and skip content without materialising it; declaring the
// whole of message as json.RawMessage would keep the conversation in memory in
// exchange for nothing.
//
// toolUseResult is absent rather than ignored. A user record means the turn is
// still running whether it carries a tool result or a fresh prompt, so the
// field is not needed to derive a state, and a field that is not declared
// cannot be read.
type record struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Version   string `json:"version"`
	Timestamp string `json:"timestamp"`
	// IsSidechain marks a subagent's turn. A subagent's records are written to
	// the same file as its parent's, so without this a Task tool finishing would
	// report the whole pane done while the agent that spawned it kept working.
	IsSidechain bool `json:"isSidechain"`
	Message     struct {
		StopReason string `json:"stop_reason"`
	} `json:"message"`
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
}

// tailWindow bounds the first read of a file this package has not seen before.
//
// These files reach a hundred megabytes and the newest record is the only one
// that matters, so a cold read looks at the last window and no further. After
// that the reader knows where it stopped and reads only what was appended,
// which is a few kilobytes a turn.
const tailWindow = 128 << 10

// ErrNoFile reports that the transcript is gone: deleted, or never there. The
// caller uses it to give up its claim rather than to retry, so it is
// distinguished from any other read failure.
var ErrNoFile = errors.New("transcript: file does not exist")

// Reader tails one transcript. It is not safe for concurrent use; one reader
// belongs to one joined pane and is driven by that pane's watcher.
type Reader struct {
	path string
	// off is where the last read stopped, always immediately after a newline, so
	// a resumed read never begins inside a record.
	off int64
	// buf is the read buffer, kept here and zeroed after every parse. See the
	// package comment for why it is a field.
	buf []byte
	// skipped counts lines that did not parse. It exists so a file this package
	// cannot read at all is visible as a number, without the number costing
	// anyone a line of their conversation in a log.
	skipped int
}

// NewReader returns a reader for path. Nothing is opened until Read.
func NewReader(path string) *Reader { return &Reader{path: path} }

// Path returns the file this reader tails.
func (r *Reader) Path() string { return r.path }

// Skipped returns how many lines have failed to parse over this reader's life.
func (r *Reader) Skipped() int { return r.skipped }

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
	f, err := os.Open(r.path) //nolint:gosec // the path came from the agent's own hook or from the manifest's directory
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
		// Shorter than where we stopped: truncated, or a new file at the same
		// path. Either way the offset means nothing now, so start over.
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

	// start == r.off means we read from where we stopped, which is either byte
	// zero or the byte after a newline: a record boundary either way. It is only
	// the jump forward to the tail window that lands mid-record.
	obs, ok, consumed := r.scan(buf, start == r.off)
	// Zeroed before returning, on every path, so the page holding someone's
	// prompts does not outlive the call that needed it.
	clear(r.buf[:read])
	if consumed >= 0 {
		r.off = start + int64(consumed)
	}
	return obs, ok, nil
}

// scan walks complete lines in buf and returns the newest record that speaks to
// the turn, plus how many bytes were consumed (through the final newline), or
// -1 when there was no complete line to consume.
//
// atBoundary says the buffer begins where a record begins. When it does not,
// the leading partial record is dropped.
func (r *Reader) scan(buf []byte, atBoundary bool) (Observation, bool, int) {
	end := lastIndexByte(buf, '\n')
	if end < 0 {
		// No complete line. Consume nothing and wait for the newline; a file
		// whose single record is longer than the window would otherwise stall
		// forever, but the window is 128 KB and a record is a few kilobytes.
		return Observation{}, false, -1
	}
	consumed := end + 1
	lines := buf[:end]

	if !atBoundary {
		if i := indexByte(lines, '\n'); i >= 0 {
			lines = lines[i+1:]
		} else {
			lines = nil
		}
	}

	var obs Observation
	found := false
	for len(lines) > 0 {
		line := lines
		if i := indexByte(lines, '\n'); i >= 0 {
			line, lines = lines[:i], lines[i+1:]
		} else {
			lines = nil
		}
		if len(line) == 0 {
			continue
		}
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Dropped without the error, which would quote the line. The count is
			// the whole diagnostic this package is willing to keep.
			r.skipped++
			continue
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
	return obs, found, consumed
}

// turn maps one record to what it says about the turn, reporting whether it
// says anything at all.
//
// The alternation these three cases read is the one the files actually show: an
// assistant record calling a tool, a user record carrying that tool's result,
// repeated, and then an assistant record with end_turn followed by the human's
// next prompt. Everything else in the file (titles, modes, queue operations,
// snapshots) is bookkeeping and is silent here.
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
			// A streamed record whose stop reason has not been decided yet. It is
			// evidence the agent is talking, which is evidence it is working, but
			// it must never be read as a finished turn.
			return TurnWorking, true
		default:
			// tool_use, max_tokens, stop_sequence: all mid-turn.
			return TurnWorking, true
		}
	case "user":
		// A tool result or a fresh prompt. Both mean a turn is in progress: the
		// human's prompt starts one, and a tool result lands inside one.
		return TurnWorking, true
	default:
		return TurnUnknown, false
	}
}

// indexByte and lastIndexByte are here rather than bytes.IndexByte only to keep
// the buffer's lifetime obvious in one file; they compile to the same thing.
func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func lastIndexByte(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}
