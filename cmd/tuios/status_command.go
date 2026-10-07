package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
	"github.com/spf13/cobra"
)

// statusOptions are the flags of tuios status.
type statusOptions struct {
	kind     string
	progress int
	app      string
	title    string
	msg      string
	id       string
	clear    bool
	stdout   bool
}

// newStatusCommand is tuios status: it writes one OSC 7501 report (the
// Program Status Protocol) for a script. It needs no daemon and works in any
// terminal that reads the protocol, tuios or not.
func newStatusCommand() *cobra.Command {
	var o statusOptions
	cmd := &cobra.Command{
		Use:   "status [idle|working|done|blocked|error]",
		Short: "Report what a program is doing with OSC 7501",
		Long: `Report what a program is doing with OSC 7501, the Program Status Protocol.

The command writes one report to the terminal. tuios shows it as the pane's
agent state, in the rail and in the Inbox. Other terminals that read the
protocol show it too. The command needs no daemon.

Each report replaces the record it addresses. Send --app and --title in every
report that should keep them. A working or blocked record ends at the next
shell prompt and when the program exits. A done or error record stays until
you type in the pane.

The report goes to the controlling terminal, so it is not lost when the output
of the script goes to a file or a pipe. Use --stdout to write it to standard
output.`,
		Example: `  # A build that reports its progress
  tuios status working --app build --msg 'Compiling' --progress 40
  tuios status done --app build --msg 'Built 12 crates'

  # Wait for a person
  tuios status blocked --kind permission --app deploy --msg 'Approve deploy to production?'

  # A child record, and removing it again
  tuios status working --id eu-west --title 'EU West' --msg 'Pushing image'
  tuios status --clear --id eu-west`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			state := ""
			if len(args) == 1 {
				state = args[0]
			}
			if !cmd.Flags().Changed("progress") {
				o.progress = progstatus.NoProgress
			}
			seq, err := statusSequence(state, o)
			if err != nil {
				return err
			}
			return writeStatusSequence(cmd.OutOrStdout(), seq, o.stdout)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.kind, "kind", "", "What a blocked program waits for: permission, question or auth")
	f.IntVar(&o.progress, "progress", 0, "Progress of a working or blocked program, 0 to 100")
	f.StringVar(&o.app, "app", "", "The program's name, such as cargo or terraform")
	f.StringVar(&o.title, "title", "", "A short label for the record")
	f.StringVar(&o.msg, "msg", "", "One line that says what the program does or waits for")
	f.StringVar(&o.id, "id", "", "The record to address, such as build or build/test. Empty for the main record")
	f.BoolVar(&o.clear, "clear", false, "Remove the record and the records under it. Without --id, remove all records")
	f.BoolVar(&o.stdout, "stdout", false, "Write the report to standard output, not to the terminal")
	return cmd
}

// statusSequence builds the sequence for one tuios status call, and refuses
// anything the protocol would make a terminal discard.
func statusSequence(state string, o statusOptions) (string, error) {
	r := progstatus.Report{ID: o.id, Progress: progstatus.NoProgress}
	switch {
	case o.clear && state != "" && state != string(progstatus.Clear):
		return "", errors.New("use --clear without a state")
	case o.clear || state == string(progstatus.Clear):
		r.State = progstatus.Clear
	case state == "":
		return "", errors.New("give a state: idle, working, done, blocked or error")
	default:
		switch s := progstatus.State(state); s {
		case progstatus.Idle, progstatus.Working, progstatus.Done, progstatus.Blocked, progstatus.Error:
			r.State = s
		default:
			return "", fmt.Errorf("%q is not a state. Use idle, working, done, blocked or error", state)
		}
	}
	if o.id != "" && !progstatus.ValidID(o.id) {
		return "", fmt.Errorf("--id %q is not valid. Use up to %d parts of 1 to %d letters, digits, '.', '_', '+' or '-', joined with '/'", o.id, progstatus.MaxDepth, progstatus.MaxSegment)
	}
	if r.State == progstatus.Clear {
		return progstatus.Sequence(r), nil
	}
	if o.kind != "" {
		if r.State != progstatus.Blocked {
			return "", errors.New("--kind needs the state blocked")
		}
		switch k := progstatus.Kind(o.kind); k {
		case progstatus.Permission, progstatus.Question, progstatus.Auth:
			r.Kind = k
		default:
			return "", fmt.Errorf("--kind %q is not valid. Use permission, question or auth", o.kind)
		}
	}
	if o.progress != progstatus.NoProgress {
		if o.progress < 0 || o.progress > 100 {
			return "", errors.New("--progress must be 0 to 100")
		}
		if r.State != progstatus.Working && r.State != progstatus.Blocked {
			return "", errors.New("--progress needs the state working or blocked")
		}
		r.Progress = o.progress
	}
	if o.app != "" {
		if !progstatus.ValidApp(o.app) {
			return "", fmt.Errorf("--app %q is not valid. Use 1 to %d letters, digits, '.', '_', '+' or '-'", o.app, progstatus.MaxApp)
		}
		r.App = o.app
	}
	var err error
	if r.Title, err = statusText("--title", o.title, progstatus.MaxTitleDecoded); err != nil {
		return "", err
	}
	if r.Msg, err = statusText("--msg", o.msg, progstatus.MaxMsgDecoded); err != nil {
		return "", err
	}
	seq := progstatus.Sequence(r)
	// The terminal reads it back with the same parser; a report it would
	// discard is refused here instead.
	body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b]7501;"), "\x1b\\")
	if _, ok := progstatus.Parse([]byte(body), len(seq)); !ok {
		return "", errors.New("the report is not valid")
	}
	return seq, nil
}

// statusText checks a free-text value: UTF-8, no control characters, at most
// limit bytes.
func statusText(flag, s string, limit int) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("%s is not valid UTF-8", flag)
	}
	for _, r := range s {
		if progstatus.IsControl(r) {
			return "", fmt.Errorf("%s has a control character, such as a tab or a new line. Remove it", flag)
		}
	}
	if len(s) > limit {
		return "", fmt.Errorf("%s is %d bytes. The limit is %d", flag, len(s), limit)
	}
	return s, nil
}

// writeStatusSequence writes the report to the controlling terminal, or to w
// when toStdout is set or there is no terminal to open.
func writeStatusSequence(w io.Writer, seq string, toStdout bool) error {
	if !toStdout {
		if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
			_, werr := io.WriteString(tty, seq)
			cerr := tty.Close()
			return errors.Join(werr, cerr)
		}
	}
	_, err := io.WriteString(w, seq)
	return err
}
