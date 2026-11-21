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

const linkPolicyVerb = "link-peer"

const capRelay = "*"

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

func (n linkNeed) list() []string {
	var out []string
	for _, b := range linkNeedNames {
		if n&b.bit != 0 {
			out = append(out, b.name)
		}
	}
	return out
}

type linkCaps [3]string

func (c linkCaps) list() []string {
	for i, name := range c {
		if name == "" {
			return c[:i]
		}
	}
	return c[:]
}

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
	MsgSidebarVisibility: {config.LinkAllowWrite},
	MsgSessionUsed:       {config.LinkAllowWrite},
	MsgClientTheme:       {config.LinkAllowWrite},
	MsgExecuteCommand:    {config.LinkAllowWrite},
	MsgCommandResult:     {config.LinkAllowWrite},
	MsgKill:              {config.LinkAllowWrite},
	MsgNew:               {config.LinkAllowOpen, config.LinkAllowList, config.LinkAllowWrite},
	MsgCreatePTY:         {config.LinkAllowOpen},
	MsgResurrect:         {config.LinkAllowOpen},
}

type linkPolicyTable = map[string]config.HostConfig

// SetLinkPolicies replaces the table link policies are resolved from. A call
// in flight keeps the policy it resolved; the next call reads the new one.
func (d *Daemon) SetLinkPolicies(hosts map[string]config.HostConfig) {
	d.storeLinkPolicies(hosts, hosts)
}

func (d *Daemon) storeLinkPolicies(policies, file map[string]config.HostConfig) {
	t := maps.Clone(policies)
	names := make(map[string]bool, len(file))
	for k := range file {
		if k != config.LinkPolicyDefaultName {
			names[strings.ToLower(k)] = true
		}
	}
	d.linkTableNames.Store(&names)
	d.linkPolicies.Store(&t)
}

func (d *Daemon) pinnedWithoutTable(cs *connState) (string, bool) {
	if cs == nil {
		return "", false
	}
	cs.mu.Lock()
	peer, pinned := cs.linkPeer, cs.linkPinned
	cs.mu.Unlock()
	if !pinned || peer == "" {
		return "", false
	}
	t := d.linkTableNames.Load()
	return peer, t == nil || !(*t)[strings.ToLower(peer)]
}

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
	if p, orphan := d.pinnedWithoutTable(cs); orphan {
		return config.LinkPolicy{Peer: p, Allow: []string{}, FilesRoots: []string{}}
	}
	return config.LinkPolicyFor(hosts, peer)
}

type linkPolicyPointer = atomic.Pointer[linkPolicyTable]

type linkTableNamesPointer = atomic.Pointer[map[string]bool]

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
	if policy.AllowSource != "" {
		table = policy.AllowSource
	}
	if p, orphan := d.pinnedWithoutTable(cs); orphan {
		LogBasic("Link %s (peer %q, pinned) refused %s: the config has no [hosts.%s] table", cs.clientID, p, what, p)
		return hintedVerbError(ErrVerbForbidden, what+" is refused: the ssh key of "+p+" is pinned to a name that has no [hosts."+p+"] table on "+here, &VerbHint{
			Detail: "Nothing was done. To allow it, add a [hosts." + p + "] table to the config on " + here + ". To remove the key, run 'tuios hosts remove " + p + "' there.",
		})
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

func (d *Daemon) verbLinkPeer(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Peer        string `json:"peer"`
		Pinned      bool   `json:"pinned"`
		SSHAuthSock string `json:"ssh_auth_sock"`
		Closes      string `json:"closes"`
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
	cs.takeover = func(br *bufio.Reader) { d.serveConnection(cs, br) }
	LogBasic("Link %s names its peer %q (pinned %v)", cs.clientID, peer, p.Pinned)
	return map[string]any{"type": "link_peer", "peer": peer, "close_token": cs.linkCloseToken}, nil
}

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

func (cs *connState) linkClosedOnPurposeNow() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.linkClosedOnPurpose
}

var linkPeerPattern = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func linkSelfName(host string) string {
	name, _, _ := strings.Cut(strings.TrimSpace(host), ".")
	name = strings.ToLower(name)
	if name == "" || len(name) > 64 || !linkPeerPattern().MatchString(name) {
		return ""
	}
	return name
}

func markLinkServed(cs *connState) {
	if cs == nil || !cs.viaLink {
		return
	}
	cs.mu.Lock()
	cs.linkServed = true
	cs.mu.Unlock()
}

func verbLinkCaps(verb string) (need linkNeed, known bool) {
	e, ok := verbRegistry()[verb]
	if !ok || e.link&needDeclared == 0 {
		return 0, false
	}
	return e.link, true
}
