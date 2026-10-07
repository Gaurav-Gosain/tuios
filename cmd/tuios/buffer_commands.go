package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/spf13/cobra"
)

// The paste buffer commands, after tmux's: list-buffers, show-buffer,
// set-buffer, delete-buffer and paste-buffer. The buffers live in this
// machine's daemon, which every client and session shares. A yank in copy
// mode adds one. See internal/session/verb_buffers.go.

// maxSetBufferInput bounds what set-buffer reads from standard input. The
// daemon's byte cap is the real limit; this only stops an endless pipe.
const maxSetBufferInput = 64 << 20

// bufferGrantsNote is the part of each command's help that says what a pane
// needs.
const bufferGrantsNote = `Run from inside a pane, reading the buffers needs the read grant, and
changing them needs the write grant. See 'tuios pane-grants'.`

// newBufferCommands builds the five paste buffer commands.
func newBufferCommands() []*cobra.Command {
	return []*cobra.Command{
		newListBuffersCommand(), newShowBufferCommand(), newSetBufferCommand(),
		newDeleteBufferCommand(), newPasteBufferCommand(),
	}
}

func newListBuffersCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "list-buffers",
		Short: "List the paste buffers, newest first",
		Long: `List the paste buffers, newest first: each buffer's name, its size and the
start of its text.

A yank in copy mode adds a buffer, and so does a copied mouse selection and
set-buffer. Every client and session on this machine shares the buffers. When
there are more than [paste_buffers] limit, or they hold more than max_kb, the
oldest go.

` + bufferGrantsNote,
		Example: `  # What can be pasted again
  tuios list-buffers

  # Every buffer name, for a script
  tuios list-buffers --json | jq -r '.buffers[].name'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runListBuffers(jsonOutput)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	return cmd
}

func newShowBufferCommand() *cobra.Command {
	var name string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show-buffer",
		Short: "Print the text of a paste buffer",
		Long: `Print the text of one paste buffer as it is, with no line feed added. With no
--buffer it prints the newest.

` + bufferGrantsNote,
		Example: `  # The last yank
  tuios show-buffer

  # One buffer, into a file
  tuios show-buffer -b buffer0003 > snippet.txt`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runShowBuffer(name, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&name, "buffer", "b", "", "The buffer (default: the newest)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("buffer", completeBufferNames)
	return cmd
}

func newSetBufferCommand() *cobra.Command {
	var name string
	var appendTo, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set-buffer [text]",
		Short: "Store text in a paste buffer",
		Long: `Store text in a paste buffer and put it on top. With no text, or with -, the
text comes from the standard input.

With no --buffer a new buffer is made, named bufferNNNN. With --buffer the
buffer of that name is set, and made when there is none. --append adds the text
to the end of the buffer, or of the newest buffer when there is no --buffer.

` + bufferGrantsNote,
		Example: `  # Keep a command to paste later
  tuios set-buffer -b deploy 'kubectl rollout restart deploy/api'

  # Store the output of a command
  git log -1 --format=%H | tuios set-buffer`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			text := "-"
			if len(args) == 1 {
				text = args[0]
			}
			if text == "-" {
				data, err := io.ReadAll(io.LimitReader(os.Stdin, maxSetBufferInput))
				if err != nil {
					return fmt.Errorf("read the standard input: %w", err)
				}
				text = string(data)
			}
			return runSetBuffer(name, text, appendTo, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&name, "buffer", "b", "", "The buffer to set (default: a new buffer)")
	cmd.Flags().BoolVarP(&appendTo, "append", "a", false, "Add the text to the end of the buffer")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("buffer", completeBufferNames)
	return cmd
}

func newDeleteBufferCommand() *cobra.Command {
	var name string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "delete-buffer",
		Short: "Delete a paste buffer",
		Long: `Delete one paste buffer. With no --buffer it deletes the newest.

` + bufferGrantsNote,
		Example: `  # Forget the last yank
  tuios delete-buffer

  # Forget one buffer
  tuios delete-buffer -b buffer0002`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDeleteBuffer(name, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&name, "buffer", "b", "", "The buffer (default: the newest)")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("buffer", completeBufferNames)
	return cmd
}

func newPasteBufferCommand() *cobra.Command {
	var name, sessionName, window string
	var del, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "paste-buffer",
		Short: "Paste a paste buffer into a pane",
		Long: `Paste a paste buffer into a pane, as the paste key does. With no --buffer it
