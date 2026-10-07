// Package pastebuf keeps paste buffers: the text of recent yanks, newest
// first, after tmux's paste buffers.
//
// A yank in copy mode, a mouse selection copied to the clipboard and a
// set-buffer call each add a buffer. The store is bounded two ways: by a count
// (Limit) and by the bytes all buffers hold together (MaxBytes). When either
// is passed, the oldest buffer goes. A single text larger than MaxBytes is
// refused rather than stored by dropping every other buffer.
//
// Each buffer records where it came from (Owner): the session it was copied
// or set in, and the pane that set it when a process in a pane did. A caller
// that may see only some sessions passes a Filter, and the store then acts as
// if the other buffers were not there.
//
// The daemon holds one store, so every client and every session sees the
// same buffers, as tmux's server does. A client with no daemon behind it holds
// its own. Nothing is written to disk: buffers often hold what a person copied
// out of a terminal, which includes secrets, and they end with the process.
package pastebuf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The defaults, used when the config says nothing.
const (
	// DefaultLimit is how many buffers the store keeps.
	DefaultLimit = 20
	// DefaultMaxBytes is how many bytes all buffers hold together. It is the
	// 16 MiB a tmux shim buffer could always hold, so a load-buffer that
	// worked before the daemon kept the buffers still works.
	DefaultMaxBytes = 16 << 20
	// MaxLimit bounds the count a config may ask for.
	MaxLimit = 1000
	// MaxNameBytes bounds a buffer name.
	MaxNameBytes = 64
)

// Errors a caller can test for.
var (
	// ErrOff is the error of a store whose limit is 0.
	ErrOff = errors.New("paste buffers are off: paste_buffers.limit is 0")
	// ErrEmpty is the error of an add with no text.
	ErrEmpty = errors.New("the text is empty")
	// ErrNotFound is the error of a name no buffer has, or none the caller
	// may see.
	ErrNotFound = errors.New("no such buffer")
	// ErrNone is the error of a call on the newest buffer when there is none.
	ErrNone = errors.New("there are no paste buffers")
	// ErrTooLarge is the error of a text larger than the byte cap.
	ErrTooLarge = errors.New("the text is larger than the byte cap")
	// ErrBadName is the error of a name that is too long or not printable.
	ErrBadName = errors.New("a buffer name is 1 to 64 printable characters")
	// ErrChanged is the error of a delete of a buffer that was set again
	// after the caller read it.
	ErrChanged = errors.New("the buffer was set again after it was read")
)

// Owner says where a buffer came from.
type Owner struct {
	// Session is the session the text was copied or set in, "" for none:
	// the person's own command line, outside every session.
	Session string
	// Pane is the pane whose process set the buffer, "" when the person did
	// with a yank or from outside every pane.
	Pane string
}

// Buffer is one paste buffer.
type Buffer struct {
	// Name is the buffer's name: bufferNNNN for one the store named, or the
	// name a set-buffer call gave it.
	Name string
	// Data is the text.
	Data string
	// Created is when the text was last set.
	Created time.Time
	// Automatic says the store named the buffer.
	Automatic bool
	// Owner says where the text came from.
	Owner Owner
}

// Filter says which buffers a caller may see. A nil Filter sees them all.
type Filter func(Buffer) bool

func (f Filter) sees(b Buffer) bool { return f == nil || f(b) }

// Store is a bounded list of paste buffers, newest first. It is safe for
// concurrent use.
type Store struct {
	mu       sync.Mutex
	bufs     []Buffer // newest first
	bytes    int
	limit    int
	maxBytes int
	next     int // the number of the next automatic name
}

// New makes a store with the given limits. A negative limit or a byte cap
// below 1 takes the default.
func New(limit, maxBytes int) *Store {
	s := &Store{}
	s.SetLimits(limit, maxBytes)
	return s
}

// SetLimits changes the limits and drops the oldest buffers that no longer
// fit. It returns how many it dropped.
func (s *Store) SetLimits(limit, maxBytes int) int {
	if limit < 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	if maxBytes < 1 {
		maxBytes = DefaultMaxBytes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limit, s.maxBytes = limit, maxBytes
	return s.trim()
}

// Limits reports the count and byte limits in force.
func (s *Store) Limits() (limit, maxBytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit, s.maxBytes
}

// Bytes reports how many bytes the buffers hold together.
func (s *Store) Bytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytes
}

// trim drops the oldest buffers until both limits hold. s.mu is held.
func (s *Store) trim() int {
	dropped := 0
	for len(s.bufs) > 0 && (len(s.bufs) > s.limit || s.bytes > s.maxBytes) {
		last := len(s.bufs) - 1
		s.bytes -= len(s.bufs[last].Data)
		s.bufs[last] = Buffer{}
		s.bufs = s.bufs[:last]
		dropped++
	}
	return dropped
}

