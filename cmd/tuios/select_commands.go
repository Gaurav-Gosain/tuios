package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"golang.org/x/term"
)

// The CLI half of selectors: the look-then-write step a write addressed by
// selector goes through. The daemon never writes to a selection it has not
// been handed the token for, so the CLI shows the person the set first and
// sends the token only once they said yes, with --yes, or with --confirm and a
// token they got from list-agents.

// selectConfirm says how the CLI may confirm a selection.
type selectConfirm struct {
	// yes confirms whatever the selector matches now, without asking.
	yes bool
	// token is a confirm token the caller already has.
	token string
	// in and tty are where a question is read from and whether it can be
	// asked at all. A caller that is not at a terminal is never asked.
	in  io.Reader
	tty bool
	// out is where the set is printed.
	out io.Writer
}

// stdinConfirm is selectConfirm on the process's own terminal.
func stdinConfirm(yes bool, token string) selectConfirm {
	return selectConfirm{
		yes:   yes,
		token: token,
		in:    os.Stdin,
		tty:   term.IsTerminal(int(os.Stdin.Fd())),
		out:   os.Stderr,
	}
}

// callWithSelection makes a write addressed by selector. The first call names
// no token unless the caller has one, so the daemon answers confirm_required
// with the set. The set is printed, and the call is made again with the token
// when the caller confirms. A set that changed between the two calls is
// refused by the daemon and reported, never retried.
func callWithSelection(client *session.VerbClient, verb string, params map[string]any, timeout time.Duration, c selectConfirm) (json.RawMessage, error) {
	if c.token != "" {
		params["confirm"] = c.token
		return client.CallWithTimeout(verb, params, timeout)
	}
	raw, err := client.CallWithTimeout(verb, params, 30*time.Second)
	var callErr *session.VerbCallError
	if err == nil || !errors.As(err, &callErr) || callErr.Code != session.ErrVerbConfirmRequired || callErr.Hint == nil || callErr.Hint.Confirm == "" {
		return raw, err
	}
	panes := callErr.Hint.Available
	fmt.Fprintf(c.out, "The selector matches %d %s:\n", len(panes), pluralWord(len(panes), "pane", "panes"))
	for _, p := range panes {
		fmt.Fprintln(c.out, "  "+plainLine(p))
	}
	switch {
	case c.yes:
	case c.tty:
		action := "Send to"
		if verb == "ask-agent" {
			action = "Ask"
		}
		fmt.Fprintf(c.out, "%s all %d? [y/N] ", action, len(panes))
		line, _ := bufio.NewReader(c.in).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			return nil, &diagnosticError{What: "Nothing was sent.", Cause: "the selection was not confirmed."}
		}
	default:
		return nil, &diagnosticError{
			What:  "Nothing was sent.",
			Cause: "a selector writes only to a set someone confirmed, and this is not a terminal to ask at.",
			Fix:   "run it again with --yes, or with --confirm " + callErr.Hint.Confirm + " to send to exactly the panes listed above.",
		}
	}
	params["confirm"] = callErr.Hint.Confirm
	return client.CallWithTimeout(verb, params, timeout)
}

// selectedRow is one pane's row of a write by selector.
type selectedRow struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	RawErr  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// who is how a row names its pane.
func (r selectedRow) who() string {
	return fmt.Sprintf("%s/%s (%s)", plainLine(r.Session), orNone(plainLine(r.Name)), shortWindowID(r.Window))
}

// failure is a row's refusal, in one line.
func (r selectedRow) failure() string {
	return plainLine(r.RawErr.Message) + " (" + plainLine(r.RawErr.Code) + ")"
}

// runSendAgentMessageSelect sends one message to every pane a selector matches.
func runSendAgentMessageSelect(selector, from, subject, text string, attachments []string, c selectConfirm, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"select": selector, "text": text}
	if from != "" {
		params["from"] = from
	}
	if subject != "" {
		params["subject"] = subject
	}
	if len(attachments) > 0 {
		params["attachments"] = attachments
	}
	raw, err := callWithSelection(client, "send-agent-message", params, 60*time.Second, c)
	if err != nil {
		return reportVerbError(explainVerbError("send-agent-message", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Results []struct {
			selectedRow
			MessageID uint64 `json:"message_id"`
		} `json:"results"`
		Sent   int `json:"sent"`
		Failed int `json:"failed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	for _, r := range res.Results {
		if r.OK {
			fmt.Printf("message %d queued for %s\n", r.MessageID, r.who())
		} else {
			fmt.Printf("not sent to %s: %s\n", r.who(), r.failure())
		}
	}
	fmt.Printf("%d sent, %d refused.\n", res.Sent, res.Failed)
	if res.Failed > 0 {
		return &statusError{code: 1}
	}
	return nil
}

// runAskAgentSelect asks every pane a selector matches the same question.
func runAskAgentSelect(selector, from, text string, readyTimeout, settle, timeout, lines, stallTimeout int, force, allowBlocked bool, c selectConfirm, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{"select": selector, "text": text, "force": force}
	if allowBlocked {
		params["allow_blocked"] = true
	}
	if from != "" {
		params["from"] = from
	}
	for name, v := range map[string]int{
		"ready_timeout": readyTimeout, "settle": settle, "timeout": timeout, "lines": lines, "stall_timeout": stallTimeout,
	} {
		if v > 0 {
			params[name] = v
		}
	}
	// The asks run at once, so the whole call takes as long as the slowest.
	ready, overall := readyTimeout, timeout
	if ready <= 0 {
		ready = 30000
	}
	if overall <= 0 {
		overall = 300000
	}
	grace := time.Duration(ready+overall)*time.Millisecond + 10*time.Second
	raw, err := callWithSelection(client, "ask-agent", params, grace, c)
	if err != nil {
		return reportVerbError(explainVerbError("ask-agent", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Replies []struct {
			selectedRow
			Reply     string `json:"reply"`
			SettledBy string `json:"settled_by"`
			State     string `json:"state"`
			Truncated bool   `json:"truncated"`
		} `json:"replies"`
		Answered int `json:"answered"`
		Failed   int `json:"failed"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	for _, r := range res.Replies {
		if !r.OK {
			fmt.Printf("%s was not asked: %s\n\n", r.who(), r.failure())
			continue
		}
		fmt.Printf(untrustedOpen+"\n", r.who())
		fmt.Println(strings.TrimRight(plainText(r.Reply), "\n"))
		fmt.Println(untrustedClose)
		fmt.Printf("settled by %s; now reports %s\n\n", plainLine(r.SettledBy), plainLine(r.State))
	}
	fmt.Printf("%d answered, %d not asked or failed.\n", res.Answered, res.Failed)
	if res.Failed > 0 {
		return &statusError{code: 1}
	}
	return nil
}
