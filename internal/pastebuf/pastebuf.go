// Package pastebuf keeps paste buffers: the text of recent yanks, newest
// first, after tmux's paste buffers.
//
// A yank in copy mode, a mouse selection copied to the clipboard and a
// set-buffer call each add a buffer. A buffer holds bytes, not only text: a
// tmux buffer can hold a binary file, and so can this one.
//
// The rules follow tmux. A buffer the store named (bufferN) is automatic; a
// buffer a caller named is not. The count limit removes only automatic
// buffers, oldest first, and a call with no name means the newest automatic
// buffer. The byte cap (MaxBytes) bounds what all buffers hold together, so
// memory stays bounded: past it the oldest automatic buffer goes first, then
// the oldest named one. A single text larger than MaxBytes is refused rather
// than stored by dropping every other buffer.
//
// Each buffer records where it came from (Owner): the session it was copied
// or set in, and the pane that set it when a process in a pane did. A caller
// that may see only some sessions passes a Filter, and the store then acts as
// if the other buffers were not there. Names are unique among the buffers one
// caller sees: a caller that names a buffer it cannot see makes a new one,
// and learns nothing about the hidden one.
//
// The daemon holds one store, so every client and every session can share the
// buffers, as tmux's server does. A client with no daemon behind it holds its
// own. Nothing is written to disk: buffers often hold what a person copied
// out of a terminal, which includes secrets, and they end with the process.
package pastebuf

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// The defaults, used when the config says nothing.
const (
	// DefaultLimit is how many automatic buffers the store keeps.
	DefaultLimit = 20
	// DefaultMaxBytes is how many bytes all buffers hold together. It is the
	// 16 MiB a tmux shim buffer could always hold, so a load-buffer that
	// worked before the daemon kept the buffers still works.
	DefaultMaxBytes = 16 << 20
	// MaxLimit bounds the count a config may ask for.
	MaxLimit = 1000
	// MaxNamed bounds the named buffers, which the count limit does not
	// remove. Past it the oldest named buffer goes.
	MaxNamed = 1000
	// MaxNameBytes bounds a buffer name.
	MaxNameBytes = 64
)

