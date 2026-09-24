package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newQueueCommand builds `tuios queue`: leave a message for an agent, typed
// as a prompt when it comes to rest, and list or drop what waits.
func newQueueCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "queue [flags] TEXT...",
		Short: "Queue a message for an agent, typed when it comes to rest",
		Long: `Queue a message for the agent in a pane. It is typed as a prompt once the
agent has been at rest for a second, one message per rest, and never over a
prompt the agent is waiting on. If the agent is at rest now it is typed right
away.

The daemon watches for the agent to take it. A message the agent shows no sign
of taking is marked stalled, is never typed again, and opens a question in the
Inbox: look at the pane. A stalled message holds the ones behind it until the
agent next works or you drop it with 'tuios queue rm'.

A pane holds at most [agents.queue] max messages (8 by default), of at most
16 KiB each. The queue is kept in the daemon's memory: it is dropped when the
daemon restarts, when the pane closes, and when the agent leaves the pane.

From inside a pane, the message is checked like send-text: the target must
hold no grant this pane does not, and is checked against this pane's grants
again when it is typed. The words are joined with spaces. To queue text that
reads ls or rm, put -- before it.`,
		Example: `  # Reply to the agent in the build pane once it finishes its turn
  tuios queue -w build 'make the backoff jitter configurable'

  # What waits, in this session or for one pane
  tuios queue ls
  tuios queue ls -w build

  # Drop one message, or everything queued for a pane
  tuios queue rm q3
  tuios queue rm --all -w build`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runQueuePrompt(sessionName, window, strings.Join(args, " "), jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The agent's pane, by name or id (default: the focused pane)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	cmd.AddCommand(newQueueListCommand(), newQueueRemoveCommand())
	return cmd
}

// newQueueListCommand builds `tuios queue ls`.
func newQueueListCommand() *cobra.Command {
	var sessionName, window string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the messages waiting for agents",
		Long: `List the messages waiting to be typed to agents: every pane of the session, or
one pane with -w. Each line has the entry's id, its state (waiting,
delivering, or stalled), who queued it (human, shell, a pane's id, or
link:HOST), its age, the pane, and the first line of the message.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runQueueList(os.Stdout, sessionName, window, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session to list (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "Only this pane, by name or id")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// newQueueRemoveCommand builds `tuios queue rm`.
func newQueueRemoveCommand() *cobra.Command {
	var sessionName, window string
	var all, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "rm [ID | --all]",
		Short: "Drop queued messages before they are typed",
		Long: `Drop a queued message by its id, or with --all every message queued for a pane
(-w, default the focused pane). A message being typed cannot be dropped.

From inside a pane only the messages that pane queued can be dropped. From a
shell outside every pane, every message but the ones you queued from the
Inbox, which only the attached client can drop.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 0:
				return errors.New("pass an id or --all, not both")
			case !all && len(args) == 0:
				return errors.New("name the message to drop by its id (see tuios queue ls), or pass --all")
			}
			id := ""
			if len(args) > 0 {
				id = args[0]
			}
			return runQueueRemove(sessionName, window, id, all, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Session of the pane (default: this pane's, else the most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "The pane, by name or id")
	cmd.Flags().BoolVar(&all, "all", false, "Drop every message queued for the pane that you may drop")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

func runQueuePrompt(sessionName, window, text string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(map[string]any{"text": text})
	if t.window != "" {
		params["window"] = t.window
	}
	raw, err := t.client.Call("queue-prompt", params)
	if err != nil {
		return reportVerbError(t.explain("queue-prompt", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		ID         string `json:"id"`
		Position   int    `json:"position"`
		Queued     int    `json:"queued"`
		Delivering bool   `json:"delivering"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(describeQueued(res.ID, window, res.Position, res.Delivering) + t.on())
	return nil
}

// describeQueued is the line queue prints.
func describeQueued(id, window string, position int, delivering bool) string {
	target := "the focused pane"
	if window != "" {
		target = plainLine(window)
	}
	switch {
	case delivering:
		return fmt.Sprintf("Queued %s for %s. The agent is at rest, so it is typed now.", plainLine(id), target)
	case position <= 1:
		return fmt.Sprintf("Queued %s for %s. It is typed when the agent comes to rest.", plainLine(id), target)
	default:
		return fmt.Sprintf("Queued %s for %s, number %d in line. Each is typed at the agent's next rest.", plainLine(id), target, position)
	}
}

// queueRow is one entry of list-queued.
type queueRow struct {
	ID      string `json:"id"`
	Window  string `json:"window"`
	Name    string `json:"name"`
	At      int64  `json:"at"`
	By      string `json:"by"`
	From    string `json:"from"`
	Preview string `json:"preview"`
	State   string `json:"state"`
}

func runQueueList(w io.Writer, sessionName, window string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(nil)
	if t.window != "" {
		params["window"] = t.window
	}
	raw, err := t.client.Call("list-queued", params)
	if err != nil {
		return reportVerbError(t.explain("list-queued", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Session string     `json:"session"`
		Entries []queueRow `json:"entries"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return printQueueRows(w, res.Session+t.on(), res.Entries)
}

// printQueueRows writes list-queued's entries as a table. Every field but the
// id and state came from a caller, so each is kept to one plain line.
func printQueueRows(w io.Writer, where string, rows []queueRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintf(w, "Nothing is queued in session %s.\n", plainLine(where))
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tBY\tAGE\tPANE\tMESSAGE")
	for _, r := range rows {
		by := r.By
		if len(by) > 12 && !strings.HasPrefix(by, "link:") {
			by = shortWindowID(by)
		}
		pane := r.Name
		if pane == "" {
			pane = shortWindowID(r.Window)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", plainLine(r.ID), plainLine(r.State), plainLine(by), agoOf(r.At), plainLine(pane), plainLine(r.Preview))
	}
	return tw.Flush()
}

func runQueueRemove(sessionName, window, id string, all, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params := t.params(nil)
	if t.window != "" {
		params["window"] = t.window
	}
	if all {
		params["all"] = true
	} else {
		params["id"] = id
	}
	raw, err := t.client.Call("cancel-queued", params)
	if err != nil {
		return reportVerbError(t.explain("cancel-queued", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Cancelled []string `json:"cancelled"`
		Queued    int      `json:"queued"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	switch len(res.Cancelled) {
	case 0:
		fmt.Printf("Nothing was dropped%s. %d still queued.\n", t.on(), res.Queued)
	case 1:
		fmt.Printf("Dropped %s%s. %d still queued.\n", plainLine(res.Cancelled[0]), t.on(), res.Queued)
	default:
		fmt.Printf("Dropped %d messages%s. %d still queued.\n", len(res.Cancelled), t.on(), res.Queued)
	}
	return nil
}
