package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// taskAttachment is one attachment as the task verbs return it.
type taskAttachment struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
	Hash      string `json:"hash"`
	URL       string `json:"url"`
	Note      string `json:"note"`
	By        string `json:"by"`
	At        int64  `json:"at"`
}

// attachMaxFileBytes is the most one file may be on the wire. The daemon
// holds the configured limit, which is at most this; a file over it is
// refused here, before it is read whole.
const attachMaxFileBytes = 8 << 20

// newTaskAttachCommand builds `tuios task attach`.
func newTaskAttachCommand() *cobra.Command {
	var f taskFlags
	var name, note, link string
	var replace bool
	cmd := &cobra.Command{
		Use:   "attach ID [FILE...] [--name N] [--note TEXT] [--link URL] [--replace]",
		Short: "Put files or a link on a task card",
		Long: `Put files or a link on a task card, so the board shows them beside the card:
a screenshot, a log, a diff, a Markdown note, a Mermaid diagram, or the pull
request. This command reads each file and sends its bytes. The card keeps them
until it is removed or they are detached, also after a restart.

A name already on the card is refused unless --replace. The limits are [tasks]
attachment_max_mib (8 MiB a file by default), attachments_per_card and
attachment_session_mib.

From inside a pane, a pane attaches to the card it holds, and to the card it
finished while that card waits in verify or review.`,
		Example: `  tuios task attach t3 after.png --note 'the layout after the fix'
  tuios task attach t3 build.log test-output.txt
  tuios task attach t3 --link https://github.com/o/r/pull/12`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			id, files := args[0], args[1:]
			switch {
			case link != "" && len(files) > 0:
				return errors.New("pass files or --link, not both")
			case link == "" && len(files) == 0:
				return errors.New("name a file to attach, or pass --link URL")
			case name != "" && len(files) > 1:
				return errors.New("--name names one file; attach the files one at a time to name each")
			}
			if link != "" {
				params := map[string]any{"id": id, "link": link, "replace": replace}
				if name != "" {
					params["name"] = name
				}
				if note != "" {
					params["note"] = note
				}
				return attachCall(f, params)
			}
			for _, file := range files {
				data, err := readAttachFile(file)
				if err != nil {
					return err
				}
				params := map[string]any{
					"id": id, "name": cmpOr(name, filepath.Base(file)), "replace": replace,
					"content": base64.StdEncoding.EncodeToString(data),
				}
				if note != "" {
					params["note"] = note
				}
				if err := attachCall(f, params); err != nil {
					return err
				}
			}
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&name, "name", "", "The name the card shows (default: the file's name, or the link)")
	cmd.Flags().StringVar(&note, "note", "", "A line about it")
	cmd.Flags().StringVar(&link, "link", "", "Attach an http or https address instead of a file")
	cmd.Flags().BoolVar(&replace, "replace", false, "Put it in the place of the attachment with the same name")
	return cmd
}

// cmpOr is a when set, else b.
func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// readAttachFile reads one file to attach, refusing a folder and a file over
// the wire limit before it reads it whole.
func readAttachFile(path string) ([]byte, error) {
	fh, err := os.Open(path) // #nosec G304 -- the caller's own file, read with the caller's rights
	if err != nil {
		return nil, errfmt.Errorf("failed to open %s: %w", path, err)
	}
	defer func() { _ = fh.Close() }()
	fi, err := fh.Stat()
	if err != nil {
		return nil, errfmt.Errorf("failed to read %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, errfmt.Errorf("%s is not a file", path)
	}
	data, err := io.ReadAll(io.LimitReader(fh, attachMaxFileBytes+1))
	if err != nil {
		return nil, errfmt.Errorf("failed to read %s: %w", path, err)
	}
	if len(data) > attachMaxFileBytes {
		return nil, errfmt.Errorf("%s is larger than %d MiB, the most one attachment can be", path, attachMaxFileBytes>>20)
	}
	return data, nil
}

// attachCall sends one attach-task and prints what it put on the card.
func attachCall(f taskFlags, params map[string]any) error {
	t, err := dialSessionTarget(f.session)
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("attach-task", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("attach-task", err), f.json)
	}
	if f.json {
		return printVerbResultOn(t, raw, true)
	}
	var res struct {
		Attachment taskAttachment `json:"attachment"`
		Task       taskRow        `json:"task"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	a := res.Attachment
	fmt.Printf("Attached %s to %s%s (%s).\n", plainLine(a.Name), plainLine(res.Task.ID), t.on(), attachWhat(a))
	return nil
}

// attachWhat is an attachment's kind and size, or its address.
func attachWhat(a taskAttachment) string {
	if a.URL != "" {
		return plainLine(a.Kind) + ", " + plainLine(a.URL)
	}
	return plainLine(a.Kind) + ", " + humanBytes(a.Size)
}

// newTaskAttachmentsCommand builds `tuios task attachments`.
func newTaskAttachmentsCommand() *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   "attachments ID",
		Short: "List the files and links on a task card",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			t, err := dialSessionTarget(f.session)
			if err != nil {
				return err
			}
			defer t.Close()
			raw, err := t.client.Call("get-task", t.params(map[string]any{"id": args[0]}))
			if err != nil {
				return reportVerbError(t.explain("get-task", err), f.json)
			}
			var res struct {
				Attachments []taskAttachment `json:"attachments"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			if f.json {
				out, err := jsonx.Marshal(map[string]any{"id": args[0], "attachments": res.Attachments})
				if err != nil {
					return err
				}
				return printVerbResultOn(t, out, true)
			}
			if len(res.Attachments) == 0 {
				fmt.Printf("%s%s has no attachments.\n", plainLine(args[0]), t.on())
				return nil
			}
			return writeAttachments(os.Stdout, res.Attachments)
		},
	}
	f.register(cmd)
	return cmd
}

