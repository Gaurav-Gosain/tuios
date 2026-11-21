package config

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/fsnotify/fsnotify"
)

const configDebounce = 200 * time.Millisecond

var relinkUnseen = runtime.GOOS == "darwin" || runtime.GOOS == "dragonfly" ||
	strings.HasSuffix(runtime.GOOS, "bsd")

const relinkPoll = 2 * time.Second

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
	// AssetDirs are directories of files the config names by id, the themes
	// and glyphs directories, watched beside the config. A directory that is
	// not there at start is watched once it is made in the config directory.
	// A change to a .json file in one, or to ThemeReloadStamp, calls OnAssets
	// after the same debounce. OnAssets runs on the watcher's goroutine.
	AssetDirs []string
	OnAssets  func()
}

// ThemeReloadStamp is a file in the config directory whose change asks every
// watcher to read the theme and glyph files again: what tuios theme reload
// writes.
const ThemeReloadStamp = ".reload-themes"

// ConfigApplyStamp is a file in the config directory whose change asks every
// client's watcher to deliver the config as it stands, even when the watcher
// thinks it is what is in force. tuios config apply writes it, and the daemon
// that command has just applied the file to does not follow it.
const ConfigApplyStamp = ".apply-config"

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
	lastHashes    map[string][sha256.Size]byte
	selfSeen      uint64
	applyAsked    bool
	files         map[string]bool
	dirs          map[string]bool
	assets        map[string]bool
	assetsTimer   *time.Timer
	links         map[string]string
	relinkTimer   *time.Timer
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
		assets:   map[string]bool{},
		links:    map[string]string{},
	}

	if err := w.Add(filepath.Dir(main)); err != nil {
		_ = w.Close()
		return nil, err
	}
	cw.dirs[filepath.Dir(main)] = true
	if opts.OnAssets != nil {
		cw.mu.Lock()
		for _, dir := range opts.AssetDirs {
			cw.watchAsset(absPath(dir))
		}
		cw.mu.Unlock()
	}

	cw.selfSeen = lastSelfWrite()
	if lc, err := LoadLayered(main); err == nil {
		cw.lastHashes = layerHashes(lc)
		cw.follow(lc)
	} else if data, err := os.ReadFile(main); err == nil { //nolint:gosec // the user's own config file
		cw.lastHashes = map[string][sha256.Size]byte{main: sha256.Sum256(data)}
	}

	go cw.run()
	return cw, nil
}

func layerHashes(lc *LayeredConfig) map[string][sha256.Size]byte {
	out := make(map[string][sha256.Size]byte, len(lc.Layers))
	for _, l := range lc.Layers {
		out[l.Path] = sha256.Sum256(l.Data)
	}
	return out
}

func (cw *Watcher) follow(lc *LayeredConfig) {
	files := map[string]bool{cw.path: true}
	for _, l := range lc.Layers {
		files[l.Path] = true
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
		if err := cw.watcher.Add(dir); err != nil {
			continue
		}
		cw.mu.Lock()
		cw.dirs[dir] = true
		cw.mu.Unlock()
	}
}

func (cw *Watcher) applyEvent(name string) bool {
	if cw.opts.DeliverUnchanged || name != filepath.Join(filepath.Dir(cw.path), ConfigApplyStamp) {
		return false
	}
	cw.mu.Lock()
	cw.applyAsked = true
	cw.mu.Unlock()
	return true
}

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

func (cw *Watcher) run() {
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			name := filepath.Clean(event.Name)
			if cw.assetEvent(name, event) {
				cw.armAssets()
				continue
			}
			if cw.applyEvent(name) {
				cw.arm()
				continue
			}
			if !cw.relevant(name) {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
				!event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
				continue
			}
			cw.arm()
		case _, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
		case <-cw.stopCh:
			return
		}
	}
}

func (cw *Watcher) assetEvent(name string, event fsnotify.Event) bool {
	if cw.opts.OnAssets == nil {
		return false
	}
	if name == filepath.Join(filepath.Dir(cw.path), ThemeReloadStamp) {
		return true
	}
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if _, ok := cw.assets[name]; ok {
		switch {
		case event.Has(fsnotify.Create):
			cw.watchAsset(name)
		case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
			_ = cw.watcher.Remove(name)
			cw.assets[name] = false
		}
		return true
	}
	return cw.isAssetDir(filepath.Dir(name)) && strings.HasSuffix(strings.ToLower(name), ".json")
}

