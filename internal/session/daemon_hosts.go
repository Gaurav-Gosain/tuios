//go:build !slim

package session

import (
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// Hot reload for the [hosts] table.
//
// The daemon follows the config file, so adding a machine does not mean
// restarting the daemon that holds every running session. A save that adds a
// host opens a link, a save that removes one closes it, and a save that changes
// an address dials the new one. Nothing else in the file is looked at here, and no other part of the
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

// reloadHosts applies a changed host table only where it dials less: a host
// that is gone is dropped, and a host that is new or dials another way waits.
// The host keeps its current entry until then.
func (d *Daemon) reloadHosts(hosts []federation.Host) (waits bool) {
	if d.federation == nil {
		d.ApplyHosts(hosts)
		return false
	}
	cur := d.federation.Table()
	keep := make([]federation.Host, 0, len(hosts))
	var waiting []string
	for _, h := range hosts {
		old, err := cur.Lookup(strings.TrimSpace(h.Name))
		switch {
		case err != nil:
			waiting = append(waiting, strings.TrimSpace(h.Name))
		case !federation.SameHost(old, h):
			waiting = append(waiting, old.Name)
			keep = append(keep, old)
		default:
			keep = append(keep, h)
		}
	}
	d.ApplyHosts(keep)
	if len(waiting) == 0 {
		return false
	}
	slices.Sort(waiting)
	msg := "host " + strings.Join(waiting, ", ") + " changed in config.toml. The change applies after tuios config apply from outside tuios, or a daemon restart"
	d.federationMu.Lock()
	d.federationProblems = append(d.federationProblems, msg)
	d.federationMu.Unlock()
	log.Printf("[FEDERATION] %s", msg)
	return true
}

// reloadLinkPolicies applies changed link policies only where they give a
// machine less. For every machine the tables name, and for any other, the
// policy in force becomes what both the old and the new table allow.
func (d *Daemon) reloadLinkPolicies(next map[string]config.HostConfig) (waits bool) {
	var cur linkPolicyTable
	if t := d.linkPolicies.Load(); t != nil {
		cur = *t
	}
	merged := make(map[string]config.HostConfig, len(cur)+len(next)+1)
	widened := false
	peers := map[string]bool{config.LinkPolicyDefaultName: true}
	for k := range cur {
		peers[k] = true
	}
	for k := range next {
		peers[k] = true
	}
	for key := range peers {
		peer := key
		if key == config.LinkPolicyDefaultName {
			peer = ""
		}
		was, now := config.LinkPolicyFor(cur, peer), config.LinkPolicyFor(next, peer)
		allow := []string{}
		for _, c := range now.Allow {
			if was.Allows(c) {
				allow = append(allow, c)
			} else {
				widened = true
			}
		}
		hold := was.HoldMail || now.HoldMail
		if was.HoldMail && !now.HoldMail {
			widened = true
		}
		grace := min(was.HostedGrace, now.HostedGrace)
		if now.HostedGrace > was.HostedGrace {
			widened = true
		}
		h := next[key]
		h.Allow, h.HoldMail, h.HostedGrace = allow, &hold, grace.String()
		merged[key] = h
	}
	d.SetLinkPolicies(merged)
	if widened {
		log.Printf("[FEDERATION] A link policy in config.toml gives another machine more than before. That part applies after tuios config apply from outside tuios, or a daemon restart.")
	}
	return widened
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
	d.noteHostProblems(texts)

	if d.federation == nil {
		return
	}
	change := d.federation.SetTable(table)
	// A host that left the table takes its Inbox items and its cached rows
	// with it, and a host that now names another machine starts over.
	d.fleet.reconcile(table.Names(), change.Redialed)
	if !change.Changed() && len(problems) == 0 {
		return
	}
	for _, p := range problems {
		log.Printf("[FEDERATION] %v", p)
	}
	if change.Changed() {
		log.Printf("[FEDERATION] The host table changed: %s", describeTableChange(change))
		d.broadcastHostsChanged(change, nil)
	}
}

// broadcastHostsChanged tells every attached TUI client, whatever session it is
// on, that the host table changed.
//
// It exists because the client's poll for hosts stops for good once the daemon
// reports none, which is the default install, and a host added from the
// command line while a client is attached would otherwise stay invisible in
// that client until it reattached. The push costs nothing while the table is
// still: it runs from ApplyHosts and only on a change.
//
// It is also how the fleet (host_fleet.go) tells the clients that what a host
// holds changed, with the hosts named in changed, which is what lets the rail
// stop polling a host the daemon streams. An older client reads the push the
// way it always has, as a reason to list the hosts once.
func (d *Daemon) broadcastHostsChanged(change federation.TableChange, changed []string) {
	payload := &HostsChangedPayload{Added: change.Added, Removed: change.Removed, Redialed: change.Redialed, Changed: changed}
	msg, err := NewMessage(MsgHostsChanged, payload)
	if err != nil {
		debugLog("[DEBUG] broadcastHostsChanged: encode: %v", err)
		return
	}
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.isTUIClient && cs.attached
		cs.mu.Unlock()
		if !match {
			continue
		}
		d.queueBroadcast(cs, msg, "broadcastHostsChanged")
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

// hostProblemsNotice names the Inbox item about host entries that were ignored.
const hostProblemsNotice = "config.toml hosts"

// noteHostProblems opens the Inbox item that says which host entries were
// ignored and why, or closes it when none was. A host dropped for an ssh
// option that is refused is otherwise only in the log and in tuios hosts.
func (d *Daemon) noteHostProblems(problems []string) {
	if len(problems) == 0 {
		d.attention.closeConfigNotice(hostProblemsNotice)
		return
	}
	summary := "Fix [hosts] in config.toml. " + problems[0]
	if len(problems) > 1 {
		summary = fmt.Sprintf("Fix [hosts] in config.toml. tuios hosts lists %d problems. %s", len(problems), problems[0])
	}
	d.attention.noteConfigNotice(hostProblemsNotice, summary)
}
