//go:build slim

package session

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/edition"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// The slim counterparts of daemon_features_full.go. tuios-slim has no
// agents, Inbox, queue, stash, transcripts, herdr API or links to other
// machines, so each hook the core daemon calls does nothing, or only the
// part of it that is core.

// daemonFeatures holds stand-ins for the feature state the core daemon
// touches as it starts, runs and stops. Each is nil or empty in tuios-slim,
// and each method below does nothing, so the core code reads the same in
// both builds and the slim build links none of the real features.
type daemonFeatures struct {
	hostDial          federation.Dialer
	fleet             *noFleet
	federation        *noFederation
	attention         *noAttention
	activity          *noActivity
	stash             *noStash
	outbox            *noOutbox
	reviewNotes       noReviewNotes
	transcriptWatcher *noTranscripts
	pendingResumes    []resumeOffer
	linkListener      net.Listener
	linkHumanListener net.Listener
	herdrListener     net.Listener
	hostedPanes       map[string]*hostedPane
	hostedPanesMu     sync.Mutex

	agents              *noAgentBus
	agentMatcher        noMatcher
	agentStallTimeout   time.Duration
	agentDetectInterval time.Duration
	respondFromShell    bool
	resumeAgents        string
}

type (
	noAgentBus struct{}
	noMatcher  struct{}
)

func newAgentBus() *noAgentBus                                        { return nil }
func newAgentMatcher([]string) noMatcher                              { return noMatcher{} }
func resolveAgentStallTimeout(time.Duration) time.Duration            { return 0 }
func resolveAgentBinaries([]string) []string                          { return nil }
func resolveAgentDetectInterval(*bool, time.Duration) time.Duration   { return 0 }
func resolveResumeMode(string) string                                 { return "" }
func newAttentionStore(func(streamEvent), func() uint64) *noAttention { return nil }
func newActivityStore(func(streamEvent)) *noActivity                  { return nil }
func newHostOutbox(*Daemon) *noOutbox                                 { return nil }
func newStashStore(func() string) *noStash                            { return nil }
func (d *Daemon) SetApprovalPolicy(ApprovalPolicy)                    {}
func (d *Daemon) SetRecapTestPatterns([]string)                       {}
func (d *Daemon) SetLinkPolicies(map[string]config.HostConfig)        {}
func (d *Daemon) SetQueueMax(int)                                     {}

// NewTranscriptWatcher: tuios-slim follows no agent transcript.
func NewTranscriptWatcher() (*noTranscripts, error) { return nil, errNoTranscripts }

var errNoTranscripts = edition.Missing("Agent transcripts")

func (s *Session) SetTranscriptWatcher(*noTranscripts)                    {}
func (s *Session) SetFederation(*noFederation)                            {}
func (s *Session) SetRemotePaneHook(func(windowID string, p *remotePane)) {}
func (d *Daemon) serveHostedCalls(*Session, string, *remotePane)          {}

type (
	noFleet       struct{}
	noFederation  struct{}
	noAttention   struct{}
	noActivity    struct{}
	noStash       struct{}
	noOutbox      struct{}
	noReviewNotes struct{}
	noTranscripts struct{}
)

// hostedPane stands in for a pane this machine runs for another one.
// tuios-slim never runs one.
type hostedPane struct {
	cmd *exec.Cmd
}

func (*noFleet) start(context.Context)                              {}
func (*noFleet) stop()                                              {}
func (*noFederation) Start(context.Context)                         {}
func (*noFederation) Stop()                                         {}
func (*noAttention) load(string, func(session, window string) bool) {}
func (*noAttention) noteSessionEvent(string, SessionEvent)          {}
func (*noAttention) saveNowAndFreeze()                              {}
func (*noActivity) noteSessionEvent(*Session, SessionEvent)         {}
func (*noStash) sweep()                                             {}
func (*noOutbox) load(string)                                       {}
func (noReviewNotes) load(string, func(window string) bool)         {}
func (noReviewNotes) noteSessionEvent(SessionEvent)                 {}
func (noReviewNotes) saveNowAndFreeze()                             {}
func (*noTranscripts) Close() error                                 { return nil }

func newHostFleet(*Daemon) *noFleet                 { return nil }
func (d *Daemon) setupFederation([]federation.Host) {}
func attentionPath() string                         { return "" }
func outboxPath() string                            { return "" }
func reviewNotesPath() string                       { return "" }
func (d *Daemon) attentionLive(string, string) bool { return false }
func (d *Daemon) configProblems() []string          { return nil }
func (d *Daemon) noteHostProblems([]string)         {}
func (d *Daemon) acceptLinkLoop()                   {}
func (d *Daemon) acceptHerdrLoop(net.Listener)      {}
func (d *Daemon) closeHostedPanes()                 {}

// noteQueueEvent: tuios-slim queues no message for an agent.
func (d *Daemon) noteQueueEvent(string, SessionEvent) {}

func (d *Daemon) listenFeatureSockets(string)     {}
func (d *Daemon) hostsWait() bool                 { return false }
func (d *Daemon) noteConfigNotice(string, string) {}
func (d *Daemon) closeConfigNotice(string)        {}

