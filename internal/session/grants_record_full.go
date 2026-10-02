//go:build !slim

package session

import (
	"log"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

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

// snapshotHosts adds the host table and the link policies to s.
func (d *Daemon) snapshotHosts(s *configSnapshot) {
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
}
