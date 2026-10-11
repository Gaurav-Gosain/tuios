package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios call sends one verb and prints the daemon's reply as JSON. It is for
// a program that can start a process but cannot write to a unix socket, such
// as a Tern plugin, and for trying a verb from a shell. The daemon checks the
// call as it checks any other: a pane is held to its grants.

// streamVerbs turn the connection into a stream after their reply, which a
// one-shot call would cut off. Each has its own command.
var streamVerbs = map[string]string{
	"subscribe":        "tuios subscribe",
	"stream-pane":      "tuios pane-view",
	"open-file-stream": "tuios transfers",
}

func newCallCommand() *cobra.Command {
	var pretty bool
	cmd := &cobra.Command{
		Use:   "call <verb> [params]",
		Short: "Send one verb to the daemon and print its reply as JSON",
		Long: `Send one verb to the daemon and print its reply as JSON.

params is a JSON object, or - to read it from standard input. Omit it for a
verb that takes none. The reply is the daemon's result object as it is. A
verb error prints {"error": {"code", "message", "hint"}} and exits 1.

tuios list-verbs lists every verb and what it takes. A verb that turns the
connection into a stream (subscribe, stream-pane, open-file-stream) is
refused here: use the command named in the refusal.`,
		Example: `  # Every session, as the daemon reports it
  tuios call list-sessions

  # A verb with params
  tuios call list-windows '{"session":"work"}'

  # Params from a file
  tuios call review-diff - < params.json`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			params := ""
			if len(args) == 2 {
				params = args[1]
			}
			return runCall(os.Stdout, os.Stdin, args[0], params, pretty)
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Indent the JSON")
	return cmd
}

func runCall(out io.Writer, in io.Reader, verb, rawParams string, pretty bool) error {
	if use, ok := streamVerbs[verb]; ok {
		return fmt.Errorf("%s turns the connection into a stream, so call cannot send it. Use %s", verb, use)
	}
	var params any
	if rawParams == "-" {
		b, err := io.ReadAll(io.LimitReader(in, 16<<20))
		if err != nil {
			return fmt.Errorf("failed to read params from standard input: %w", err)
		}
		rawParams = string(b)
	}
	if strings.TrimSpace(rawParams) != "" {
		var obj map[string]any
		if err := json.Unmarshal([]byte(rawParams), &obj); err != nil {
			return fmt.Errorf("params must be a JSON object: %w", err)
		}
		params = obj
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call(verb, params)
	if err != nil {
		if verr, ok := errors.AsType[*session.VerbCallError](err); ok {
			body := map[string]any{"code": verr.Code, "message": verr.Message}
			if verr.Hint != nil {
				body["hint"] = verr.Hint
			}
			writeCallJSON(out, map[string]any{"error": body}, pretty)
			return &statusError{code: 1}
		}
		return err
	}
	if pretty {
		var buf bytes.Buffer
		if json.Indent(&buf, raw, "", "  ") == nil {
			raw = buf.Bytes()
		}
	}
	_, err = fmt.Fprintln(out, string(raw))
	return err
}

func writeCallJSON(out io.Writer, v any, pretty bool) {
	var b []byte
	if pretty {
		b, _ = json.MarshalIndent(v, "", "  ")
	} else {
		b, _ = json.Marshal(v)
	}
	_, _ = fmt.Fprintln(out, string(b))
}
