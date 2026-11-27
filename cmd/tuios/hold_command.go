package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// holdRow is one hold as the hold verbs return it (internal/session/holds.go).
type holdRow struct {
	Name      string `json:"name"`
	Reason    string `json:"reason"`
	Holder    string `json:"holder"`
	Label     string `json:"label"`
	TakenAt   int64  `json:"taken_at"`
	ExpiresAt int64  `json:"expires_at"`
}

// newHoldCommand builds `tuios hold`: named holds the daemon keeps for every
// session, such as a quiet window for timing runs.
func newHoldCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hold",
		Short: "Take, check and release named holds on this machine",
		Long: `A hold is a named lock that the daemon keeps for all of its sessions. Use one
for shared machine state: a quiet window for timing runs, a shared tool that
must not restart, or a pull request that a landing train owns.

A build script runs 'tuios hold check NAME' first and stops when it fails. A
pane that must wait runs 'tuios wait-for hold-released NAME'.

A hold ends when its holder releases it, when its --ttl ends, or when the
holder pane closes. A hold taken outside a pane needs --ttl. A live upgrade
keeps the holds. A daemon restart drops them.

From inside a pane, taking and releasing a hold needs the grant that
send-agent-message needs. Releasing another holder's hold needs the admin
grant and --force.`,
		Example: `  # Hold the machine quiet for timing runs, for at most four hours
  tuios hold take quiet-window --reason 'timing gates run' --ttl 4h

  # In a build script: stop while the machine is held
  tuios hold check quiet-window || exit 1

  # Wait for it, then build
  tuios wait-for hold-released quiet-window --timeout 14400000

  # What is held, and the end of the window
  tuios hold ls
  tuios hold release quiet-window`,
	}
	cmd.AddCommand(newHoldTakeCommand(), newHoldReleaseCommand(), newHoldCheckCommand(), newHoldListCommand())
	return cmd
}

func newHoldTakeCommand() *cobra.Command {
	var reason string
	var ttl time.Duration
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "take NAME",
		Short: "Take a named hold",
		Long: `Take a named hold. A take of a hold you already have renews it with the new
reason and ttl. A hold another holder has fails with the code held, and the
message names the holder.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(reason) == "" {
				return errfmt.Errorf("--reason is required: it tells the panes that wait what the hold is for")
			}
			if ttl < 0 {
				return errfmt.Errorf("--ttl must be positive")
			}
			params := map[string]any{"name": args[0], "reason": reason}
			if ttl > 0 {
				params["ttl"] = max(ttl.Milliseconds(), 1)
			}
			var res struct {
				Renewed bool    `json:"renewed"`
				Hold    holdRow `json:"hold"`
			}
			raw, err := localVerb("take-hold", params, 0, jsonOut)
			if err != nil {
				return err
			}
			if jsonOut {
				return printVerbResult(raw, true)
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			verb := "Took"
			if res.Renewed {
				verb = "Renewed"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s hold %s. %s\n", verb, plainLine(res.Hold.Name), holdEnds(res.Hold, time.Now()))
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "What the hold is for, at most 200 characters (required)")
	cmd.Flags().DurationVar(&ttl, "ttl", 0, "End the hold on its own after this long, such as 4h. Required outside a pane. At most 168h")
	addJSONFlag(cmd, &jsonOut)
	return cmd
}

func newHoldReleaseCommand() *cobra.Command {
	var force, jsonOut bool
	cmd := &cobra.Command{
		Use:   "release NAME",
		Short: "Release a named hold",
		Long: `Release a named hold. A name no one holds is not an error. Releasing another
holder's hold needs the admin grant and --force.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"name": args[0]}
			if force {
				params["force"] = true
			}
			raw, err := localVerb("release-hold", params, 0, jsonOut)
			if err != nil {
				return err
			}
			if jsonOut {
				return printVerbResult(raw, true)
			}
			var res struct {
				Released bool `json:"released"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			if !res.Released {
				fmt.Fprintf(cmd.OutOrStdout(), "No one holds %s.\n", plainLine(args[0]))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Released hold %s.\n", plainLine(args[0]))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Release the hold when another holder has it. Needs the admin grant")
	addJSONFlag(cmd, &jsonOut)
	return cmd
}

func newHoldCheckCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check NAME",
		Short: "Exit 0 when a hold is free, and fail when it is held",
		Long: `Exit 0 when no one holds NAME. When it is held, fail with the code held and
say who holds it, why, and for how long. Run it first in a build script.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := localVerb("check-hold", map[string]any{"name": args[0]}, 0, jsonOut)
			if err != nil {
				return err
			}
			if jsonOut {
				return printVerbResult(raw, true)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "No one holds %s.\n", plainLine(args[0]))
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut)
	return cmd
}

func newHoldListCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the holds on this machine",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := localVerb("list-holds", map[string]any{}, 0, jsonOut)
			if err != nil {
				return err
			}
			if jsonOut {
				return printVerbResult(raw, true)
			}
			var res struct {
				Holds []holdRow `json:"holds"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			if len(res.Holds) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No holds.")
				return nil
			}
			writeHoldTable(cmd.OutOrStdout(), res.Holds, time.Now())
			return nil
		},
	}
	addJSONFlag(cmd, &jsonOut)
	return cmd
}

// runWaitForHold is tuios wait-for hold-released NAME.
func runWaitForHold(name string, timeout int, jsonOut bool) error {
	params := map[string]any{"condition": "hold-released", "name": name, "timeout": timeout}
	grace := time.Duration(timeout)*time.Millisecond + 10*time.Second
	raw, err := localVerb("wait-for", params, grace, jsonOut)
	if err != nil {
		return err
	}
	if jsonOut {
		return printVerbResult(raw, true)
	}
	var res struct {
		WasHeld bool `json:"was_held"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.WasHeld {
		fmt.Printf("Hold %s was released.\n", plainLine(name))
	} else {
		fmt.Printf("No one holds %s.\n", plainLine(name))
	}
	return nil
}

// writeHoldTable prints one line per hold.
func writeHoldTable(w io.Writer, holds []holdRow, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tHOLDER\tAGE\tENDS\tREASON")
	for _, h := range holds {
		ends := "when released"
		if h.ExpiresAt > 0 {
			ends = "in " + holdSpan(time.Unix(0, h.ExpiresAt).Sub(now))
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", plainLine(h.Name), plainLine(h.Label),
			holdSpan(now.Sub(time.Unix(0, h.TakenAt))), ends, plainLine(h.Reason))
	}
	_ = tw.Flush()
}

// printHoldsUnder prints the holds of a list-agents answer under its table.
func printHoldsUnder(w io.Writer, raw []byte) {
	var res struct {
		Holds []holdRow `json:"holds"`
	}
	if jsonx.Unmarshal(raw, &res) != nil || len(res.Holds) == 0 {
		return
	}
	fmt.Fprintln(w, "\nHolds on this machine:")
	writeHoldTable(w, res.Holds, time.Now())
}

// holdEnds says how a hold ends.
func holdEnds(h holdRow, now time.Time) string {
	switch {
	case h.ExpiresAt > 0 && h.Holder != "":
		return "It ends in " + holdSpan(time.Unix(0, h.ExpiresAt).Sub(now)) + ", or when this pane closes."
	case h.ExpiresAt > 0:
		return "It ends in " + holdSpan(time.Unix(0, h.ExpiresAt).Sub(now)) + "."
	default:
		return "It ends when you release it or this pane closes."
	}
}

// holdSpan is a duration for a person: whole seconds.
func holdSpan(d time.Duration) string {
	return max(d, 0).Round(time.Second).String()
}
