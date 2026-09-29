package session

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
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
				d.attention.noteConfigNotice(configWidenedNotice, grantsWidenedNote)
			}
		}
	}
	d.recordAppliedGrants(now)
}

// The Inbox items about config.toml, by name.
const (
	// configWidenedNotice says panes hold more than at the last run.
	configWidenedNotice = "config.toml"
	// configWaitsNotice says a change that gives more waits for the person.
	configWaitsNotice = "config.toml change waits"
)

// configWaitsNote is the summary of the configWaitsNotice item.
const configWaitsNote = "Run tuios config apply in a terminal outside tuios. The change gives panes or other machines more, so it waits for you."

// closeConfigNotice closes the Inbox item about config.toml named name.
func (a *attentionStore) closeConfigNotice(name string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeKeyLocked(attentionItemKey(&AttentionItem{Kind: AttentionErrored, Name: name}), AttentionClosedResolved)
}

// noteConfigNotice opens or updates the Inbox item about config.toml named
// name, which the person dismisses.
func (a *attentionStore) noteConfigNotice(name, summary string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.upsertLocked(AttentionItem{
		Kind:    AttentionErrored,
		Name:    name,
		Summary: attentionText(summary, attentionMaxSummary),
	})
}

// verbApplyConfig applies config.toml for the person. A file change applies
// only what narrows (applyUserConfig); this is how the person applies a change
// that widens without restarting the daemon. With host it applies that one
// host entry and nothing else, which is what tuios hosts add asks for: a
// widening some other process wrote to the file is not applied with it.
func (d *Daemon) verbApplyConfig(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Host string `json:"host"`
	}
	if verr := decodeParams(params, &p); verr != nil {
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

	before := d.configSnapshot()
	if p.Host != "" {
		if verr := d.applyOneHost(cfg, p.Host); verr != nil {
			return nil, verr
		}
	} else {
		d.applyUserConfig(cfg, true)
		log.Printf("config.toml was applied in full by the person")
	}
	after := d.configSnapshot()
	out := map[string]any{
		"type":           "config_applied",
		"mode":           after.mode,
		"default_grants": after.grants,
		"changes":        describeConfigChanges(before, after),
		"still_waiting":  d.configWaiting(),
	}
	if p.Host != "" {
		out["host"] = p.Host
	}
	return out, nil
}

// applyOneHost applies the entry of one host from cfg, or its removal, and
// nothing else.
func (d *Daemon) applyOneHost(cfg *config.UserConfig, name string) *verbError {
	if d.federation == nil {
		return newVerbError(ErrVerbCommandFailed, "this daemon dials no hosts")
	}
	var want *federation.Host
	for _, h := range HostsFromConfig(cfg) {
		if strings.TrimSpace(h.Name) == name {
			h := h
			want = &h
		}
	}
	cur := d.federation.Table()
	hosts := make([]federation.Host, 0, cur.Len()+1)
	for _, n := range cur.Names() {
		if n == name {
			continue
		}
		if h, err := cur.Lookup(n); err == nil {
			hosts = append(hosts, h)
		}
	}
	if want != nil {
		hosts = append(hosts, *want)
	}
	d.ApplyHosts(hosts)
	log.Printf("[FEDERATION] Host %s was applied by the person", name)
	return nil
}

// configSnapshot is what apply-config reports a change of.
type configSnapshot struct {
	mode   string
	grants []string
	hosts  map[string]federation.Host
	links  map[string]config.HostConfig
}

func (d *Daemon) configSnapshot() configSnapshot {
	s := configSnapshot{mode: d.permissionMode(), grants: d.manager.grants.defaults().Names(), hosts: map[string]federation.Host{}}
	if d.federation != nil {
		t := d.federation.Table()
		for _, n := range t.Names() {
			if h, err := t.Lookup(n); err == nil {
				s.hosts[n] = h
			}
		}
	}
	if t := d.linkPolicies.Load(); t != nil {
		s.links = *t
	}
	return s
}

// describeConfigChanges says in words what changed between two snapshots.
func describeConfigChanges(before, after configSnapshot) []string {
	var out []string
	if before.mode != after.mode || !slices.Equal(before.grants, after.grants) {
		out = append(out, fmt.Sprintf("Panes on the default: mode %s, grants %s. Before: mode %s, grants %s.",
			after.mode, grantWords(after.grants), before.mode, grantWords(before.grants)))
	}
	for _, n := range slices.Sorted(maps.Keys(after.hosts)) {
		old, had := before.hosts[n]
		switch {
		case !had:
			out = append(out, "Host "+n+" is added.")
		case !federation.SameHost(old, after.hosts[n]):
			out = append(out, "Host "+n+" dials another way now.")
		}
	}
	for _, n := range slices.Sorted(maps.Keys(before.hosts)) {
		if _, ok := after.hosts[n]; !ok {
			out = append(out, "Host "+n+" is removed.")
		}
	}
	// The policy each machine gets, compared machine by machine: a host
	// entry with no policy of its own adds a key and changes nothing.
	peers := map[string]bool{config.LinkPolicyDefaultName: true}
	for k := range before.links {
		peers[k] = true
	}
	for k := range after.links {
		peers[k] = true
	}
	for _, k := range slices.Sorted(maps.Keys(peers)) {
		peer := k
		if k == config.LinkPolicyDefaultName {
			peer = ""
		}
		was, now := config.LinkPolicyFor(before.links, peer), config.LinkPolicyFor(after.links, peer)
		if slices.Equal(was.Allow, now.Allow) && was.HoldMail == now.HoldMail && was.HostedGrace == now.HostedGrace {
			continue
		}
		who := "Machine " + k
		if peer == "" {
			who = "A machine with no table of its own"
		}
		out = append(out, fmt.Sprintf("%s may now do %s here. Before: %s.", who, grantWords(now.Allow), grantWords(was.Allow)))
	}
	return out
}

// grantWords is a grant list for a sentence.
func grantWords(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
