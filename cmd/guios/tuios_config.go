package main

import (
	"log"
	"slices"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/guios/settings"
)

// Some settings the app shows belong to the tuios daemon, which reads them
// from tuios's config.toml: whether a pane's history outlives a restart,
// what a restart does with the agents' conversations, and the shell of new
// panes. The app reads them from that file and writes them there, so the
// TUI and guios show one value. The daemon reads the daemon keys when it
// starts.

// tuiosHeld copies the settings tuios's config holds from src into dst.
func tuiosHeld(dst *settings.Settings, src settings.Settings) {
	dst.KeepHistory, dst.ResumeAgents, dst.Shell = src.KeepHistory, src.ResumeAgents, src.Shell
	dst.Approvals = src.Approvals
}

// approvalsHarness is the harness the Approvals switch turns on: the one
// whose hook tuios installs with its integration.
const approvalsHarness = "claude-code"

// loadTuiosSettings reads the settings tuios's config holds into s. A config
// that does not load leaves the defaults.
func loadTuiosSettings(s *settings.Settings) {
	cfg, err := config.LoadUserConfig()
	if err != nil || cfg == nil {
		return
	}
	tuiosFromConfig(s, cfg)
}

// tuiosFromConfig copies the settings tuios's config holds from cfg into s.
func tuiosFromConfig(s *settings.Settings, cfg *config.UserConfig) {
	s.KeepHistory = cfg.Daemon.PersistScrollback == nil || *cfg.Daemon.PersistScrollback
	switch cfg.Daemon.ResumeAgents {
	case "auto", "off":
		s.ResumeAgents = cfg.Daemon.ResumeAgents
	default:
		s.ResumeAgents = "ask"
	}
	s.Shell = cfg.Appearance.PreferredShell
	s.Approvals = len(cfg.Agents.Approvals.Enabled) > 0
}

// followTuiosConfig keeps the settings tuios's config holds in step with
// the file while the app runs. They were read only at start, so a file
// another program changed, as `tuios config` or an editor does, left the
// app on the old values: with approvals turned on there, the card still
// offered Turn on. The app's own saves are not delivered (the watcher drops
// a self write). A watcher that cannot start leaves the values read at
// start.
func (a *App) followTuiosConfig() {
	path, err := config.GetConfigPath()
	if err != nil {
		return
	}
	w, err := config.NewWatcherWithOptions(path, func(cfg *config.UserConfig, err error) {
		if err != nil || cfg == nil {
			return
		}
		a.update(func() {
			var s settings.Settings
			tuiosFromConfig(&s, cfg)
			tuiosHeld(&a.set, s)
			tuiosHeld(&a.applied, s)
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}, config.WatcherOptions{})
	if err != nil {
		log.Printf("Warning: guios cannot follow config.toml, its tuios settings change at the next start: %v", err)
		return
	}
	a.cfgWatch = w
}

// saveTuiosSettings writes the settings tuios's config holds when they
// changed from old. It writes only the keys that changed.
func saveTuiosSettings(old, s settings.Settings) error {
	if old.KeepHistory == s.KeepHistory && old.ResumeAgents == s.ResumeAgents && old.Shell == s.Shell && old.Approvals == s.Approvals {
		return nil
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return err
	}
	if old.KeepHistory != s.KeepHistory {
		keep := s.KeepHistory
		cfg.Daemon.PersistScrollback = &keep
	}
	if old.ResumeAgents != s.ResumeAgents {
		cfg.Daemon.ResumeAgents = s.ResumeAgents
	}
	if old.Shell != s.Shell {
		cfg.Appearance.PreferredShell = s.Shell
	}
	if old.Approvals != s.Approvals {
		// On adds Claude Code to the harnesses already listed; off
		// lists none.
		if s.Approvals {
			if !slices.Contains(cfg.Agents.Approvals.Enabled, approvalsHarness) {
				cfg.Agents.Approvals.Enabled = append(cfg.Agents.Approvals.Enabled, approvalsHarness)
			}
		} else {
			cfg.Agents.Approvals.Enabled = nil
		}
	}
	_, err = config.SaveUserConfig(cfg)
	return err
}
