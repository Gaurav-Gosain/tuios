package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// The config file watcher: editing config.toml in one pane takes effect in the
// running tuios in the next one, with no restart and no reload command.
//
// # Why the directory and not the file
//
// The unit of watching is the directory, and the events are filtered by name.
// An inotify watch on a file follows the inode, and an editor does not write
// through the inode: vim writes a temporary file, renames the original aside
// and renames the new one into place, so the watch ends up on a file nobody
// will ever write to again and the first :w is the last one seen. Watching the
// directory reports the rename, the create and the write, all naming the path,
// and it survives every editor. internal/session's transcript watcher chose the
// directory for the same reason.
//
// # Why the debounce
//
// One save is several events. The rename, the create and the write arrive
// within a few milliseconds of each other, and a reload per event would parse
// the file three times and, worse, parse it once while it was half written.
// Every event restarts a 200 ms timer, so a burst produces one reload after the
// burst is over.
//
// # What a broken file does
//
// Nothing. A file that does not parse or does not validate leaves the running
// config exactly as it is and reports the error, which the client shows on
// screen. A half-written file caught between an editor's two writes is the
// ordinary case of this, and a reload that rendered defaults over a running
// session every time somebody had an unbalanced quote would be far worse than
// waiting for the next save.
//
// # Why some saves are dropped
//
// tuios writes this file itself: every settings row saves. Without a guard the
// save would come back through the watcher as somebody else's edit, and a held
// arrow key would retile once per repeat for a config already in force. The
// content is hashed, and two hashes are dropped: the one already in force, and
// one tuios itself wrote (see selfWrites in save.go). The first also drops the
// second and third events of an editor's save when the debounce has not merged
// them.
//
// # What it costs at idle
//
// One descriptor and one goroutine blocked on a channel receive. There is no
// ticker here and none is reachable from here: a file nobody edits produces no
// events, so an idle client pays no wakeups. That is why notification was
// chosen over a poll, and it is what BenchmarkIdleTick measures.

// configDebounce is how long the watcher waits for a save to finish before it
// reads the file.
const configDebounce = 200 * time.Millisecond

// ConfigReloadCallback is called when config changes are detected. Exactly one
// of newConfig and err is set. An err means the file on disk cannot be used and
// the running config stands.
type ConfigReloadCallback func(newConfig *UserConfig, err error)

// WatcherOptions tune one watcher.
type WatcherOptions struct {
	// DeliverSelfWrites delivers a change tuios itself wrote, which is normally
	// dropped (see the note above on why some saves are dropped).
	//
	// The daemon sets it. The settings page writes the config file from the
	// client, and in a server that holds a daemon in the same process the two
	// share the ring of hashes, so the daemon's watcher would call its own
	// client's save a self write and never see the [hosts] table it changed.
	// The daemon writes no config of its own, so it has nothing to suppress.
	DeliverSelfWrites bool
	// DeliverUnchanged delivers a change even when the file says what was
	// last delivered. The daemon sets it: it can apply part of the file by
	// another path (tuios config apply), so what it last saw here is not
	// what is in force, and an edit and its undo inside one debounce must
	// still reach it.
	DeliverUnchanged bool
}

// Watcher watches the config file for changes and triggers reloads. A config
// split over several files is watched as a whole: every included file, every
// file in config.d, and config.d itself, so a drop-in file added or removed is
// a change too.
type Watcher struct {
	watcher  *fsnotify.Watcher
	path     string
	dropIn   string
	callback ConfigReloadCallback
	opts     WatcherOptions
	stopCh   chan struct{}
	once     sync.Once

	mu            sync.Mutex
	debounceTimer *time.Timer
	// lastHashes is the content the running config was built from, file by
	// file, so a set of files that says what is already in force is not
	// delivered again.
	lastHashes map[string][sha256.Size]byte
	// files are the paths whose events count: every file read, and every
	// included file that was not there.
	files map[string]bool
	// dirs are the directories under watch.
	dirs map[string]bool
}

