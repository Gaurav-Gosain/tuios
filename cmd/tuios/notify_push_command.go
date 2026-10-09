package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// newNotifyPushCommand is tuios notify push: the phones registered for Web
// Push, through register-push, list-push and remove-push.
func newNotifyPushCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Register, list and remove the phones that get Web Push",
		Long: `Register, list and remove the phones that get Inbox items by Web Push.

A phone app usually registers itself with the register-push verb. These
commands do the same from a terminal outside tuios. They hold a presence on
their own connection to prove that you run them. A process inside a pane
cannot.`,
	}
	var jsonOutput bool
	var nonce string
	var r struct {
		endpoint, p256dh, auth, device string
		kinds                          []string
	}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register a phone's push subscription",
		Example: `  # A phone whose push service is at push.example.net
  tuios notify push register --device pixel --endpoint https://push.example.net/s/abc \
    --p256dh BNc... --auth tBH...`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			params := map[string]any{"endpoint": r.endpoint, "p256dh": r.p256dh, "auth": r.auth, "device": r.device}
			if len(r.kinds) > 0 {
				params["kinds"] = r.kinds
			}
			return runPushVerb(cmd.OutOrStdout(), "register-push", params, nonce, jsonOutput, func(w io.Writer, res map[string]any) {
				_, _ = fmt.Fprintf(w, "Registered %v for %s.\n", res["device"], joinAny(res["kinds"]))
			})
		},
	}
	register.Flags().StringVar(&r.endpoint, "endpoint", "", "The push service's address for the phone")
	register.Flags().StringVar(&r.p256dh, "p256dh", "", "The phone's P-256 public key, base64url")
	register.Flags().StringVar(&r.auth, "auth", "", "The phone's 16-byte authentication secret, base64url")
	register.Flags().StringVar(&r.device, "device", "", "A name for the phone")
	register.Flags().StringSliceVar(&r.kinds, "kind", nil, "An Inbox kind to push. Repeatable (default: approval, plan, ask, question)")
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List the registered phones and the VAPID public key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPushVerb(cmd.OutOrStdout(), "list-push", map[string]any{}, nonce, jsonOutput, func(w io.Writer, res map[string]any) {
				devices, _ := res["devices"].([]any)
				if len(devices) == 0 {
					_, _ = fmt.Fprintln(w, "No phone is registered.")
				}
				for _, d := range devices {
					m, _ := d.(map[string]any)
					line := fmt.Sprintf("%v: %v, %s", m["device"], m["service"], joinAny(m["kinds"]))
					if e, ok := m["last_error"].(string); ok && e != "" {
						line += ". Last push failed: " + e
					}
					_, _ = fmt.Fprintln(w, line)
				}
				_, _ = fmt.Fprintf(w, "VAPID public key: %v\n", res["vapid_public_key"])
			})
		},
	}
	rm := &cobra.Command{
		Use:   "rm <device>",
		Short: "Remove a registered phone",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPushVerb(cmd.OutOrStdout(), "remove-push", map[string]any{"device": args[0]}, nonce, jsonOutput, func(w io.Writer, res map[string]any) {
				_, _ = fmt.Fprintf(w, "Removed %v.\n", res["device"])
			})
		},
	}
	for _, c := range []*cobra.Command{register, ls, rm} {
		c.Flags().BoolVar(&jsonOutput, "json", false, "Output the daemon's reply as JSON")
		c.Flags().StringVar(&nonce, "human-nonce", "", "Use this nonce instead of a presence of this command's own")
		cmd.AddCommand(c)
	}
	return cmd
}

// runPushVerb calls a push verb as the person: with the nonce given, or with
// the nonce of a presence held on the same connection.
func runPushVerb(w io.Writer, verb string, params map[string]any, nonce string, jsonOutput bool, text func(io.Writer, map[string]any)) error {
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	if nonce == "" {
		raw, err := client.CallWithTimeout("attach-presence", nil, 5*time.Second)
		if err != nil {
			return reportVerbError(explainVerbError("attach-presence", err), jsonOutput)
		}
		var pres struct {
			Nonce string `json:"human_nonce"`
		}
		if err := json.Unmarshal(raw, &pres); err != nil {
			return reportVerbError(err, jsonOutput)
		}
		nonce = pres.Nonce
	}
	params["human_nonce"] = nonce
	raw, err := client.CallWithTimeout(verb, params, 10*time.Second)
	if err != nil {
		return reportVerbError(explainVerbError(verb, err), jsonOutput)
	}
	if jsonOutput {
		_, err := fmt.Fprintln(w, string(raw))
		return err
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	text(w, res)
	return nil
}

// joinAny joins a JSON list of strings with commas.
func joinAny(v any) string {
	list, _ := v.([]any)
	parts := make([]string, 0, len(list))
	for _, x := range list {
		parts = append(parts, fmt.Sprint(x))
	}
	return strings.Join(parts, ", ")
}
