package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

const (
	promptExitCancelled = 1
	promptExitTimeout   = 2
	promptExitNoClient  = 3
	promptExitBusy      = 4
)

type promptOptions struct {
	kind, title          string
	placeholder, def     string
	items                []string
	itemsJSON            string
	multiple, allowOther bool
	timeout              int
	jsonOut              bool
}

type promptCLIResult struct {
	Status string   `json:"status"`
	Value  string   `json:"value"`
	IDs    []string `json:"ids"`
	Other  string   `json:"other"`
}

func promptItemsOf(o promptOptions, stdin io.Reader) ([]session.PromptItem, error) {
	var items []session.PromptItem
	if o.itemsJSON != "" {
		var raw []byte
		var err error
		if o.itemsJSON == "-" {
			raw, err = io.ReadAll(stdin)
		} else {
			raw, err = os.ReadFile(o.itemsJSON)
		}
		if err != nil {
			return nil, errfmt.Errorf("cannot read the items: %w", err)
		}
		if err := jsonx.Unmarshal(raw, &items); err != nil {
			return nil, errfmt.Errorf("the items are not a JSON list of {id, title, subtitle, icon}: %w", err)
		}
	}
	for _, s := range o.items {
		id, title, ok := strings.Cut(s, "=")
		if !ok {
			title = id
		}
		items = append(items, session.PromptItem{ID: id, Title: title})
	}
	return items, nil
}

func runPrompt(o promptOptions) error {
	params := map[string]any{"kind": o.kind, "title": o.title}
	if o.placeholder != "" {
		params["placeholder"] = o.placeholder
	}
	if o.kind == session.PromptInput && o.def != "" {
		params["default"] = o.def
	}
	if o.kind == session.PromptPick {
		items, err := promptItemsOf(o, os.Stdin)
		if err != nil {
			return err
		}
		params["items"] = items
		if o.multiple {
			params["multiple"] = true
		}
		if o.allowOther {
			params["allow_other"] = true
		}
	}
	if o.timeout > 0 {
		params["timeout"] = o.timeout
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
	raw, err := client.CallWithTimeout("prompt", params, wait+15*time.Second)
	if err != nil {
		if o.jsonOut {
			return reportVerbError(err, true)
		}
		return explainVerbError("prompt", err)
	}
	return printPrompt(os.Stdout, os.Stderr, raw, o.jsonOut)
}

func printPrompt(out, errs io.Writer, raw json.RawMessage, jsonOut bool) error {
	var res promptCLIResult
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if jsonOut {
		var fields map[string]any
		if err := jsonx.Unmarshal(raw, &fields); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		delete(fields, "type")
		if b, err := json.MarshalIndent(fields, "", "  "); err == nil {
			_, _ = fmt.Fprintln(out, jsonSafe(string(b)))
		}
	}
	if res.Status == session.PromptAnswered {
		if jsonOut {
			return nil
		}
		for _, id := range res.IDs {
			fmt.Fprintln(out, id)
		}
		switch {
		case res.Other != "":
			fmt.Fprintln(out, res.Other)
		case res.IDs == nil:
			fmt.Fprintln(out, res.Value)
		}
		return nil
	}
	if !jsonOut {
		fmt.Fprintln(errs, promptNoAnswerText(res.Status))
	}
	return &statusError{code: promptExitCode(res.Status)}
}

func promptExitCode(status string) int {
	switch status {
	case session.PromptTimeout:
		return promptExitTimeout
	case session.PromptNoClient:
		return promptExitNoClient
	case session.PromptBusy:
		return promptExitBusy
	}
	return promptExitCancelled
}

func promptNoAnswerText(status string) string {
	switch status {
	case session.PromptCancelled:
		return "The person cancelled the prompt."
	case session.PromptTimeout:
		return "Nobody answered the prompt in time. It is closed."
	case session.PromptNoClient:
		return "No client is attached on this machine to show the prompt. Attach a client, then ask again."
	case session.PromptBusy:
		return "Another prompt is open. Wait for it to close, then ask again."
	}
	return "The prompt ended without an answer: " + strings.ReplaceAll(status, "_", " ")
}

func newPromptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompt",
		Short: "Ask the person for text or a choice in the palette",
		Long: `Show a text field or a list in the command palette of every client attached
on this machine, and print the answer.

The first answer from any client wins, and the prompt closes on the others.
Only the person at a client can answer. An agent, a plugin or a script cannot.
Prompts work with the agent features off.

The exit status says why there is no answer: 1 cancelled (Esc), 2 timeout,
3 no client attached to show it, 4 another prompt is open.`,
	}
	cmd.AddCommand(newPromptInputCommand(), newPromptPickCommand())
	return cmd
}

func addPromptFlags(cmd *cobra.Command, o *promptOptions) {
	cmd.Flags().StringVar(&o.placeholder, "placeholder", "", "Text that shows in the empty field")
	cmd.Flags().IntVar(&o.timeout, "timeout", 0, "Milliseconds to wait for the answer (default: 120000, at most one hour)")
	addJSONFlag(cmd, &o.jsonOut)
}

func newPromptInputCommand() *cobra.Command {
	o := promptOptions{kind: session.PromptInput}
	cmd := &cobra.Command{
		Use:   "input <title>",
		Short: "Ask the person to type a line, and print it",
		Example: `  # Ask for a branch name
  branch=$(tuios prompt input 'Branch name?' --placeholder feature/x) || exit

  # Start with a value the person can change
  tuios prompt input 'Commit message?' --default 'fix: '`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			o.title = args[0]
			return runPrompt(o)
		},
	}
	cmd.Flags().StringVar(&o.def, "default", "", "The text the field starts with")
	addPromptFlags(cmd, &o)
	return cmd
}

func newPromptPickCommand() *cobra.Command {
	o := promptOptions{kind: session.PromptPick}
	cmd := &cobra.Command{
		Use:   "pick <title> (--item <id=title>... | --items <file|->)",
		Short: "Ask the person to choose from a list, and print the ids",
		Long: `Ask the person to choose from a list in the palette, and print the id of each
row chosen, one per line. Typing filters the list.

Give the rows with --item, as id=title or as a title that is its own id, or
with --items, a JSON list of {id, title, subtitle, icon} read from a file or
from standard input (-). With --multiple, Tab marks rows. With --allow-other,
the person may send typed text that is not a row, and it is printed as it is.`,
		Example: `  # Choose one
  env=$(tuios prompt pick 'Deploy to?' --item stg=Staging --item prod=Production) || exit

  # Rows from a program, more than one allowed
  jq -n '[{id:"a",title:"Alpha",subtitle:"first"},{id:"b",title:"Beta"}]' |
    tuios prompt pick 'Which ones?' --items - --multiple`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			o.title = args[0]
			if len(o.items) == 0 && o.itemsJSON == "" {
				return errfmt.Errorf("prompt pick needs rows: give --item id=title, or --items with a JSON list")
			}
			return runPrompt(o)
		},
	}
	cmd.Flags().StringArrayVarP(&o.items, "item", "i", nil, "A row: id=title, or a title that is its own id")
	cmd.Flags().StringVar(&o.itemsJSON, "items", "", "A file with a JSON list of {id, title, subtitle, icon} rows, or - for standard input")
	cmd.Flags().BoolVar(&o.multiple, "multiple", false, "Let the person mark more than one row with Tab")
	cmd.Flags().BoolVar(&o.allowOther, "allow-other", false, "Let the person send typed text that is not a row")
	addPromptFlags(cmd, &o)
	return cmd
}
