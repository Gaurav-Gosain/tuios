//go:build !linux

package dirwatch

import (
	"path/filepath"
	"sync"
)

// Set watches several directories. On this platform it holds one Watcher per
// directory; see the Linux version for why that one shares an instance.
type Set struct {
	notify func(dir string)
	// content says the set also reports files written (NewContentSet).
	content bool

	mu     sync.Mutex
	byDir  map[string]*Watcher
	closed bool
}

// NewSet starts an empty set. notify runs on a watcher goroutine with the
// directory whose entries changed, and must not block.
func NewSet(notify func(dir string)) (*Set, error) {
	return &Set{notify: notify, byDir: map[string]*Watcher{}}, nil
}

// NewContentSet is NewSet for a caller that also needs to know when a file
// is written. A directory's watch reports its entries, and the writes in it
// where the platform's directory watch can (Windows). AddFile watches one
// file for writes where it cannot (kqueue), and reports it as a change of its
// directory.
func NewContentSet(notify func(dir string)) (*Set, error) {
	return &Set{notify: notify, content: true, byDir: map[string]*Watcher{}}, nil
}

// AddFile starts watching the file at path for writes, in a content set.
func (s *Set) AddFile(path string) error {
	if fileByDir {
		return s.Add(filepath.Dir(path))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnsupported
	}
	if _, ok := s.byDir[path]; ok {
		return nil
	}
	dir := filepath.Dir(path)
	w, err := watchFile(path, func() { s.notify(dir) })
	if err != nil {
		return err
	}
	s.byDir[path] = w
	return nil
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
	watch := Watch
	if s.content {
		watch = watchContentDir
	}
	w, err := watch(dir, func() { s.notify(dir) })
	if err != nil {
		return err
	}
	s.byDir[dir] = w
	return nil
}

// Refresh makes sure dir is watched. A watcher here holds the directory it was
// opened on, which is gone when the path was deleted and made again, and it
// cannot tell, so the watch is opened afresh. The caller asks only after the
// directory changed, so this is not on an idle path.
func (s *Set) Refresh(dir string) error {
	s.Remove(dir)
	return s.Add(dir)
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