// NewWatcher creates a file watcher for the config file.
// The callback is called with the new config (or error) when changes are detected.
func NewWatcher(configPath string, callback ConfigReloadCallback) (*Watcher, error) {
	return NewWatcherWithOptions(configPath, callback, WatcherOptions{})
}

// NewWatcherWithOptions is NewWatcher with the options set.
func NewWatcherWithOptions(configPath string, callback ConfigReloadCallback, opts WatcherOptions) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	main := absPath(configPath)
	cw := &Watcher{
		watcher:  w,
		path:     main,
		dropIn:   filepath.Join(filepath.Dir(main), DropInDirName),
		callback: callback,
		opts:     opts,
		stopCh:   make(chan struct{}),
		files:    map[string]bool{main: true},
		dirs:     map[string]bool{},
	}

	// The directory, not the file: see the note at the top of this file. Added
	// before the goroutine starts so a failure is reported to the caller rather
	// than logged into a watcher that then watches nothing.
	if err := w.Add(filepath.Dir(main)); err != nil {
		_ = w.Close()
		return nil, err
	}
	cw.dirs[filepath.Dir(main)] = true

	// The files as they stand are what the client is already running, so the
	// first event that finds them unchanged is dropped rather than delivered.
	if lc, err := LoadLayered(main); err == nil {
		cw.lastHashes = layerHashes(lc)
		cw.follow(lc)
	} else if data, err := os.ReadFile(main); err == nil { //nolint:gosec // the user's own config file
		cw.lastHashes = map[string][sha256.Size]byte{main: sha256.Sum256(data)}
	}

	go cw.run()
	return cw, nil
}

// layerHashes is the content hash of every file of the config.
func layerHashes(lc *LayeredConfig) map[string][sha256.Size]byte {
	out := make(map[string][sha256.Size]byte, len(lc.Layers))
	for _, l := range lc.Layers {
		out[l.Path] = sha256.Sum256(l.Data)
	}
	return out
}

// follow brings the watch set in line with the files lc read. A directory
// added stays watched until Stop: the set is small, and dropping one that an
// include returns to a moment later would lose its events.
func (cw *Watcher) follow(lc *LayeredConfig) {
	files := map[string]bool{cw.path: true}
	for _, l := range lc.Layers {
		files[l.Path] = true
		// A file that is a link is edited where the link points. An edit
		// in place there changes the file and not the link, so the
		// directory of the target is watched too.
		if l.Real != "" {
			files[l.Real] = true
		}
	}
	for _, m := range lc.Missing {
		files[m] = true
	}
	want := []string{cw.dropIn}
	for p := range files {
		want = append(want, filepath.Dir(p))
	}
	cw.mu.Lock()
	cw.files = files
	cw.mu.Unlock()
	for _, dir := range want {
		cw.mu.Lock()
		have := cw.dirs[dir]
		cw.mu.Unlock()
		if have {
			continue
		}
		// A directory that is not there cannot be watched. An include in a
		// directory made later is seen on the next reload of a file that is
		// watched.
		if err := cw.watcher.Add(dir); err != nil {
			continue
		}
		cw.mu.Lock()
		cw.dirs[dir] = true
		cw.mu.Unlock()
	}
}

// relevant reports whether an event on name can change the config.
func (cw *Watcher) relevant(name string) bool {
	if name == cw.dropIn {
		return true
	}
	if filepath.Dir(name) == cw.dropIn && strings.HasSuffix(name, ".toml") {
		return true
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return cw.files[name]
}

// run drains the event channel and arms the debounce.
func (cw *Watcher) run() {
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			if !cw.relevant(filepath.Clean(event.Name)) {
				continue
			}
			// Chmod alone is not a content change. Rename and Remove are: an
			// editor's save arrives as a rename of the old file followed by a
			// create of the new one, and the reload reads whatever is at the
			// path when the debounce fires.
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
				!event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
				continue
			}
			cw.arm()
		case _, ok := <-cw.watcher.Errors:
			// Drained and dropped. An fsnotify error names the path it happened
			// on, which is the user's own config directory, so it is not written
			// anywhere. Draining matters more than reporting: leaving the
			// channel full stops events.
			if !ok {
				return
			}
		case <-cw.stopCh:
			return
		}
	}
}

