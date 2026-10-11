package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// tuios call sends one verb and prints the daemon's reply as JSON. It is for
// a program that can start a process but cannot write to a unix socket, such
// as a Tern plugin, and for trying a verb from a shell. The daemon checks the
// call as it checks any other: a pane is held to its grants, and the verbs
// only the person may call stay refused from a pane.

// streamVerbs turn the connection into a stream after their reply, or hold
// it open for a peer, which a one-shot call would cut off. The value names
// the command that handles the verb, when there is one.
var streamVerbs = map[string]string{
	"subscribe":            "tuios subscribe",
	"stream-pane":          "",
	"open-file-stream":     "",
	"open-pane":            "",
	"pane-calls":           "",
	"link-peer":            "",
	"open-host-connection": "",
}

// Exit statuses of tuios call.
const (
	callExitVerb      = 1 // the daemon answered with an error
	callExitInput     = 2 // the command line or the params are wrong
	callExitTransport = 3 // the daemon could not be reached, or did not answer
)

// callDefaultTimeout bounds a call to a verb that names no wait of its own.
const callDefaultTimeout = 30 * time.Second

// callTimeoutMargin is added to the wait a verb's own timeout param asks for,
// so the reply that wait ends with is still read.
const callTimeoutMargin = 10 * time.Second

func newCallCommand() *cobra.Command {
	var pretty, jsonOut bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "call <verb> [params]",
		Short: "Send one verb to the daemon and print its reply as JSON",
		Long: `Send one verb to the daemon and print its reply as JSON.

params is a JSON object, or - to read it from standard input. Omit it for a
verb that takes none. The reply is the daemon's result object as it is.

A verb error prints {"error": {"code", "message", "hint"}} and exits 1. Bad
params or arguments exit 2. A daemon that cannot be reached, or does not
answer in time, exits 3. With --json, those two print as JSON too, with the
code invalid_input or transport.

The call waits 30 seconds, or for the verb's own timeout param (in
milliseconds) and 10 seconds more. --timeout sets the wait.

tuios list-verbs lists every verb and what it takes. A verb that turns the
connection into a stream, or holds it for a peer, is refused here.`,
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
			return runCall(os.Stdout, os.Stdin, args[0], params, callOptions{pretty: pretty, json: jsonOut, timeout: timeout})
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Indent the JSON")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print bad input and transport errors as JSON too")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "How long to wait for the reply (default: 30s, or the verb's own timeout and 10s)")
	return cmd
}

type callOptions struct {
	pretty  bool
	json    bool
	timeout time.Duration
}

// callFail ends tuios call with status and, under --json, prints err as JSON
// with code.
func callFail(out io.Writer, o callOptions, status int, code string, err error) error {
	if o.json {
		writeCallJSON(out, map[string]any{"error": map[string]any{"code": code, "message": err.Error()}}, o.pretty)
		return &statusError{code: status}
	}
	return &callError{err: err, status: status}
}

// callError is an error printed as text with a status of its own.
type callError struct {
	err    error
	status int
}

func (e *callError) Error() string   { return e.err.Error() }
func (e *callError) Unwrap() error   { return e.err }
func (e *callError) ExitStatus() int { return e.status }

func runCall(out io.Writer, in io.Reader, verb, rawParams string, o callOptions) error {
	if use, ok := streamVerbs[verb]; ok {
		how := "Write JSON to $TUIOS_SOCKET for it (tuios --skill events)"
		if use != "" {
			how = "Use " + use
		}
		return callFail(out, o, callExitInput, "invalid_input",
			fmt.Errorf("%s holds the connection open after its reply, so call cannot send it. %s", verb, how))
	}
	var params map[string]any
	if rawParams == "-" {
		b, err := io.ReadAll(io.LimitReader(in, 16<<20))
		if err != nil {
			return callFail(out, o, callExitInput, "invalid_input", fmt.Errorf("failed to read params from standard input: %w", err))
		}
		rawParams = string(b)
	}
	if strings.TrimSpace(rawParams) != "" {
		if err := json.Unmarshal([]byte(rawParams), &params); err != nil {
			return callFail(out, o, callExitInput, "invalid_input", fmt.Errorf("params must be a JSON object: %w", err))
		}
	}

	timeout := o.timeout
	if timeout <= 0 {
		timeout = callTimeoutFor(params)
	}

	client, err := dialVerb()
	if err != nil {
		return callFail(out, o, callExitTransport, "transport", err)
	}
	defer func() { _ = client.Close() }()
	var send any
	if params != nil {
		send = params
	}
	raw, err := client.CallWithTimeout(verb, send, timeout)
	if err != nil {
		if verr, ok := errors.AsType[*session.VerbCallError](err); ok {
			body := map[string]any{"code": verr.Code, "message": verr.Message}
			if verr.Hint != nil {
				body["hint"] = verr.Hint
			}
			writeCallJSON(out, map[string]any{"error": body}, o.pretty)
			return &statusError{code: callExitVerb}
		}
		return callFail(out, o, callExitTransport, "transport", err)
	}
	if o.pretty {
		var buf bytes.Buffer
		if json.Indent(&buf, raw, "", "  ") == nil {
			raw = buf.Bytes()
		}
	}
	_, err = fmt.Fprintln(out, string(raw))
	return err
}

// callTimeoutFor is the wait for a call with these params: the verb's own
// timeout (milliseconds, as wait-for, run and respond take it) and a margin,
// or the default.
func callTimeoutFor(params map[string]any) time.Duration {
	for _, key := range []string{"timeout", "timeout_ms"} {
		if v, ok := params[key].(float64); ok && v > 0 {
			return max(callDefaultTimeout, time.Duration(v)*time.Millisecond+callTimeoutMargin)
		}
	}
	return callDefaultTimeout
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