pastes the newest. With no --window it pastes into the focused pane.

Control characters other than tab, line feed and carriage return are removed.
When the program in the pane turned bracketed paste on, the text goes in the
bracketed paste marks, so a shell does not run it line by line.

The buffers are this machine's. With a session on another machine, the text of
the buffer goes there as a paste.

Run from inside a pane, this needs the read and write grants: it reads a buffer
and types it.`,
		Example: `  # Paste the last yank into the focused pane
  tuios paste-buffer

  # Paste one buffer into another pane, then delete it
  tuios paste-buffer -b deploy -w ops -d`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runPasteBuffer(name, sessionName, window, del, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&name, "buffer", "b", "", "The buffer (default: the newest)")
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Target session (default: most recently active)")
	cmd.Flags().StringVarP(&window, "window", "w", "", "Target window by name or ID (default: focused)")
	cmd.Flags().BoolVarP(&del, "delete", "d", false, "Delete the buffer after the paste")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	_ = cmd.RegisterFlagCompletionFunc("buffer", completeBufferNames)
	return cmd
}

// bufferListing is the list-buffers result.
type bufferListing struct {
	Buffers []struct {
		Name   string `json:"name"`
		Bytes  int    `json:"bytes"`
		Sample string `json:"sample"`
	} `json:"buffers"`
}

// completeBufferNames completes --buffer from the daemon's buffers.
func completeBufferNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	client, err := dialVerb()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call("list-buffers", nil)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var res bufferListing
	if json.Unmarshal(raw, &res) != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(res.Buffers))
	for _, b := range res.Buffers {
		names = append(names, b.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// callBufferVerb makes one buffer verb call on this machine's daemon.
func callBufferVerb(verb string, params map[string]any) (json.RawMessage, error) {
	client, err := dialVerb()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call(verb, params)
	if err != nil {
		return nil, explainVerbError(verb, err)
	}
	return raw, nil
}

// bufferNameParams is the params of a call that names a buffer, or the newest.
func bufferNameParams(name string) map[string]any {
	if name == "" {
		return map[string]any{}
	}
	return map[string]any{"name": name}
}

func runListBuffers(jsonOutput bool) error {
	raw, err := callBufferVerb("list-buffers", nil)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res bufferListing
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Buffers) == 0 {
		fmt.Println("There are no paste buffers. A yank in copy mode adds one.")
		return nil
	}
	for _, b := range res.Buffers {
		fmt.Printf("%s: %s: %q\n", plainLine(b.Name), plural.Count(b.Bytes, "byte"), b.Sample)
	}
	return nil
}

func runShowBuffer(name string, jsonOutput bool) error {
	raw, err := callBufferVerb("show-buffer", bufferNameParams(name))
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	var res struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	_, err = io.WriteString(os.Stdout, res.Data)
	return err
}

func runSetBuffer(name, text string, appendTo, jsonOutput bool) error {
	params := bufferNameParams(name)
	params["data"] = text
	if appendTo {
		params["append"] = true
	}
	raw, err := callBufferVerb("set-buffer", params)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	return nil
}

func runDeleteBuffer(name string, jsonOutput bool) error {
	raw, err := callBufferVerb("delete-buffer", bufferNameParams(name))
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	return nil
}

func runPasteBuffer(name, sessionName, window string, del, jsonOutput bool) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	if t.host == "" {
		params := t.params(bufferNameParams(name))
		params["window"] = t.window
		if del {
			params["delete"] = true
		}
		raw, err := t.client.Call("paste-buffer", params)
		if err != nil {
			return reportVerbError(t.explain("paste-buffer", err), jsonOutput)
		}
		if jsonOutput {
			return printVerbResult(raw, true)
		}
		return nil
	}
	// The buffers are this machine's, and the pane is on another one: the
	// text goes there as a paste.
	raw, err := callBufferVerb("show-buffer", bufferNameParams(name))
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	var buf struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &buf); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	res, err := t.client.Call("send-text", t.params(map[string]any{"window": t.window, "text": buf.Data, "paste": true}))
	if err != nil {
		return reportVerbError(t.explain("send-text", err), jsonOutput)
	}
	if del {
		if _, err := callBufferVerb("delete-buffer", bufferNameParams(buf.Name)); err != nil {
			return reportVerbError(err, jsonOutput)
		}
	}
	if jsonOutput {
		return printVerbResultOn(t, res, true)
	}
	return nil
}