// arm restarts the debounce timer.
func (cw *Watcher) arm() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.debounceTimer != nil {
		cw.debounceTimer.Stop()
	}
	cw.debounceTimer = time.AfterFunc(configDebounce, cw.reload)
}

// reload reads the files and calls the callback, unless they say what is
// already in force or tuios wrote the changed ones itself.
func (cw *Watcher) reload() {
	select {
	case <-cw.stopCh:
		return
	default:
	}

	lc, err := LoadLayered(cw.path)
	if errors.Is(err, fs.ErrNotExist) {
		// A file that is not there right now is an editor mid-save, not a
		// change. The create that follows brings its own event.
		return
	}
	if err != nil {
		// A file that does not parse. The running config stands, and the
		// hashes are not recorded, so the fix is delivered.
		cw.callback(nil, err)
		return
	}
	cw.follow(lc)

	sums := layerHashes(lc)
	cw.mu.Lock()
	same := maps.Equal(sums, cw.lastHashes) && !cw.opts.DeliverUnchanged
	self := !cw.opts.DeliverSelfWrites && onlySelfWrites(cw.lastHashes, sums)
	cw.mu.Unlock()
	if same || self {
		// Either the files say what is already in force, or tuios wrote the
		// changed ones itself from a settings row. Both are already applied.
		cw.mu.Lock()
		cw.lastHashes = sums
		cw.mu.Unlock()
		return
	}

	cfg, err := validateLayered(lc)
	if err != nil {
		// The hashes are not recorded: files that could not be used are not
		// what the client is running, so the next save is delivered even if
		// the user only fixed the syntax and changed nothing else.
		cw.callback(nil, err)
		return
	}
	cw.mu.Lock()
	cw.lastHashes = sums
	cw.mu.Unlock()
	cw.callback(cfg, nil)
}

// onlySelfWrites reports whether every file that differs from before is one
// tuios wrote itself. A file that went away is nobody's save.
func onlySelfWrites(before, after map[string][sha256.Size]byte) bool {
	changed := false
	for p := range before {
		if _, ok := after[p]; !ok {
			return false
		}
	}
	for p, sum := range after {
		if old, ok := before[p]; ok && old == sum {
			continue
		}
		if !isSelfWrite(sum) {
			return false
		}
		changed = true
	}
	return changed
}

// Stop stops the file watcher.
func (cw *Watcher) Stop() {
	cw.once.Do(func() {
		close(cw.stopCh)
		cw.mu.Lock()
		if cw.debounceTimer != nil {
			cw.debounceTimer.Stop()
		}
		cw.mu.Unlock()
		_ = cw.watcher.Close()
	})
}

// ReloadConfig loads and validates a config from the given path, with every
// file it includes.
func ReloadConfig(path string) (*UserConfig, error) {
	lc, err := LoadLayered(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		return nil, err
	}
	return validateLayered(lc)
}

// validateLayered is the reload path's parse of a loaded config: every
// section filled from the defaults, then the keys tuios cannot read taken
// out. It is the same rule as LoadUserConfig: a bad key costs that key.
// Rejecting the reload kept the old config, so a file that started fine with
// the keys dropped could then never reload.
func validateLayered(lc *LayeredConfig) (*UserConfig, error) {
	data, err := lc.Bytes()
	if err != nil {
		return nil, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, err
	}
	cfg.LoadWarnings = append(append([]string(nil), lc.Warnings...), DroppedWarnings(DropUnreadableKeys(cfg, lc))...)
	return cfg, nil
}
