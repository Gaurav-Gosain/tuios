//go:build !slim

package session

import (
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/herdrcli"
)

// The daemon's half of the features tuios-slim leaves out: agents, their
// mail, attention, approvals, the queue, the stash, transcripts, the herdr
// API, and links to other machines. Each function here is called from one
// place in the core daemon, and daemon_features_slim.go has the slim
// counterpart, so the slim build reaches none of this code.

// daemonFeatures is the daemon's state for the features tuios-slim leaves
// out. Daemon embeds it, so the fields read as the daemon's own.
type daemonFeatures struct {
	// linkListener is the second socket, the one `tuios stdio-proxy` dials
	// for a connection that arrived over another machine's link. Every
	// connection accepted on it is marked viaLink; see LinkSocketPath.
	linkListener net.Listener
	// linkHumanListener is the third socket, the one the proxy dials for a
	// stream the hub vouched for. See LinkHumanSocketPath.
	linkHumanListener net.Listener
	// herdrListener is the socket harnesses that speak herdr's pane state
	// protocol report to. See herdr_compat.go.
	herdrListener net.Listener
	// herdrSeqs is the highest seq each pane's herdr reporter has sent.
	herdrSeqs herdrSeqs
	// herdrEvents limits the notifications and metadata each pane sends
	// over the herdr protocol socket.
	herdrEvents paneBuckets
	// activityReports limits the report-agent-activity calls each pane
	// makes. See verbReportAgentActivity.
	activityReports paneBuckets
	// herdrConns counts each caller's open connections on the herdr
	// protocol socket. See herdrConnLimits.
	herdrConns herdrConnCount

	// federation holds the outbound links to the hosts named in config. It is
	// nil when no hosts are configured, which is the default, and every reader
	// checks. Links are outbound only: a remote daemon never dials this one and
	// never gets a channel into it (federation package, section 1 of the design
	// document).
	federation *federation.Manager
	// hostDial is DaemonConfig.HostDial, kept for setupFederation.
	hostDial federation.Dialer
	// fleet follows the agents and the Inbox of every linked host over the
	// links, so the Inbox and the host listings cover every machine. See
	// host_fleet.go.
	fleet *hostFleet
	// federationProblems are the config entries that were dropped, kept so the
	// list-hosts verb can report them instead of leaving the user to wonder
	// where a host went.
	federationProblems []string
	// hostsWaiting is set when a config reload changed [hosts] in a way that
	// dials more or gives a linked machine more, and only what narrows was
	// applied (reloadHosts, reloadLinkPolicies).
	hostsWaiting atomic.Bool

	// hostedPanes are the panes this daemon runs on another machine's behalf,
	// keyed by the id open-pane returned. They are held here rather than on a
	// Session because a hosted pane belongs to no session on this machine: the
	// session it is a window of is on the daemon that asked for it. See
	// hosted_pane.go.
	hostedPanes   map[string]*hostedPane
	hostedPanesMu sync.Mutex

	// outbox holds mail for machines whose link is down, and delivers it when
	// the link comes back. See host_outbox.go.
	outbox *hostOutbox

	// linkPolicies is the [hosts] table the policy for a machine linked to
	// this one is resolved from. Nil means no table: every link gets the
	// built-in default. See link_policy.go.
	linkPolicies linkPolicyPointer

	// agents is the cross-agent mailbox: the bounded per-session message rings
	// and the in-flight ask graph. It is held here rather than on a Session
	// because it must never reach disk: SessionState is what resurrection
	// serialises, and a message that outlived the daemon would be addressed to a
	// pane whose shell is new. See verb_mailbox.go.
	agents *agentBus

	// attention is the Inbox: every approval, question, message to the person,
	// error and unseen finished turn in every session. It is fed from the
	// session event sinks and the mailbox, and saved beside the session state.
	// See attention.go.
	attention *attentionStore

	// responds serialises the respond verb per window and remembers the last
	// prompt each window was answered on, so two clients answering the same
	// prompt get one answer through and the other refused. See verb_respond.go.
	responds respondSlots
	// fanVerifies holds the verify-fan checks running now, one per fan
	// session. See verb_fan_compare.go.
	fanVerifies fanVerifyRuns
	// respondFromShell is the [daemon] respond_from_shell grant: a caller
	// outside every pane may call respond without an attach nonce.
	respondFromShell bool

	// approvals is the [agents.approvals] policy: which harnesses may hold a
	// permission prompt for an answer from the Inbox, and for how long. It is
	// swapped whole when the config file changes. See approvals.go.
	approvals atomic.Pointer[ApprovalPolicy]
	// activity holds each agent pane's ring of hook events, and recapTests
	// the [agents.recap] test_patterns its recap reads a test run by. See
	// agent_activity.go.
	activity   *activityStore
	recapTests atomic.Pointer[[]string]
	// hostedPeer names the hosted pane a caller runs in, for pane grants.
	// Nil walks the process table (hostedPaneOfPeer); a test sets it.
	hostedPeer func(cs *connState) string

	// protocolPanes holds the windows start-agent --protocol opened, window
	// id to protocol. See agent_protocol.go.
	protocolPanes sync.Map
	// agentProtoExe finds the binary a protocol pane runs. Nil is
	// os.Executable; a test points it at a built tuios.
	agentProtoExe func() (string, error)

	// stash is the per-session file store the stash verbs write into. It is held
	// beside agents for the same reason: it must never reach disk as state, and
	// its lifetime is the session's. Unlike the ring it does put bytes on disk,
	// which is why the daemon deletes them on session deletion, on shutdown, and
	// again on the next start. See stash.go.
	stash *stashStore

	// bundles holds the worktree transfers bundle-worktree has open. Its zero
	// value is ready. See verb_bundle_worktree.go.
	bundles bundleStore

	// queue holds the messages waiting to be typed to an agent when it comes
	// to rest. Its zero value is ready. See agent_queue.go.
	queue agentQueues

	// reviewNotes holds the review notes left on panes' changes. Its zero
	// value is ready; Start loads what the last daemon saved. See
	// review_notes.go.
	reviewNotes reviewNoteStore

	// promptStallOverride replaces promptStallDefault when set. Only tests set
	// it, to keep a stall test from waiting five seconds. See prompt_gate.go.
	promptStallOverride time.Duration

	// agentStallTimeout is how long a pane may report working while producing no
	// output before the stall heuristic demotes it to idle. Zero disables the
	// heuristic. It is resolved once in NewDaemon from config or the
	// TUIOS_AGENT_STALL_SECONDS environment override.
	agentStallTimeout time.Duration

	// agentDetectInterval is how often the foreground-process auto-detector polls
	// each pane to mark or clear a running agent. Zero disables auto-detection. It
	// is resolved once in NewDaemon from config or the TUIOS_AGENT_DETECT_SECONDS
	// environment override.
	agentDetectInterval time.Duration

	// agentMatcher decides whether a pane's foreground process is a known agent
	// CLI. It merges the built-in agent binary names with any the user added.
	agentMatcher agentMatcher

	// evidenceClock is the time the detection verbs measure evidence_age_ms
	// against. Nil means time.Now; tests replace it. See agent_evidence.go.
	evidenceClock func() time.Time

	// transcriptWatcher is the one filesystem notification the transcript source
	// runs on, shared by every session. Nil when the kernel would not give the
	// daemon one, in which case every join reads on its pane's own output
	// instead and nothing else changes.
	transcriptWatcher *TranscriptWatcher

	// resumeAgents is the resolved daemon.resume_agents mode: one of the
	// resumeMode values. See agent_resume.go.
	resumeAgents string

	// pendingResumes holds the resume offers the start-up restore found, until
	// the Inbox has loaded its saved items and they can be opened without
	// taking ids the saved items already hold. See agent_resume.go.
	pendingResumes []resumeOffer
}

