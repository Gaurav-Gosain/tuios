package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// askPendingStatus is the exit status of an ask the person has not answered
// yet, so a script can tell "no answer yet" (2) from "no" and from a failure.
const askPendingStatus = 2

// askHumanResult is the ask-human result the CLI reads.
type askHumanResult struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Answer    string `json:"answer"`
}

// askHumanOptions is what tuios ask-human was given.
type askHumanOptions struct {
	session, window, requestID string
	question                   string
	options                    []string
	timeout                    int
	noWait, jsonOut            bool
}

func runAskHuman(o askHumanOptions) error {
	params := map[string]any{"session": o.session}
	if o.window != "" {
		params["window"] = o.window
	}
	if o.requestID != "" {
		params["request_id"] = o.requestID
	} else {
		params["question"] = o.question
		params["options"] = o.options
	}
	if o.timeout > 0 {
		params["timeout"] = o.timeout
	}
	if o.noWait {
		params["wait"] = false
	}
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, o.jsonOut)
	}
	defer func() { _ = client.Close() }()
	wait := 120 * time.Second
	if o.timeout > 0 {
		wait = time.Duration(o.timeout) * time.Millisecond
	}
	raw, err := client.CallWithTimeout("ask-human", params, wait+15*time.Second)
	if err != nil {
		if o.jsonOut {
			return reportVerbError(err, true)
		}
		return explainVerbError("ask-human", err)
	}
	if o.jsonOut {
		return printVerbResult(raw, true)
	}
	return printAskHuman(os.Stdout, os.Stderr, raw)
}

// printAskHuman prints the answer alone on out, so it can be captured, and
// anything else on errs with a status that says what happened.
func printAskHuman(out, errs io.Writer, raw json.RawMessage) error {
	var res askHumanResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	switch res.Status {
	case "answered":
		fmt.Fprintln(out, res.Answer)
		return nil
	case "pending":
		fmt.Fprintf(errs, "No answer yet; the question stays in the Inbox. Come back with: tuios ask-human --request-id %s\n", res.RequestID)
		return &statusError{code: askPendingStatus}
	default:
		fmt.Fprintf(errs, "The question ended without an answer: %s\n", strings.ReplaceAll(res.Status, "_", " "))
		return &statusError{code: 1}
	}
}

// newAskHumanCommand is `tuios ask-human`.
func newAskHumanCommand() *cobra.Command {
	var o askHumanOptions
	cmd := &cobra.Command{
		Use:   "ask-human <question> -o <answer> [-o <answer>...]",
		Short: "Ask the person a question and print their answer",
		Long: `Ask the person a question with a fixed set of answers, wait for them to pick
one, and print it.

The question goes in the Inbox. A client that shows the asking pane opens the
Inbox on it at once, and the keys 1 to 9 pick an answer; anywhere else it waits
there with the usual alert, and with nobody attached it waits for the next
attach. Only the person at an attached client can answer: an agent cannot.

When --timeout runs out first, this exits 2 and the question stays. The answer
is then mailed to the asking pane from human, marked verified_human, so
'tuios wait-for agent-message' picks it up; or come back with --request-id.

From inside a pane the question is asked as that pane. It exits 0 with the
answer on stdout, 2 when there is no answer yet, and 1 when the question ended
without one (dismissed, superseded, or its pane closed).`,
		Example: `  # Ask, and keep the answer
  answer=$(tuios ask-human 'Deploy to staging?' -o yes -o no) && echo "$answer"

  # Ask and move on; the answer arrives as mail
  tuios ask-human 'Which region?' -o us -o eu --no-wait

  # Come back for an answer
  tuios ask-human --request-id 9f86d081884c7d65`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.question = args[0]
			}
			if o.requestID == "" && (o.question == "" || len(o.options) == 0) {
				return fmt.Errorf("ask-human needs a question and at least one -o answer, e.g. tuios ask-human 'Deploy?' -o yes -o no")
			}
			return runAskHuman(o)
		},
	}
	cmd.Flags().StringVarP(&o.session, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&o.window, "window", "w", "", "The pane asking, where a late answer is mailed (default: your own pane)")
	cmd.Flags().StringArrayVarP(&o.options, "option", "o", nil, "An answer the person can pick; give 1 to 9")
	cmd.Flags().IntVar(&o.timeout, "timeout", 0, "Milliseconds to wait for the answer (default: 120000, at most one hour)")
	cmd.Flags().BoolVar(&o.noWait, "no-wait", false, "Ask and return at once; the answer arrives as mail")
	cmd.Flags().StringVar(&o.requestID, "request-id", "", "Come back for a question already asked")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}
