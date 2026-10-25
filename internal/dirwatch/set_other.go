//go:build !linux

package dirwatch

import "sync"

// Set watches several directories. On this platform it holds one Watcher per
// directory; see the Linux version for why that one shares an instance.
type Set struct {
	notify func(dir string)

	mu     sync.Mutex
	byDir  map[string]*Watcher
	closed bool
}

// NewSet starts an empty set. notify runs on a watcher goroutine with the
// directory whose entries changed, and must not block.
func NewSet(notify func(dir string)) (*Set, error) {
	return &Set{notify: notify, byDir: map[string]*Watcher{}}, nil
}

// Add starts watching dir. Adding a directory already in the set does nothing.
func (s *Set) Add(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnsupported
	}
	if _, ok := s.byDir[dir]; ok {
		return nil
	}
	w, err := Watch(dir, func() { s.notify(dir) })
	if err != nil {
		return err
	}
	s.byDir[dir] = w
	return nil
}

// Remove stops watching dir. Removing a directory not in the set does nothing.
func (s *Set) Remove(dir string) {
	s.mu.Lock()
	w, ok := s.byDir[dir]
	delete(s.byDir, dir)
	s.mu.Unlock()
	if ok && w != nil {
		w.Close()
	}
}

// Dirs is how many directories the set watches.
func (s *Set) Dirs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byDir)
}

// Close stops every watch. It is safe to call more than once.
func (s *Set) Close() {
	s.mu.Lock()
	ws := s.byDir
	s.byDir = map[string]*Watcher{}
	s.closed = true
	s.mu.Unlock()
	for _, w := range ws {
		if w != nil {
			w.Close()
		}
	}
}
