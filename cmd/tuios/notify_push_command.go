package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
		subscription, device string
		kinds                []string
	}
	register := &cobra.Command{
		Use:   "register",
		Short: "Register a phone's push subscription",
		Long: `Register a phone's push subscription.

The subscription holds secrets: the endpoint, the p256dh key and the auth
secret. Anyone who has them can send to the phone. So the command reads them
from a file or from stdin, never from the command line, where other programs
can see them. Give the JSON a browser gives for PushSubscription.toJSON(), or
an object with endpoint, p256dh and auth.`,
		Example: `  # Read the subscription from a file
  tuios notify push register --device pixel --subscription pixel.json

  # Read it from stdin
  tuios notify push register --device pixel --subscription - < pixel.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sub, err := readPushSubscription(cmd.InOrStdin(), r.subscription)
			if err != nil {
				return reportVerbError(err, jsonOutput)
			}
			params := map[string]any{"endpoint": sub.Endpoint, "p256dh": sub.P256dh, "auth": sub.Auth, "device": r.device}
			if len(r.kinds) > 0 {
				params["kinds"] = r.kinds
			}
			return runPushVerb(cmd.OutOrStdout(), "register-push", params, nonce, jsonOutput, func(w io.Writer, res map[string]any) {
				_, _ = fmt.Fprintf(w, "Registered %v for %s.\n", res["device"], joinAny(res["kinds"]))
			})
		},
	}
	register.Flags().StringVar(&r.subscription, "subscription", "", "The file that holds the phone's subscription as JSON, or - for stdin")
	register.Flags().StringVar(&r.device, "device", "", "A name for the phone")
	register.Flags().StringSliceVar(&r.kinds, "kind", nil, "An Inbox kind to push. Repeatable (default: approval, plan, ask, question)")
	_ = register.MarkFlagRequired("subscription")
	_ = register.MarkFlagRequired("device")
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

// pushSubscriptionMax bounds the subscription file. An endpoint is at most
// 2048 bytes, and the keys are short.
const pushSubscriptionMax = 16 << 10

// pushSubscription is the subscription register reads.
type pushSubscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// readPushSubscription reads a subscription from path, or from stdin when
// path is "-". It takes the shape of PushSubscription.toJSON(), with the keys
// under "keys", and the flat shape of register-push.
func readPushSubscription(stdin io.Reader, path string) (pushSubscription, error) {
	src := stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return pushSubscription{}, fmt.Errorf("cannot read the subscription: %w", err)
		}
		defer func() { _ = f.Close() }()
		src = f
	}
	data, err := io.ReadAll(io.LimitReader(src, pushSubscriptionMax+1))
	if err != nil {
		return pushSubscription{}, fmt.Errorf("cannot read the subscription: %w", err)
	}
	if len(data) > pushSubscriptionMax {
		return pushSubscription{}, errors.New("the subscription is longer than 16 KiB")
	}
	var sub pushSubscription
	if err := json.Unmarshal(data, &sub); err != nil {
		return pushSubscription{}, fmt.Errorf("the subscription is not JSON: %w", err)
	}
	if sub.P256dh == "" {
		sub.P256dh = sub.Keys.P256dh
	}
	if sub.Auth == "" {
		sub.Auth = sub.Keys.Auth
	}
	if sub.Endpoint == "" || sub.P256dh == "" || sub.Auth == "" {
		return pushSubscription{}, errors.New("the subscription needs endpoint, p256dh and auth (or keys.p256dh and keys.auth)")
	}
	return sub, nil
}