// listenFeatureSockets opens the link sockets other machines reach this
// daemon through, and the herdr socket.
func (d *Daemon) listenFeatureSockets(socketPath string) {
	d.linkListener = listenLinkSocket(LinkSocketPath(socketPath), "Mail from other machines is not marked.")
	// The link-human socket is optional in the same way. Without it a proxy
	// falls back to the plain link socket, and no attach through a link can
	// verify a reply from human, which is the safe way to lose it.
	d.linkHumanListener = listenLinkSocket(LinkHumanSocketPath(socketPath), "A reply from human over a link is not verified.")
	// The herdr protocol socket is optional in the same way. Without it no
	// pane is told it may report there, and the screen rules carry those
	// harnesses as before.
	if l := listenHerdrSocket(HerdrSocketPath(socketPath)); l != nil {
		d.herdrListener = l
		d.manager.SetHerdrSocket(HerdrSocketPath(socketPath))
		// herdr's command line, for a tool that runs "$HERDR_BIN_PATH"
		// pane split and the rest, is this binary run as herdr: a link
		// named herdr beside the daemon socket, as the tmux shim's is.
		// Where the link cannot be made, the binary itself answers
		// herdr's pane and notification commands. See internal/herdrcli.
		if exe, err := d.agentProtoExecutable(); err == nil {
			bin := exe
			if link, err := herdrcli.InstallLink(HerdrLinkDir(socketPath), exe); err == nil {
				bin = link
			} else {
				log.Printf("The herdr link could not be made: %v. Panes get the tuios binary as HERDR_BIN_PATH, which answers herdr's pane and notification commands only.", err)
			}
			d.manager.SetHerdrBin(bin)
		}
	}
}