// onPaneOutput keeps the one core part of the full hook: the pane's
// directory, looked at when it prints. Agent reports the emulator parked are
// never read, so tuios-slim ignores them.
func (d *Daemon) onPaneOutput(s *Session, _ SessionEvent, pty *PTY) {
	s.noteCwdOnOutput(pty)
}

// connFeatures is empty in tuios-slim, which has no subscribe verb and no
// links. See daemon_features_full.go.
type connFeatures struct{} //nolint:unused // embedded in connState so both builds share one struct

// resumeOffer stands in for a pane that a restore could resume. tuios-slim
// resumes no agent conversation, so it never holds one.
type resumeOffer struct{}

// refuseGlobalSession refuses new --global from a full client: a global
// session holds panes from more than one machine, and tuios-slim links to no
// other machine. The slim CLI refuses the flag itself.
func refuseGlobalSession() string { return edition.MissingMessage("new --global") }

// applyOneHost: tuios-slim dials no hosts.
func (d *Daemon) applyOneHost(*config.UserConfig, string) *verbError {
	return newVerbError(ErrVerbCommandFailed, edition.MissingMessage("apply-config with a host"))
}

func (d *Daemon) snapshotHosts(*configSnapshot) {}

// inputProfileFor is the default input profile in tuios-slim, which knows no
// harness: a prompt is pasted and submitted with a carriage return.
func (d *Daemon) inputProfileFor(*Session, string) harness.InputProfile {
	return harness.DefaultInputProfile()
}

// tuios-slim accepts no connection over a link, so there is no link policy
// to hold one to, and no pane runs here for another machine.

func (d *Daemon) checkLinkVerb(*connState, string) *verbError         { return nil }
func (d *Daemon) checkLinkMessage(*connState, MessageType) *verbError { return nil }
func markLinkServed(*connState)                                       {}

func (d *Daemon) forwardHostedCall(*connState, string, json.RawMessage) (any, *verbError, bool) {
	return nil, nil, false
}

// checkWindowHost accepts only this machine in tuios-slim, which opens no
// window on another one.
func checkWindowHost(_ *Daemon, host *string) *verbError {
	switch *host {
	case "", "local":
		*host = ""
		return nil
	}
	return newVerbError(ErrVerbInvalidParams, edition.MissingMessage("A window on another machine"))
}

// remoteListing: tuios-slim has no link to list another machine's folder.
func (d *Daemon) remoteListing(_, dir string, _ int) *DirListingPayload {
	return &DirListingPayload{Dir: dir, Err: edition.MissingMessage("A folder on another machine")}
}

// watchRemoteDir: tuios-slim has no link to watch another machine's folder.
func (d *Daemon) watchRemoteDir(string, string, <-chan struct{}, func()) error {
	return errNoFederation
}

// tuios-slim offers to resume no agent conversation.

func (d *Daemon) queueResumeOffers([]resumeOffer)                        {}
func (d *Daemon) applyResumeOffers([]resumeOffer)                        {}
func (d *Daemon) resumeOfferFor(string, WindowState) (resumeOffer, bool) { return resumeOffer{}, false }

// ApprovalPolicy is empty in tuios-slim, which holds no approval.
type ApprovalPolicy struct{}

// ApprovalPolicyFromConfig reads nothing in tuios-slim.
func ApprovalPolicyFromConfig(config.ApprovalsConfig) ApprovalPolicy { return ApprovalPolicy{} }

// tuios-slim runs no pane for another machine.

func (d *Daemon) addressesHostedPane(string, json.RawMessage) bool { return false }
func (d *Daemon) hostedPaneOfPeer(*connState) string               { return "" }

// applyUserConfig applies what tuios-slim reads from config.toml while it
// runs: the preferred shell and [agents.permissions]. From a file change
// (byPerson false) the permissions apply only where they narrow, as in the
// full build. The rest of the file is for features this build leaves out.
func (d *Daemon) applyUserConfig(cfg *config.UserConfig, byPerson bool) {
	d.manager.SetPreferredShell(cfg.Appearance.PreferredShell)
	perms := PanePermissionsFromConfig(cfg.Agents.Permissions)
	if byPerson {
		d.manager.SetPanePermissions(perms)
		d.recordAppliedGrants(perms)
		d.noteConfigWaiting()
		return
	}
	d.reloadPanePermissions(perms)
	d.noteConfigWaiting()
}

// onSessionRenamed moves the session's grants to its new name. A listing
// reader has no event for a rename, so the old name closes and the new one
// opens.
func (d *Daemon) onSessionRenamed(s *Session, old string) {
	name := s.Name()
	d.manager.grants.renameSession(old, name)
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: old})
	d.events.publish(streamEvent{Type: EventSessionCreated, Session: name})
}

// onSessionDeleted tells the clients of a deleted session that it ended.
func (d *Daemon) onSessionDeleted(s *Session) {
	d.forgetLatest(s.ID)
	d.events.publish(streamEvent{Type: EventSessionClosed, Session: s.Name()})
	d.broadcastToSession(s.ID, MsgSessionEnded, &SessionEndedPayload{
		SessionName: s.Name(),
		Reason:      "the session was terminated",
	}, "")
}

// The agent monitors do not run in tuios-slim.

func (d *Daemon) stallMonitor() {}
func (d *Daemon) agentMonitor() {}
