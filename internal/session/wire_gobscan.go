package session

import (
	"errors"
	"fmt"
)

// A bound on how deeply any gob payload nests, checked on the raw bytes
// before encoding/gob sees them.
//
// wire_bounds.go keeps a client or linked peer from overflowing the daemon's
// stack with two message types. The same hazard runs the other way: a client
// reads daemon frames of up to 16 MB of any type, and the daemon on the other
// end may be another machine's (tuios attach through a host). A state sync or
// an attach reply carrying a layout tree nested a few million deep, or a
// command result whose data nests maps as deep, overflows the client's stack
// during gob's decode, which is fatal before any check after the decode can
// run. A per-type frame limit does not fit the client: what a daemon sends
// includes pane contents and daemon-side fields and has no small ceiling.
//
// A second way in needs no deep value at all. gob compiles a decoder for each
// type a stream defines, by recursion over the type's elements, and a stream
// may define a chain of types (a slice of a slice of a slice, each its own
// definition) in a field the receiver does not have and would skip. So the
// number of type definitions in one payload is bounded as well.
//
// checkGobNesting walks a payload the way the gob decoder reads it, without
// recursion and without building the values, and refuses one that nests values
// deeper than maxGobNesting or defines more than maxGobTypes types. It follows
// the decoder rather than the encoder: every byte it reads is one gob would
// read in the same place, so the shape it checks is the shape gob would
// decode. A payload it cannot follow is refused, never passed through: a real
// encoder does not produce one, and gob would fail on it anyway.
//
// Every payload goes through decodePayload, so this holds for the client and
// the daemon alike. The wire does not change.

const (
	// maxGobNesting is how many levels of structs, slices, arrays, maps and
	// interfaces a payload may nest, counting the payload itself as one. The
	// deepest real payload is a state carrying a layout tree at maxBSPDepth,
	// about 262 levels. The worst level measured costs about 2.2 KB of decode
	// stack, so this bound holds a payload to a few megabytes of stack.
	maxGobNesting = 1024

	// maxGobTypes is how many type definitions one payload may carry. The
	// largest real payload defines a few dozen.
	maxGobTypes = 1024

	// gobFirstUserID is the lowest type id a stream may define. The ids below
	// it are gob's own.
	gobFirstUserID = 64

	// gobTooBig is gob's own ceiling on a message length (tooBig in
	// encoding/gob) on 64-bit systems. A frame is 16 MB at most, so the
	// 32-bit ceiling does not matter.
	gobTooBig = 1 << 30
)

// Errors for what the scan refuses. errGobNested and errGobTypes are the
// bounds; errGobMalformed is a stream the scan cannot follow.
var (
	errGobNested    = fmt.Errorf("payload nests deeper than %d levels", maxGobNesting)
	errGobTypes     = fmt.Errorf("payload defines more than %d types", maxGobTypes)
	errGobMalformed = errors.New("payload is not a well-formed gob value")
)

// The builtin type ids a payload may use in a value. Ids 9 to 63 are gob's
// own reserved and bootstrap types, which no payload of this package holds.
const (
	gobBool      = 1
	gobInt       = 2
	gobUint      = 3
	gobFloat     = 4
	gobBytes     = 5
	gobString    = 6
	gobComplex   = 7
	gobInterface = 8
)

// gobKind is the shape of a type the stream defined.
type gobKind uint8

const (
	gobArray gobKind = iota + 1
	gobSlice
	gobStruct
	gobMap
	gobOpaque // GobEncoder, BinaryMarshaler or TextMarshaler: length and bytes
)

// gobType is a type definition from the stream, cut down to what the scan
// needs to find where each value ends.
type gobType struct {
	kind   gobKind
	elem   int   // array, slice and map element type
	key    int   // map key type
	length int   // array length
	fields []int // struct field types, by field number
	flat   int8  // struct: 1 when every field is a basic type, -1 when not, 0 before the first look
}

