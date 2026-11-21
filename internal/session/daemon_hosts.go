package session

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

func (d *Daemon) startHostsWatch() {
	if d.configPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(d.configPath), 0o750); err != nil {
		log.Printf("[FEDERATION] The daemon cannot watch the config file. A host change needs a restart: %v", err)
		return
	}
	w, err := config.NewWatcherWithOptions(d.configPath, d.onConfigReload, config.WatcherOptions{
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

func (d *Daemon) stopHostsWatch() {
	d.federationMu.Lock()
	w := d.hostsWatcher
	d.hostsWatcher = nil
	d.federationMu.Unlock()
	if w != nil {
		w.Stop()
	}
}

func (d *Daemon) onConfigReload(cfg *config.UserConfig, err error) {
	if err != nil {
		log.Printf("[FEDERATION] The config file has an error, so the hosts did not change: %v", err)
		return
	}
	d.applyUserConfig(cfg, false)
}

func (d *Daemon) applyUserConfig(cfg *config.UserConfig, byPerson bool) {
	d.SetAgentsEnabled(cfg.Agents.On())
	d.SetMachineOffer(cfg.Notifications.MachineOfferOn())
	d.singleClient.Store(cfg.Daemon.SingleClient)
	d.SetSSHAgent(cfg.Daemon.SSHAgent)
	d.SetDisplay(cfg.Daemon.Display)
	d.SetKeepAwake(cfg.Daemon.KeepAwake)
	d.forgetHostForwards()
	d.agentPruneHosts(HostsFromConfig(cfg))
	d.manager.SetPreferredShell(cfg.Appearance.PreferredShell)
	d.manager.SetShellIntegration(cfg.Shell.Integration)
	d.manager.SetShellLogin(cfg.Shell.Login)
	d.manager.SetHerdrProtocol(cfg.Agents.HerdrProtocol)
	d.acpVersion.Store(int32(cfg.Agents.ACPVersionOr()))
	d.manager.SetReturnWhenEmpty(cfg.Workspaces.ReturnsWhenEmpty())
	d.manager.SetDragAndDrop(cfg.Panes.DragAndDrop)
	d.manager.SetScrollbackLines(cfg.Appearance.ScrollbackLines)
	d.manager.SetNewWindowInheritCwd(newWindowInheritCwd(cfg))
	d.SetWorktreePath(cfg.Worktree.Path)
	d.setups.waiting.Store(!d.setups.applyConfig(cfg.Worktree.Setup, cfg.Worktree.SetupAsync, byPerson))
	d.reloadHooks(cfg, byPerson)
	if d.buffers != nil {
		d.buffers.SetLimits(cfg.PasteBuffers.Resolved())
	}
	approvals, approvalsWait := ApprovalPolicyFromConfig(cfg.Agents.Approvals), false
	if !byPerson {
		approvals, approvalsWait = narrowApprovalPolicy(d.approvalPolicy(), approvals)
		if approvalsWait {
			log.Printf("The new [agents.approvals.risk] gives panes more than before. The risk rules in force stay, and panes may not allow a risky call. The rest applies after tuios config apply from outside tuios, or a daemon restart.")
		}
	}
	d.SetApprovalPolicy(approvals)
	d.approvalsWaiting.Store(approvalsWait)
	d.SetRecapTestPatterns(cfg.Agents.Recap.Resolved().TestPatterns)
	d.SetQueueMax(cfg.Agents.Queue.MaxEntries())
	d.SetAgentSilentAfter(resolveSilentAfter(silentAfterFromConfig(cfg.Agents)))
	wake := cfg.Agents.Mail.WakeOn()
	if wake && !byPerson && !d.mailWake.on.Load() {
		wake = false
		log.Printf("[agents.mail] wake = true in the file waits for tuios config apply from outside tuios, or a daemon restart. The wake stays off.")
	}
	d.SetMailWake(wake)
	toParent := cfg.Agents.Mail.QuestionsToParentOn()
	if toParent && !byPerson && d.attention != nil && !d.attention.parentFirst.Load() {
		toParent = false
		log.Printf("[agents.mail] questions_to_parent = true in the file waits for tuios config apply from outside tuios, or a daemon restart. A child's question goes to the Inbox at once until then.")
	}
	d.SetChildQuestionsToParent(toParent)
	d.SetCheckpoints(cfg.Agents.Checkpoints)
	d.SetAgentPresets(cfg.Agents.Presets)
	d.replyMarkers.Store(cfg.Agents.ReplyMarkersOn())
	d.taskAutoOff.Store(!cfg.Tasks.AutoStageOn())
	d.lifecycleOff.Store(!cfg.Tasks.LifecycleOn())
	d.machineCap.Store(int64(max(cfg.Tasks.MachineCap, 0)))
	d.setAttachLimits(cfg.Tasks)
	d.notify.reload(cfg.Notify, byPerson)
	perms := PanePermissionsFromConfig(cfg.Agents.Permissions)
	pluginsWait := d.plugins != nil && d.plugins.apply(cfg.Plugins, byPerson)
	d.pluginsWaiting.Store(pluginsWait)
	if byPerson {
		d.manager.SetPanePermissions(perms)
		d.SetLinkPolicies(cfg.Hosts)
		d.ApplyHosts(HostsFromConfig(cfg))
		d.hostsWaiting.Store(false)
		d.recordAppliedGrants(perms)
		d.noteConfigWaiting()
		return
	}
	d.reloadPanePermissions(perms)
	policyWaits := d.reloadLinkPolicies(cfg.Hosts)
	hostsWait := d.reloadHosts(HostsFromConfig(cfg))
	d.hostsWaiting.Store(policyWaits || hostsWait)
	d.noteConfigWaiting()
}

func (d *Daemon) noteConfigWaiting() {
	if d.configWaiting() {
		d.attention.noteConfigNotice(configWaitsNotice, configWaitsNote)
		return
	}
	d.attention.closeConfigNotice(configWaitsNotice)
}

func (d *Daemon) configWaiting() bool {
	return d.manager.grants.restartNeeded.Load() || d.hostsWaiting.Load() || d.notify.waitingApply() || d.pluginsWaiting.Load() || d.approvalsWaiting.Load() || d.setups.waiting.Load() || d.hooksWaiting.Load()
}

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
		roots, rootsWiden := narrowFilesRoots(was, now)
		if rootsWiden {
			widened = true
		}
		readRoots, readWiden := narrowReadRoots(was, now)
		if readWiden {
			widened = true
		}
		minFree := max(was.FilesMinFree, now.FilesMinFree)
		if now.FilesMinFree < was.FilesMinFree {
			widened = true
		}
		bound := func(n int64) int64 {
			if n <= 0 {
				return math.MaxInt64
			}
			return n
		}
		maxEntries := now.FilesMaxEntries
		if bound(now.FilesMaxEntries) > bound(was.FilesMaxEntries) {
			maxEntries = was.FilesMaxEntries
			widened = true
		}
		h := next[key]
		h.Allow, h.HoldMail, h.HostedGrace, h.FilesRoots = allow, &hold, grace.String(), roots
		h.FilesReadRoots, h.FilesMinFree, h.FilesMaxEntries = readRoots, strconv.FormatInt(minFree, 10), &maxEntries
		merged[key] = h
	}
	d.storeLinkPolicies(merged, next)
	if d.transfers != nil {
		d.transfers.failRevoked()
	}
	if widened {
		log.Printf("[FEDERATION] A link policy in config.toml gives another machine more than before. That part applies after tuios config apply from outside tuios, or a daemon restart.")
	}
	return widened
}

