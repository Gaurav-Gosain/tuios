package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/units"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/spf13/cobra"
)

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
	addSessionFlag(putCmd, &putSession)
	addJSONFlag(putCmd, &putJSON)

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
	addSessionFlag(listCmd, &listSession)
	addJSONFlag(listCmd, &listJSON)

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
	addSessionFlag(getCmd, &getSession)
	addJSONFlag(getCmd, &getJSON)

	stashCmd.AddCommand(putCmd, listCmd, getCmd)
	return stashCmd
}

const stashTransferMaxBytes = 8 << 20

func runStashPut(sessionName, path string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	params := t.params(map[string]any{"path": path})
	if t.host != "" {
		content, err := readForTransfer(path)
		if err != nil {
			return err
		}
		abs, _ := filepath.Abs(path)
		params["path"] = config.ThisMachine() + ":" + abs
		params["content"] = content
	}
	raw, err := t.client.Call("stash-put", params)
	if err != nil {
		return reportVerbError(t.explain("stash-put", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printStashPut(os.Stdout, os.Stderr, raw)
}

func stashAttachments(t *verbTarget, paths []string, notes io.Writer) ([]string, error) {
	raw, err := t.client.Call("stash-list", t.params(map[string]any{}))
	if err != nil {
		return nil, t.explain("stash-list", err)
	}
	var listing struct {
		Dir string `json:"dir"`
	}
	if err := jsonx.Unmarshal(raw, &listing); err != nil {
		return nil, errfmt.Errorf("failed to parse the stash listing from %s: %w", t.host, err)
	}
	return stashAttachmentsWith(paths, listing.Dir, t.host, notes, func(params map[string]any) (json.RawMessage, error) {
		raw, err := t.client.Call("stash-put", t.params(params))
		if err != nil {
			return nil, t.explain("stash-put", err)
		}
		return raw, nil
	})
}

func stashAttachmentsWith(paths []string, farRoot, host string, notes io.Writer, put func(map[string]any) (json.RawMessage, error)) ([]string, error) {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if underDir(path, farRoot) {
			out = append(out, path)
			continue
		}
		content, err := readForTransfer(path)
		if err != nil {
			return nil, err
		}
		abs, _ := filepath.Abs(path)
		raw, err := put(map[string]any{"path": config.ThisMachine() + ":" + abs, "content": content})
		if err != nil {
			return nil, err
		}
		var res struct {
			Path string `json:"path"`
		}
		if err := jsonx.Unmarshal(raw, &res); err != nil || res.Path == "" {
			return nil, errfmt.Errorf("the stash put for %s returned no stored path", path)
		}
		fmt.Fprintf(notes, "Sent %s to %s's stash.\n", plainLine(filepath.Base(path)), plainLine(host))
		out = append(out, res.Path)
	}
	return out, nil
}

func underDir(path, dir string) bool {
	if dir == "" || !filepath.IsAbs(path) {
		return false
	}
	rel, ok := pathroot.Within(filepath.Clean(dir), filepath.Clean(path))
	return ok && rel != "."
}

func readForTransfer(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", &diagnosticError{
			What:  fmt.Sprintf("Cannot read %s: %v.", path, err),
			Cause: "for a session on another machine, this command reads the file here and sends its bytes.",
			Fix:   "give the path of a file on this machine.",
			Err:   err,
		}
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", errfmt.Errorf("cannot read %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", errfmt.Errorf("%s is not a regular file. The stash stores one file at a time", path)
	}
	if info.Size() > stashTransferMaxBytes {
		return "", transferTooLarge(path, info.Size())
	}
	if afterTransferStat != nil {
		afterTransferStat(f)
	}
	data, err := io.ReadAll(io.LimitReader(f, stashTransferMaxBytes+1))
	if err != nil {
		return "", errfmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(data) > stashTransferMaxBytes {
		return "", transferTooLarge(path, int64(len(data)))
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

var afterTransferStat func(*os.File)

func transferTooLarge(path string, size int64) error {
	return errfmt.Errorf("%s is at least %d bytes. A file sent to another machine is capped at %s", path, size, units.Bytes(int64(stashTransferMaxBytes)))
}

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(res.Content)
	if err != nil {
		return errfmt.Errorf("the daemon%s sent content this build cannot read: %w", t.on(), err)
	}
	if out == "" {
		out = res.Name
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		return errfmt.Errorf("cannot write %s: %w", out, err)
	}
	if jsonOutput {
		outputJSON(stashGetJSON(out, len(data), t.host, stored))
		return nil
	}
	printStashGet(os.Stdout, os.Stderr, out, int64(len(data)), t.on())
	return nil
}

func stashGetJSON(path string, size int, host, stored string) map[string]any {
	res := map[string]any{"success": true, "message": "file copied", "path": path, "bytes": size, "host": host, "stored": stored}
	if host != "" {
		markUntrusted(res, host)
	}
	return res
}

func printStashGet(out, notes io.Writer, path string, size int64, on string) {
	fmt.Fprintln(out, path)
	fmt.Fprintf(notes, "copied %s%s\n", units.Bytes(size), on)
}

func printStashPut(out, notes io.Writer, raw json.RawMessage) error {
	var res struct {
		Path      string `json:"path"`
		Bytes     int64  `json:"bytes"`
		Deduped   bool   `json:"deduped"`
		Evicted   int    `json:"evicted"`
		Evictions uint64 `json:"evictions"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Fprintln(out, res.Path)
	note := fmt.Sprintf("stored %s", units.Bytes(res.Bytes))
	if res.Deduped {
		note = fmt.Sprintf("already stored, %s", units.Bytes(res.Bytes))
	}
	if res.Evicted > 0 {
		note += fmt.Sprintf(", dropped %s to make room", plural.Count(res.Evicted, "older file"))
	} else if res.Evictions > 0 {
		note += fmt.Sprintf(", %s dropped so far in this session", plural.Count(res.Evictions, "file"))
	}
	fmt.Fprintln(notes, note)
	return nil
}

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
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
			units.Bytes(e.Bytes),
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

	lipgloss.Fprintln(w, t.Render())
	fmt.Fprintf(w, "\n%s, %s of %s, in %s\n",
		plural.Count(res.Total, "file"), units.Bytes(res.Bytes), units.Bytes(res.MaxBytes), res.Dir)
	if res.Evicted > 0 {
		fmt.Fprintf(w, "%s %s dropped to make room. USED marks the ones a message still points at.\n", plural.Count(res.Evicted, "file"), plural.Word(res.Evicted, "was", "were"))
	}
	fmt.Fprintln(w, "Every file here is deleted when the session is killed or the daemon stops.")
	return nil
}
