package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// What a machine linked to this one may do here.
//
// Every connection that arrives on a link socket (daemon.go) is marked viaLink
// before a byte of it is read, and every verb and every binary message on such
// a connection is checked here against the policy for the machine it came
// from, before its handler runs. The policy comes from the [hosts] table
// (internal/config's link_policy.go): the built-in default, then
// [hosts."*"], then [hosts.PEER].
//
// The peer is named by the link-peer handshake, which `tuios stdio-proxy`
// sends as the first line of every connection it opens for a link: the name
// the hub gave for itself in the stream's open frame, or the name the proxy
// was pinned to with --as, which a forced command in authorized_keys fixes. A
// connection names its peer at most once and before anything else, so the
// bytes the hub relays after the handshake cannot rename it. A link from a
// proxy too old to send the handshake has no name and gets [hosts."*"].
//
// How each limit is enforced:
//
//   - Every JSON verb has an entry in verbCapabilities, and a test holds the
//     table to the registry, so a verb added without one fails the build's
//     tests rather than being reachable from a link unchecked. A verb missing
//     from the table at run time is refused, never allowed.
//   - Every binary message type has an entry in msgCapabilities, with the same
//     refuse-when-missing rule. An attach needs list and write, because an
//     attached client reads every pane and types into them.
//   - open-host-connection, which relays a connection on to this machine's
//     own links, needs every capability: the next machine sees the relayed
//     connection as coming from this one and applies this machine's grant, so
//     a peer allowed less here must not borrow it.
//   - Nothing here widens anything. A refused call does nothing, and the
//     checks that already applied to link connections (human_origin.go, the
//     stash rule for attachments, the link mail caps) still run after this
//     one.

// linkPolicyVerb is the handshake verb. It is not a capability of its own: it
// only names the peer, and only on a link connection that has not named one.
const linkPolicyVerb = "link-peer"

// capRelay marks open-host-connection, which needs every capability.
const capRelay = "*"

// verbCapabilities is what each verb needs on a link connection. An empty list
// needs nothing.
var verbCapabilities = map[string][]string{
	"hello":      nil,
	"list-verbs": nil,
	"link-peer":  nil,
	// restrict-connection only gives up authority on the calling
	// connection, so it needs nothing.
	"restrict-connection": nil,
	// pane-grants only reports; over a link it says no pane grants apply.
	// set-pane-grants is refused over a link by its handler whatever the
	// policy, and needs every capability here as well.
	"pane-grants":     nil,
	"set-pane-grants": {capRelay},

	"list-hooks":           {config.LinkAllowList},
	"list-dock-components": {config.LinkAllowList},
	"list-sessions":        {config.LinkAllowList},
	"list-worktrees":       {config.LinkAllowList},
	"list-hosts":           {config.LinkAllowList},
	"list-host-sessions":   {config.LinkAllowList},
	"list-host-agents":     {config.LinkAllowList},
	"session-info":         {config.LinkAllowList},
	"list-windows":         {config.LinkAllowList},
	"get-window":           {config.LinkAllowList},
	"list-workspaces":      {config.LinkAllowList},
	"capture-pane":         {config.LinkAllowList},
	"screenshot":           {config.LinkAllowList},
	"list-options":         {config.LinkAllowList},
	"list-themes":          {config.LinkAllowList},
	"list-glyphs":          {config.LinkAllowList},
	"get-option":           {config.LinkAllowList},
	"subscribe":            {config.LinkAllowList},
	"unsubscribe":          {config.LinkAllowList},
	"get-agent-state":      {config.LinkAllowList},
	"resolve-pane":         {config.LinkAllowList},
	"explain-agent-detect": {config.LinkAllowList},
	"explain-agent-screen": {config.LinkAllowList},
	"wait-for":             {config.LinkAllowList},
	"list-agents":          {config.LinkAllowList},
	"list-attention":       {config.LinkAllowList},
	"peek-prompt":          {config.LinkAllowList},
	"read-dir":             {config.LinkAllowList},

	"send-agent-message":  {config.LinkAllowMail},
	"read-agent-messages": {config.LinkAllowMail},
	"stash-put":           {config.LinkAllowMail},
	"stash-list":          {config.LinkAllowMail},
	"stash-get":           {config.LinkAllowMail},

	"new-session":  {config.LinkAllowOpen},
	"new-worktree": {config.LinkAllowOpen},
	"fan":          {config.LinkAllowOpen},
	"start-agent":  {config.LinkAllowOpen},
	"new-window":   {config.LinkAllowOpen},
	"split-window": {config.LinkAllowOpen},
	"popup":        {config.LinkAllowOpen},
	"open-pane":    {config.LinkAllowOpen},
	"resize-pane":  {config.LinkAllowOpen},
	"close-pane":   {config.LinkAllowOpen},
	"pane-cwd":     {config.LinkAllowOpen},
	"pane-agent":   {config.LinkAllowOpen},
	"pane-calls":   {config.LinkAllowOpen},

	"refresh-dock":    {config.LinkAllowWrite},
	"remove-worktree": {config.LinkAllowWrite},
	// bundle-worktree reads a worktree's files out, which write already
	// reaches through a shell, and list must not.
	"bundle-worktree":     {config.LinkAllowWrite},
	"focus-window":        {config.LinkAllowWrite},
	"move-window":         {config.LinkAllowWrite},
	"set-window":          {config.LinkAllowWrite},
	"select-workspace":    {config.LinkAllowWrite},
	"set-layout":          {config.LinkAllowWrite},
	"run-command":         {config.LinkAllowWrite},
	"close-window":        {config.LinkAllowWrite},
	"send-keys":           {config.LinkAllowWrite},
	"send-text":           {config.LinkAllowWrite},
	"ask-agent":           {config.LinkAllowWrite},
	"resize":              {config.LinkAllowWrite},
	"kill-session":        {config.LinkAllowWrite},
	"set-option":          {config.LinkAllowWrite},
	"set-session-name":    {config.LinkAllowWrite},
	"set-session-accent":  {config.LinkAllowWrite},
	"set-workspace-name":  {config.LinkAllowWrite},
	"set-workspace-order": {config.LinkAllowWrite},
	"set-agent-state":     {config.LinkAllowWrite},
	"set-agent-session":   {config.LinkAllowWrite},
	"set-agent-meta":      {config.LinkAllowWrite},
	"resume-agent":        {config.LinkAllowWrite},
	"request-approval":    {config.LinkAllowWrite},
	"run":                 {config.LinkAllowWrite},
	// ask-human's own handler refuses every link caller as well.
	"ask-human": {config.LinkAllowWrite},

	"respond":               {config.LinkAllowRespond},
	"reply-approval":        {config.LinkAllowRespond},
	"dismiss-attention":     {config.LinkAllowRespond},
	"release-agent-message": {config.LinkAllowRespond},
	"answer-ask":            {config.LinkAllowRespond},

	"open-host-connection": {capRelay},

	// The agent review, triage, queue and approval work. compare-fan,
	// agent-activity, list-queued and get-approval return states, counts and
	// text an agent wrote, which list reaches. review-diff returns file
	// contents, which write already reaches through a shell and list must
	// not, as bundle-worktree does. verify-fan opens a window and runs a
	// command in it, so it needs open and write. mark-attention is the
	// person's act, like dismiss-attention.
	"compare-fan":    {config.LinkAllowList},
	"agent-activity": {config.LinkAllowList},
	"list-queued":    {config.LinkAllowList},
	"get-approval":   {config.LinkAllowList},
	"review-diff":    {config.LinkAllowWrite},
	"review-note":    {config.LinkAllowWrite},
	"send-review":    {config.LinkAllowWrite},
	"queue-prompt":   {config.LinkAllowWrite},
	"cancel-queued":  {config.LinkAllowWrite},
	"keep-fan":       {config.LinkAllowWrite},
	"verify-fan":     {config.LinkAllowOpen, config.LinkAllowWrite},
	"mark-attention": {config.LinkAllowRespond},
}

