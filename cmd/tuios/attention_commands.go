package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The Inbox on the command line: what is waiting for the person, in every
// session, in the order the TUI's Inbox shows it.

// attentionRow is one item of the list-attention result.
type attentionRow struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	Session   string `json:"session"`
	Window    string `json:"window"`
	Workspace int    `json:"workspace"`
	Harness   string `json:"harness"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Since     int64  `json:"since"`
	Thread    uint64 `json:"thread"`
	Count     int    `json:"count"`
	// RequestID is set while a harness hook holds the approval for an answer
	// from the Inbox.
	RequestID string `json:"request_id"`
	// Options are the answers a question ask-human put takes.
	Options []string `json:"options"`
	// Stale marks an item of a linked host whose link is down, last heard
	// from at SeenAt (unix nanoseconds).
	Stale  bool  `json:"stale"`
	SeenAt int64 `json:"seen_at"`
	// HeldFor is set on mail another machine sent an agent here that the
	// link policy held for the person; HeldID is its message id.
	HeldFor string `json:"held_for"`
	HeldID  uint64 `json:"held_id"`
	// ForHost is the machine an outbox item's mail waits for.
	ForHost string `json:"for_host"`
}

// attentionHeldNote ends the row of an approval a hook is holding, so the
// person knows the pane shows no prompt and where to answer it.
const attentionHeldNote = "held: answer in the Inbox"

// attentionGroupTitle is the heading a kind's rows sit under.
func attentionGroupTitle(kind string) string {
	switch kind {
	case session.AttentionApproval:
		return "Approvals"
	case session.AttentionQuestion:
		return "Questions"
	case session.AttentionAsk:
		return "Asked you"
	case session.AttentionMail:
		return "Mail"
	case session.AttentionErrored:
		return "Errored"
	case session.AttentionResume:
		return "Resume"
	case session.AttentionFinished:
		return "Finished"
	case session.AttentionOutbox:
		return "Waiting to send"
	}
	return kind
}

// waitedFor is how long an item has waited, in words short enough for a
// column: 40s, 12m, 3h, 2d.
func waitedFor(since int64, now time.Time) string {
	if since <= 0 {
		return "?"
	}
	d := max(now.Sub(time.Unix(0, since)), 0)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		return strconv.Itoa(int(d.Hours())/24) + "d"
	}
}

func runListAttention(sessionName, host string, kinds []string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	params := map[string]any{}
	if sessionName != "" {
		params["session"] = sessionName
	}
	if host != "" {
		params["host"] = host
	}
	if len(kinds) > 0 {
		params["kinds"] = kinds
	}
	raw, err := client.Call("list-attention", params)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	return printAttentionList(os.Stdout, raw, time.Now())
}

// printAttentionList prints the Inbox grouped by kind, oldest first, one row
// per item: how long it has waited, where it is, and what it says. Every name
// and summary was written by an agent, so each is kept to one plain line.
func printAttentionList(w io.Writer, raw json.RawMessage, now time.Time) error {
	var res struct {
		Items []attentionRow `json:"items"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Items) == 0 {
		fmt.Fprintln(w, "Nothing is waiting for you.")
		return nil
	}
	kind := ""
	for _, it := range res.Items {
		if it.Kind != kind {
			if kind != "" {
				fmt.Fprintln(w)
			}
			kind = it.Kind
			fmt.Fprintf(w, "%s\n", attentionGroupTitle(kind))
		}
		where := plainLine(it.Session)
		if it.Host != "" {
			where = plainLine(it.Host) + ":" + where
		}
		name := plainLine(it.Name)
		if name == "" && it.Window != "" {
			name = shortWindowID(it.Window)
		}
		if name != "" {
			where += "/" + name
		}
		if it.Kind == session.AttentionOutbox {
			where = "for " + plainLine(it.ForHost)
		}
		summary := plainLine(it.Summary)
		if it.HeldID != 0 {
			summary = fmt.Sprintf("[held for %s, message %d] %s", plainLine(it.HeldFor), it.HeldID, summary)
		}
		if it.Count > 1 {
			summary = strings.TrimSpace(summary + fmt.Sprintf(" (%d)", it.Count))
		}
		fmt.Fprintf(w, "  %4s  %-6s %s", waitedFor(it.Since, now), "#"+it.ID, where)
		if summary != "" {
			fmt.Fprintf(w, "  %s", summary)
		}
		switch {
		case it.Kind == session.AttentionAsk:
			// The answers are what the question is; the keys are the Inbox's.
			opts := make([]string, len(it.Options))
			for i, o := range it.Options {
				opts[i] = strconv.Itoa(i+1) + " " + plainLine(o)
			}
			fmt.Fprintf(w, "  (%s; answer in the Inbox)", strings.Join(opts, ", "))
		case it.RequestID != "":
			fmt.Fprintf(w, "  (%s)", attentionHeldNote)
		}
		if it.Stale {
			// What that machine said last. Nobody can check it now.
			seen := "never"
			if it.SeenAt > 0 {
				seen = waitedFor(it.SeenAt, now) + " ago"
			}
			fmt.Fprintf(w, "  [unreachable, seen %s]", seen)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "\n%d waiting. Open the Inbox with the prefix key then i, or jump to the oldest with the prefix key then o.\n", len(res.Items))
	return nil
}

// newListAttentionCommand is `tuios list-attention`.
func newListAttentionCommand() *cobra.Command {
	var sessionName, host string
	var kinds []string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list-attention",
		Short: "List what is waiting for you in every session: the Inbox",
		Long: `List the Inbox: every approval and question an agent is blocked on, mail to
you, errored agents, conversations a restart left to resume, and finished turns
nobody has looked at, in every session on this machine and on every linked
host. Rows are grouped Approvals, Questions, Mail, Errored, Resume, Finished,
oldest first, with how long each has waited. A row of another machine is named
host:session, and a row of a machine whose link is down says when it was last
heard from.

An item closes by itself when what opened it stops being true: the agent
leaves needs_input or errored, the mail is read, a client focuses the pane
that finished, or the conversation is resumed ('tuios resume-agent').
Dismissing one is for the person at an attached client, from the Inbox
(prefix i). 'tuios subscribe --types attention' streams every change.`,
		Example: `  # What needs me?
  tuios list-attention

  # Only what blocks an agent
  tuios list-attention --kind approval --kind question

  # Only what waits on the build host
  tuios list-attention --host build

  # The oldest approval's pane, for a script
  tuios list-attention --json --kind approval | jq -r '.items[0].window'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListAttention(sessionName, host, kinds, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Only this session on this machine, or on --host (default: every session)")
	cmd.Flags().StringVar(&host, "host", "", "Only this machine: local, or a linked host by name (default: every machine)")
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "Only these kinds: "+strings.Join(session.AttentionKindNames, ", "))
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("kind", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return session.AttentionKindNames, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}
