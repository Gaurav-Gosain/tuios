package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/courier"
	"github.com/spf13/cobra"
)

// syncTimeout bounds the sync a command does before it reads the store.
const syncTimeout = 15 * time.Second

// short is the first eight characters of an id, what people type.
func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// formatEntry is one message as an agent reads it: a header of trusted local
// facts, the fenced text the sender wrote, and how to answer.
func formatEntry(e courier.Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%s  from %s", short(e.Msg.ID), e.Peer)
	if e.Msg.FromAgent != "" {
		fmt.Fprintf(&b, " (%s)", e.Msg.FromAgent)
	}
	if e.Msg.Agent != "" {
		fmt.Fprintf(&b, "  to %s", e.Msg.Agent)
	}
	fmt.Fprintf(&b, "  thread %s", short(e.Msg.Thread))
	if e.Msg.ReplyTo != "" {
		fmt.Fprintf(&b, "  reply to %s", short(e.Msg.ReplyTo))
	}
	fmt.Fprintf(&b, "  %s\n", e.Msg.SentAt.Local().Format("2006-01-02 15:04"))
	body := courier.CleanText(e.Msg.Body)
	if s := courier.CleanText(e.Msg.Subject); s != "" {
		body = "Subject: " + s + "\n\n" + body
	}
	b.WriteString(courier.Fence(e.Peer+" via tuios-courier", body))
	fmt.Fprintf(&b, "\nReply: tuios-courier reply %s 'your answer'\n", short(e.Msg.ID))
	return b.String()
}

func writeEntries(w io.Writer, es []courier.Entry) error {
	for i, e := range es {
		if i > 0 {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, formatEntry(e)); err != nil {
			return err
		}
	}
	return nil
}

// syncQuietly fetches new mail, and on failure says so on stderr and goes on
// with what the store already has.
func syncQuietly(ctx context.Context, cmd *cobra.Command, e *env) {
	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	res, err := e.client.Sync(ctx, 0)
	reportSync(cmd.ErrOrStderr(), res, err)
}

func reportSync(w io.Writer, res courier.SyncResult, err error) {
	if err != nil {
		fmt.Fprintf(w, "tuios-courier: could not reach the relay, showing mail already here: %v\n", err)
	}
	for reason, n := range res.Rejected {
		fmt.Fprintf(w, "tuios-courier: refused %d message(s): %s\n", n, reason)
	}
	if res.Pending > 0 {
		fmt.Fprintf(w, "tuios-courier: %d message(s) from senders not in peers are kept until the person adds them\n", res.Pending)
	}
}

func heldCount(st *courier.Store) int {
	n := 0
	for _, e := range st.List() {
		if !e.Released {
			n++
		}
	}
	return n
}

// readBody is the message argument, or stdin when it is "-".
func readBody(cmd *cobra.Command, arg string) (string, error) {
	if arg != "-" {
		return arg, nil
	}
	data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), courier.MaxBodyBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > courier.MaxBodyBytes {
		return "", fmt.Errorf("the message is over %d bytes", courier.MaxBodyBytes)
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func printSent(cmd *cobra.Command, m courier.Message, to string, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"id": m.ID, "thread": m.Thread, "to": to})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Sent %s to %s (thread %s). Wait for the answer with: tuios-courier wait --thread %s\n",
		short(m.ID), to, short(m.Thread), short(m.Thread))
	return nil
}