// hostsWait reports whether a host or link policy change waits for the
// person.
func (d *Daemon) hostsWait() bool { return d.hostsWaiting.Load() }

// noteConfigNotice opens the Inbox item name with summary.
func (d *Daemon) noteConfigNotice(name, summary string) { d.attention.noteConfigNotice(name, summary) }

// closeConfigNotice closes the Inbox item name.
func (d *Daemon) closeConfigNotice(name string) { d.attention.closeConfigNotice(name) }

// connFeatures is the part of connState that only the full build reads.
type connFeatures struct {
	// streaming guards against a second subscribe on the same connection.
	streaming bool

	// linkPeer is the machine a link connection came from, as the link-peer
	// handshake named it; empty when it named none. linkPeerSet says the
	// handshake ran, linkPinned that the name was pinned on this machine with
	// stdio-proxy --as, and linkServed that something other than the
	// handshake has run, after which the peer can no longer be named. All
	// four are guarded by connState.mu. See link_policy.go.
	linkPeer    string
	linkPeerSet bool
	linkPinned  bool
	linkServed  bool
}

// refuseGlobalSession refuses nothing in the full build, which links to
// other machines.
func refuseGlobalSession() string { return "" }

// onPaneOutput runs on every output event of a pane: the agent reports the
// emulator parked, the pane's directory and foreground, and the agent
// detector's probe and screen look.
func (d *Daemon) onPaneOutput(s *Session, ev SessionEvent, pty *PTY) {
	// An OSC 9;4 the emulator parked while writing these same bytes. It
	// is applied before the probe and is not throttled: the sequence
	// only arrives when the harness has something to say, and it is a
	// better answer than anything the probe can work out.
	if state, ok := pty.takeAgentProgress(); ok {
		s.applyPaneProgress(ev.PTYID, ev.Window, state, d.agentMatcher.registry)
	}
	// A desktop notification the emulator parked while writing these
	// bytes, on the same terms: the harness speaking about itself.
	if n, ok := pty.takeAgentNotify(); ok {
		s.applyAgentNotify(ev.PTYID, n, d.agentMatcher.registry)
	}
	// Where the pane is.
	//
	// A shell that does not announce over OSC 7 tells nobody when
	// it changes directory, and a pane on another machine has no
	// process here to read either. The only hint is that the pane
	// printed something, which is what a prompt after a cd is.
	//
	// So the look is made here. It is paced to once a second, with
	// one more look after the pane goes quiet, and only made for a
	// pane whose bytes are arriving, so a session sitting idle pays
	// nothing. See pane_cwd_check.go.
	s.noteCwdOnOutput(pty)
	// And what it is running, on the same terms and its own
	// slower clock. A pane that has just written is a pane where
	// something may have started or finished.
	pty.remoteForeground()
	if d.agentDetectInterval > 0 && pty.probeAgentExitDue(time.Now().UnixNano()) {
		s.reconcileAgentOnOutput(ev.PTYID, d.foregroundResolver(s), d.agentMatcher.identifyDetail)
	}
	// The screen tier. Throttled like the probe, and armed to run once
	// more after the pane goes quiet: a harness waiting on a human
	// paints the prompt in its last chunk and then says nothing at
	// all, so the scan the throttle swallowed is the only one that
	// would have seen it.
	reg := d.agentMatcher.registry
	if reg != nil {
		ptyID := ev.PTYID
		// The transcript read goes first so the screen gets the last
		// word in a single pass: the file can only say working or
		// done, and a rule that can see a prompt on the pane right
		// now must be able to write over that. Running them the
		// other way round would let a read of a file the agent wrote
		// a moment ago undo a blocker the screen just matched.
		// Installed on first use, not at construction: the registry
		// belongs to the daemon and a PTY is built before the daemon
		// wires this sink. Guarded so a flooding pane does not build
		// an identical closure again on every chunk just to store it;
		// the matcher is fixed for the daemon's life, so the first
		// one stays correct.
		if !pty.hasScreenLook() {
			pty.setScreenLook(func() {
				// The output event can run before the emulator
				// has parsed its bytes, so a notification sent in
				// a pane's last chunk is picked up here, on the
				// settle look, rather than waiting for output
				// that may never come.
				if n, ok := pty.takeAgentNotify(); ok {
					s.applyAgentNotify(ptyID, n, reg)
				}
				s.readTranscriptOnOutput(ptyID)
				s.scanPaneForAgent(ptyID, reg)
			})
		}
		if pty.screenScanDue(time.Now().UnixNano()) {
			pty.runScreenLook()
		}
		pty.armScreenSettle()
	}
}

