package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/pushnotify"
	"github.com/spf13/cobra"
)

// newNotifyCommand is tuios notify: the [notify] push notifications.
func newNotifyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Push notifications for the Inbox",
		Long: `Push notifications for the Inbox.

The daemon sends a notification to your phone when the Inbox gets an item
that waits for you. Set the providers in the [notify] table of config.toml.`,
	}
	var jsonOutput bool
	test := &cobra.Command{
		Use:   "test",
		Short: "Send a test notification through each provider",
		Long: `Send a test notification through each provider in [notify] and report the result.

This command sends from this process, not from the daemon. A token_env
variable is read from this shell. The daemon reads it from its own
environment. The output never shows a token or a full address.`,
		Example: `  # Try ntfy, Pushover and the webhook
  tuios notify test`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNotifyTest(cmd.Context(), cmd.OutOrStdout(), jsonOutput)
		},
	}
	test.Flags().BoolVar(&jsonOutput, "json", false, "Output the result per provider as JSON")
	cmd.AddCommand(test)
	return cmd
}

// notifyTestResult is one provider's result.
type notifyTestResult struct {
	Provider string `json:"provider"`
	Host     string `json:"host,omitempty"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// errNotifyTestFailed makes the command exit non-zero after it printed why.
var errNotifyTestFailed = errors.New("a provider failed. The result for each provider is above")

func runNotifyTest(ctx context.Context, w io.Writer, jsonOutput bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		return fmt.Errorf("cannot read config.toml: %w", err)
	}
	n := &cfg.Notify
	providers := pushnotify.Providers(n)
	if len(providers) == 0 {
		return errors.New("no provider is set. Add [notify.ntfy], [notify.pushover] or [notify.webhook] to config.toml")
	}
	if !n.On() && !jsonOutput {
		_, _ = fmt.Fprintln(w, "Notifications are off (notify.enabled = false). The test sends all the same.")
	}
	client := pushnotify.NewClient(n.AllowHTTPRedirects)
	msg := pushnotify.TestMessage(n.WebURL)
	results := make([]notifyTestResult, 0, len(providers))
	failed := false
	for _, p := range providers {
		r := notifyTestResult{Provider: p.Name, Host: p.Host, OK: true}
		if err := p.Send(ctx, client, msg); err != nil {
			r.OK, r.Error = false, err.Error()
			failed = true
		}
		results = append(results, r)
	}
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	} else {
		for i, r := range results {
			if r.OK {
				_, _ = fmt.Fprintf(w, "%s: sent.\n", providers[i].Label())
				continue
			}
			_, _ = fmt.Fprintf(w, "%s: failed. %s.\n", providers[i].Label(), upperFirst(trimStop(r.Error)))
		}
		if msg.Link == "" && n.WebURL != "" {
			_, _ = fmt.Fprintln(w, "notify.web_url is not an http or https address, so the notification has no link.")
		}
	}
	if failed {
		return errNotifyTestFailed
	}
	return nil
}

// upperFirst starts a sentence with a capital letter.
func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// trimStop drops a final full stop, so a line does not end in two.
func trimStop(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
