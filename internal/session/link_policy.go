package session

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
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
//   - Every JSON verb names its link policy on its verb entry, and a test
//     fails on an entry that names none, so a verb added without one fails
//     the build's tests rather than being reachable from a link unchecked. A
//     verb with no policy at run time is refused, never allowed.
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

// capRelay marks open-host-connection and the other verbs that act on this
// machine's own links: they need config.LinkRelayCapabilities.
const capRelay = "*"

// linkNeed is what a verb needs on a link connection, as a set of bits, so
// the verb registry holds it as a number rather than building strings for
// every verb. needDeclared is in every value a verb entry names, so an entry
// with no link policy at all, the zero linkNeed, is told apart and refused
// over every link. needNothing is a policy that needs no capability.
type linkNeed uint16

const (
	needDeclared linkNeed = 1 << iota
	needRelayBit
	needListBit
	needMailBit
	needOpenBit
	needWriteBit
	needRespondBit
	needFilesBit
	needTranscriptsBit
)

// The policies a verb entry names. Each carries needDeclared.
const (
	needNothing     = needDeclared
	needRelay       = needDeclared | needRelayBit
	needList        = needDeclared | needListBit
	needMail        = needDeclared | needMailBit
	needOpen        = needDeclared | needOpenBit
	needWrite       = needDeclared | needWriteBit
	needRespond     = needDeclared | needRespondBit
	needFiles       = needDeclared | needFilesBit
	needTranscripts = needDeclared | needTranscriptsBit
)

// linkNeedNames pairs each bit with the capability it stands for, in the
// order a refusal names them.
var linkNeedNames = [...]struct {
	bit  linkNeed
	name string
}{
	{needRelayBit, capRelay},
	{needListBit, config.LinkAllowList},
	{needMailBit, config.LinkAllowMail},
	{needOpenBit, config.LinkAllowOpen},
	{needWriteBit, config.LinkAllowWrite},
	{needRespondBit, config.LinkAllowRespond},
	{needFilesBit, config.LinkAllowFiles},
	{needTranscriptsBit, config.LinkAllowTranscripts},
}

// list is the capabilities n needs, by name.
func (n linkNeed) list() []string {
	var out []string
	for _, b := range linkNeedNames {
		if n&b.bit != 0 {
			out = append(out, b.name)
		}
	}
	return out
}

// linkCaps is what a verb or a message needs on a link connection. An array
// rather than a slice, because the compiler lays a map of arrays of constants
// out as data. A map of slices is about 28 KB of code that runs at every start
// of every tuios command, for a table only a link connection reads.
type linkCaps [3]string

// list is the capabilities, without the unused places of the array.
func (c linkCaps) list() []string {
	for i, name := range c {
		if name == "" {
			return c[:i]
		}
	}
	return c[:]
}

// msgCapabilities is what each binary message needs on a link connection.
var msgCapabilities = map[MessageType]linkCaps{
	MsgHello:             {},
	MsgDetach:            {},
	MsgPing:              {},
	MsgList:              {config.LinkAllowList},
	MsgSubscribePTY:      {config.LinkAllowList},
	MsgUnsubscribePTY:    {config.LinkAllowList},
	MsgGetTerminalState:  {config.LinkAllowList},
	MsgReadDir:           {config.LinkAllowList},
	MsgWatchDir:          {config.LinkAllowList},
	MsgGetLogs:           {config.LinkAllowList},
	MsgAttach:            {config.LinkAllowList, config.LinkAllowWrite},
	MsgInput:             {config.LinkAllowWrite},
	MsgResize:            {config.LinkAllowWrite},
	MsgClientFocus:       {config.LinkAllowWrite},
	MsgClientGraphics:    {config.LinkAllowWrite},
	MsgClientActivity:    {config.LinkAllowWrite},
	MsgTypeAtPrompt:      {config.LinkAllowWrite},
	MsgClosePTY:          {config.LinkAllowWrite},
	MsgUpdateState:       {config.LinkAllowWrite},
	MsgLayoutTree:        {config.LinkAllowWrite},
	MsgMasterLayout:      {config.LinkAllowWrite},
	MsgScrollColumns:     {config.LinkAllowWrite},
	MsgSidebarVisibility: {config.LinkAllowWrite},
	MsgSessionUsed:       {config.LinkAllowWrite},
	MsgExecuteCommand:    {config.LinkAllowWrite},
	MsgCommandResult:     {config.LinkAllowWrite},
	MsgKill:              {config.LinkAllowWrite},
	MsgNew:               {config.LinkAllowOpen, config.LinkAllowList, config.LinkAllowWrite},
	MsgCreatePTY:         {config.LinkAllowOpen},
	MsgResurrect:         {config.LinkAllowOpen},
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
	if verr := d.linkFromPane(cs); verr != nil {
		return verr
	}
	caps, known := verbLinkCaps(verb)
	if !known {
		return linkForbidden(d, cs, verb, nil, "this verb has no link policy, so it is refused over a link")
	}
	return d.checkLinkCaps(cs, verb, caps.list())
}

// checkLinkMessage is checkLinkVerb for a binary message.
func (d *Daemon) checkLinkMessage(cs *connState, t MessageType) *verbError {
	if cs == nil || !cs.viaLink {
		return nil
	}
	if verr := d.linkFromPane(cs); verr != nil {
		return verr
	}
	caps, known := msgCapabilities[t]
	if !known {
		return linkForbidden(d, cs, fmt.Sprintf("message %d", t), nil, "this message has no link policy, so it is refused over a link")
	}
	return d.checkLinkCaps(cs, fmt.Sprintf("message %d", t), caps.list())
}

