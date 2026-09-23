package session

import (
	"os"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/agentproto"
)

// Protocol panes: an agent start-agent runs headless, over ACP or the Codex
// app-server protocol, with `tuios agent-proto` as the pane's process.
//
// The daemon's part is small, because the pane program does the talking (see
// internal/agentproto). start-agent --protocol runs this daemon's own binary as
// `tuios agent-proto --protocol P --harness H -- <agent argv>` in place of the
// agent, and remembers the window as a protocol pane. The pane program reports
// the agent's state for its own pane with set-agent-state, as a hook does, and
// holds a permission for the Inbox with request-approval.
//
// The one thing the mark changes is request-approval's policy check: a
// protocol pane may hold a permission for the Inbox without its harness in
// [agents.approvals] enabled. That table is the opt-in for letting a hook
// answer a harness's own prompt; a protocol pane has no prompt of its own
// beyond the pane program's, and choosing --protocol is the opt-in. Every other
// rule of request-approval still holds: the caller must be in that pane, the
// pane must be on needs_input with kind approval, it must not be in front of
// the person, and the line must be shown whole; and only reply-approval, with a
// human nonce, can answer.
//
// The mark lives in the daemon, keyed by window id. No caller can set it, and
// it goes with the window. It is not saved: a pane a restart brings back runs
// its command again, and until then its approvals follow the table like any
// other pane's.

// markProtocolPane records a protocol pane.
func (d *Daemon) markProtocolPane(windowID, protocol string) {
	d.protocolPanes.Store(windowID, protocol)
}

// unmarkProtocolPane forgets a window's mark.
func (d *Daemon) unmarkProtocolPane(windowID string) {
	d.protocolPanes.Delete(windowID)
}

// paneProtocol is the protocol a window's agent speaks, empty for a window
// start-agent --protocol did not open.
func (d *Daemon) paneProtocol(windowID string) string {
	if v, ok := d.protocolPanes.Load(windowID); ok {
		p, _ := v.(string)
		return p
	}
	return ""
}

// agentProtoExecutable is the binary that runs `tuios agent-proto`: this
// daemon's own, so the pane program always matches the daemon it reports to.
func (d *Daemon) agentProtoExecutable() (string, error) {
	if d.agentProtoExe != nil {
		return d.agentProtoExe()
	}
	return os.Executable()
}

// checkProtocol refuses a protocol tuios does not speak.
func checkProtocol(protocol string) *verbError {
	if agentproto.CheckProtocol(protocol) != nil {
		return invalidParam("protocol", "protocol: "+echoName(protocol)+" is not a protocol tuios speaks", agentproto.Protocols...)
	}
	return nil
}

// protocolAgentArgv is the agent's argv in a protocol pane: the resolved
// program with the spec's arguments, then extra. For codex the app-server
// subcommand goes between the two when neither names it, so the agent codex is
// enough and args are the app-server's own.
func protocolAgentArgv(protocol string, base, extra []string) []string {
	argv := slices.Clone(base)
	if protocol == agentproto.ProtocolCodex && !slices.Contains(argv[1:], "app-server") && !slices.Contains(extra, "app-server") {
		argv = append(argv, "app-server")
	}
	return append(argv, extra...)
}

// protocolArgv is the argv a protocol pane runs: the pane program, then the
// agent's argv after --, which the pane program runs as it is.
func (d *Daemon) protocolArgv(protocol, harness string, agentArgv []string) ([]string, *verbError) {
	if verr := checkProtocol(protocol); verr != nil {
		return nil, verr
	}
	exe, err := d.agentProtoExecutable()
	if err != nil {
		return nil, hintedVerbError(ErrVerbInternal, "could not find the tuios binary to run the protocol pane: "+err.Error(), nil)
	}
	argv := agentArgv
	out := []string{exe, "agent-proto", "--protocol", protocol}
	if harness = strings.TrimSpace(harness); harness != "" {
		out = append(out, "--harness", harness)
	}
	out = append(out, "--")
	return append(out, argv...), nil
}
