package session

import (
	"log"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// startHostsWatch begins following the config file for changes to the [hosts]
// table. A watcher that cannot be opened is logged once, and the daemon then
// behaves as it did before this existed: the table is what it was at start.
func (d *Daemon) startHostsWatch() {
	if d.configPath == "" {
		return
	}
	// The watch is on the directory, not the file (see internal/config's
	// watcher.go), so the directory has to exist. On a machine that has never
	// saved a setting it does not, and that is the machine where the first host
	// is added.
	if err := os.MkdirAll(filepath.Dir(d.configPath), 0o750); err != nil {
		log.Printf("[FEDERATION] The daemon cannot watch the config file. A host change needs a restart: %v", err)
		return
	}
	w, err := config.NewWatcherWithOptions(d.configPath, d.onConfigReload, config.WatcherOptions{
		// The settings page writes the config from a client that can share this
		// process, and its write is the change this watcher exists to see.
		DeliverSelfWrites: true,
		DeliverUnchanged:  true,
	})
	if err != nil {
		log.Printf("[FEDERATION] The daemon cannot watch the config file. A host change needs a restart: %v", err)
		return
	}
	d.federationMu.Lock()
	d.hostsWatcher = w
	d.federationMu.Unlock()
}

// stopHostsWatch ends the config watch and returns its inotify descriptor.
func (d *Daemon) stopHostsWatch() {
	d.federationMu.Lock()
	w := d.hostsWatcher
	d.hostsWatcher = nil
	d.federationMu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// onConfigReload runs on the watcher goroutine. It applies the [hosts] table,
// appearance.preferred_shell, [agents] herdr_protocol, the
// [agents.approvals], [agents.permissions] and [agents.queue] tables and
// [agents.recap] test_patterns, and reads nothing else out of the file. A new
// approval policy applies to the next request; a hold already running keeps
// the length it started with. A new permission default that narrows applies
// to the next call from every pane that holds the default; one that widens
// waits for a restart (reloadPanePermissions).
func (d *Daemon) onConfigReload(cfg *config.UserConfig, err error) {
	if err != nil {
		log.Printf("[FEDERATION] The config file has an error, so the hosts did not change: %v", err)
		return
	}
	d.applyUserConfig(cfg, false)
}

// noteConfigWaiting opens the Inbox item that says a change waits for the
// person, or closes it when nothing waits.
func (d *Daemon) noteConfigWaiting() {
	if d.configWaiting() {
		d.noteConfigNotice(configWaitsNotice, configWaitsNote)
		return
	}
	d.closeConfigNotice(configWaitsNotice)
}

// configWaiting reports whether config.toml holds a change that widens what
// panes or links may do and waits for tuios config apply or a restart.
func (d *Daemon) configWaiting() bool {
	return d.manager.grants.restartNeeded.Load() || d.hostsWait()
}