// Errors a caller can test for.
var (
	// ErrOff is the error of a store whose limit is 0.
	ErrOff = errors.New("paste buffers are off: paste_buffers.limit is 0")
	// ErrNotFound is the error of a name no buffer has, or none the caller
	// may see.
	ErrNotFound = errors.New("no such buffer")
	// ErrNone is the error of a call on the newest buffer when there is none.
	ErrNone = errors.New("there are no paste buffers")
	// ErrTooLarge is the error of a text larger than the byte cap.
	ErrTooLarge = errors.New("the text is larger than the byte cap")
	// ErrBadName is the error of a name that is too long or not printable.
	ErrBadName = errors.New("a buffer name is 1 to 64 printable characters")
	// ErrChanged is the error of a call on a buffer that was set again after
	// the caller read it.
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
	// Name is the buffer's name: bufferN for one the store named, or the
	// name a set-buffer call gave it.
	Name string
	// Data is the content: any bytes.
	Data string
	// Created is when the content was last set.
	Created time.Time
	// Version counts the sets of the store: a buffer set again gets a new
	// one. A caller that read a buffer passes it back to act only on that
	// content. It is a small integer, so it survives a trip through JSON as
	// a float, where a time in nanoseconds would not.
	Version uint64
	// Automatic says the store named the buffer.
	Automatic bool
	// Owner says where the content came from.
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
	version  uint64
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

// trim drops buffers until the limits hold: the oldest automatic buffers
// past the count limit, the oldest named ones past MaxNamed, and past the
// byte cap the oldest automatic buffer, or the oldest named one when no
// automatic buffer is left. s.mu is held.
func (s *Store) trim() int {
	dropped := 0
	for {
		auto, named := 0, 0
		oldestAuto, oldestNamed := -1, -1
		for i, b := range s.bufs {
			if b.Automatic {
				auto++
				oldestAuto = i
			} else {
				named++
				oldestNamed = i
			}
		}
		victim := -1
		switch {
		case auto > s.limit:
			victim = oldestAuto
		case named > MaxNamed:
			victim = oldestNamed
		case s.bytes > s.maxBytes && oldestAuto >= 0:
			victim = oldestAuto
		case s.bytes > s.maxBytes:
			victim = oldestNamed
		}
		if victim < 0 {
			return dropped
		}
		s.remove(victim)
		dropped++
	}
}

// remove takes the buffer at i out. s.mu is held.
func (s *Store) remove(i int) {
	s.bytes -= len(s.bufs[i].Data)
	s.bufs = append(s.bufs[:i], s.bufs[i+1:]...)
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

// Add stores a yank as a new automatic buffer on top, owned by owner. A text
// equal to the newest buffer's, from the same owner, is not stored twice:
// that buffer comes back instead. Only a yank does this; Set never does, so
// text set in parts never lands in an older buffer.
func (s *Store) Add(data string, owner Owner) (Buffer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit > 0 && len(s.bufs) > 0 && s.bufs[0].Automatic && s.bufs[0].Owner == owner && s.bufs[0].Data == data {
		return s.bufs[0], nil
	}
	return s.set("", data, false, owner, nil)
}

// Set stores data in the buffer called name, or in a new automatic buffer
// when name is "". Names are unique: there is never a second buffer of one
// name, so no buffer can stand in for another. A name set by a call is a
// named buffer from then on, as in tmux, even when it was automatic.
//
// change says which buffers the caller may change. A nil change may change
// any buffer and make a named one. A non-nil change may set only an existing
// buffer it passes: any other name, one that is missing as well as one it may
// not change, answers ErrNotFound, so the answer says nothing about a buffer
// it cannot see. It may always make a new automatic buffer.
//
// With appendTo the data goes after the named buffer's content; with no
// name, appendTo makes a new buffer, as tmux's set-buffer -a does. Empty data
// stores nothing and is no error, as in tmux: the Buffer returned then has no
// name. The buffer goes on top and is owner's from now on.
func (s *Store) Set(name, data string, appendTo bool, owner Owner, change Filter) (Buffer, error) {
	if name != "" && !ValidName(name) {
		return Buffer{}, ErrBadName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set(name, data, appendTo, owner, change)
}

// set is Set with s.mu held.
func (s *Store) set(name, data string, appendTo bool, owner Owner, change Filter) (Buffer, error) {
	if s.limit == 0 {
		return Buffer{}, ErrOff
	}
	idx := -1
	if name != "" {
		idx = s.index(name)
		if change != nil && (idx < 0 || !change(s.bufs[idx])) {
			return Buffer{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
	}
	if data == "" {
		return Buffer{}, nil
	}
	if appendTo && idx >= 0 {
		data = s.bufs[idx].Data + data
	}
	if len(data) > s.maxBytes {
		return Buffer{}, fmt.Errorf("%w: %d bytes, the cap is %d", ErrTooLarge, len(data), s.maxBytes)
	}
	s.version++
	b := Buffer{Name: name, Data: data, Created: time.Now(), Owner: owner, Version: s.version}
	if idx >= 0 {
		s.remove(idx)
	} else if name == "" {
		b.Name, b.Automatic = s.newName(), true
	}
	s.bufs = append([]Buffer{b}, s.bufs...)
	s.bytes += len(data)
	s.trim()
	return b, nil
}

// newName is the next automatic name, bufferN as tmux names them. The
// number only goes up, so a deleted buffer's name is not given again.
// s.mu is held.
func (s *Store) newName() string {
	for {
		name := fmt.Sprintf("buffer%d", s.next)
		s.next++
		if s.index(name) < 0 {
			return name
		}
	}
}

// index is the position of the buffer called name, -1 for none. s.mu is
// held.
func (s *Store) index(name string) int {
	for i, b := range s.bufs {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// find is the position of the buffer called name, when f sees it, or of the
// newest automatic buffer f sees when name is "". A name f does not see
// answers as a missing one. s.mu is held.
func (s *Store) find(name string, f Filter) (int, error) {
	if name == "" {
		for i, b := range s.bufs {
			if b.Automatic && f.sees(b) {
				return i, nil
			}
		}
		return -1, ErrNone
	}
	if i := s.index(name); i >= 0 && f.sees(s.bufs[i]) {
		return i, nil
	}
	return -1, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Get returns the buffer called name, or the newest automatic one when name
// is "", among the buffers f sees. A nonzero version returns it only while
// its content is the one of that version.
func (s *Store) Get(name string, version uint64, f Filter) (Buffer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.find(name, f)
	if err != nil {
		return Buffer{}, err
	}
	b := s.bufs[i]
	if version != 0 && b.Version != version {
		return Buffer{}, fmt.Errorf("%w: %s", ErrChanged, b.Name)
	}
	return b, nil
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

// Delete removes the buffer called name, or the newest automatic one when
// name is "", among the buffers change passes, and returns it. A nonzero version
// deletes the buffer only while its content is the one of that version, so
// a delete after a paste never removes content set after the paste read it.
func (s *Store) Delete(name string, version uint64, change Filter) (Buffer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, err := s.find(name, change)
	if err != nil {
		return Buffer{}, err
	}
	b := s.bufs[i]
	if version != 0 && b.Version != version {
		return Buffer{}, fmt.Errorf("%w: %s", ErrChanged, b.Name)
	}
	s.remove(i)
	return b, nil
}

// Sample is a buffer's content as one short line for a listing, escaped the
// way tmux escapes a sample: a line feed, tab, carriage return and backslash
// in C style, and every other byte that is not part of a printable character
// in octal, such as \001 and \377. It is cut to at most width characters with
// "...", and never inside a character. The result is already escaped, so a
// caller prints it as it is.
func Sample(data string, width int) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(data); {
		if n >= width {
			b.WriteString("...")
			break
		}
		r, size := utf8.DecodeRuneInString(data[i:])
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\\':
			b.WriteString(`\\`)
		case (r == utf8.RuneError && size == 1) || (!unicode.IsPrint(r) && r != ' '):
			for _, c := range []byte(data[i : i+size]) {
				fmt.Fprintf(&b, `\%03o`, c)
			}
		default:
			b.WriteRune(r)
		}
		i += size
		n++
	}
	return b.String()
}

// PasteText is content as paste-buffer types it: each line feed turned into
// a carriage return, as tmux does, unless raw.
func PasteText(data string, raw bool) string {
	if raw {
		return data
	}
	return strings.ReplaceAll(data, "\n", "\r")
}
