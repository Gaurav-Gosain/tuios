package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
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

// Watcher watches the config file for changes and triggers reloads.
type Watcher struct {
	watcher  *fsnotify.Watcher
	path     string
	callback ConfigReloadCallback
	stopCh   chan struct{}
	once     sync.Once

	mu            sync.Mutex
	debounceTimer *time.Timer
	// lastHash is the content the running config was built from, so a file that
	// says what is already in force is not delivered again.
	lastHash [sha256.Size]byte
}

// NewWatcher creates a file watcher for the config file.
// The callback is called with the new config (or error) when changes are detected.
func NewWatcher(configPath string, callback ConfigReloadCallback) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	cw := &Watcher{
		watcher:  w,
		path:     filepath.Clean(configPath),
		callback: callback,
		stopCh:   make(chan struct{}),
	}
	// The file as it stands is what the client is already running, so the first
	// event that finds it unchanged is dropped rather than delivered.
	if data, err := os.ReadFile(cw.path); err == nil {
		cw.lastHash = sha256.Sum256(data)
	}

	// The directory, not the file: see the note at the top of this file. Added
	// before the goroutine starts so a failure is reported to the caller rather
	// than logged into a watcher that then watches nothing.
	if err := w.Add(filepath.Dir(cw.path)); err != nil {
		_ = w.Close()
		return nil, err
	}

	go cw.run()
	return cw, nil
}

// run drains the event channel and arms the debounce.
func (cw *Watcher) run() {
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != cw.path {
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

// reload reads the file and calls the callback, unless the file says what is
// already in force or tuios wrote it itself.
func (cw *Watcher) reload() {
	select {
	case <-cw.stopCh:
		return
	default:
	}

	data, err := os.ReadFile(cw.path)
	if err != nil {
		// A file that is not there right now is an editor mid-save, not a
		// change. The create that follows brings its own event.
		return
	}
	sum := sha256.Sum256(data)
	cw.mu.Lock()
	same := sum == cw.lastHash
	cw.mu.Unlock()
	if same || isSelfWrite(sum) {
		// Either the file says what is already in force, or tuios wrote it
		// itself from a settings row. Both are already applied.
		cw.mu.Lock()
		cw.lastHash = sum
		cw.mu.Unlock()
		return
	}

	cfg, err := parseAndValidate(data)
	if err != nil {
		// The hash is not recorded: a file that could not be used is not what
		// the client is running, so the next save is delivered even if the user
		// only fixed the syntax and changed nothing else.
		cw.callback(nil, err)
		return
	}
	cw.mu.Lock()
	cw.lastHash = sum
	cw.mu.Unlock()
	cw.callback(cfg, nil)
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

// ReloadConfig loads and validates a config from the given path.
func ReloadConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	return parseAndValidate(data)
}

// parseAndValidate is the reload path's parse: every section filled from the
// defaults, then validated. It is the same fill LoadUserConfig does, which is
// the point of ParseUserConfig holding the list.
func parseAndValidate(data []byte) (*UserConfig, error) {
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, err
	}
	if v := ValidateConfig(cfg); v.HasErrors() {
		first := v.Errors[0]
		if len(v.Errors) == 1 {
			return nil, fmt.Errorf("[%s] %s: %s", first.Field, first.Key, first.Message)
		}
		return nil, fmt.Errorf("[%s] %s: %s (and %d more)",
			first.Field, first.Key, first.Message, len(v.Errors)-1)
	}
	return cfg, nil
}