func newSendCmd() *cobra.Command {
	var agent, as, subject string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "send PEER MESSAGE|-",
		Short: "Send a message to a peer's agent",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			body, err := readBody(cmd, args[1])
			if err != nil {
				return err
			}
			m, err := e.client.Send(cmd.Context(), courier.Outgoing{To: args[0], Agent: agent, FromAgent: as, Subject: subject, Body: body})
			if err != nil {
				return err
			}
			return printSent(cmd, m, args[0], asJSON)
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "Which of the peer's agents it is for (empty: any)")
	cmd.Flags().StringVar(&as, "as", os.Getenv(agentEnv), "Your agent's label, so the reply comes back to you (default $"+agentEnv+")")
	cmd.Flags().StringVar(&subject, "subject", "", "A short subject")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newReplyCmd() *cobra.Command {
	var as string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "reply ID MESSAGE|-",
		Short: "Answer a message, in its thread, to the agent that wrote it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			body, err := readBody(cmd, args[1])
			if err != nil {
				return err
			}
			orig, err := e.store.Find(args[0])
			if err != nil {
				return err
			}
			m, err := e.client.Reply(cmd.Context(), orig.Key, courier.Outgoing{Body: body, FromAgent: as})
			if err != nil {
				return err
			}
			return printSent(cmd, m, orig.Peer, asJSON)
		},
	}
	cmd.Flags().StringVar(&as, "as", os.Getenv(agentEnv), "Your agent's label (default $"+agentEnv+")")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func filterFlags(cmd *cobra.Command, f *courier.Filter) {
	cmd.Flags().StringVar(&f.Agent, "agent", os.Getenv(agentEnv), "Your agent's label: mail for it and unlabeled mail (default $"+agentEnv+")")
	cmd.Flags().StringVar(&f.Thread, "thread", "", "Only this thread (an id or its first eight characters)")
}

// resolveThread turns a thread prefix into the full id, from the mail this
// person sent or received.
func resolveThread(e *env, f *courier.Filter) error {
	if f.Thread == "" || courier.ValidID(f.Thread) {
		return nil
	}
	if len(f.Thread) < 6 {
		return fmt.Errorf("thread %q: give its id, or at least six characters of it", f.Thread)
	}
	if full, ok := e.store.ThreadByPrefix(f.Thread); ok {
		f.Thread = full
		return nil
	}
	return fmt.Errorf("no thread %q", f.Thread)
}

func newReadCmd() *cobra.Command {
	var f courier.Filter
	cmd := &cobra.Command{
		Use:   "read",
		Short: "Print the mail released to you, and mark it read",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			if err := resolveThread(e, &f); err != nil {
				return err
			}
			syncQuietly(cmd.Context(), cmd, e)
			n, err := e.store.Deliver(f, func(es []courier.Entry) error { return writeEntries(cmd.OutOrStdout(), es) })
			if err != nil {
				return err
			}
			if n == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "No new mail.")
			}
			if h := heldCount(e.store); h > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "%d message(s) are held for your person to release.\n", h)
			}
			return nil
		},
	}
	filterFlags(cmd, &f)
	return cmd
}