// acceptLinkLoop is acceptLoop for the link socket. A connection from it is
// served exactly like any other, with one difference: it is marked as having
// come over a link before a byte of it is read.
func (d *Daemon) acceptLinkLoop() {
	d.acceptLinkOn(d.linkListener, false)
}

// queueResumeOffers keeps the resume offers a restore at start found, for
// startFeatures to open once the Inbox has loaded.
func (d *Daemon) queueResumeOffers(offers []resumeOffer) {
	d.pendingResumes = append(d.pendingResumes, offers...)
}

// applyUserConfig applies what the daemon reads from config.toml while it
// runs. The file is the user's, and a process in a pane runs as the user and
// can write it. So from a file change (byPerson false) the parts that bound
// panes and links apply only where they narrow: [agents.permissions], the
// link policies of [hosts], and the hosts the daemon dials, since ssh runs
// what a host entry or ~/.ssh/config says as the daemon's child. What widens
// waits for tuios config apply from outside every pane (byPerson true), or a
// daemon restart.
func (d *Daemon) applyUserConfig(cfg *config.UserConfig, byPerson bool) {
	d.manager.SetPreferredShell(cfg.Appearance.PreferredShell)
	d.manager.SetHerdrProtocol(cfg.Agents.HerdrProtocol)
	d.SetApprovalPolicy(ApprovalPolicyFromConfig(cfg.Agents.Approvals))
	d.SetRecapTestPatterns(cfg.Agents.Recap.Resolved().TestPatterns)
	d.SetQueueMax(cfg.Agents.Queue.MaxEntries())
	perms := PanePermissionsFromConfig(cfg.Agents.Permissions)
	if byPerson {
		d.manager.SetPanePermissions(perms)
		// A policy change applies to the next call on every link, including
		// links already open, so tightening it does not wait for a reconnect.
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

// onSessionRenamed moves what the daemon keeps by session name to the new
// name, and tells every event reader and linked machine to list again. It runs on
// the manager's rename hook, after the session and its state carry the name.
func (d *Daemon) onSessionRenamed(s *Session, old string) {
	name := s.Name()
	d.agents.rename(old, name)
	d.attention.renameSession(old, name)
	d.renameQueuedSession(old, name)
	d.manager.grants.renameSession(old, name)
	// A listing reader has no event for a rename. The old name closes and the
	// new one opens, which is what every reader, a linked machine's fleet
	// cache included, already handles by listing again.
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: old})
	d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
}

