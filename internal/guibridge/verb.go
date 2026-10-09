package guibridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The verb proxy: the renderer calls any daemon verb through the bridge, so
// the Inbox can peek a prompt, answer it, snooze an item or queue a reply
// without attaching to the pane (plan 2.13).
//
// The verbs that only the person may call take the attach nonce the daemon
// issued to this bridge's client. The bridge adds it itself, and it drops any
// nonce the renderer sent, so the renderer never sees the nonce and cannot
// pass another. The daemon checks the nonce against a client attached right
// now and refuses a caller inside any pane, so a bridge that a pane starts
// gets not_human however its renderer asks. The bridge reads commands only
// from its own stdin, the pipe of the renderer that started it.

// personVerbs are the verbs that act for the person and carry the nonce.
var personVerbs = map[string]bool{
	"respond":               true,
	"reply-approval":        true,
	"answer-ask":            true,
	"mark-attention":        true,
	"dismiss-attention":     true,
	"release-agent-message": true,
	"queue-prompt":          true,
	"cancel-queued":         true,
	"send-agent-message":    true,
}

// streamVerbs never answer with one reply, so the proxy refuses them.
var streamVerbs = map[string]bool{
	"subscribe":        true,
	"request-approval": true,
}

// verbTimeout bounds one proxied call. respond waits up to 30 s for the pane
// to settle, so the bound is above that.
const verbTimeout = 35 * time.Second

// VerbResult answers one verb command.
type VerbResult struct {
	// Req is the command's req.
	Req  int64  `json:"req,omitempty"`
	Verb string `json:"verb"`
	OK   bool   `json:"ok"`
	// Result is the verb's own answer, as the daemon sent it.
	Result json.RawMessage `json:"result,omitempty"`
	// Code and Error say why the call failed: the daemon's error code
	// (not_human, prompt_changed, ...) and its message.
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// verbDial opens a verb connection to the daemon. Tests replace it.
var verbDial = func(version string) (verbCaller, error) {
	return session.DialVerbClientAs(version)
}

// verbDialHost opens a verb connection to a host's daemon through this
// machine's daemon. Tests replace it.
var verbDialHost = func(host, version string) (verbCaller, error) {
	c, _, err := session.DialVerbClientThroughHost(host, version)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// verbCaller is the part of session.VerbClient the proxy uses.
type verbCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
	Close() error
}

// verbParams returns the params the bridge sends for a verb: the renderer's,
// without any nonce it gave, and with this client's nonce for a person verb.
func verbParams(verb string, in map[string]any, nonce string) (map[string]any, error) {
	params := make(map[string]any, len(in)+1)
	for k, v := range in {
		if k == "human_nonce" {
			continue
		}
		params[k] = v
	}
	if personVerbs[verb] {
		if nonce == "" {
			return nil, errors.New("the daemon issued no attach nonce to this client, so it cannot answer for the person")
		}
		params["human_nonce"] = nonce
	}
	return params, nil
}

// runVerb proxies one verb command and sends its result. It runs on its own
// goroutine: a verb can wait for seconds, and the model must not.
func runVerb(out *frameWriter, version, nonce string, c Command) {
	res := VerbResult{Req: c.Req, Verb: c.Verb}
	defer func() { out.JSON(Event{Type: "verb_result", VerbResult: &res}) }()
	if c.Verb == "" {
		res.Error = "name the verb"
		return
	}
	if streamVerbs[c.Verb] {
		res.Code, res.Error = "unsupported", fmt.Sprintf("%s streams its answer; the bridge proxies only verbs with one reply", c.Verb)
		return
	}
	host := c.Host
	if host == "local" {
		host = ""
	}
	if host != "" && personVerbs[c.Verb] {
		// The nonce is this machine's daemon's word about this client. It
		// means nothing to another machine's daemon, and must not reach it.
		res.Code, res.Error = "not_human", c.Verb+" answers for the person on this machine only; attach the session on "+host+" to answer there"
		return
	}
	params, err := verbParams(c.Verb, c.Params, nonce)
	if err != nil {
		res.Code, res.Error = "not_human", err.Error()
		return
	}
	var client verbCaller
	if host != "" {
		client, err = verbDialHost(host, version)
	} else {
		client, err = verbDial(version)
	}
	if err != nil {
		res.Error = err.Error()
		return
	}
	defer func() { _ = client.Close() }()
	raw, err := client.CallWithTimeout(c.Verb, params, verbTimeout)
	if err != nil {
		var ve *session.VerbCallError
		if errors.As(err, &ve) {
			res.Code, res.Error = ve.Code, ve.Message
		} else {
			res.Error = err.Error()
		}
		return
	}
	res.OK = true
	res.Result = raw
}
