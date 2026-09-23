package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// shellPane is one pane as tuios doctor shell reports it.
type shellPane struct {
	Session  string `json:"session"`
	Window   string `json:"window_id"`
	Name     string `json:"name"`
	Marks    bool   `json:"marks_commands"`
	AtPrompt bool   `json:"at_prompt"`
	Commands uint64 `json:"command_seq"`
}

// doctorShellReport is what tuios doctor shell prints.
type doctorShellReport struct {
	DaemonRunning bool        `json:"daemon_running"`
	Panes         []shellPane `json:"panes"`
	// Shell is the login shell's name, from $SHELL, and Setup the lines that
	// turn its OSC 133 marks on. Setup is empty for a shell with no recipe.
	Shell string `json:"shell"`
	Setup string `json:"setup,omitempty"`
}

// shellSetups are the lines that make each shell mark its commands with OSC
// 133: A where the prompt starts, B where the input starts, C when a command
// runs and D with its status when it finishes. They go at the end of the
// shell's rc file.
var shellSetups = map[string]string{
	"zsh": `# ~/.zshrc: mark commands for tuios run (OSC 133)
autoload -Uz add-zsh-hook
_tuios_osc133_precmd()  { local s=$?; print -n "\e]133;D;$s\a\e]133;A\a"; }
_tuios_osc133_preexec() { print -n "\e]133;C\a"; }
add-zsh-hook precmd _tuios_osc133_precmd
add-zsh-hook preexec _tuios_osc133_preexec
PS1+=$'%{\e]133;B\a%}'`,
	"bash": `# ~/.bashrc: mark commands for tuios run (OSC 133)
PROMPT_COMMAND='printf "\e]133;D;%s\a\e]133;A\a" "$?"'${PROMPT_COMMAND:+";$PROMPT_COMMAND"}
PS0='\e]133;C\a'
PS1+='\[\e]133;B\a\]'`,
	// fish 4 sends the marks itself, and a second set from config.fish would
	// only double them, so the advice is the upgrade.
	"fish": `# fish 4 and newer mark their commands by themselves. Upgrade fish:
#   brew upgrade fish, or your system's package manager`,
}

func newDoctorShellCommand() *cobra.Command {
	var asJSON bool
	var sessionName string
	cmd := &cobra.Command{
		Use:   "shell",
		Short: "Report which panes' shells mark their commands, for tuios run",
		Long: `Report, for every pane, whether its shell marks its commands with OSC 133.

tuios run, wait-for command-finished and capture-pane --last-command read where
a command starts and ends, and its exit status, from those marks. A pane whose
shell sends none is refused with no_shell_integration. When one is, this prints
the lines that turn the marks on for your shell ($SHELL): zsh, bash and fish
have a recipe. A pane counts once its shell has drawn one prompt with the marks
on, so open a new pane after adding them.`,
		Example: `  tuios doctor shell
  tuios doctor shell -s work --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			report := doctorShell(sessionName, liveShellPanes, os.Getenv("SHELL"))
			return printDoctorShell(os.Stdout, report, asJSON)
		},
	}
	cmd.Flags().StringVarP(&sessionName, "session", "s", "", "Only this session (default: every session)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the report as JSON")
	return cmd
}

// liveShellPanes lists the panes of the running daemon with whether each
// shell marks its commands, and false when no daemon runs.
func liveShellPanes(only string) ([]shellPane, bool) {
	if !session.DiagnoseDaemon().Running() {
		return nil, false
	}
	client, err := session.DialVerbClientAs(version)
	if err != nil {
		return nil, false
	}
	defer func() { _ = client.Close() }()
	names := []string{only}
	if only == "" {
		raw, err := client.CallWithTimeout("list-sessions", nil, 2*time.Second)
		if err != nil {
			return nil, true
		}
		var sessions struct {
			Sessions []struct {
				Name string `json:"name"`
			} `json:"sessions"`
		}
		if json.Unmarshal(raw, &sessions) != nil {
			return nil, true
		}
		names = names[:0]
		for _, s := range sessions.Sessions {
			names = append(names, s.Name)
		}
	}
	var out []shellPane
	for _, name := range names {
		raw, err := client.CallWithTimeout("list-windows", map[string]any{"session": name}, 2*time.Second)
		if err != nil {
			continue
		}
		var res struct {
			Windows []struct {
				ID         string  `json:"window_id"`
				Name       string  `json:"display_name"`
				AtPrompt   *bool   `json:"at_prompt"`
				CommandSeq *uint64 `json:"command_seq"`
			} `json:"windows"`
		}
		if json.Unmarshal(raw, &res) != nil {
			continue
		}
		for _, w := range res.Windows {
			p := shellPane{Session: name, Window: w.ID, Name: w.Name}
			// The facts are present only for a pane whose shell has sent a
			// mark, so their presence is the answer.
			if w.AtPrompt != nil {
				p.Marks, p.AtPrompt = true, *w.AtPrompt
			}
			if w.CommandSeq != nil {
				p.Commands = *w.CommandSeq
			}
			out = append(out, p)
		}
	}
	return out, true
}

// doctorShell builds the report. panes is injected so it can be tested
// without a daemon.
func doctorShell(only string, panes func(string) ([]shellPane, bool), loginShell string) doctorShellReport {
	r := doctorShellReport{Shell: filepath.Base(loginShell)}
	if loginShell == "" {
		r.Shell = ""
	}
	r.Panes, r.DaemonRunning = panes(only)
	for _, p := range r.Panes {
		if !p.Marks {
			r.Setup = shellSetups[r.Shell]
			break
		}
	}
	return r
}

func printDoctorShell(w io.Writer, r doctorShellReport, asJSON bool) error {
	if asJSON {
		out, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(out))
		return err
	}
	if !r.DaemonRunning {
		fmt.Fprintln(w, "No daemon is running, so no panes were checked.")
		return nil
	}
	missing := 0
	for _, p := range r.Panes {
		verdict := "marks its commands"
		switch {
		case !p.Marks:
			verdict = "no marks: run refuses it with no_shell_integration"
			missing++
		case !p.AtPrompt:
			verdict = "marks its commands, running one now"
		}
		fmt.Fprintf(w, "  %s:%s (%s) %s\n", p.Session, shortWindowID(p.Window), plainLine(p.Name), verdict)
	}
	switch {
	case len(r.Panes) == 0:
		fmt.Fprintln(w, "No panes to check.")
	case missing == 0:
		fmt.Fprintln(w, "Every pane's shell marks its commands, so tuios run works in all of them.")
	case r.Setup != "":
		fmt.Fprintf(w, "\nTo turn the marks on for %s, add these lines, then open a new pane:\n\n%s\n", r.Shell, r.Setup)
	default:
		fmt.Fprintln(w, "\nA pane that is not running a shell never marks commands. For a shell, turn on its OSC 133 prompt integration.")
	}
	return nil
}