// writeAttachments prints attachments as a table.
func writeAttachments(w io.Writer, list []taskAttachment) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tSIZE\tBY\tADDED\tNOTE")
	for _, a := range list {
		size := humanBytes(a.Size)
		if a.URL != "" {
			size = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", plainLine(a.Name), plainLine(a.Kind), size, byLabel(a.By), agoOf(a.At), plainLine(a.Note))
	}
	return tw.Flush()
}

// describeAttachments is the Attachments block of task get.
func describeAttachments(b *strings.Builder, list []taskAttachment) {
	if len(list) == 0 {
		return
	}
	b.WriteString("\nAttachments:\n")
	for _, a := range list {
		line := fmt.Sprintf("  %s  %s  %s by %s", plainLine(a.Name), attachWhat(a), agoOf(a.At), byLabel(a.By))
		if a.Note != "" {
			line += ": " + plainLine(a.Note)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// newTaskDetachCommand builds `tuios task detach`.
func newTaskDetachCommand() *cobra.Command {
	var f taskFlags
	cmd := &cobra.Command{
		Use:   "detach ID NAME",
		Short: "Take a file or a link off a task card",
		Long: `Take an attachment off a task card. Its file is removed when no card names it.
From inside a pane, the same rule as attach.`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return callTask(f, "", "detach-task", map[string]any{"id": args[0], "name": args[1]}, func(r taskRow, on string) string {
				return fmt.Sprintf("Detached %s from %s%s.", plainLine(args[1]), plainLine(r.ID), on)
			})
		},
	}
	f.register(cmd)
	return cmd
}

// newTaskAttachmentCommand builds `tuios task attachment`, which holds get.
func newTaskAttachmentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Read one attachment of a task card",
	}
	cmd.AddCommand(newTaskAttachmentGetCommand())
	return cmd
}

// newTaskAttachmentGetCommand builds `tuios task attachment get`.
func newTaskAttachmentGetCommand() *cobra.Command {
	var f taskFlags
	var out string
	cmd := &cobra.Command{
		Use:   "get ID NAME -o FILE",
		Short: "Save one attachment of a task card to a file",
		Long: `Save one attachment of a task card to FILE, or print it with -o -. A link
prints its address. An existing FILE is not written over.`,
		Example: `  tuios task attachment get t3 after.png -o /tmp/after.png
  tuios task attachment get t3 build.log -o - | tail`,
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if out == "" && !f.json {
				return errors.New("name where to save it with -o FILE, or -o - for standard output")
			}
			t, err := dialSessionTarget(f.session)
			if err != nil {
				return err
			}
			defer t.Close()
			raw, err := t.client.Call("get-task-attachment", t.params(map[string]any{"id": args[0], "name": args[1]}))
			if err != nil {
				return reportVerbError(t.explain("get-task-attachment", err), f.json)
			}
			if f.json {
				return printVerbResultOn(t, raw, true)
			}
			var res struct {
				Attachment taskAttachment `json:"attachment"`
				Content    string         `json:"content"`
			}
			if err := jsonx.Unmarshal(raw, &res); err != nil {
				return errfmt.Errorf("failed to parse response: %w", err)
			}
			data := []byte(res.Attachment.URL + "\n")
			if res.Attachment.URL == "" {
				if data, err = base64.StdEncoding.DecodeString(res.Content); err != nil {
					return errfmt.Errorf("failed to decode the attachment: %w", err)
				}
			}
			if out == "-" {
				_, err := os.Stdout.Write(data)
				return err
			}
			fh, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- the caller's own path
			if err != nil {
				return errfmt.Errorf("failed to create %s: %w", out, err)
			}
			if _, err := fh.Write(data); err != nil {
				_ = fh.Close()
				return errfmt.Errorf("failed to write %s: %w", out, err)
			}
			if err := fh.Close(); err != nil {
				return errfmt.Errorf("failed to write %s: %w", out, err)
			}
			fmt.Printf("Saved %s of %s%s to %s (%s).\n", plainLine(res.Attachment.Name), plainLine(args[0]), t.on(), out, humanBytes(int64(len(data))))
			return nil
		},
	}
	f.register(cmd)
	cmd.Flags().StringVarP(&out, "output", "o", "", "The file to save it to, or - for standard output")
	return cmd
}