// isBasic reports whether id is one of gob's basic types other than an
// interface: a value of it holds nothing nested.
func isBasic(id int) bool {
	return id >= gobBool && id <= gobComplex
}

// isFlat reports whether t is a struct of basic fields only, which the scan
// reads in one loop without a frame. A cell of a terminal state is one, and
// there are hundreds of thousands in a pane with its scrollback.
func (t *gobType) isFlat() bool {
	if t.flat == 0 {
		t.flat = 1
		for _, f := range t.fields {
			if !isBasic(f) {
				t.flat = -1
				break
			}
		}
	}
	return t.flat == 1
}

// gobFrame is one composite value the scan is inside.
type gobFrame struct {
	t     *gobType
	depth int
	left  uint64 // elements (array, slice) or keys and elements (map) still to read
	field int    // struct: the last field number read, -1 before the first
}

// gobDenseIDs is how many type ids after gobFirstUserID the scan keeps in a
// slice. An encoder numbers types from gobFirstUserID up, so a real payload's
// ids fall here; one past it goes in a map.
const gobDenseIDs = 4096

// gobScan reads one payload. pos is the next byte; lim is the end of the
// gob message pos is in, past which no value may read.
type gobScan struct {
	data   []byte
	pos    int
	lim    int
	dense  []*gobType // by id - gobFirstUserID
	sparse map[int]*gobType
	ntypes int
	stack  []gobFrame
}

// typeOf is the definition of id, or nil when the stream has not defined it.
func (s *gobScan) typeOf(id int) *gobType {
	if i := id - gobFirstUserID; i >= 0 && i < len(s.dense) {
		return s.dense[i]
	}
	return s.sparse[id]
}

// setType records the definition of id.
func (s *gobScan) setType(id int, t *gobType) {
	i := id - gobFirstUserID
	if i >= gobDenseIDs {
		if s.sparse == nil {
			s.sparse = make(map[int]*gobType)
		}
		s.sparse[id] = t
		return
	}
	if i >= len(s.dense) {
		s.dense = append(s.dense, make([]*gobType, i+1-len(s.dense))...)
	}
	s.dense[i] = t
}

// checkGobNesting refuses a payload that nests deeper than maxGobNesting or
// defines more than maxGobTypes types. It reads the first value in data, as
// one Decode would.
func checkGobNesting(data []byte) error {
	s := &gobScan{data: data}
	id, err := s.typeSequence(false)
	if err != nil {
		return err
	}
	if s.boundedByType(id) {
		return nil
	}
	if err := s.topValue(id, 0); err != nil {
		return err
	}
	return s.run()
}

// boundedByType reports whether every value of type id nests within
// maxGobNesting whatever its bytes, so the value need not be walked: its type
// reaches no interface and no type that contains itself, and the longest
// chain of types below it is short enough. A pane's terminal state is such a
// type, and the largest payload there is. A type that reaches an interface or
// a recursive type, or one the stream has not defined, is walked.
//
// Every type the payload needs is defined before its value unless an
// interface brings more, and a type that reaches an interface is walked, so
// what is checked here is all gob will decode.
func (s *gobScan) boundedByType(id int) bool {
	const visiting = -1    // on the walk's path: reaching it again is a cycle
	depth := map[int]int{} // id to the most levels a value of it nests
	type step struct {
		id   int
		next int // index of the next child to visit
	}
	children := func(t *gobType) []int {
		switch t.kind {
		case gobStruct:
			return t.fields
		case gobArray, gobSlice:
			return []int{t.elem}
		case gobMap:
			return []int{t.key, t.elem}
		}
		return nil
	}
	path := []step{{id: id}}
	depth[id] = visiting
	for len(path) > 0 {
		top := &path[len(path)-1]
		t := s.typeOf(top.id)
		if t == nil {
			return false
		}
		kids := children(t)
		if top.next < len(kids) {
			child := kids[top.next]
			top.next++
			switch {
			case isBasic(child):
				continue
			case child == gobInterface:
				return false
			}
			d, seen := depth[child]
			if seen && d == visiting {
				return false
			}
			if !seen {
				depth[child] = visiting
				path = append(path, step{id: child})
			}
			continue
		}
		// Every child is done: this type nests one level more than its
		// deepest composite child.
		most := 0
		for _, child := range kids {
			if !isBasic(child) && depth[child] > most {
				most = depth[child]
			}
		}
		depth[top.id] = most + 1
		if most+1 > maxGobNesting {
			return false
		}
		path = path[:len(path)-1]
	}
	return true
}

