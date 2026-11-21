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

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

type attentionRow struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Host            string   `json:"host"`
	Session         string   `json:"session"`
	Window          string   `json:"window"`
	Workspace       int      `json:"workspace"`
	Harness         string   `json:"harness"`
	Name            string   `json:"name"`
	Summary         string   `json:"summary"`
	RiskyFlags      []string `json:"risky_flags"`
	Since           int64    `json:"since"`
	Thread          uint64   `json:"thread"`
	Count           int      `json:"count"`
	Seen            int      `json:"seen"`
	RequestID       string   `json:"request_id"`
	Options         []string `json:"options"`
	Stale           bool     `json:"stale"`
	SeenAt          int64    `json:"seen_at"`
	HeldFor         string   `json:"held_for"`
	HeldID          uint64   `json:"held_id"`
	ForHost         string   `json:"for_host"`
	SnoozedUntil    int64    `json:"snoozed_until"`
	WithParentUntil int64    `json:"with_parent_until"`
}

const attentionHeldNote = "held: answer in the Inbox"

func attentionGroupTitle(kind string) string {
	switch kind {
	case session.AttentionApproval:
		return "Approvals"
	case session.AttentionPlan:
		return "Plans"
	case session.AttentionQuestion, session.AttentionAsk:
		return "Questions"
	case session.AttentionMail:
		return "Mail"
	case session.AttentionErrored:
		return "Errored"
	case session.AttentionResume:
		return "Resume"
	case session.AttentionFinished:
		return "Done"
	case session.AttentionOutbox:
		return "Waiting to send"
	}
	return kind
}

func runListAttention(sessionName, host string, kinds []string, selector string, snoozed, jsonOutput bool) error {
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
	if selector != "" {
		params["select"] = selector
	}
	if snoozed {
		params["include_snoozed"] = true
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

func printAttentionList(w io.Writer, raw json.RawMessage, now time.Time) error {
	var res struct {
		Items []attentionRow `json:"items"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Items) == 0 {
		fmt.Fprintln(w, "Nothing is waiting for you.")
		return nil
	}
	heading := ""
	waiting := 0
	for _, it := range res.Items {
		title := attentionGroupTitle(it.Kind)
		if it.SnoozedUntil != 0 {
			title = "Snoozed"
		} else if it.WithParentUntil == 0 {
			waiting++
		}
		if title != heading {
			if heading != "" {
				fmt.Fprintln(w)
			}
			heading = title
			fmt.Fprintf(w, "%s\n", heading)
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
		if warn := session.ResumeWarning(it.RiskyFlags); warn != "" {
			summary = plainLine(warn) + ": " + summary
		}
		if it.Kind == session.AttentionMail && it.Seen > 0 {
			summary = strings.TrimSpace(summary + fmt.Sprintf(" [%d seen]", it.Seen))
		}
		fmt.Fprintf(w, "  %4s  %-6s %s", units.Waited(it.Since, now), "#"+it.ID, where)
		if summary != "" {
			fmt.Fprintf(w, "  %s", summary)
		}
		switch {
		case it.Kind == session.AttentionAsk:
			opts := make([]string, len(it.Options))
			for i, o := range it.Options {
				opts[i] = strconv.Itoa(i+1) + " " + plainLine(o)
			}
			fmt.Fprintf(w, "  (%s; answer in the Inbox)", strings.Join(opts, ", "))
		case it.RequestID != "":
			fmt.Fprintf(w, "  (%s)", attentionHeldNote)
		}
		if it.SnoozedUntil != 0 {
			fmt.Fprintf(w, "  [%s]", snoozedUntilWords(it.SnoozedUntil, now))
		}
		if it.WithParentUntil != 0 {
			left := max(it.WithParentUntil-now.UnixNano(), 0)
			fmt.Fprintf(w, "  [with the parent agent, comes to you in %s]", units.Waited(now.UnixNano()-left, now))
		}
		if it.Stale {
			seen := "never"
			if it.SeenAt > 0 {
				seen = units.Waited(it.SeenAt, now) + " ago"
			}
			fmt.Fprintf(w, "  [unreachable, seen %s]", seen)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "\n%d waiting. Open the agents view with the prefix key then i, or jump to the oldest with the prefix key then o.\n", waiting)
	return nil
}

func snoozedUntilWords(until int64, now time.Time) string {
	if until < 0 {
		return "snoozed until it changes"
	}
	at := time.Unix(0, until).In(now.Location())
	if y, m, d := now.Date(); at.Year() == y && at.Month() == m && at.Day() == d {
		return "snoozed until " + at.Format("15:04")
	}
	return "snoozed until " + at.Format("Mon Jan 2 15:04")
}

func newListAttentionCommand() *cobra.Command {
	var sessionName, host, selector string
	var kinds []string
	var jsonOutput, snoozed bool
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
(prefix i), and so are snoozing, marking unread and undo: they have no command
here. --snoozed also lists the items the person snoozed, after the rest, with
when each opens again. 'tuios subscribe --types attention' streams every
change.`,
		Example: `  # What needs me?
  tuios list-attention

  # Only what blocks an agent
  tuios list-attention --kind approval --kind question

  # Only what waits on the build host
  tuios list-attention --host build

  # Only the codex agents of one fan-out
  tuios list-attention --select 'harness:codex group:fan/add-retry'

  # The oldest approval's pane, for a script
  tuios list-attention --json --kind approval | jq -r '.items[0].window'

  # Everything, the snoozed items too
  tuios list-attention --snoozed`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListAttention(sessionName, host, kinds, selector, snoozed, jsonOutput)
		},
	}
	cmd.Flags().StringVar(&selector, "select", "", "Only the items a selector matches: space-separated key:value terms, such as 'harness:codex needs:you'")
	addSessionFlag(cmd, &sessionName, "Only this session on this machine, or on --host (default: every session)")
	cmd.Flags().StringVar(&host, "host", "", "Only this machine: local, or a linked host by name (default: every machine)")
	cmd.Flags().StringSliceVar(&kinds, "kind", nil, "Only these kinds: "+strings.Join(session.AttentionKindNames, ", "))
	cmd.Flags().BoolVar(&snoozed, "snoozed", false, "Also list the items snoozed in the Inbox, after the rest")
	addJSONFlag(cmd, &jsonOutput)
	_ = cmd.RegisterFlagCompletionFunc("kind", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return session.AttentionKindNames, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}
