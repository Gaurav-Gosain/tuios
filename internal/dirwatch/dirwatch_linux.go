package dirwatch

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// entryMask is the set of inotify events that change what a listing shows: a
// name created, deleted, or moved in or out, and the folder itself going away.
// IN_MODIFY and IN_CLOSE_WRITE are left out on purpose; see the package doc.
const entryMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR

// Watcher watches one directory. Close it when the directory is no longer
// listed.
type Watcher struct {
	file *os.File
	done chan struct{}
	once sync.Once
}

// fileMask is what WatchFile reports: each write, not only the close after
// the last one, so a log a running program appends to is seen as it grows,
// and the file itself going away.
const fileMask = unix.IN_MODIFY | unix.IN_CLOSE_WRITE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// Watch starts watching dir. notify runs on the watcher's own goroutine each
// time a batch of changes to dir's entries arrives, and must not block.
func Watch(dir string, notify func()) (*Watcher, error) {
	return watchMask(dir, entryMask, notify)
}

// WatchFile starts watching one file for writes, and for its removal or
// rename. notify runs on the watcher's own goroutine and must not block. It
// is for a caller that waits on the file's content, such as wait-for file,
// and is watched only while it waits.
func WatchFile(path string, notify func()) (*Watcher, error) {
	return watchMask(path, fileMask, notify)
}

func watchMask(path string, mask uint32, notify func()) (*Watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	if _, err := unix.InotifyAddWatch(fd, path, mask); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// A non-blocking descriptor handed to os.NewFile goes on the runtime's
	// poller, so the read below parks the goroutine rather than a thread, and
	// Close wakes it.
	w := &Watcher{file: os.NewFile(uintptr(fd), "inotify"), done: make(chan struct{})}
	go w.run(notify)
	return w, nil
}

func (w *Watcher) run(notify func()) {
	defer close(w.done)
	// The events themselves are not read for their names. Any of them means
	// the listing is out of date, and one read of the buffer is one batch.
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
