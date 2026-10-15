package swatch

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is how long a Watcher waits after the last relevant change
// before it reads the theme again. Omarchy's switch replaces the theme
// directory and writes its name within about 2 ms, so one read after the
// burst sees the whole switch.
const DefaultDebounce = 50 * time.Millisecond

// Option configures a Watcher.
type Option func(*options)

type options struct {
	debounce time.Duration
}

// WithDebounce sets how long the Watcher waits after the last change before
// it reads the theme. The default is DefaultDebounce.
func WithDebounce(d time.Duration) Option {
	return func(o *options) { o.debounce = max(d, 0) }
}

// Watcher follows one Source. It holds one inotify (or kqueue, or
// ReadDirectoryChanges) handle and one goroutine that sleeps until the file
// system reports a change. It runs no timer and polls nothing while the theme
// does not change.
//
// A watched directory that does not exist yet is followed from its nearest
// existing parent, so a theme source installed after the Watcher started is
// still seen.
type Watcher struct {
	src      Source
	debounce time.Duration
	fsw      *fsnotify.Watcher
	changes  chan Palette
	done     chan struct{}
	stop     sync.Once
	wg       sync.WaitGroup

	mu      sync.Mutex
	current Palette
	err     error
	watched map[string]bool
}

// Watch reads src once and starts following it. It fails only when the
// platform's file watcher cannot be opened. A source that is not installed
// is not an error here: Current reports ErrNotFound until it appears.
func Watch(src Source, opts ...Option) (*Watcher, error) {
	o := options{debounce: DefaultDebounce}
	for _, opt := range opts {
		opt(&o)
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		src:      src,
		debounce: o.debounce,
		fsw:      fsw,
		changes:  make(chan Palette, 1),
		done:     make(chan struct{}),
		watched:  map[string]bool{},
	}
	w.current, w.err = src.Load()
	w.arm()
	w.wg.Add(1)
	go w.run()
	return w, nil
}

// Current returns the palette in force. The error is the first read's, such
// as a wrapped ErrNotFound for a source that is not installed, until a read
// succeeds. A failed read after that keeps the last palette and no error.
func (w *Watcher) Current() (Palette, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.current, w.err
}

// Changes delivers each new palette. It holds one: a palette that arrives
// while the last is still unread replaces it, because only the newest theme
// is worth applying. A read that gives the palette already in force sends
// nothing, so applying the same theme again is quiet. The channel is closed
// by Close.
func (w *Watcher) Changes() <-chan Palette { return w.changes }

// Close stops the Watcher and closes Changes.
func (w *Watcher) Close() error {
	var err error
	w.stop.Do(func() {
		close(w.done)
		err = w.fsw.Close()
		w.wg.Wait()
		close(w.changes)
	})
	return err
}

// arm watches each of the source's directories, or its nearest existing
// parent while it does not exist, and drops watches that are no longer
// needed. It reports whether the set changed.
func (w *Watcher) arm() bool {
	want := map[string]bool{}
	for _, dir := range w.src.WatchPaths() {
		if d := nearestExisting(dir); d != "" {
			want[d] = true
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := false
	for d := range w.watched {
		if !want[d] {
			_ = w.fsw.Remove(d)
			delete(w.watched, d)
			changed = true
		}
	}
	for d := range want {
		if w.watched[d] {
			continue
		}
		if err := w.fsw.Add(d); err == nil {
			w.watched[d] = true
			changed = true
		}
	}
	return changed
}

// nearestExisting is dir when it is a directory, else its nearest parent
// that is one. It is empty when not even the root is.
func nearestExisting(dir string) string {
	dir = filepath.Clean(dir)
	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// onChain reports whether path is one of the source's directories or a
// parent of one, so its appearance or removal moves a watch.
func (w *Watcher) onChain(path string) bool {
	path = filepath.Clean(path)
	for _, dir := range w.src.WatchPaths() {
		for d := filepath.Clean(dir); ; {
			if d == path {
				return true
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	return false
}

func (w *Watcher) run() {
	defer w.wg.Done()
	// fire is nil while no read is pending, so an idle Watcher waits on the
	// file system alone.
	var timer *time.Timer
	var fire <-chan time.Time
	schedule := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			timer.Reset(w.debounce)
		}
		fire = timer.C
	}
	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 && w.onChain(ev.Name) {
				// A directory on the way to the source appeared or went.
				// The new watch can miss what was made under it before it
				// was added, so read again either way.
				w.arm()
				schedule()
				continue
			}
			if w.src.Relevant(ev.Name) {
				schedule()
			}
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// An overflow or a dropped watch: read again, which is right
			// whatever was missed.
			w.arm()
			schedule()
		case <-fire:
			fire = nil
			w.reload()
		}
	}
}

// reload reads the source and delivers the palette when it changed. A read
// that fails keeps the palette in force: Omarchy's switch removes the theme
// and renames the new one in its place, and a read in that gap fails, but the
// rename that follows is another event and another read.
func (w *Watcher) reload() {
	p, err := w.src.Load()
	if err != nil {
		return
	}
	w.mu.Lock()
	if w.err == nil && p.Equal(w.current) {
		w.mu.Unlock()
		return
	}
	w.current, w.err = p, nil
	w.mu.Unlock()
	select {
	case <-w.changes:
	default:
	}
	select {
	case w.changes <- p:
	default:
	}
}