// msgCapabilities is what each binary message needs on a link connection.
var msgCapabilities = map[MessageType][]string{
	MsgHello:            nil,
	MsgDetach:           nil,
	MsgList:             {config.LinkAllowList},
	MsgSubscribePTY:     {config.LinkAllowList},
	MsgUnsubscribePTY:   {config.LinkAllowList},
	MsgGetTerminalState: {config.LinkAllowList},
	MsgReadDir:          {config.LinkAllowList},
	MsgGetLogs:          {config.LinkAllowList},
	MsgAttach:           {config.LinkAllowList, config.LinkAllowWrite},
	MsgInput:            {config.LinkAllowWrite},
	MsgResize:           {config.LinkAllowWrite},
	MsgClosePTY:         {config.LinkAllowWrite},
	MsgUpdateState:      {config.LinkAllowWrite},
	MsgExecuteCommand:   {config.LinkAllowWrite},
	MsgCommandResult:    {config.LinkAllowWrite},
	MsgKill:             {config.LinkAllowWrite},
	MsgNew:              {config.LinkAllowOpen, config.LinkAllowList, config.LinkAllowWrite},
	MsgCreatePTY:        {config.LinkAllowOpen},
	MsgResurrect:        {config.LinkAllowOpen},
}

// linkPolicyTable is the [hosts] table the policies are resolved from. It is
// swapped whole on a config reload.
type linkPolicyTable = map[string]config.HostConfig

// SetLinkPolicies replaces the table link policies are resolved from. A call
// in flight keeps the policy it resolved; the next call reads the new one.
func (d *Daemon) SetLinkPolicies(hosts map[string]config.HostConfig) {
	t := maps.Clone(hosts)
	d.linkPolicies.Store(&t)
}

// linkPolicy is the policy for the machine at the other end of cs. It is only
// meaningful for a link connection.
func (d *Daemon) linkPolicy(cs *connState) config.LinkPolicy {
	var hosts linkPolicyTable
	if t := d.linkPolicies.Load(); t != nil {
		hosts = *t
	}
	peer := ""
	if cs != nil {
		cs.mu.Lock()
		peer = cs.linkPeer
		cs.mu.Unlock()
	}
	return config.LinkPolicyFor(hosts, peer)
}

