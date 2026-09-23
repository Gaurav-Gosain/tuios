package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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
}

// newSubscribeCommand builds tuios subscribe, the command-line end of the
// subscribe verb. It prints the stream as NDJSON so a script can pipe it to jq
// or keep the last seq and boot id to resume from after a reconnect.
func newSubscribeCommand() *cobra.Command {
	var opts subscribeOptions
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "Print the daemon's event stream as JSON lines",
		Long: `Open the daemon's event stream and print it, one JSON object per line.

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

  # Every agent on every machine, and every host coming or going
  tuios subscribe --hosts --types agent-state,host-changed`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.resume = cmd.Flags().Changed("after-seq")
			if opts.bootID != "" && !opts.resume {
				return errors.New("--boot-id only means something with --after-seq: pass the seq the last run printed with it")
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
	cmd.Flags().IntVar(&opts.count, "count", 0, "Exit after printing this many events, gap lines included (default: run until the stream ends)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("types", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return session.EventTypeNames, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}

// runSubscribe subscribes and copies the stream to out until it ends or count
// events have been printed.
func runSubscribe(opts subscribeOptions, out io.Writer) error {
	t, err := dialTarget(opts.session, opts.window)
	if err != nil {
		return err
	}
	defer t.Close()

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
	if _, err := fmt.Fprintln(out, string(t.result(raw))); err != nil {
		return err
	}

	for printed := 0; opts.count <= 0 || printed < opts.count; printed++ {
		line, err := t.client.ReadEventLine(0)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("the daemon closed the event stream. Resubscribe with --after-seq and --boot-id to pick up where it stopped")
			}
			return fmt.Errorf("failed to read the event stream: %w", err)
		}
		if t.host != "" {
			line = t.result(json.RawMessage(line))
		}
		if _, err := fmt.Fprintln(out, string(line)); err != nil {
			return err
		}
	}
	return nil
}
