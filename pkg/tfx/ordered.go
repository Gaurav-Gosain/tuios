package tfx

import "strconv"

// orderedMap keeps insertion order alongside key lookup. Scene and path ids
// are auto-allocated by probing upward from the current count, and several
// effects walk the collection in the order they built it, so a plain Go map
// would change behaviour rather than just layout.
type orderedMap[T any] struct {
	keys   []string
	index  map[string]int
	values []*T
}

func newOrderedMap[T any]() orderedMap[T] {
	return orderedMap[T]{index: make(map[string]int)}
}

func (m *orderedMap[T]) ensure() {
	if m.index == nil {
		m.index = make(map[string]int)
	}
}

func (m *orderedMap[T]) Len() int { return len(m.keys) }

func (m *orderedMap[T]) Has(key string) bool {
	m.ensure()
	_, ok := m.index[key]
	return ok
}

func (m *orderedMap[T]) Get(key string) *T {
	m.ensure()
	i, ok := m.index[key]
	if !ok {
		return nil
	}
	return m.values[i]
}

// Set inserts or replaces. A replaced key keeps its original position, which
// is what a Python dict does on reassignment.
func (m *orderedMap[T]) Set(key string, value *T) {
	m.ensure()
	if i, ok := m.index[key]; ok {
		m.values[i] = value
		return
	}
	m.index[key] = len(m.keys)
	m.keys = append(m.keys, key)
	m.values = append(m.values, value)
}

func (m *orderedMap[T]) Keys() []string { return m.keys }

// nextAutoID reproduces upstream's id allocation: start at the current length
// and count up until an unused id turns up.
func (m *orderedMap[T]) nextAutoID() string {
	m.ensure()
	candidate := len(m.keys)
	for {
		id := strconv.Itoa(candidate)
		if _, taken := m.index[id]; !taken {
			return id
		}
		candidate++
	}
}
