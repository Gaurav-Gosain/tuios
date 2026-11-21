package dirwatch

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

const entryMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR

// Watcher watches one directory. Close it when the directory is no longer
// listed.
type Watcher struct {
	file *os.File
	done chan struct{}
	once sync.Once
}

// Watch starts watching dir. notify runs on the watcher's own goroutine each
// time a batch of changes to dir's entries arrives, and must not block.
func Watch(dir string, notify func()) (*Watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if _, err := unix.InotifyAddWatch(fd, dir, entryMask); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	w := &Watcher{file: os.NewFile(uintptr(fd), "inotify"), done: make(chan struct{})}
	go w.run(notify)
	return w, nil
}

func (w *Watcher) run(notify func()) {
	defer close(w.done)
	buf := make([]byte, 4096)
	for {
		n, err := w.file.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			notify()
		}
	}
}

// Close stops the watcher and waits for its goroutine to finish, so notify is
// never called after Close returns. It is safe to call more than once.
func (w *Watcher) Close() {
	w.once.Do(func() {
		_ = w.file.Close()
		<-w.done
	})
}