// onSessionDeleted publishes a session-closed event and tells every client
// attached to the session that it is gone. It runs on the manager's delete hook,
// so every deletion path (the kill-session verb, the legacy kill message, and
// any internal teardown) notifies clients through one place.
//
// Without this a killed session leaves its clients attached to nothing: their
// PTYs are closed and their windows are gone, but the socket stays open, so the
// client sits in a dead session with no way to learn what happened.
func (d *Daemon) onSessionDeleted(s *Session) {
	d.forgetLatest(s.ID)
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: s.Name()})
	// A session with no windows has no inboxes, so its ring is dropped with it.
	d.agents.forget(s.Name())
	// And so are its panes' activity rings.
	d.activity.forgetSession(s.ID)
	// And nothing in it is waiting for anybody any more.
	d.attention.closeSession(s.Name())
	// And no message waits for an agent in it.
	d.forgetQueuedSession(s.Name())
	// And its stashed files go with it. This is the lifetime the stash promises,
	// and it runs on the manager's delete hook, so every path that kills a
	// session takes the files with it.
	d.stash.forget(s.ID)
	d.broadcastToSession(s.ID, MsgSessionEnded, &SessionEndedPayload{
		SessionName: s.Name(),
		Reason:      "the session was terminated",
	}, "")
}

// defaultAgentStallTimeout is the conservative default silence window before a
// pane that reported working but never reported anything after is assumed idle.
// It is long on purpose: the heuristic is a fallback for agents that do not
// report, and demoting a genuinely-busy-but-quiet pane too eagerly is worse than
// leaving it looking busy a little longer.
const defaultAgentStallTimeout = 30 * time.Second

// defaultAgentDetectInterval is how often the foreground-process auto-detector
// polls each pane. It is modest on purpose: agent presence changes on a human
// timescale, and a per-pane /proc read every couple of seconds is cheap.
const defaultAgentDetectInterval = 2 * time.Second

// agentDetectQuietBound is the longest a quiet pane goes between two reads of
// its foreground process. A pane that has printed nothing since it was last
// read, and holds no agent, is read only every few ticks: nothing a person
// starts in it goes unechoed, so output is what says it is worth reading
// again, and this bound covers a program started with no output at all. At the
// default two-second tick it is every fifth tick. See PTY.detectScanDue.
const agentDetectQuietBound = 10 * time.Second

// listenLinkSocket opens one of the optional link sockets, owner only, and
// returns nil after logging what is lost when it cannot. The start lock is
// held, so a stale file at path is a dead daemon's and is removed.
func listenLinkSocket(path, lost string) net.Listener {
	_ = os.Remove(path)
	ll, err := net.Listen("unix", path)
	if err != nil {
		log.Printf("The link socket %s could not be opened: %v. %s", path, err, lost)
		return nil
	}
	if ul, ok := ll.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := os.Chmod(path, 0700); err != nil { //nolint:gosec // a socket, owner only; the execute bit means nothing on it
		_ = ll.Close()
		_ = os.Remove(path)
		log.Printf("The link socket %s could not be secured: %v. %s", path, err, lost)
		return nil
	}
	return ll
}
