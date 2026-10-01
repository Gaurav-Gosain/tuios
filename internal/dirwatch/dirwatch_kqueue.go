//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package dirwatch

import (
	"sync"

	"golang.org/x/sys/unix"
)

// Watcher watches one directory. Close it when the directory is no longer
// listed.
type Watcher struct {
	kq, dir int
	wake    [2]int
	done    chan struct{}
	once    sync.Once
}

// Watch starts watching dir. notify runs on the watcher's own goroutine each
// time a batch of changes to dir's entries arrives, and must not block.
//
// One descriptor is opened, on the directory itself. NOTE_WRITE on a directory
// fires when an entry is added, removed or renamed, and not when a file in it
// is written to, which is exactly the set the listing cares about.
func Watch(dir string, notify func()) (*Watcher, error) {
	dfd, err := unix.Open(dir, openMode|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	kq, err := unix.Kqueue()
	if err != nil {
		_ = unix.Close(dfd)
		return nil, err
	}
	unix.CloseOnExec(kq)
	var wake [2]int
	if err := unix.Pipe(wake[:]); err != nil {
		_ = unix.Close(kq)
		_ = unix.Close(dfd)
		return nil, err
	}
	unix.CloseOnExec(wake[0])
	unix.CloseOnExec(wake[1])

	changes := make([]unix.Kevent_t, 2)
	unix.SetKevent(&changes[0], dfd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_ENABLE|unix.EV_CLEAR)
	changes[0].Fflags = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME
	// The pipe is how Close wakes a goroutine parked in kevent, which closing
	// the kqueue does not reliably do.
	unix.SetKevent(&changes[1], wake[0], unix.EVFILT_READ, unix.EV_ADD|unix.EV_ENABLE)
	if _, err := unix.Kevent(kq, changes, nil, nil); err != nil {
		for _, fd := range []int{wake[0], wake[1], kq, dfd} {
			_ = unix.Close(fd)
		}
		return nil, err
	}
	w := &Watcher{kq: kq, dir: dfd, wake: wake, done: make(chan struct{})}
	go w.run(notify)
	return w, nil
}

func (w *Watcher) run(notify func()) {
	defer close(w.done)
	events := make([]unix.Kevent_t, 8)
	for {
		n, err := unix.Kevent(w.kq, nil, events, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return
		}
		changed := false
		for _, ev := range events[:n] {
			if int(ev.Ident) == w.wake[0] {
				return
			}
			changed = true
		}
		if changed {
			notify()
		}
	}
}

// Close stops the watcher and waits for its goroutine to finish, so notify is
// never called after Close returns. It is safe to call more than once.
func (w *Watcher) Close() {
	w.once.Do(func() {
		_, _ = unix.Write(w.wake[1], []byte{0})
		<-w.done
		for _, fd := range []int{w.wake[0], w.wake[1], w.kq, w.dir} {
			_ = unix.Close(fd)
		}
	})
}
