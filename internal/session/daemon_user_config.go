package session

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// DaemonConfigFromUser maps the parts of the user's config file the daemon
// owns onto the daemon's own config: the [daemon] section, the [hosts] table
// and the hooks the daemon fires.
//
// Every starter calls it: `tuios daemon`, and the SSH and web servers when
// they start a daemon in-process. It used to be three hand-written subsets,
// and a daemon started by `tuios ssh` ran with no agent detection settings and
// no hosts while the same file gave both to a daemon started by `tuios
// attach`. The fields the starter owns (Version, Foreground, LogFile,
// DisableAutoRestore) stay the starter's to fill.
//
// The log level is applied here as well, when nothing set it first: a flag or
// the environment wins over the file. It is a process global, so it is set
// once whoever starts the daemon.
//
// TestEveryDaemonKeyReachesTheDaemon holds this to every key in the section.
func DaemonConfigFromUser(uc *config.UserConfig) *DaemonConfig {
	cfg := &DaemonConfig{}
	if uc == nil {
		return cfg
	}
	if GetDebugLevel() == DebugOff && uc.Daemon.LogLevel != "" {
		SetDebugLevel(ParseDebugLevel(uc.Daemon.LogLevel))
	}
	cfg.AgentAutoDetect = uc.Daemon.AgentAutoDetect
	cfg.AgentDetectInterval = time.Duration(uc.Daemon.AgentDetectSeconds) * time.Second
	cfg.AgentBinaries = uc.Daemon.AgentBinaries
	cfg.Hosts = HostsFromConfig(uc)
	// The path, not the table, is what lets the daemon follow later edits to
	// [hosts]. Every real starter goes through here, so every real daemon
	// watches the file; a daemon built from a hand-made config in a test does
	// not, and neither does one whose config file could not be loaded at all.
	// LoadUserConfig writes a default file when there is none, so the machine
	// that has never saved a setting still arrives here with a path.
	if path, err := config.GetConfigPath(); err == nil {
		cfg.ConfigPath = path
	}
	// The daemon owns every pane's history, so the depth the user asked for
	// has to reach it: the client's emulator honoured the setting and the
	// daemon's kept ten thousand lines whatever it said.
	cfg.ScrollbackLines = uc.Appearance.ScrollbackLines
	// The daemon runs the hooks for the facts it owns, so a session with
	// nobody attached still runs them. The client keeps the hooks that need a
	// terminal.
	cfg.ApplyUserHooks(uc)
	return cfg
}

// HostsFromConfig turns the [hosts] config table into the daemon's host list.
// Nothing is validated here; the federation table does that and reports what it
// dropped, so the reason reaches 'tuios hosts' rather than only the log.
func HostsFromConfig(cfg *config.UserConfig) []federation.Host {
	if cfg == nil || len(cfg.Hosts) == 0 {
		return nil
	}
	out := make([]federation.Host, 0, len(cfg.Hosts))
	for name, h := range cfg.Hosts {
		out = append(out, federation.Host{
			Name:           name,
			Addr:           h.Addr,
			ConnectTimeout: time.Duration(h.ConnectTimeout) * time.Second,
			Command:        h.Command,
			SSHOptions:     h.SSHOptions,
		})
	}
	return out
}
