package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// barStatus is one line of tuios bar-status: the fields a Waybar custom
// module reads (text, alt, tooltip, class), and the counts and rows a bar
// widget of its own draws from.
type barStatus struct {
	Text     string         `json:"text"`
	Alt      string         `json:"alt"`
	Tooltip  string         `json:"tooltip"`
	Class    string         `json:"class"`
	NeedsYou int            `json:"needs_you"`
	Mail     int            `json:"mail"`
	Review   int            `json:"review"`
	Working  int            `json:"working"`
	Rows     []barStatusRow `json:"rows"`
}

// barStatusRow is one Inbox item that needs the person.
type barStatusRow struct {
	Kind    string `json:"kind"`
	Host    string `json:"host,omitempty"`
	Session string `json:"session"`
	Window  string `json:"window,omitempty"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Since   int64  `json:"since"`
}

// barStatusMaxRows bounds the rows a line carries and the tooltip lists.
const barStatusMaxRows = 8

// barStatusSettle is how long a burst of events is left to settle before
// the line is worked out again. It is spent only after an event.
const barStatusSettle = 150 * time.Millisecond

func newBarStatusCommand() *cobra.Command {
	var follow bool
	cmd := &cobra.Command{
		Use:   "bar-status",
		Short: "Print the agent state for a status bar, as one JSON line",
		Long: `Print what your agents need from you as one JSON line, for a status bar.

The line has the fields a Waybar custom module reads: text, alt, tooltip and
class. text is the number of items that need you. When nothing needs you, it is
the number of agents that work, and when no agent works, it is empty. alt and
class are the same word:

  needs-you  an agent waits on an approval, a question or a plan, or has failed
  mail       an agent sent you mail
  working    an agent works
  review     an agent finished a turn that nobody has looked at
  idle       nothing of the above
  off        the tuios daemon does not run

The line also has needs_you, mail, review and working as numbers, and rows: the
items that need you, oldest first, at most 8. It covers every session on
this machine and the items of every linked host.

--follow prints a line at the start and a new line each time the line changes.
It waits on the daemon's event stream and does no work while nothing changes.
When the daemon stops, it prints an off line and waits for the daemon to start
again.`,
		Example: `  # One line, for a bar that runs the command at an interval
  tuios bar-status

  # A line per change, for a Waybar custom module with "return-type": "json"
  tuios bar-status --follow`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if follow {
				startPprofServer()
				return followBarStatus(cmd.OutOrStdout())
			}
			return writeBarStatus(cmd.OutOrStdout(), readBarStatus())
		},
	}
	cmd.Flags().BoolVar(&follow, "follow", false, "Print a new line each time the state changes, until the command is stopped")
	return cmd
}

// readBarStatus dials the daemon and works out the line. A daemon that does
// not answer gives the off line.
func readBarStatus() barStatus {
	client, err := dialVerb()
	if err != nil {
		return barStatusOff()
	}
	defer func() { _ = client.Close() }()
	st, err := queryBarStatus(client)
	if err != nil {
		return barStatusOff()
	}
	return st
}

func barStatusOff() barStatus {
	return barStatus{Alt: "off", Class: "off", Tooltip: "tuios is not running.", Rows: []barStatusRow{}}
}

// queryBarStatus asks the daemon for the Inbox and the agents, and folds
// them into one line.
func queryBarStatus(client *session.VerbClient) (barStatus, error) {
	raw, err := client.Call("list-attention", map[string]any{})
	if err != nil {
		return barStatus{}, err
	}
	var att struct {
		Items []attentionRow `json:"items"`
	}
	if err := jsonx.Unmarshal(raw, &att); err != nil {
		return barStatus{}, err
	}
	raw, err = client.Call("list-agents", map[string]any{"all": true, "all_sessions": true})
	if err != nil {
		return barStatus{}, err
	}
	var ag struct {
		Agents []agentRow `json:"agents"`
	}
	if err := jsonx.Unmarshal(raw, &ag); err != nil {
		return barStatus{}, err
	}
	return foldBarStatus(att.Items, ag.Agents), nil
}

// foldBarStatus turns the Inbox items and the agents into a line. Snoozed
// items and items left with a parent agent do not count: the person put them
// away.
func foldBarStatus(items []attentionRow, agents []agentRow) barStatus {
	st := barStatus{Rows: []barStatusRow{}}
	for _, it := range items {
		if it.SnoozedUntil != 0 || it.WithParentUntil != 0 {
			continue
		}
		switch it.Kind {
		case session.AttentionApproval, session.AttentionQuestion, session.AttentionAsk,
			session.AttentionPlan, session.AttentionErrored:
			st.NeedsYou++
			if len(st.Rows) < barStatusMaxRows {
				st.Rows = append(st.Rows, barStatusRow{
					Kind: it.Kind, Host: plainLine(it.Host), Session: plainLine(it.Session), Window: it.Window,
					Name: plainLine(it.Name), Summary: plainLine(it.Summary), Since: it.Since,
				})
			}
		case session.AttentionMail, session.AttentionHeldMail:
			st.Mail++
		case session.AttentionFinished:
			st.Review++
		}
	}
	for _, a := range agents {
		if a.State == "working" {
			st.Working++
		}
	}
	switch {
	case st.NeedsYou > 0:
		st.Class = "needs-you"
		st.Text = fmt.Sprint(st.NeedsYou)
	case st.Mail > 0:
		st.Class = "mail"
		st.Text = fmt.Sprint(st.Mail)
	case st.Working > 0:
		st.Class = "working"
		st.Text = fmt.Sprint(st.Working)
	case st.Review > 0:
		st.Class = "review"
		st.Text = fmt.Sprint(st.Review)
	default:
		st.Class = "idle"
	}
	st.Alt = st.Class
	st.Tooltip = barStatusTooltip(st)
	return st
}

// barStatusTooltip is the tooltip: a count line per kind, then the items
// that need you. Waybar reads a tooltip as Pango markup, so the agents' words
// are escaped.
func barStatusTooltip(st barStatus) string {
	var lines []string
	if st.NeedsYou > 0 {
		lines = append(lines, countWords(st.NeedsYou, "item needs you.", "items need you."))
		for _, r := range st.Rows {
			where := r.Session
			if r.Host != "" {
				where = r.Host + ":" + where
			}
			if r.Name != "" {
				where += "/" + r.Name
			}
			line := attentionKindWord(r.Kind) + "  " + where
			if r.Summary != "" {
				line += "  " + r.Summary
			}
			lines = append(lines, markupEscape(line))
		}
		if more := st.NeedsYou - len(st.Rows); more > 0 {
			lines = append(lines, fmt.Sprintf("%d more in the Inbox.", more))
		}
	}
	if st.Mail > 0 {
		lines = append(lines, countWords(st.Mail, "message for you.", "messages for you."))
	}
	if st.Working > 0 {
		lines = append(lines, countWords(st.Working, "agent works.", "agents work."))
	}
	if st.Review > 0 {
		lines = append(lines, countWords(st.Review, "turn to review.", "turns to review."))
	}
	if len(lines) == 0 {
		return "Nothing needs you."
	}
	return strings.Join(lines, "\n")
}

func countWords(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// attentionKindWord names an item's kind in a tooltip row.
func attentionKindWord(kind string) string {
	switch kind {
	case session.AttentionApproval:
		return "Approval"
	case session.AttentionPlan:
		return "Plan"
	case session.AttentionQuestion, session.AttentionAsk:
		return "Question"
	case session.AttentionErrored:
		return "Error"
	}
	return kind
}

func markupEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func writeBarStatus(w io.Writer, st barStatus) error {
	b, err := jsonx.Marshal(st)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// followBarStatus prints a line, then a new one each time it changes. It
// holds two connections: one reads the event stream and one asks for the
// state after a burst of events. While the daemon is down it watches the
// socket's folder, so it does no work until a daemon starts.
func followBarStatus(w io.Writer) error {
	var last []byte
	emit := func(st barStatus) error {
		b, err := jsonx.Marshal(st)
		if err != nil {
			return err
		}
		if string(b) == string(last) {
			return nil
		}
		last = b
		_, err = fmt.Fprintln(w, string(b))
		return err
	}
	for {
		err := followBarStatusOnce(emit)
		if werr, ok := errors.AsType[writeError](err); ok {
			return werr.err
		}
		if err := emit(barStatusOff()); err != nil {
			return err
		}
		waitForDaemonSocket()
	}
}

// writeError marks an error from writing a line, which ends --follow: the
// bar that read the lines has gone.
type writeError struct{ err error }

func (e writeError) Error() string { return e.err.Error() }

// followBarStatusOnce follows one daemon until its event stream ends.
func followBarStatusOnce(emit func(barStatus) error) error {
	events, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = events.Close() }()
	if _, err := events.Call("subscribe", map[string]any{
		"types": []string{session.EventAttention, session.EventAgentState, session.EventSessionClosed},
	}); err != nil {
		return err
	}
	query, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = query.Close() }()

	lines := make(chan struct{}, 1)
	ended := make(chan error, 1)
	go func() {
		for {
			if _, err := events.ReadEventLine(0); err != nil {
				ended <- err
				return
			}
			dirwatch.Nudge(lines)
		}
	}()
	refresh := func() error {
		st, err := queryBarStatus(query)
		if err != nil {
			return err
		}
		if err := emit(st); err != nil {
			return writeError{err}
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	settle := time.NewTimer(0)
	<-settle.C
	for {
		select {
		case err := <-ended:
			return err
		case <-lines:
			settle.Reset(barStatusSettle)
		case <-settle.C:
			if err := refresh(); err != nil {
				return err
			}
		}
	}
}

// waitForDaemonSocket returns when the daemon's socket folder changes, or
// when the folder cannot be watched, after a pause.
func waitForDaemonSocket() {
	path, err := session.GetSocketPath()
	if err == nil {
		dir := filepath.Dir(path)
		for dir != "" && dir != "/" {
			if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
				break
			}
			dir = filepath.Dir(dir)
		}
		changed := make(chan struct{}, 1)
		if wt, werr := dirwatch.Watch(dir, func() { dirwatch.Nudge(changed) }); werr == nil {
			defer wt.Close()
			if _, statErr := os.Stat(path); statErr == nil {
				if c, derr := dialVerb(); derr == nil {
					_ = c.Close()
					return
				}
			}
			<-changed
			dirwatch.AfterBurst(changed, nil)
			return
		}
	}
	time.Sleep(10 * time.Second)
}