// ValidName reports whether name may name a buffer: 1 to 64 bytes of
// printable characters. A space is allowed, as tmux allows it.
func ValidName(name string) bool {
	if name == "" || len(name) > MaxNameBytes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// Add stores data as a new automatic buffer on top, owned by owner. A text
// equal to the newest buffer's, from the same owner, is not stored twice:
// that buffer comes back instead.
func (s *Store) Add(data string, owner Owner) (Buffer, error) {
	return s.Set("", data, false, owner, nil)
}

// Set stores data in the buffer called name, or in a new automatic buffer
// when name is "". With appendTo the data goes after the buffer's text, and a
// name of "" means the newest buffer f sees. The buffer goes on top and is
// owner's from now on. A name held by a buffer f does not see is refused
// with ErrNotFound, so a caller cannot take over a buffer it may not read.
func (s *Store) Set(name, data string, appendTo bool, owner Owner, f Filter) (Buffer, error) {
	if name != "" && !ValidName(name) {
		return Buffer{}, ErrBadName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit == 0 {
		return Buffer{}, ErrOff
	}
	idx := -1
	switch {
	case name != "":
		idx = s.index(name)
		if idx >= 0 && !f.sees(s.bufs[idx]) {
			return Buffer{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
	case appendTo:
		idx = s.newest(f)
	}
	if appendTo && idx >= 0 {
		data = s.bufs[idx].Data + data
	}
	if data == "" {
		return Buffer{}, ErrEmpty
	}
	if len(data) > s.maxBytes {
		return Buffer{}, fmt.Errorf("%w: %d bytes, the cap is %d", ErrTooLarge, len(data), s.maxBytes)
	}
	if name == "" && !appendTo && len(s.bufs) > 0 && s.bufs[0].Automatic && s.bufs[0].Owner == owner && s.bufs[0].Data == data {
		// The same yank twice adds nothing.
		return s.bufs[0], nil
	}
	b := Buffer{Name: name, Data: data, Created: time.Now(), Owner: owner}
	if idx >= 0 {
		b.Name, b.Automatic = s.bufs[idx].Name, s.bufs[idx].Automatic
		s.bytes -= len(s.bufs[idx].Data)
		s.bufs = append(s.bufs[:idx], s.bufs[idx+1:]...)
	} else if name == "" {
		b.Name, b.Automatic = s.newName(), true
	}
	s.bufs = append([]Buffer{b}, s.bufs...)
	s.bytes += len(data)
	s.trim()
	return b, nil
}

// newName is the next free automatic name, bufferNNNN as tmux names them.
// s.mu is held.
func (s *Store) newName() string {
	for {
		name := fmt.Sprintf("buffer%04d", s.next)
		s.next++
		if s.index(name) < 0 {
			return name
		}
	}
}

// index is the position of the buffer called name, -1 for none. s.mu is held.
func (s *Store) index(name string) int {
	for i, b := range s.bufs {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// newest is the position of the newest buffer f sees, -1 for none. s.mu is
// held.
func (s *Store) newest(f Filter) int {
	for i, b := range s.bufs {
		if f.sees(b) {
			return i
		}
	}
	return -1
}

// find is the position of the buffer called name that f sees, or of the
// newest f sees when name is "". s.mu is held.
func (s *Store) find(name string, f Filter) (int, error) {
	if name == "" {
		if i := s.newest(f); i >= 0 {
			return i, nil
		}
		return -1, ErrNone
	}
	if i := s.index(name); i >= 0 && f.sees(s.bufs[i]) {
		return i, nil
	}
	return -1, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Get returns the buffer called name, or the newest when name is "", among
// the buffers f sees.
func (s *Store) Get(name string, f Filter) (Buffer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.find(name, f)
	if err != nil {
		return Buffer{}, err
	}
	return s.bufs[i], nil
}

// List returns the buffers f sees, newest first.
func (s *Store) List(f Filter) []Buffer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Buffer, 0, len(s.bufs))
	for _, b := range s.bufs {
		if f.sees(b) {
			out = append(out, b)
		}
	}
	return out
}

// Delete removes the buffer called name, or the newest when name is "",
// among the buffers f sees, and returns it. A nonzero created deletes the
// buffer only when its text is still the one set at that time, so a delete
// after a paste never removes text set after the paste read it.
func (s *Store) Delete(name string, created time.Time, f Filter) (Buffer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.find(name, f)
	if err != nil {
		return Buffer{}, err
	}
	b := s.bufs[i]
	if !created.IsZero() && !b.Created.Equal(created) {
		return Buffer{}, fmt.Errorf("%w: %s", ErrChanged, b.Name)
	}
	s.bytes -= len(b.Data)
	s.bufs = append(s.bufs[:i], s.bufs[i+1:]...)
	return b, nil
}

// Sample is a buffer's text as one short line for a listing: control
// characters shown as escapes, cut to at most width runes with an ellipsis.
// The result is already escaped, so a caller prints it as it is.
func Sample(data string, width int) string {
	var b strings.Builder
	n := 0
	for _, r := range data {
		if n >= width {
			b.WriteString("…")
			break
		}
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case !unicode.IsPrint(r) && r != ' ':
			b.WriteString(strings.Trim(strconv.QuoteRune(r), "'"))
		default:
			b.WriteRune(r)
		}
		n++
	}
	return b.String()
}
