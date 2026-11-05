package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// subscribeOptions are the flags of tuios subscribe.
type subscribeOptions struct {
	session  string
	window   string
	types    []string
	afterSeq uint64
	resume   bool
	bootID   string
	count    int
	hosts    bool
	format   string
	states   []string
	once     bool
	timeout  int
}

// newSubscribeCommand builds tuios subscribe, the command-line end of the
// subscribe verb. It prints the stream as NDJSON so a script can pipe it to jq
// or keep the last seq and boot id to resume from after a reconnect.
func newSubscribeCommand() *cobra.Command {
	var opts subscribeOptions
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Print the daemon's event stream as JSON lines or readable lines",
		Long: `Open the daemon's event stream and print it, one JSON object per line.

--format text prints one readable line per event instead: the local time,
the session and pane, and what happened. An agent-state line shows the
previous state, the new state and the message the agent gave:

  12:04:31  work api  working -> needs_input  Allow Bash?

In text mode the ack goes to standard error, so standard output holds only
event lines.

--state keeps only the agent-state events whose new state is in the list.
Without --types, it also sets --types agent-state. Events of other types and
gap lines are not filtered by it.

--count counts the lines that pass the filters, gap lines included. --once is
the same as --count 1. --timeout stops the stream after some milliseconds.
With --once or --count, the command exits 1 if the limit is not met by then.

The first line is the subscribe ack. It carries seq, the last event number
assigned when the stream went live, and boot_id, a random id for this daemon
start. Every event after it carries its own seq and boot_id.

Without --session the stream covers every session on the daemon. That is not
the rule most commands follow, where an omitted session means the most
recently active one.

To resume after a disconnect, pass the seq of the last event you printed and
the boot_id it came with. The daemon replays the events it still holds after
that seq, then carries on live. When it cannot replay everything you missed, a
gap line comes first and names the reason:

  evicted       the replay ring no longer holds the oldest events you missed
  boot_changed  the daemon restarted, so the seq numbers start over
  not_retained  your filter includes output events, which are never replayed
  overflow      you read too slowly and events were dropped (live stream)

After a gap, read current state again (list-agents, list-windows) instead of
trusting that you saw every change.`,
		Example: `  # Every agent state change on the daemon
  tuios subscribe --types agent-state

  # One session's window lifecycle
  tuios subscribe -s work --types window-created,window-closed

  # Resume where a previous run stopped
  tuios subscribe --types agent-state --after-seq 118 --boot-id 9f2c41d07a3e8b65

  # Wait for the next bell anywhere, then exit
  tuios subscribe --types bell --count 1

  # Watch agents as readable lines
  tuios subscribe --format text --types agent-state

  # Wait up to a minute for an agent that needs you, then exit
  tuios subscribe --format text --state needs_input,errored --once --timeout 60000

  # Every agent on every machine, and every host coming or going
  tuios subscribe --hosts --types agent-state,host-changed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The stream keeps this process running; --pprof is a persistent
			// flag that subscribe used to accept and ignore.
			startPprofServer()
			opts.resume = cmd.Flags().Changed("after-seq")
			if opts.bootID != "" && !opts.resume {
				return errors.New("--boot-id only means something with --after-seq: pass the seq the last run printed with it")
			}
			if err := checkSubscribeFlags(cmd, &opts); err != nil {
				return err
			}
			return runSubscribe(opts, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&opts.session, "session", "s", "", "Only events from this session (default: every session)")
	cmd.Flags().StringVarP(&opts.window, "window", "w", "", "Only events about this window id")
	cmd.Flags().StringSliceVar(&opts.types, "types", nil, "Only these event types, comma-separated (default: all)")
	cmd.Flags().Uint64Var(&opts.afterSeq, "after-seq", 0, "Replay the retained events after this seq before streaming live")
	cmd.Flags().StringVar(&opts.bootID, "boot-id", "", "The boot_id the --after-seq came with, so a daemon restart shows as a gap")
	cmd.Flags().BoolVar(&opts.hosts, "hosts", false, "Also print the agent-state and session events of every linked host, each with host set")
	cmd.Flags().IntVar(&opts.count, "count", 0, "Exit after printing this many events, after filters and with gap lines (default: run until the stream ends)")
	cmd.Flags().BoolVar(&opts.once, "once", false, "Exit after printing the first event that passes the filters, the same as --count 1")
	cmd.Flags().StringVar(&opts.format, "format", "json", "Output format: json (one JSON object per line) or text (one readable line per event)")
	cmd.Flags().StringSliceVar(&opts.states, "state", nil, "Only agent-state events with these new states, comma-separated. Sets --types agent-state when --types is not given")
	cmd.Flags().IntVar(&opts.timeout, "timeout", 0, "Stop after this many milliseconds. Exit 1 if --once or --count is not met by then (default: no limit)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("format", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"json", "text"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("state", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return session.AgentStateNames, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("types", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return session.EventTypeNames, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// runSubscribe subscribes and copies the stream to out until it ends, the
// --count or --once limit is met, or the --timeout ends.
func runSubscribe(opts subscribeOptions, out io.Writer) error {
	text := opts.format == "text"
	t, err := dialTarget(opts.session, opts.window)
	if err != nil {
		return err
	}
	defer t.Close()

	var printer *textPrinter
	if text {
		printer = newTextPrinter()
		printer.host = t.host
		// The panes as they are now, so the first state line can say where
		// its agent came from. A daemon that refuses the call costs only
		// the names and the previous states.
		seed := map[string]any{"all": true}
		if t.session == "" {
			seed["all_sessions"] = true
		} else {
			seed["session"] = t.session
		}
		if raw, err := t.client.Call("list-agents", seed); err == nil {
			printer.seed(raw)
		}
		var second *verbTarget
		defer func() {
			if second != nil {
				second.Close()
			}
		}()
		printer.lookup = func(sessionName string) []agentRow {
			if second == nil {
				dialed, err := dialTarget(opts.session, opts.window)
				if err != nil {
					printer.lookup = nil
					return nil
				}
				second = dialed
			}
			raw, err := second.client.Call("list-agents", map[string]any{"session": sessionName, "all": true})
			if err != nil {
				return nil
			}
			var res struct {
				Agents []agentRow `json:"agents"`
			}
			_ = jsonx.Unmarshal(raw, &res)
			return res.Agents
		}
	}

	params := map[string]any{"window": opts.window}
	if len(opts.types) > 0 {
		params["types"] = opts.types
	}
	if opts.resume {
		params["after_seq"] = opts.afterSeq
		if opts.bootID != "" {
			params["boot_id"] = opts.bootID
		}
	}
	if opts.hosts {
		params["hosts"] = true
	}
	raw, err := t.client.Call("subscribe", t.params(params))
	if err != nil {
		return t.explain("subscribe", err)
	}
	if text {
		// The ack goes to standard error, so standard output holds one line
		// per event and nothing else.
		var ack struct {
			Seq      uint64 `json:"seq"`
			BootID   string `json:"boot_id"`
			Replayed int    `json:"replayed"`
		}
		_ = jsonx.Unmarshal(raw, &ack)
		msg := fmt.Sprintf("Subscribed at seq %d, boot id %s.", ack.Seq, plainLine(ack.BootID))
		if ack.Replayed > 0 {
			msg += fmt.Sprintf(" %d events from before follow.", ack.Replayed)
		}
		fmt.Fprintln(os.Stderr, msg)
	} else if _, err := fmt.Fprintln(out, string(t.result(raw))); err != nil {
		return err
	}

	states := map[string]bool{}
	for _, s := range opts.states {
		states[s] = true
	}
	limit := opts.count
	if opts.once {
		limit = 1
	}
	var deadline time.Time
	if opts.timeout > 0 {
		deadline = time.Now().Add(time.Duration(opts.timeout) * time.Millisecond)
	}
	for printed := 0; limit <= 0 || printed < limit; {
		var wait time.Duration
		if !deadline.IsZero() {
			if wait = time.Until(deadline); wait <= 0 {
				return subscribeTimeout(opts.timeout, printed, limit)
			}
		}
		line, err := t.client.ReadEventLine(wait)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) && !deadline.IsZero() {
				return subscribeTimeout(opts.timeout, printed, limit)
			}
			if errors.Is(err, io.EOF) {
				return errors.New("the daemon closed the event stream. Resubscribe with --after-seq and --boot-id to pick up where it stopped")
			}
			return errfmt.Errorf("failed to read the event stream: %w", err)
		}
		var ev subscribeEvent
		if text || len(states) > 0 {
			_ = jsonx.Unmarshal(line, &ev)
		}
		keep := len(states) == 0 || ev.Type != session.EventAgentState || states[ev.State]
		if text {
			if keep {
				if _, err := fmt.Fprintln(out, printer.line(&ev)); err != nil {
					return err
				}
			}
			printer.note(&ev)
		} else if keep {
			if t.host != "" {
				line = t.result(json.RawMessage(line))
			}
			// The daemon escapes C1 controls itself; a daemon on another
			// machine may be older, so the line is escaped here too.
			if _, err := fmt.Fprintln(out, jsonSafe(string(line))); err != nil {
				return err
			}
		}
		if keep {
			printed++
		}
	}
	return nil
}

// subscribeTimeout is how a --timeout ends the run. Without --once or
// --count it only stops the stream, so the run succeeds. With one, the limit
// was not met, and that fails like a wait-for that timed out.
func subscribeTimeout(ms, printed, limit int) error {
	if limit <= 0 {
		return nil
	}
	if limit == 1 {
		return errfmt.Errorf("no event matched in %d ms", ms)
	}
	return errfmt.Errorf("the timeout of %d ms ended after %d of %d events", ms, printed, limit)
}

// checkSubscribeFlags checks the flags that work together and fills in what
// --state implies. It runs before the daemon is dialed.
func checkSubscribeFlags(cmd *cobra.Command, opts *subscribeOptions) error {
	switch opts.format {
	case "json", "text":
	default:
		return errfmt.Errorf("--format takes json or text, not %q", opts.format)
	}
	if opts.once && cmd.Flags().Changed("count") {
		return errors.New("--once is the same as --count 1. Use one of them")
	}
	if opts.count < 0 || opts.timeout < 0 {
		return errors.New("--count and --timeout take 0 or more")
	}
	for i, s := range opts.states {
		s = strings.TrimSpace(s)
		if !slices.Contains(session.AgentStateNames, s) {
			return errfmt.Errorf("--state %q is not an agent state. The states are %s", s, strings.Join(session.AgentStateNames, ", "))
		}
		opts.states[i] = s
	}
	if len(opts.states) > 0 {
		if len(opts.types) == 0 {
			opts.types = []string{session.EventAgentState}
		} else if !slices.Contains(opts.types, session.EventAgentState) {
			return errors.New("--state keeps only agent-state events with these states. Add agent-state to --types, or remove --types")
		}
	}
	return nil
}
