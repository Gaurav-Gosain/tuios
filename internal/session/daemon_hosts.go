package session

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Hot reload for the [hosts] table.
//
// The daemon used to read the table once, at start, so adding a machine meant
// editing a file and then killing the daemon that held every running session.
// It now follows the config file: a save that adds a host opens a link, a save
// that removes one closes it, and a save that changes an address dials the new
// one. Nothing else in the file is looked at here, and no other part of the
// daemon's configuration changes under it.
//
// The watcher is only started when a config path is set, which
// DaemonConfigFromUser fills for every real starter. A daemon built from a
// hand-made DaemonConfig, which is every test, follows no file.
//
// A file that does not parse changes nothing. The links that are up stay up,
// and the reason is logged, for the same reason the client keeps its running
// settings: a half-written file caught between an editor's two writes must not
// tear down a working link.

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

// onConfigReload runs on the watcher goroutine. It applies the [hosts] table
// and reads nothing else out of the file.
func (d *Daemon) onConfigReload(cfg *config.UserConfig, err error) {
	if err != nil {
		log.Printf("[FEDERATION] The config file has an error, so the hosts did not change: %v", err)
		return
	}
	d.ApplyHosts(HostsFromConfig(cfg))
}

// ApplyHosts swaps the daemon's host table for a new one and reconciles the
// links to match.
//
// It is safe to call at any time and as often as an editor saves. A host that
// did not change keeps the link it has, so an unrelated edit costs no listing;
// a host that is gone has its ssh child killed and its supervisor ended before
// this returns, so repeated edits leak neither goroutines nor processes.
func (d *Daemon) ApplyHosts(hosts []federation.Host) {
	table, problems := federation.NewTable(hosts)
	texts := make([]string, 0, len(problems))
	for _, p := range problems {
		texts = append(texts, p.Error())
	}

	d.federationMu.Lock()
	d.federationProblems = texts
	d.federationMu.Unlock()

	if d.federation == nil {
		return
	}
	change := d.federation.SetTable(table)
	if !change.Changed() && len(problems) == 0 {
		return
	}
	for _, p := range problems {
		log.Printf("[FEDERATION] %v", p)
	}
	if change.Changed() {
		log.Printf("[FEDERATION] The host table changed: %s", describeTableChange(change))
	}
}

// configProblems is the dropped-entry list a listing reports.
func (d *Daemon) configProblems() []string {
	d.federationMu.Lock()
	defer d.federationMu.Unlock()
	if len(d.federationProblems) == 0 {
		return nil
	}
	out := make([]string, len(d.federationProblems))
	copy(out, d.federationProblems)
	return out
}

// hasHosts reports whether any host is configured right now.
func (d *Daemon) hasHosts() bool {
	return d.federation != nil && d.federation.Table().Len() > 0
}

// describeTableChange is the log line for one reconcile.
func describeTableChange(c federation.TableChange) string {
	parts := make([]string, 0, 3)
	if len(c.Added) > 0 {
		parts = append(parts, "added "+strings.Join(c.Added, ", "))
	}
	if len(c.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(c.Removed, ", "))
	}
	if len(c.Redialed) > 0 {
		parts = append(parts, "redialed "+strings.Join(c.Redialed, ", "))
	}
	return strings.Join(parts, "; ")
}
