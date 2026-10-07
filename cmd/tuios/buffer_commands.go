package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strconv"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/pastebuf"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	var name, sessionName string
	var appendTo, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "set-buffer [text]",
		Short: "Store text in a paste buffer",
		Long: `Store text in a paste buffer and put it on top. With no text, or with -, the
text comes from the standard input.

With no --buffer a new buffer is made, named bufferNNNN. With --buffer the
buffer of that name is set, and made when there is none. --append adds the text
to the end of the named buffer. With no --buffer, --append makes a new buffer,
as in tmux. A buffer can hold any bytes, a binary file included.

A buffer you set from outside every pane is yours only. With --session, the
panes of that session that hold the read grant may read it too.

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
				if len(args) == 0 && term.IsTerminal(int(os.Stdin.Fd())) {
					return errors.New("set-buffer: give the text as an argument, or pipe it in: echo hi | tuios set-buffer")
				}
				// One byte past the limit says the input was longer, so it
				// is refused and never stored cut short.
				data, err := io.ReadAll(io.LimitReader(os.Stdin, maxSetBufferInput+1))
				if err != nil {
					return fmt.Errorf("read the standard input: %w", err)
				}
				if len(data) > maxSetBufferInput {
					return fmt.Errorf("set-buffer: the standard input is longer than %d MiB. Nothing was stored", maxSetBufferInput>>20)
				}
				text = string(data)
			}
			return runSetBuffer(name, sessionName, text, appendTo, jsonOutput)
		},
	}
	cmd.Flags().StringVarP(&name, "buffer", "b", "", "The buffer to set (default: a new buffer)")
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "The session the text belongs to, so its panes may read it (default: none, only you)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
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
	var del, raw, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "paste-buffer",
		Short: "Paste a paste buffer into a pane",
		Long: `Paste a paste buffer into a pane, as the paste key does. With no --buffer it
pastes the newest buffer that tuios named. With no --window it pastes into the
focused pane.

Each line feed becomes a carriage return, as in tmux. --raw keeps the line
feeds. Control characters other than tab, line feed and carriage return are
removed.
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
			return runPasteBuffer(name, sessionName, window, del, raw, jsonOutput)
		},
	}
	cmd.Flags().BoolVarP(&raw, "raw", "r", false, "Keep each line feed instead of turning it into a carriage return")
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
		Pane   string `json:"pane"`
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
		// The daemon escaped the sample already, so it is printed as it is.
		line := fmt.Sprintf("%s: %s: \"%s\"", plainLine(b.Name), plural.Count(b.Bytes, "byte"), plainLine(b.Sample))
		if b.Pane != "" {
			line += " (set by pane " + plainLine(b.Pane) + ")"
		}
		fmt.Println(line)
	}
	return nil
}

// bufferContent is the content of a show-buffer result: every byte from
// data_b64, or the text of a daemon that sends no data_b64.
func bufferContent(raw json.RawMessage) (name, data string, version uint64, err error) {
	var res struct {
		Name    string `json:"name"`
		Data    string `json:"data"`
		DataB64 string `json:"data_b64"`
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "", 0, fmt.Errorf("failed to parse response: %w", err)
	}
	data = res.Data
	if res.DataB64 != "" {
		b, err := base64.StdEncoding.DecodeString(res.DataB64)
		if err != nil {
			return "", "", 0, fmt.Errorf("failed to parse response: %w", err)
		}
		data = string(b)
	}
	return res.Name, data, res.Version, nil
}

func runShowBuffer(name string, jsonOutput bool) error {
	params := bufferNameParams(name)
	if !jsonOutput {
		params["encoding"] = "base64"
	}
	raw, err := callBufferVerb("show-buffer", params)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, true)
	}
	_, data, _, err := bufferContent(raw)
	if err != nil {
		return err
	}
	_, err = io.WriteString(os.Stdout, data)
	return err
}

// setBufferPart is how many bytes one set-buffer call carries. A larger
// content goes as an upload, which the daemon sets once, from all parts.
const setBufferPart = 768 << 10

func runSetBuffer(name, sessionName, text string, appendTo, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer func() { _ = client.Close() }()
	base := bufferNameParams(name)
	if sessionName != "" {
		base["session"] = sessionName
	}
	if appendTo {
		base["append"] = true
	}
	upload := ""
	if len(text) > setBufferPart {
		upload = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	var raw json.RawMessage
	for rest := text; ; {
		part := rest[:min(len(rest), setBufferPart)]
		rest = rest[len(part):]
		params := map[string]any{"data_b64": base64.StdEncoding.EncodeToString([]byte(part))}
		if upload != "" {
			params["upload"] = upload
		}
		if rest != "" {
			params["more"] = true
		} else {
			maps.Copy(params, base)
		}
		raw, err = client.Call("set-buffer", params)
		if err != nil {
			return reportVerbError(explainVerbError("set-buffer", err), jsonOutput)
		}
		if rest == "" {
			break
		}
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

func runPasteBuffer(name, sessionName, window string, del, rawText, jsonOutput bool) error {
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
		if rawText {
			params["raw"] = true
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
	params := bufferNameParams(name)
	params["encoding"] = "base64"
	raw, err := callBufferVerb("show-buffer", params)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	bufName, data, version, err := bufferContent(raw)
	if err != nil {
		return err
	}
	res, err := t.client.Call("send-text", t.params(map[string]any{"window": t.window, "text": pastebuf.PasteText(data, rawText), "paste": true}))
	if err != nil {
		return reportVerbError(t.explain("send-text", err), jsonOutput)
	}
	if del {
		// Only the text that was pasted: a buffer set again meanwhile stays.
		params := bufferNameParams(bufName)
		params["version"] = version
		if _, err := callBufferVerb("delete-buffer", params); err != nil {
			return reportVerbError(err, jsonOutput)
		}
	}
	if jsonOutput {
		return printVerbResultOn(t, res, true)
	}
	return nil
}
