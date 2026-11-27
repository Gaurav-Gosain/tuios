package dirwatch

import (
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches one directory. Close it when the directory is no longer
// listed.
//
// On Windows fsnotify watches the folder with one ReadDirectoryChangesW call
// and no handle per file, so it is used as it is. Its events include writes,
// which are dropped below rather than passed on.
type Watcher struct {
	w    *fsnotify.Watcher
	done chan struct{}
	once sync.Once
}

// Watch starts watching dir. notify runs on the watcher's own goroutine each
// time an entry of dir is created, removed or renamed, and must not block.
func Watch(dir string, notify func()) (*Watcher, error) {
	return watchDir(dir, false, notify)
}

// fileByDir is true: the directory's watch sees the writes to its files.
const fileByDir = true

// watchContentDir is Watch that also reports a file in dir written.
func watchContentDir(dir string, notify func()) (*Watcher, error) {
	return watchDir(dir, true, notify)
}

// WatchFile starts watching one file for writes, and for its removal or
// rename. The watch is on its directory, which reports the writes of every
// file in it, so notify can run for a write to another file there.
func WatchFile(path string, notify func()) (*Watcher, error) {
	return watchContentDir(filepath.Dir(path), notify)
}

// watchFile is not used here: AddFile watches the file's directory.
func watchFile(string, func()) (*Watcher, error) { return nil, ErrUnsupported }

func watchDir(dir string, writes bool, notify func()) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(dir); err != nil {
		_ = fw.Close()
		return nil, err
	}
	w := &Watcher{w: fw, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			select {
			case ev, ok := <-fw.Events:
				if !ok {
					return
				}
				if ev.Has(fsnotify.Create) || ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) || writes && ev.Has(fsnotify.Write) {
					notify()
				}
			case _, ok := <-fw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return w, nil
}

// Close stops the watcher and waits for its goroutine to finish, so notify is
// never called after Close returns. It is safe to call more than once.
func (w *Watcher) Close() {
	w.once.Do(func() {
		_ = w.w.Close()
		<-w.done
	})
}
