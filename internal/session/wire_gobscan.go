package session

import (
	"errors"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

const (
	maxGobNesting = 1024

	maxGobTypes = 1024

	gobFirstUserID = 64

	gobTooBig = 1 << 30
)

var (
	errGobNested    = errfmt.Errorf("payload nests deeper than %d levels", maxGobNesting)
	errGobTypes     = errfmt.Errorf("payload defines more than %d types", maxGobTypes)
	errGobMalformed = errors.New("payload is not a well-formed gob value")
)

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

type gobKind uint8

const (
	gobArray gobKind = iota + 1
	gobSlice
	gobStruct
	gobMap
	gobOpaque
)

type gobType struct {
	kind   gobKind
	elem   int
	key    int
	length int
	fields []int
	flat   int8
}

func isBasic(id int) bool {
	return id >= gobBool && id <= gobComplex
}

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

type gobFrame struct {
	t     *gobType
	depth int
	left  uint64
	field int
}

const gobDenseIDs = 4096

type gobScan struct {
	data   []byte
	pos    int
	lim    int
	dense  []*gobType
	sparse map[int]*gobType
	ntypes int
	stack  []gobFrame
}

func (s *gobScan) typeOf(id int) *gobType {
	if i := id - gobFirstUserID; i >= 0 && i < len(s.dense) {
		return s.dense[i]
	}
	return s.sparse[id]
}

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

func (s *gobScan) boundedByType(id int) bool {
	const visiting = -1
	depth := map[int]int{}
	type step struct {
		id   int
		next int
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

func (s *gobScan) run() error {
	for len(s.stack) > 0 {
		f := &s.stack[len(s.stack)-1]
		var next int
		switch f.t.kind {
		case gobStruct:
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
		if isBasic(next) {
			if err := s.basic(next); err != nil {
				return err
			}
			continue
		}
		if err := s.value(next, f.depth); err != nil {
			return err
		}
	}
	return nil
}

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
		case 0:
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
		case 1:
			t = &gobType{kind: gobSlice}
			return s.fields(2, func(f int) error {
				if f == 0 {
					return s.commonType()
				}
				return s.typeID(&t.elem)
			})
		case 2:
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
		case 3:
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
		default:
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

func (s *gobScan) commonType() error {
	return s.fields(2, func(f int) error {
		if f == 0 {
			return s.skipCounted()
		}
		_, err := s.int()
		return err
	})
}

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

func (s *gobScan) uint() (uint64, error) {
	if s.pos < s.lim && s.data[s.pos] <= 0x7f {
		s.pos++
		return uint64(s.data[s.pos-1]), nil
	}
	return s.uintLong()
}

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
