package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

type appliedGrants struct {
	Strict bool     `json:"strict"`
	Grants []string `json:"grants"`
}

func appliedGrantsPath() string {
	return filepath.Join(getResurrectionDir(), "grants", "applied.json")
}

func (d *Daemon) recordAppliedGrants(r config.ResolvedPermissions) {
	data, err := jsonx.Marshal(appliedGrants{Strict: r.Strict, Grants: r.Grants})
	if err != nil {
		return
	}
	_ = atomicfile.WritePrivate(appliedGrantsPath(), data, false)
}

func (t *paneGrantTable) inForce() config.ResolvedPermissions {
	if !t.strict() {
		return config.ResolvedPermissions{Grants: t.strictDefaults().Names()}
	}
	return config.ResolvedPermissions{Strict: true, Grants: t.defaults().Names()}
}

const grantsWidenedNote = "Panes on the default hold more than when tuios last ran, because config.toml changed. Check [agents.permissions] if you did not change it."

func (d *Daemon) checkGrantsSinceLastRun() {
	now := d.manager.grants.inForce()
	if data, err := os.ReadFile(appliedGrantsPath()); err == nil {
		var prev appliedGrants
		if jsonx.Unmarshal(data, &prev) == nil {
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

const (
	configWidenedNotice = "config.toml"
	configWaitsNotice   = "config.toml change waits"
)

const configWaitsNote = "Run tuios config apply in a terminal outside tuios. The change gives panes, other machines or a notification address more, or adds a command to run. So it waits for you."

func (a *attentionStore) closeConfigNotice(name string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeKeyLocked(attentionItemKey(&AttentionItem{Kind: AttentionErrored, Name: name}), AttentionClosedResolved)
}

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
	var lc *config.LayeredConfig
	var data []byte
	lc, err := config.LoadLayered(d.configPath)
	switch {
	case err == nil:
		if data, err = lc.Bytes(); err != nil {
			return nil, newVerbError(ErrVerbCommandFailed, "config.toml could not be read: "+err.Error())
		}
	case errors.Is(err, fs.ErrNotExist):
		lc = nil
	default:
		return nil, newVerbError(ErrVerbCommandFailed, "config.toml could not be read: "+err.Error())
	}
	cfg, err := config.ParseUserConfig(data)
	if err != nil {
		return nil, newVerbError(ErrVerbCommandFailed, "config.toml has an error, so nothing was applied: "+err.Error())
	}
	dropped := append(config.UnknownKeybindTables(lc), config.UnknownKeybindActions(lc)...)
	dropped = append(dropped, config.DroppedWarnings(config.DropUnreadableKeys(cfg, lc))...)
	notices := config.StartupLayoutWarnings(lc, cfg)

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
		"dropped_keys":   dropped,
		"notices":        notices,
	}
	if p.Host != "" {
		out["host"] = p.Host
	}
	return out, nil
}

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

type configSnapshot struct {
	mode   string
	grants []string
	hosts  map[string]federation.Host
	links  map[string]config.HostConfig
	hooks  []string
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
	s.hooks = d.hooks.Commands()
	return s
}

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
		sameRoots := slices.Equal(linkRootPaths(was), linkRootPaths(now))
		if slices.Equal(was.Allow, now.Allow) && was.HoldMail == now.HoldMail && was.HostedGrace == now.HostedGrace && sameRoots {
			continue
		}
		who := "Machine " + k
		if peer == "" {
			who = "A machine with no table of its own"
		}
		if !slices.Equal(was.Allow, now.Allow) || was.HoldMail != now.HoldMail || was.HostedGrace != now.HostedGrace {
			out = append(out, fmt.Sprintf("%s may now do %s here. Before: %s.", who, grantWords(now.Allow), grantWords(was.Allow)))
		}
		if !sameRoots {
			out = append(out, fmt.Sprintf("%s may now write files in %s here. Before: %s.", who, grantWords(linkRootPaths(now)), grantWords(linkRootPaths(was))))
		}
	}
	for _, h := range after.hooks {
		if !slices.Contains(before.hooks, h) {
			out = append(out, "Hook "+h+" runs now.")
		}
	}
	for _, h := range before.hooks {
		if !slices.Contains(after.hooks, h) {
			out = append(out, "Hook "+h+" no longer runs.")
		}
	}
	return out
}

func grantWords(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