func narrowFilesRoots(was, now config.LinkPolicy) (roots []string, widens bool) {
	a, b := linkRootPaths(was), linkRootPaths(now)
	roots = []string{}
	add := func(r string) {
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	for _, r := range b {
		if slices.ContainsFunc(a, func(w string) bool { return pathUnder(r, w) }) {
			add(r)
		} else {
			widens = true
		}
	}
	for _, r := range a {
		if slices.ContainsFunc(b, func(n string) bool { return pathUnder(r, n) }) {
			add(r)
		}
	}
	return roots, widens
}

func narrowReadRoots(was, now config.LinkPolicy) (roots []string, widens bool) {
	switch {
	case now.FilesReadRoots == nil && was.FilesReadRoots == nil:
		return nil, false
	case was.FilesReadRoots == nil:
		return linkPathsOf(now.FilesReadRoots), false
	case now.FilesReadRoots == nil:
		return linkPathsOf(was.FilesReadRoots), true
	}
	a, b := linkPathsOf(was.FilesReadRoots), linkPathsOf(now.FilesReadRoots)
	roots = []string{}
	add := func(r string) {
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	for _, r := range b {
		if slices.ContainsFunc(a, func(w string) bool { return pathUnder(r, w) }) {
			add(r)
		} else {
			widens = true
		}
	}
	for _, r := range a {
		if slices.ContainsFunc(b, func(n string) bool { return pathUnder(r, n) }) {
			add(r)
		}
	}
	return roots, widens
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

func (d *Daemon) hasHosts() bool {
	return d.federation != nil && d.federation.Table().Len() > 0
}

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

const hostProblemsNotice = "config.toml hosts"

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
