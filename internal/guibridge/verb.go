package guibridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

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
	"agent-transcript":      true,
	"review-note":           true,
	"send-review":           true,
	"review-stage":          true,
}

var heldVerbs = map[string]bool{
	"request-approval": true,
}

var personOnlyVerbs = map[string]bool{
	"daemon-upgrade": true,
}

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

var verbDial = func(version string) (verbCaller, error) {
	return session.DialVerbClientAs(version)
}

var verbDialHost = func(host, version string) (verbCaller, error) {
	c, _, err := session.DialVerbClientThroughHost(host, version)
	if err != nil {
		return nil, err
	}
	return c, nil
}

type verbCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
	Close() error
}

func verbParams(verb string, in map[string]any, nonce string) (map[string]any, error) {
	params := make(map[string]any, len(in)+1)
	for k, v := range in {
		if k == "human_nonce" || k == "upgrade_dialog" {
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

func runVerb(out *frameWriter, version, nonce string, c Command) {
	res := VerbResult{Req: c.Req, Verb: c.Verb}
	defer func() { out.JSON(Event{Type: "verb_result", VerbResult: &res}) }()
	if c.Verb == "" {
		res.Error = "name the verb"
		return
	}
	if doc, ok := session.LookupVerbDoc(c.Verb); ok && doc.HoldsConnection(c.Params) {
		res.Code, res.Error = "unsupported", fmt.Sprintf("%s keeps the connection after its reply; the bridge proxies only verbs with one reply", c.Verb)
		return
	}
	if heldVerbs[c.Verb] {
		res.Code, res.Error = "unsupported", fmt.Sprintf("%s answers only when the person decides; the bridge proxies only verbs that answer promptly", c.Verb)
		return
	}
	if personOnlyVerbs[c.Verb] {
		res.Code, res.Error = session.ErrVerbForbidden, c.Verb+" is for the person at a terminal, and the bridge's renderer is not one. Nothing was changed"
		return
	}
	host := c.Host
	if host == "local" {
		host = ""
	}
	if host != "" && personVerbs[c.Verb] {
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
		if ve, ok := errors.AsType[*session.VerbCallError](err); ok {
			res.Code, res.Error = ve.Code, ve.Message
		} else {
			res.Error = err.Error()
		}
		return
	}
	res.OK = true
	res.Result = raw
}
