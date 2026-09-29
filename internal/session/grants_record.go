package session

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// What panes held on the default when the daemon last ran.
//
// A reload of config.toml applies [agents.permissions] only where it narrows
// (reloadPanePermissions). A process in a pane can still widen the file and
// then end the daemon, so the next start reads the wider file. The start
// cannot tell who started it, and the record below is a file a process of the
// user can also change, so neither is a boundary. What the start does is make
// the change visible: when the default in force at start gives panes more
// than the one recorded at the last run, it says so in the log, in
// tuios pane-grants, and in the Inbox, where the person sees it.

// appliedGrants is the record of the default in force.
type appliedGrants struct {
	Strict bool     `json:"strict"`
	Grants []string `json:"grants"`
}

// appliedGrantsPath is where the record lives, under the saved state.
func appliedGrantsPath() string {
	return filepath.Join(getResurrectionDir(), "grants", "applied.json")
}

// recordAppliedGrants writes the default now in force.
func (d *Daemon) recordAppliedGrants(r config.ResolvedPermissions) {
	path := appliedGrantsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(appliedGrants{Strict: r.Strict, Grants: r.Grants})
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

// inForce is the default the grant table holds now, as a config value.
func (t *paneGrantTable) inForce() config.ResolvedPermissions {
	if !t.strict() {
		return config.ResolvedPermissions{Grants: t.strictDefaults().Names()}
	}
	return config.ResolvedPermissions{Strict: true, Grants: t.defaults().Names()}
}

// grantsWidenedNote is what the start says when panes on the default hold
// more than at the last run.
const grantsWidenedNote = "Panes on the default hold more than when tuios last ran, because config.toml changed. Check [agents.permissions] if you did not change it."

// checkGrantsSinceLastRun compares the default in force at start with the one
// recorded at the last run, says so when it widened, and records the new one.
func (d *Daemon) checkGrantsSinceLastRun() {
	now := d.manager.grants.inForce()
	if data, err := os.ReadFile(appliedGrantsPath()); err == nil {
		var prev appliedGrants
		if json.Unmarshal(data, &prev) == nil {
			was := policyDefaults(config.ResolvedPermissions{Strict: prev.Strict, Grants: prev.Grants})
			if !was.Covers(policyDefaults(now)) {
				d.grantsWidenedAtStart.Store(true)
				log.Printf("%s Before: %s. Now: %s.", grantsWidenedNote, was.String(), policyDefaults(now).String())
				d.attention.noteConfigNotice(grantsWidenedNote)
			}
		}
	}
	d.recordAppliedGrants(now)
}

// noteConfigNotice opens an Inbox item about config.toml, which the person
// dismisses.
func (a *attentionStore) noteConfigNotice(summary string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(AttentionItem{
		Kind:    AttentionErrored,
		Name:    "config.toml",
		Summary: attentionText(summary, attentionMaxSummary),
	})
}

// verbApplyConfig applies config.toml in full, for the person. A file change
// applies only what narrows (applyUserConfig); this is how the person applies
// a change that widens without restarting the daemon.
func (d *Daemon) verbApplyConfig(cs *connState, params json.RawMessage) (any, *verbError) {
	if verr := decodeParams(params, &struct{}{}); verr != nil {
		return nil, verr
	}
	if !d.mayActAsHuman(cs) {
		return nil, hintedVerbError(ErrVerbForbidden, "apply-config is for the person: the caller runs inside a pane of this daemon or over a link", &VerbHint{
			Detail: "Nothing was applied. Run tuios config apply from a terminal outside tuios, or restart the daemon.",
		})
	}
	if d.configPath == "" {
		return nil, newVerbError(ErrVerbCommandFailed, "this daemon reads no config file")
	}
	data, err := os.ReadFile(d.configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, newVerbError(ErrVerbCommandFailed, "config.toml could not be read: "+err.Error())
	}
	cfg, err := config.ParseUserConfig(data)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, "config.toml has an error, so nothing was applied: "+err.Error())
	}
	if v := config.ValidateConfig(cfg); v.HasErrors() {
		first := v.Errors[0]
		return nil, newVerbError(ErrVerbCommandFailed, "config.toml has an error, so nothing was applied: ["+first.Field+"] "+first.Key+": "+first.Message)
	}
	d.applyUserConfig(cfg, true)
	// The watcher compares each change with the file it last delivered.
	// This file is in force now, so a change back from it must be delivered.
	d.federationMu.Lock()
	w := d.hostsWatcher
	d.federationMu.Unlock()
	w.MarkApplied(data)
	log.Printf("config.toml was applied in full by the person")
	return map[string]any{
		"type":           "config_applied",
		"mode":           d.permissionMode(),
		"default_grants": d.manager.grants.defaults().Names(),
	}, nil
}