func newWaitCmd() *cobra.Command {
	var f courier.Filter
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until mail reaches you, print it and mark it read",
		Long: `Block until released mail for you arrives, print it and exit 0. Exit 2
when the timeout passes first. Use --thread to wait for the answer to one
question.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			if err := resolveThread(e, &f); err != nil {
				return err
			}
			deadline := time.Now().Add(timeout)
			for {
				_, err := e.client.Wait(cmd.Context(), f, time.Until(deadline))
				if errors.Is(err, courier.ErrWaitTimeout) {
					return &exitError{code: 2, err: fmt.Errorf("no mail within %v", timeout)}
				}
				if err != nil {
					return err
				}
				n, err := e.store.Deliver(f, func(es []courier.Entry) error { return writeEntries(cmd.OutOrStdout(), es) })
				if err != nil {
					return err
				}
				if n > 0 {
					return nil
				}
				// Another reader took it first: keep waiting.
			}
		},
	}
	filterFlags(cmd, &f)
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "How long to wait")
	return cmd
}

func stateOf(e courier.Entry) string {
	switch {
	case e.Read:
		return "read"
	case e.Released:
		return "released"
	}
	return "held"
}

// oneLine is untrusted text cut to one short line for a listing.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(courier.CleanText(s)), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func newInboxCmd() *cobra.Command {
	var all, asJSON bool
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "List mail that is held or unread, for the person",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			syncQuietly(cmd.Context(), cmd, e)
			var rows []courier.Entry
			for _, en := range e.store.List() {
				if all || !en.Read {
					rows = append(rows, en)
				}
			}
			out := cmd.OutOrStdout()
			if asJSON {
				type row struct {
					ID         string    `json:"id"`
					Key        string    `json:"key"`
					Peer       string    `json:"peer"`
					State      string    `json:"state"`
					Agent      string    `json:"agent"`
					FromAgent  string    `json:"from_agent"`
					Subject    string    `json:"subject"`
					Thread     string    `json:"thread"`
					ReceivedAt time.Time `json:"received_at"`
				}
				list := []row{}
				for _, en := range rows {
					list = append(list, row{en.Msg.ID, en.Key, en.Peer, stateOf(en), en.Msg.Agent, en.Msg.FromAgent,
						courier.CleanText(en.Msg.Subject), en.Msg.Thread, en.ReceivedAt})
				}
				return json.NewEncoder(out).Encode(map[string]any{"messages": list, "pending": len(e.store.Pending())})
			}
			if len(rows) == 0 {
				fmt.Fprintln(out, "Nothing held or unread.")
				return nil
			}
			now := time.Now()
			for _, en := range rows {
				what := en.Msg.Subject
				if what == "" {
					what = en.Msg.Body
				}
				to := en.Msg.Agent
				if to == "" {
					to = "any"
				}
				fmt.Fprintf(out, "%s  %-8s  from %-12s to %-10s %-8s  %s\n", short(en.Msg.ID), stateOf(en), en.Peer, to, age(now, en.ReceivedAt), oneLine(what, 50))
			}
			fmt.Fprintln(out, "\nRead one with tuios-courier show ID; let your agents have it with tuios-courier release ID.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include mail already read")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show ID",
		Short: "Print one message for the person, without marking it read",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			syncQuietly(cmd.Context(), cmd, e)
			en, err := e.store.Find(args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", stateOf(en))
			_, err = io.WriteString(cmd.OutOrStdout(), formatEntry(en))
			return err
		},
	}
}

func newReleaseCmd() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "release ID... | --from PEER",
		Short: "Let your agents read held mail",
		RunE: func(cmd *cobra.Command, args []string) error {
			if (from == "") == (len(args) == 0) {
				return errors.New("name the messages to release, or --from PEER, not both")
			}
			e, err := loadEnv()
			if err != nil {
				return err
			}
			syncQuietly(cmd.Context(), cmd, e)
			n := 0
			if from != "" {
				if _, ok := e.cfg.Peers[from]; !ok {
					return fmt.Errorf("no peer named %q", from)
				}
				for _, en := range e.store.List() {
					if en.Peer == from && !en.Released {
						if err := e.store.Release(en.Key); err != nil {
							return err
						}
						n++
					}
				}
			}
			for _, ref := range args {
				en, err := e.store.Find(ref)
				if err != nil {
					return err
				}
				if err := e.store.Release(en.Key); err != nil {
					return err
				}
				n++
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Released %d message(s).\n", n)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "Release everything held from this peer")
	return cmd
}

func newDropCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drop ID...",
		Short: "Delete messages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			for _, ref := range args {
				en, err := e.store.Find(ref)
				if err != nil {
					return err
				}
				if err := e.store.Drop(en.Key); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dropped %d message(s).\n", len(args))
			return nil
		},
	}
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Print a line as mail arrives, for the person",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := loadEnv()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			seen := map[string]bool{}
			for _, en := range e.store.List() {
				seen[en.Key] = true
			}
			fmt.Fprintf(out, "Watching for mail to %s. Ctrl-C to stop.\n", e.cfg.Name)
			failing := false
			for cmd.Context().Err() == nil {
				ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
				res, err := e.client.Sync(ctx, 10*time.Second)
				cancel()
				if err != nil {
					if !failing && cmd.Context().Err() == nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "%s  relay unreachable, retrying: %v\n", time.Now().Format("15:04"), err)
					}
					failing = true
					select {
					case <-cmd.Context().Done():
					case <-time.After(5 * time.Second):
					}
					continue
				}
				if failing {
					fmt.Fprintf(cmd.ErrOrStderr(), "%s  relay reachable again\n", time.Now().Format("15:04"))
					failing = false
				}
				reportSync(cmd.ErrOrStderr(), courier.SyncResult{Rejected: res.Rejected}, nil)
				for _, en := range e.store.List() {
					if seen[en.Key] {
						continue
					}
					seen[en.Key] = true
					what := en.Msg.Subject
					if what == "" {
						what = en.Msg.Body
					}
					fmt.Fprintf(out, "%s  %s  %s from %s: %s\n", time.Now().Format("15:04"), short(en.Msg.ID), stateOf(en), en.Peer, oneLine(what, 60))
				}
			}
			return nil
		},
	}
}