// linkFromPane refuses a link connection dialled from inside a pane of this
// daemon. The link sockets are for the link proxy, which ssh starts outside
// every pane. A connection on them is held to the link policy and not to pane
// grants (checkGrants), so a process in a pane that dialled one itself would
// step out of its grants. It is refused whatever it asks, the same test
// mayActAsHuman applies on the link-human socket.
func (d *Daemon) linkFromPane(cs *connState) *verbError {
	if !d.connFromPane(cs) {
		return nil
	}
	return hintedVerbError(ErrVerbForbidden, "the link socket is for the link proxy, and this caller runs inside a pane of this daemon", &VerbHint{
		Verb:    "pane-grants",
		Command: "tuios pane-grants",
		Detail:  "Nothing was done. Use the daemon's own socket, where the pane's grants apply.",
	})
}

// checkLinkCaps checks caps against the peer's policy.
func (d *Daemon) checkLinkCaps(cs *connState, what string, caps []string) *verbError {
	if len(caps) == 0 {
		return nil
	}
	policy := d.linkPolicy(cs)
	need := expandRelay(caps)
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

// expandRelay is caps with capRelay replaced by the capabilities a relay
// needs, config.LinkRelayCapabilities.
func expandRelay(caps []string) []string {
	if !slices.Contains(caps, capRelay) {
		return caps
	}
	out := make([]string, 0, len(caps)+len(config.LinkRelayCapabilities))
	for _, c := range caps {
		if c == capRelay {
			for _, r := range config.LinkRelayCapabilities {
				if !slices.Contains(out, r) {
					out = append(out, r)
				}
			}
		} else if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
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
	// The table whose allow list is in force is the one to change. A grant
	// added to [hosts.PEER] while [hosts."*"] holds the list would work too,
	// but a grant added to [hosts."*"] while [hosts.PEER] holds it would not.
	if policy.AllowSource != "" {
		table = policy.AllowSource
	}
	if reason == "" {
		words := make([]string, 0, len(missing))
		for _, m := range missing {
			words = append(words, config.LinkCapabilityWords(m)+" ("+m+")")
		}
		reason = from + " may not " + strings.Join(words, " or ") + " on " + here
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
	hint := &VerbHint{Detail: detail}
	if len(missing) > 0 {
		hint.Missing = slices.Clone(missing)
		hint.Table = table
	}
	return hintedVerbError(ErrVerbForbidden, what+" is refused: "+reason, hint)
}

// verbLinkPeer is the handshake: `tuios stdio-proxy` names the machine a link
// connection came from, once, before anything else is sent on it.
func (d *Daemon) verbLinkPeer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Peer   string `json:"peer"`
		Pinned bool   `json:"pinned"`
		// SSHAuthSock is the SSH_AUTH_SOCK tuios stdio-proxy runs with: the
		// agent the link's ssh forwards, when it forwards one. See
		// ssh_agent_follow.go.
		SSHAuthSock string `json:"ssh_auth_sock"`
		// Closes is the close token of another link connection whose
		// stream the far side closed on purpose. See linkClosedOnPurpose.
		Closes string `json:"closes"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if cs == nil || !cs.viaLink {
		return nil, newVerbError(ErrVerbForbidden, "link-peer is only for a connection that arrived over a link")
	}
	if p.Closes != "" {
		d.noteLinkClosedOnPurpose(p.Closes)
	}
	peer := strings.TrimSpace(p.Peer)
	if peer != "" && (len(peer) > 64 || !linkPeerPattern().MatchString(peer)) {
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
	cs.linkAgentSock = p.SSHAuthSock
	cs.linkCloseToken = rand.Text()
	// The connection goes back to being read from its first byte, JSON or
	// binary, so an attach can follow the handshake on the same connection.
	cs.takeover = func(br *bufio.Reader) { d.serveConnection(cs, br) }
	LogBasic("Link %s names its peer %q (pinned %v)", cs.clientID, peer, p.Pinned)
	return map[string]any{"type": "link_peer", "peer": peer, "close_token": cs.linkCloseToken}, nil
}

// noteLinkClosedOnPurpose marks the link connection that holds token as
// closed on purpose by the far side. An unknown token does nothing: the
// connection may already be gone.
func (d *Daemon) noteLinkClosedOnPurpose(token string) {
	d.clientsMu.Lock()
	defer d.clientsMu.Unlock()
	for _, c := range d.clients {
		c.mu.Lock()
		if c.viaLink && c.linkCloseToken != "" && subtle.ConstantTimeCompare([]byte(c.linkCloseToken), []byte(token)) == 1 {
			c.linkClosedOnPurpose = true
		}
		c.mu.Unlock()
	}
}

// linkClosedOnPurposeNow reports whether the far side of a link connection
// closed it on purpose. See noteLinkClosedOnPurpose.
func (cs *connState) linkClosedOnPurposeNow() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.linkClosedOnPurpose
}

// linkPeerPattern is what a peer name may be: the same shape as a host name,
// since it is matched against the [hosts] table's keys.
var linkPeerPattern = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// linkSelfName is the name this machine gives for itself on a link: its host
// name up to the first dot, lowered, since that is the name a person writes as
// a [hosts] key on the other machine. A name that would not match a key is
// sent as none rather than mangled.
func linkSelfName(host string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(host), ".")
	name = strings.ToLower(name)
	if name == "" || len(name) > 64 || !linkPeerPattern().MatchString(name) {
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

// verbLinkCaps is what verb needs on a link connection, from its entry in the
// verb registry. known is false for a name that is not a verb, and for a verb
// whose entry names no link policy, so both are refused over a link.
func verbLinkCaps(verb string) (need linkNeed, known bool) {
	e, ok := verbRegistry()[verb]
	if !ok || e.link&needDeclared == 0 {
		return 0, false
	}
	return e.link, true
}
