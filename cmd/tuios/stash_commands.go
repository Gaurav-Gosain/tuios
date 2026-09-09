package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

// This file holds the CLI half of the session stash: put a file in, list what is
// in, and get a file out when it has to cross a link. The point of the command
// is the path it prints, so the plain output puts that path on a line of its
// own and nothing else on that line, which is what a caller pipes into --attach.
//
// With a session on another machine, `stash put` reads the file here and
// sends its bytes, because the path means nothing there, and `stash get`
// brings a stashed file's bytes back here. Both are bounded at 8 MB. On this
// machine neither copies anything through the socket.

// stashEntryRow is one entry of a stash listing or the result of a put.
type stashEntryRow struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Hash       string `json:"hash"`
	Bytes      int64  `json:"bytes"`
	MediaType  string `json:"media_type"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	StoredAt   int64  `json:"stored_at"`
	Referenced bool   `json:"referenced"`
	Missing    bool   `json:"missing"`
}

// newStashCommand builds `tuios stash` and its two subcommands.
func newStashCommand() *cobra.Command {
	stashCmd := &cobra.Command{
		Use:   "stash",
		Short: "Store files for the session, so another agent can read them later",
		Long: `Put a file in the session's own store and get back a path anyone in the
session can open.

An attachment on a message is a path the sender owns. That is fast, because
nothing is copied, but the sender can delete the file and the reader then finds
nothing there. A stashed file is the daemon's instead. It is there until the
session is killed or the daemon stops, and then it is gone. Nothing survives a
restart.

Use it when you hand a file to another agent and will not keep it yourself. Use
a plain path when you will.`,
	}

	var putSession string
	var putJSON bool
	putCmd := &cobra.Command{
		Use:   "put <file>",
		Short: "Copy a file into the session store and print the stored path",
		Long: `Copy a file into the session's store and print where it now lives.

The daemon opens the file itself, on its own host and as the user that started
it, so the path must be absolute and readable by that user.

The store is content-addressed. Put the same bytes twice and you get the same
path back, and the second put stores nothing.

One file is capped at 16 MB and one session at 256 MB. A put that would pass the
session cap deletes stored files to make room, oldest first, and never one that
a message in the ring still points at. The count of deleted files is printed, so
you can see when something you stashed earlier has gone.`,
		Example: `  # Store a screenshot and hand the path to another agent
  path=$(tuios stash put /tmp/flame.png)
  tuios send-agent-message -w review --attach "$path" 'the hot path is in decode'

  # Store a log and see what the session now holds
  tuios stash put /var/log/build.log
  tuios stash list

  # Send a file here into a session on host build, and attach it there
  path=$(tuios stash put -s build:api /tmp/flame.png)
  tuios send-agent-message -s build:api -w review --attach "$path" 'the hot path is in decode'`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runStashPut(putSession, args[0], putJSON)
		},
	}
	putCmd.Flags().StringVarP(&putSession, "session", "s", "", "Target session (default: most recently active)")
	putCmd.Flags().BoolVar(&putJSON, "json", false, "Output result as JSON")
	_ = putCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var listSession string
	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List the files in the session store",
		Long: `List what the session's store holds, oldest first.

USED says a message still in the agent ring points at the file. Those are never
deleted to make room, so the first row without it is the next one to go.`,
		Example: `  # What is in the store, and how full is it
  tuios stash list

  # Every stored path, for a script
  tuios stash list --json | jq -r '.entries[].path'`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runStashList(listSession, listJSON)
		},
	}
	listCmd.Flags().StringVarP(&listSession, "session", "s", "", "Target session (default: most recently active)")
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output result as JSON")
	_ = listCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	var getSession string
	var getJSON bool
	getCmd := &cobra.Command{
		Use:   "get <stored-path> [file]",
		Short: "Copy a stashed file out of the session store, across a link",
		Long: `Copy one stashed file to a path here. The stored path is what 'stash put' or
'stash list' printed.

It exists for a session on another machine: a path in that machine's stash
cannot be opened here, so the bytes cross the link. A file over 8 MB is refused.
On this machine, open the stored path directly instead.

