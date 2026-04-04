package config

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"
)

// ConfigReloadCallback is called when config changes are detected.
type ConfigReloadCallback func(newConfig *UserConfig, err error)

// Watcher watches the config file for changes and triggers reloads.
type Watcher struct {
	watcher  *fsnotify.Watcher
	path     string
	callback ConfigReloadCallback
	stopCh   chan struct{}
	once     sync.Once
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
		path:     configPath,
		callback: callback,
		stopCh:   make(chan struct{}),
	}

	if err := w.Add(configPath); err != nil {
		_ = w.Close()
		return nil, err
	}

	go cw.run()
	return cw, nil
}

// run is the main watcher loop with debouncing.
func (cw *Watcher) run() {
	var debounceTimer *time.Timer

	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				// Debounce: editors often write files in multiple steps
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(200*time.Millisecond, func() {
					cw.reload()
				})
			}
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("config watcher error: %v", err)
		case <-cw.stopCh:
			return
		}
	}
}

// reload parses the config file and calls the callback.
func (cw *Watcher) reload() {
	cfg, err := ReloadConfig(cw.path)
	if err != nil {
		cw.callback(nil, err)
		return
	}
	cw.callback(cfg, nil)
}

// Stop stops the file watcher.
func (cw *Watcher) Stop() {
	cw.once.Do(func() {
		close(cw.stopCh)
		_ = cw.watcher.Close()
	})
}

// ReloadConfig loads and validates a config from the given path.
func ReloadConfig(path string) (*UserConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg UserConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	defaultCfg := DefaultConfig()
	fillMissingAppearance(&cfg, defaultCfg)
	fillMissingDaemon(&cfg, defaultCfg)
	fillMissingKeybinds(&cfg, defaultCfg)

	validation := ValidateConfig(&cfg)
	if validation.HasErrors() {
		return nil, fmt.Errorf("config has %d error(s)", len(validation.Errors))
	}

	return &cfg, nil
}