// topValue starts a value as gob's decodeValue does: a struct's fields
// follow at once, and anything else is a singleton, a zero field delta and
// then the value.
func (s *gobScan) topValue(id, parentDepth int) error {
	if t := s.typeOf(id); t != nil && t.kind == gobStruct {
		return s.value(id, parentDepth)
	}
	delta, err := s.uint()
	if err != nil {
		return err
	}
	if delta != 0 {
		return errGobMalformed
	}
	return s.value(id, parentDepth)
}

// run reads the composite values on the stack until none is left.
func (s *gobScan) run() error {
	for len(s.stack) > 0 {
		f := &s.stack[len(s.stack)-1]
		var next int
		switch f.t.kind {
		case gobStruct:
			// gob stops a struct at a zero delta or at the end of the message.
			if s.pos == s.lim {
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			delta, err := s.uint()
			if err != nil {
				return err
			}
			if delta == 0 {
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			if delta > uint64(len(f.t.fields)-1-f.field) {
				return errGobMalformed
			}
			f.field += int(delta)
			next = f.t.fields[f.field]
		case gobArray, gobSlice:
			if f.left == 0 {
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			f.left--
			next = f.t.elem
		case gobMap:
			if f.left == 0 {
				s.stack = s.stack[:len(s.stack)-1]
				continue
			}
			if f.left%2 == 0 {
				next = f.t.key
			} else {
				next = f.t.elem
			}
			f.left--
		}
		// Most values in a real payload are basic: read those here rather
		// than through value.
		if isBasic(next) {
			if err := s.basic(next); err != nil {
				return err
			}
			continue
		}
		// value may grow the stack, so f is not used past here.
		if err := s.value(next, f.depth); err != nil {
			return err
		}
	}
	return nil
}

// value reads a value of type id whose container is at parentDepth. A basic
// value is read whole; a composite one is pushed for run to read.
func (s *gobScan) value(id, parentDepth int) error {
	if isBasic(id) {
		return s.basic(id)
	}
	if id == gobInterface {
		return s.iface(parentDepth)
	}
	t := s.typeOf(id)
	if t == nil {
		return errGobMalformed
	}
	depth := parentDepth + 1
	if depth > maxGobNesting {
		return errGobNested
	}
	switch t.kind {
	case gobOpaque:
		return s.skipCounted()
	case gobStruct:
		if t.isFlat() {
			return s.flatStruct(t)
		}
		s.stack = append(s.stack, gobFrame{t: t, depth: depth, field: -1})
	case gobArray, gobSlice:
		n, err := s.count(1)
		if err != nil {
			return err
		}
		if t.kind == gobArray && n != uint64(t.length) {
			return errGobMalformed
		}
		s.stack = append(s.stack, gobFrame{t: t, depth: depth, left: n})
	case gobMap:
		n, err := s.count(2)
		if err != nil {
			return err
		}
		s.stack = append(s.stack, gobFrame{t: t, depth: depth, left: 2 * n})
	}
	return nil
}

// flatStruct reads a struct of basic fields, with the same delta rules run
// applies to any struct.
func (s *gobScan) flatStruct(t *gobType) error {
	field := -1
	for s.pos < s.lim {
		delta, err := s.uint()
		if err != nil {
			return err
		}
		if delta == 0 {
			return nil
		}
		if delta > uint64(len(t.fields)-1-field) {
			return errGobMalformed
		}
		field += int(delta)
		if err := s.basic(t.fields[field]); err != nil {
			return err
		}
	}
	return nil
}

// basic reads a value of a basic type other than an interface.
func (s *gobScan) basic(id int) error {
	switch id {
	case gobBytes, gobString:
		return s.skipCounted()
	case gobComplex:
		if _, err := s.uint(); err != nil {
			return err
		}
	}
	_, err := s.uint()
	return err
}

// iface reads an interface value as gob's decodeInterface does: the concrete
// type's name (empty for nil), any type definitions it needs, its id, a byte
// count gob does not use, and the value. The interface counts as a level of
// its own, because the decoder recurses through it.
func (s *gobScan) iface(parentDepth int) error {
	n, err := s.uint()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	if n > 1024 || n > uint64(s.lim-s.pos) {
		return errGobMalformed
	}
	s.pos += int(n)
	depth := parentDepth + 1
	if depth > maxGobNesting {
		return errGobNested
	}
	id, err := s.typeSequence(true)
	if err != nil {
		return err
	}
	if _, err := s.uint(); err != nil {
		return err
	}
	return s.topValue(id, depth)
}

// typeSequence reads type definitions up to the id of a value, as gob's
// decodeTypeSequence does, and returns that id. At the top of a payload each
// definition is a message of its own; inside an interface a definition may
// share the message and is then followed by a count gob skips.
func (s *gobScan) typeSequence(inInterface bool) (int, error) {
	for {
		if s.pos == s.lim {
			if err := s.nextMessage(); err != nil {
				return 0, err
			}
		}
		id, err := s.int()
		if err != nil {
			return 0, err
		}
		if id >= 0 {
			return int(id), nil
		}
		if err := s.defineType(-id); err != nil {
			return 0, err
		}
		if s.pos < s.lim {
			if !inInterface {
				return 0, errGobMalformed
			}
			if _, err := s.uint(); err != nil {
				return 0, err
			}
		}
	}
}

// nextMessage starts the next gob message: a length, then that many bytes.
func (s *gobScan) nextMessage() error {
	s.lim = len(s.data)
	n, err := s.uint()
	if err != nil {
		return err
	}
	if n >= gobTooBig || n > uint64(len(s.data)-s.pos) {
		return errGobMalformed
	}
	s.lim = s.pos + int(n)
	return nil
}

// defineType reads a wireType for type id. Exactly one of its seven kinds
// must be set. An encoder never sets more than one, and the scan does not
// guess which of several gob would use, so a stream that sets more is
// refused.
func (s *gobScan) defineType(id int64) error {
	if id < gobFirstUserID || id >= 1<<31 || s.typeOf(int(id)) != nil {
		return errGobMalformed
	}
	s.ntypes++
	if s.ntypes > maxGobTypes {
		return errGobTypes
	}
	var t *gobType
	err := s.fields(7, func(field int) error {
		if t != nil {
			return errGobMalformed
		}
		switch field {
		case 0: // ArrayT: CommonType, Elem, Len
			t = &gobType{kind: gobArray}
			return s.fields(3, func(f int) error {
				switch f {
				case 0:
					return s.commonType()
				case 1:
					return s.typeID(&t.elem)
				}
				n, err := s.int()
				if err != nil {
					return err
				}
				if n < 0 || n > int64(len(s.data)) {
					return errGobMalformed
				}
				t.length = int(n)
				return nil
			})
		case 1: // SliceT: CommonType, Elem
			t = &gobType{kind: gobSlice}
			return s.fields(2, func(f int) error {
				if f == 0 {
					return s.commonType()
				}
				return s.typeID(&t.elem)
			})
		case 2: // StructT: CommonType, Field []fieldType{Name, Id}
			t = &gobType{kind: gobStruct}
			return s.fields(2, func(f int) error {
				if f == 0 {
					return s.commonType()
				}
				n, err := s.count(1)
				if err != nil {
					return err
				}
				t.fields = make([]int, n)
				for i := range t.fields {
					if err := s.fields(2, func(ff int) error {
						if ff == 0 {
							return s.skipCounted()
						}
						return s.typeID(&t.fields[i])
					}); err != nil {
						return err
					}
				}
				return nil
			})
		case 3: // MapT: CommonType, Key, Elem
			t = &gobType{kind: gobMap}
			return s.fields(3, func(f int) error {
				switch f {
				case 0:
					return s.commonType()
				case 1:
					return s.typeID(&t.key)
				}
				return s.typeID(&t.elem)
			})
		default: // GobEncoderT, BinaryMarshalerT, TextMarshalerT: CommonType
			t = &gobType{kind: gobOpaque}
			return s.fields(1, func(int) error { return s.commonType() })
		}
	})
	if err != nil {
		return err
	}
	if t == nil {
		return errGobMalformed
	}
	s.setType(int(id), t)
	return nil
}

// commonType reads a CommonType: Name string, Id int.
func (s *gobScan) commonType() error {
	return s.fields(2, func(f int) error {
		if f == 0 {
			return s.skipCounted()
		}
		_, err := s.int()
		return err
	})
}

// typeID reads a type id field.
func (s *gobScan) typeID(dst *int) error {
	n, err := s.int()
	if err != nil {
		return err
	}
	if n < 0 || n >= 1<<31 {
		return errGobMalformed
	}
	*dst = int(n)
	return nil
}

// fields reads a struct of one of gob's own fixed types, calling read for
// each field present. These nest a fixed few levels deep, so the recursion
// through read is bounded by the schema, not the input.
func (s *gobScan) fields(n int, read func(field int) error) error {
	field := -1
	for s.pos < s.lim {
		delta, err := s.uint()
		if err != nil {
			return err
		}
		if delta == 0 {
			return nil
		}
		if delta > uint64(n-1-field) {
			return errGobMalformed
		}
		field += int(delta)
		if err := read(field); err != nil {
			return err
		}
	}
	return nil
}

// count reads an element count and refuses one the rest of the message
// cannot hold at minBytes an element. A real encoder writes at least one byte
// for every element, so this only refuses what gob would fail on or spin on.
func (s *gobScan) count(minBytes uint64) (uint64, error) {
	n, err := s.uint()
	if err != nil {
		return 0, err
	}
	if n > uint64(s.lim-s.pos)/minBytes {
		return 0, errGobMalformed
	}
	return n, nil
}

// skipCounted skips a length and that many bytes: a string, a byte slice or
// an opaque encoded value.
func (s *gobScan) skipCounted() error {
	n, err := s.uint()
	if err != nil {
		return err
	}
	if n > uint64(s.lim-s.pos) {
		return errGobMalformed
	}
	s.pos += int(n)
	return nil
}

// uint reads one gob unsigned integer: a byte under 0x80 is its own value;
// otherwise the byte is the negated count of big-endian bytes that follow.
// The one byte case is split out so it inlines: most integers in a real
// payload are one byte.
func (s *gobScan) uint() (uint64, error) {
	if s.pos < s.lim && s.data[s.pos] <= 0x7f {
		s.pos++
		return uint64(s.data[s.pos-1]), nil
	}
	return s.uintLong()
}

// uintLong reads an integer of more than one byte.
func (s *gobScan) uintLong() (uint64, error) {
	if s.pos >= s.lim {
		return 0, errGobMalformed
	}
	b := s.data[s.pos]
	s.pos++
	n := -int(int8(b))
	if n > 8 || n > s.lim-s.pos {
		return 0, errGobMalformed
	}
	var x uint64
	for _, c := range s.data[s.pos : s.pos+n] {
		x = x<<8 | uint64(c)
	}
	s.pos += n
	return x, nil
}

// int reads one gob signed integer: an unsigned one with the sign in the low
// bit.
func (s *gobScan) int() (int64, error) {
	u, err := s.uint()
	if err != nil {
		return 0, err
	}
	i := int64(u >> 1)
	if u&1 != 0 {
		i = ^i
	}
	return i, nil
}