// linkPolicyPointer keeps the atomic type in one place.
type linkPolicyPointer = atomic.Pointer[linkPolicyTable]

// checkLinkVerb refuses a verb the peer may not call. It returns nil for a
// connection that is not a link.
func (d *Daemon) checkLinkVerb(cs *connState, verb string) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	caps, known := verbCapabilities[verb]
	if !known {
		return linkForbidden(d, cs, verb, nil, "this verb has no link policy, so it is refused over a link")
	}
	return d.checkLinkCaps(cs, verb, caps)
}

// checkLinkMessage is checkLinkVerb for a binary message.
func (d *Daemon) checkLinkMessage(cs *connState, t MessageType) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	caps, known := msgCapabilities[t]
	if !known {
		return linkForbidden(d, cs, fmt.Sprintf("message %d", t), nil, "this message has no link policy, so it is refused over a link")
	}
	return d.checkLinkCaps(cs, fmt.Sprintf("message %d", t), caps)
}

// checkLinkCaps checks caps against the peer's policy.
func (d *Daemon) checkLinkCaps(cs *connState, what string, caps []string) *verbError {
	if len(caps) == 0 {
		return nil
	}
	policy := d.linkPolicy(cs)
	need := caps
	if len(caps) == 1 && caps[0] == capRelay {
		need = config.LinkCapabilities
	}
	var missing []string
	for _, c := range need {
		if !policy.Allows(c) {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return linkForbidden(d, cs, what, missing, "")
}

// linkForbidden is the refusal a link caller reads. It names the capability
// that is missing and the table on this machine that grants it, so the person
// who reads it knows which file to change and on which machine.
func linkForbidden(d *Daemon, cs *connState, what string, missing []string, reason string) *verbError {
	policy := d.linkPolicy(cs)
	here := d.hostedPaneHostName()
	if here == "" {
		here = "this machine"
	}
	peer := policy.Peer
	table := `[hosts."*"]`
	from := "a machine that gave no name"
	if peer != "" {
		table = "[hosts." + peer + "]"
		from = peer
	}
	if reason == "" {
		reason = "the link from " + from + " may not " + strings.Join(missing, " or ") + " on " + here
	}
	LogBasic("Link %s (peer %q) refused %s: missing %s", cs.clientID, peer, what, strings.Join(missing, ","))
	detail := "Nothing was done."
	if len(missing) > 0 {
		quoted := make([]string, 0, len(missing))
		for _, m := range missing {
			quoted = append(quoted, `"`+m+`"`)
		}
		detail = "Nothing was done. To allow it, add " + strings.Join(quoted, ", ") + " to allow in " + table + " in the config on " + here + "."
	}
	return hintedVerbError(ErrVerbForbidden, what+" is refused: "+reason, &VerbHint{Detail: detail})
}

// verbLinkPeer is the handshake: `tuios stdio-proxy` names the machine a link
// connection came from, once, before anything else is sent on it.
func (d *Daemon) verbLinkPeer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Peer   string `json:"peer"`
		Pinned bool   `json:"pinned"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs == nil || !cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "link-peer is only for a connection that arrived over a link")
	}
	peer := strings.TrimSpace(p.Peer)
	if peer != "" && (len(peer) > 64 || !linkPeerPattern.MatchString(peer)) {
		return nil, invalidParam("peer", "a peer name accepts only letters, digits, dot, dash and underscore")
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.linkPeerSet || cs.linkServed {
		return nil, newVerbError(ErrVerbForbidden, "this connection has already named its peer or been used, and a link connection names it once, first")
	}
	cs.linkPeerSet = true
	cs.linkPeer = peer
	cs.linkPinned = p.Pinned
	// The connection goes back to being read from its first byte, JSON or
	// binary, so an attach can follow the handshake on the same connection.
	cs.takeover = func(br *bufio.Reader) { d.serveConnection(cs, br) }
	LogBasic("Link %s names its peer %q (pinned %v)", cs.clientID, peer, p.Pinned)
	return map[string]any{"type": "link_peer", "peer": peer}, nil
}

// linkPeerPattern is what a peer name may be: the same shape as a host name,
// since it is matched against the [hosts] table's keys.
var linkPeerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// linkSelfName is the name this machine gives for itself on a link: its host
// name up to the first dot, lowered, since that is the name a person writes as
// a [hosts] key on the other machine. A name that would not match a key is
// sent as none rather than mangled.
func linkSelfName(host string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(host), ".")
	name = strings.ToLower(name)
	if name == "" || len(name) > 64 || !linkPeerPattern.MatchString(name) {
		return ""
	}
	return name
}

// markLinkServed records that a link connection has been used for something
// other than the handshake, after which it can no longer name its peer.
func markLinkServed(cs *connState) {
	if cs == nil || !cs.viaLink {
		return
	}
	cs.mu.Lock()
	cs.linkServed = true
	cs.mu.Unlock()
}