func (cw *Watcher) watchAsset(dir string) {
	if !relinkUnseen {
		_ = cw.watcher.Remove(dir)
		cw.assets[dir] = cw.watcher.Add(dir) == nil
		return
	}
	to := linkTarget(dir)
	if old := cw.links[dir]; old != "" && !cw.watchedElsewhere(old, dir) {
		_ = cw.watcher.Remove(old)
	}
	watch := dir
	if to != "" {
		watch = to
	}
	cw.assets[dir] = cw.watcher.Add(watch) == nil
	if _, was := cw.links[dir]; was || to != "" {
		cw.links[dir] = to
	}
	if len(cw.links) > 0 && cw.relinkTimer == nil {
		cw.relinkTimer = time.AfterFunc(relinkPoll, cw.relink)
	}
}

func (cw *Watcher) watchedElsewhere(path, dir string) bool {
	if cw.dirs[path] || cw.assets[path] {
		return true
	}
	for d, to := range cw.links {
		if d != dir && to == path {
			return true
		}
	}
	return false
}

func (cw *Watcher) isAssetDir(dir string) bool {
	if cw.assets[dir] {
		return true
	}
	for _, to := range cw.links {
		if to != "" && to == dir {
			return true
		}
	}
	return false
}

func linkTarget(dir string) string {
	if fi, err := os.Lstat(dir); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	to, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	return to
}

func (cw *Watcher) relink() {
	select {
	case <-cw.stopCh:
		return
	default:
	}
	cw.mu.Lock()
	cw.relinkTimer = nil
	moved := false
	for dir, to := range cw.links {
		if linkTarget(dir) != to {
			cw.watchAsset(dir)
			moved = true
		}
	}
	if cw.relinkTimer == nil {
		cw.relinkTimer = time.AfterFunc(relinkPoll, cw.relink)
	}
	cw.mu.Unlock()
	if moved {
		cw.armAssets()
	}
}

func (cw *Watcher) armAssets() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.assetsTimer != nil {
		cw.assetsTimer.Stop()
	}
	cw.assetsTimer = time.AfterFunc(configDebounce, func() {
		select {
		case <-cw.stopCh:
		default:
			cw.opts.OnAssets()
		}
	})
}

func (cw *Watcher) arm() {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if cw.debounceTimer != nil {
		cw.debounceTimer.Stop()
	}
	cw.debounceTimer = time.AfterFunc(configDebounce, cw.reload)
}

func (cw *Watcher) reload() {
	select {
	case <-cw.stopCh:
		return
	default:
	}

	newest := lastSelfWrite()
	lc, err := LoadLayered(cw.path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	cw.mu.Lock()
	asked := cw.applyAsked
	cw.applyAsked = false
	cw.mu.Unlock()
	if err != nil {
		cw.callback(nil, err)
		return
	}
	cw.follow(lc)

	sums := layerHashes(lc)
	cw.mu.Lock()
	same := maps.Equal(sums, cw.lastHashes) && !cw.opts.DeliverUnchanged
	self, seen := onlySelfWrites(cw.lastHashes, sums, cw.selfSeen)
	self = self && !cw.opts.DeliverSelfWrites
	cw.mu.Unlock()
	if (same || self) && !asked {
		cw.mu.Lock()
		cw.lastHashes = sums
		cw.selfSeen = max(cw.selfSeen, seen)
		cw.mu.Unlock()
		return
	}

	cfg, err := validateLayered(lc)
	if err != nil {
		cw.callback(nil, err)
		return
	}
	cw.mu.Lock()
	cw.lastHashes = sums
	cw.selfSeen = max(cw.selfSeen, newest)
	cw.mu.Unlock()
	cw.callback(cfg, nil)
}

func onlySelfWrites(before, after map[string][sha256.Size]byte, seen uint64) (bool, uint64) {
	changed, foreign := false, false
	for p := range before {
		if _, ok := after[p]; !ok {
			foreign = true
		}
	}
	newest := seen
	for p, sum := range after {
		seq := selfWriteAfter(sum, seen)
		newest = max(newest, seq)
		if old, ok := before[p]; ok && old == sum {
			continue
		}
		changed = true
		if seq == 0 {
			foreign = true
		}
	}
	return changed && !foreign, newest
}

// Stop stops the file watcher.
func (cw *Watcher) Stop() {
	cw.once.Do(func() {
		close(cw.stopCh)
		cw.mu.Lock()
		if cw.debounceTimer != nil {
			cw.debounceTimer.Stop()
		}
		if cw.assetsTimer != nil {
			cw.assetsTimer.Stop()
		}
		if cw.relinkTimer != nil {
			cw.relinkTimer.Stop()
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
			return nil, errfmt.Errorf("failed to read config file: %w", err)
		}
		return nil, err
	}
	return validateLayered(lc)
}

func validateLayered(lc *LayeredConfig) (*UserConfig, error) {
	data, err := lc.Bytes()
	if err != nil {
		return nil, err
	}
	cfg, err := ParseUserConfig(data)
	if err != nil {
		return nil, err
	}
	cfg.LoadWarnings = append(append([]string(nil), lc.Warnings...), KeyWarnings(lc, cfg)...)
	return cfg, nil
}