The copy is written to the file you name, or to the stored file's name in the
current directory. The path written is printed on a line of its own.`,
		Example: `  # Bring an attachment from a session on build here
  tuios stash get -s build:api /run/user/1000/tuios/stash/<id>/<hash>.png flame.png`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			out := ""
			if len(args) > 1 {
				out = args[1]
			}
			return runStashGet(getSession, args[0], out, getJSON)
		},
	}
	getCmd.Flags().StringVarP(&getSession, "session", "s", "", "Target session (default: most recently active)")
	getCmd.Flags().BoolVar(&getJSON, "json", false, "Output result as JSON")
	_ = getCmd.RegisterFlagCompletionFunc("session", completeSessionNames)

	stashCmd.AddCommand(putCmd, listCmd, getCmd)
	return stashCmd
}

// stashTransferMaxBytes is the daemon's cap on bytes that cross the socket,
// checked here first so a file too large is refused before it is read.
const stashTransferMaxBytes = 8 << 20

// runStashPut copies a file into the session store.
func runStashPut(sessionName, path string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	// On this machine the path is sent as given. Making it absolute here
	// would resolve it against this process's directory, and the daemon may
	// be somewhere else; the daemon refuses a relative path and says so,
	// which is the honest answer.
	params := t.params(map[string]any{"path": path})
	if t.host != "" {
		// On another machine the path means nothing, so the bytes go
		// instead, and the path is only what the file is called there.
		content, err := readForTransfer(path)
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(path)
		params["path"] = thisMachine() + ":" + abs
		params["content"] = content
	}
	raw, err := t.client.Call("stash-put", params)
	if err != nil {
		return reportVerbError(t.explain("stash-put", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printStashPut(os.Stdout, raw)
}

// readForTransfer reads a file here for a put on another machine, refusing one
// over the transfer cap before a byte of it is sent.
func readForTransfer(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", &diagnosticError{
			What:  fmt.Sprintf("Cannot read %s: %v.", path, err),
			Cause: "for a session on another machine, this command reads the file here and sends its bytes.",
			Fix:   "give the path of a file on this machine.",
			Err:   err,
		}
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file. The stash stores one file at a time", path)
	}
	if info.Size() > stashTransferMaxBytes {
		return "", fmt.Errorf("%s is %d bytes. A file sent to another machine is capped at %d MB", path, info.Size(), stashTransferMaxBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// runStashGet copies a stashed file out of a session store to a path here.
func runStashGet(sessionName, stored, out string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("stash-get", t.params(map[string]any{"path": stored}))
	if err != nil {
		return reportVerbError(t.explain("stash-get", err), jsonOutput)
	}
	var res struct {
		Name    string `json:"name"`
		Bytes   int64  `json:"bytes"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(res.Content)
	if err != nil {
		return fmt.Errorf("the daemon%s sent content this build cannot read: %w", t.on(), err)
	}
	if out == "" {
		out = res.Name
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", out, err)
	}
	if jsonOutput {
		outputJSON(map[string]any{"success": true, "message": "file copied", "path": out, "bytes": len(data), "host": t.host, "stored": stored})
		return nil
	}
	fmt.Println(out)
	fmt.Printf("copied %s%s\n", stashBytes(int64(len(data))), t.on())
	return nil
}

func printStashPut(w io.Writer, raw json.RawMessage) error {
	var res struct {
		Path      string `json:"path"`
		Bytes     int64  `json:"bytes"`
		Deduped   bool   `json:"deduped"`
		Evicted   int    `json:"evicted"`
		Evictions uint64 `json:"evictions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	// The path goes on its own line first, so `path=$(tuios stash put f)` works.
	fmt.Fprintln(w, res.Path)
	note := fmt.Sprintf("stored %s", stashBytes(res.Bytes))
	if res.Deduped {
		note = fmt.Sprintf("already stored, %s", stashBytes(res.Bytes))
	}
	if res.Evicted > 0 {
		note += fmt.Sprintf(", dropped %d older file(s) to make room", res.Evicted)
	} else if res.Evictions > 0 {
		note += fmt.Sprintf(", %d file(s) dropped so far in this session", res.Evictions)
	}
	fmt.Fprintln(w, note)
	return nil
}

// runStashList prints what the session store holds.
func runStashList(sessionName string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("stash-list", t.params(map[string]any{"session": sessionName}))
	if err != nil {
		return reportVerbError(t.explain("stash-list", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printStashList(os.Stdout, raw)
}

func printStashList(w io.Writer, raw json.RawMessage) error {
	var res struct {
		Dir      string          `json:"dir"`
		Entries  []stashEntryRow `json:"entries"`
		Total    int             `json:"total"`
		Bytes    int64           `json:"bytes"`
		Evicted  uint64          `json:"evicted"`
		MaxBytes int64           `json:"max_bytes"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Entries) == 0 {
		fmt.Fprintln(w, "The session store is empty. 'tuios stash put <file>' puts a file in it.")
		return nil
	}

	rows := make([][]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		used := ""
		if e.Referenced {
			used = "yes"
		}
		state := ""
		if e.Missing {
			state = "MISSING"
		}
		rows = append(rows, []string{
			e.Name[:min(12, len(e.Name))],
			stashBytes(e.Bytes),
			e.MediaType,
			used,
			e.Source,
			state,
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("FILE", "SIZE", "TYPE", "USED", "FROM", "").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 0, 2, 4:
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})

	fmt.Fprintln(w, t.Render())
	fmt.Fprintf(w, "\n%d file(s), %s of %s, in %s\n",
		res.Total, stashBytes(res.Bytes), stashBytes(res.MaxBytes), res.Dir)
	if res.Evicted > 0 {
		fmt.Fprintf(w, "%d file(s) were dropped to make room. USED marks the ones a message still points at.\n", res.Evicted)
	}
	fmt.Fprintln(w, "Every file here is deleted when the session is killed or the daemon stops.")
	return nil
}

// stashBytes renders a size the way a person reads one.
func stashBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
