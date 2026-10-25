package dirwatch

import (
	"encoding/binary"
	"os"
	"slices"
	"sync"

	"golang.org/x/sys/unix"
)

// Set watches several directories through one inotify instance.
//
// It exists for the files section's tree, which can have a few dozen folders
// open at once. A Watcher per folder is an inotify instance per folder, and
// the kernel's default limit is 128 instances per user across every program
// that user runs, so a tree of open folders would starve an editor's watcher
// of its own. One instance holds any number of watches against the far larger
// max_user_watches limit, and one parked goroutine reads them all.
type Set struct {
	file   *os.File
	fd     int
	done   chan struct{}
	notify func(dir string)

	mu   sync.Mutex
	byWD map[int32]string
	byID map[string]int32
	once sync.Once
}

// NewSet starts an empty set. notify runs on the set's own goroutine with the
// directory whose entries changed, and must not block.
func NewSet(notify func(dir string)) (*Set, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	s := &Set{
		file:   os.NewFile(uintptr(fd), "inotify"),
		fd:     fd,
		done:   make(chan struct{}),
		notify: notify,
		byWD:   map[int32]string{},
		byID:   map[string]int32{},
	}
	go s.run()
	return s, nil
}

// Add starts watching dir. Adding a directory already in the set does nothing.
func (s *Set) Add(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[dir]; ok {
		return nil
	}
	wd, err := unix.InotifyAddWatch(s.fd, dir, entryMask)
	if err != nil {
		return err
	}
	// Two paths can name one inode, and inotify then answers both with one
	// descriptor. The later name wins, which costs at most a re-read of the
	// other one's listing when it is next opened.
	if old, ok := s.byWD[int32(wd)]; ok {
		delete(s.byID, old)
	}
	s.byWD[int32(wd)] = dir
	s.byID[dir] = int32(wd)
	return nil
}

// Remove stops watching dir. Removing a directory not in the set does nothing.
func (s *Set) Remove(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wd, ok := s.byID[dir]
	if !ok {
		return
	}
	delete(s.byID, dir)
	delete(s.byWD, wd)
	_, _ = unix.InotifyRmWatch(s.fd, uint32(wd))
}

// Dirs is how many directories the set watches.
func (s *Set) Dirs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

func (s *Set) run() {
	defer close(s.done)
	buf := make([]byte, 4096)
	for {
		n, err := s.file.Read(buf)
		if err != nil {
			return
		}
		// One read is one batch. Each directory named in it is reported once,
		// however many of its entries the batch touched.
		var seen [8]int32
		count := 0
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			wd := int32(binary.NativeEndian.Uint32(buf[off:]))
			nameLen := int(binary.NativeEndian.Uint32(buf[off+12:]))
			off += unix.SizeofInotifyEvent + nameLen
			if slices.Contains(seen[:count], wd) {
				continue
			}
			if count < len(seen) {
				seen[count] = wd
				count++
			}
			s.mu.Lock()
			dir, ok := s.byWD[wd]
			s.mu.Unlock()
			if ok {
				s.notify(dir)
			}
		}
	}
}

// Close stops every watch and waits for the set's goroutine to finish, so
// notify is never called after Close returns. It is safe to call more than
// once.
func (s *Set) Close() {
	s.once.Do(func() {
		_ = s.file.Close()
		<-s.done
	})
}
