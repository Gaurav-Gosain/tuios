package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/shellinteg"
	"github.com/spf13/cobra"
)

type shellPane struct {
	Session         string `json:"session"`
	Window          string `json:"window_id"`
	Name            string `json:"name"`
	Marks           bool   `json:"marks_commands"`
	AtPrompt        bool   `json:"at_prompt"`
	Commands        uint64 `json:"command_seq"`
	CommandMarkSeen *bool  `json:"command_mark_seen,omitempty"`
	PromptOnly      bool   `json:"prompt_marks_only,omitempty"`
}

type doctorShellReport struct {
	DaemonRunning bool        `json:"daemon_running"`
	Panes         []shellPane `json:"panes"`
	Shell         string      `json:"shell"`
	Setup         string      `json:"setup,omitempty"`
	Integration   string      `json:"integration"`
	SetUpByTuios  bool        `json:"set_up_by_tuios"`
}

var shellSetups = map[string]string{
	"zsh": `# ~/.zshrc: mark commands for tuios run (OSC 133)
autoload -Uz add-zsh-hook
_tuios_osc133_precmd()  { local s=$?; print -n "\e]133;D;$s\a\e]133;A\a"; }
_tuios_osc133_preexec() { print -n "\e]133;C\a"; }
add-zsh-hook precmd _tuios_osc133_precmd
add-zsh-hook preexec _tuios_osc133_preexec
PS1+=$'%{\e]133;B\a%}'`,
	"bash": `# ~/.bashrc: mark commands for tuios run (OSC 133)
# Needs bash 4.4 or newer. Older bash, such as macOS /bin/bash 3.2, ignores
# PS0, so it marks prompts and never commands, and run refuses the pane.
PROMPT_COMMAND='printf "\e]133;D;%s\a\e]133;A;redraw=last\a" "$?"'${PROMPT_COMMAND:+";$PROMPT_COMMAND"}
PS0='\e]133;C\a'
PS1+='\[\e]133;B\a\]'`,
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
shell sends none is refused with no_shell_integration.

tuios turns the marks on in the bash and zsh shells it starts, and changes no
file of yours. fish 4 and nushell send them by themselves. A pane that started
before you turned this on keeps its old shell, so open a new pane. With
[shell] integration = "off", or for bash before 4.4, this prints the lines that
turn the marks on for the shell new panes run: zsh, bash and fish have a recipe.
A pane counts once its shell has drawn one prompt with the marks on. A shell
that marks its prompts and not its commands is flagged as prompt marks only
once a command has run there, and run refuses it.`,
		Example: `  tuios doctor shell
  tuios doctor shell -s work --json`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			preferred, integration := "", config.ShellIntegrationAuto
			if cfg, err := config.LoadUserConfig(); err == nil && cfg != nil {
				preferred = cfg.Appearance.PreferredShell
				if !config.ShellIntegrationOn(cfg.Shell.Integration) {
					integration = config.ShellIntegrationOff
				}
			}
			shell, _ := config.ShellFor(preferred)
			report := doctorShell(sessionName, liveShellPanes, shell, integration)
			return printDoctorShell(os.Stdout, report, asJSON)
		},
	}
	addSessionFlag(cmd, &sessionName, "Only this session (default: every session)")
	addJSONFlag(cmd, &asJSON, "Print the report as JSON")
	return cmd
}

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
		if jsonx.Unmarshal(raw, &sessions) != nil {
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
				ID            string  `json:"window_id"`
				Name          string  `json:"display_name"`
				AtPrompt      *bool   `json:"at_prompt"`
				CommandSeq    *uint64 `json:"command_seq"`
				MarksCommands *bool   `json:"marks_commands"`
				PromptOnly    bool    `json:"prompt_marks_only"`
			} `json:"windows"`
		}
		if jsonx.Unmarshal(raw, &res) != nil {
			continue
		}
		for _, w := range res.Windows {
			p := shellPane{Session: name, Window: w.ID, Name: w.Name}
			if w.AtPrompt != nil {
				p.Marks, p.AtPrompt = true, *w.AtPrompt
			}
			if w.CommandSeq != nil {
				p.Commands = *w.CommandSeq
			}
			p.CommandMarkSeen, p.PromptOnly = w.MarksCommands, w.PromptOnly
			out = append(out, p)
		}
	}
	return out, true
}

func doctorShell(only string, panes func(string) ([]shellPane, bool), loginShell, integration string) doctorShellReport {
	r := doctorShellReport{Shell: filepath.Base(loginShell), Integration: integration}
	if loginShell == "" {
		r.Shell = ""
	}
	r.SetUpByTuios = config.ShellIntegrationOn(integration) && shellinteg.Shell(loginShell) != ""
	r.Panes, r.DaemonRunning = panes(only)
	for _, p := range r.Panes {
		if !p.Marks || p.PromptOnly {
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
	switch {
	case r.SetUpByTuios:
		fmt.Fprintf(w, "tuios sets this up for you: new %s panes mark their commands. Your files do not change.\n", r.Shell)
	case shellinteg.MarksItself(r.Shell):
		fmt.Fprintf(w, "%s sends the marks by itself, so tuios adds none.\n", r.Shell)
	case r.Integration == config.ShellIntegrationOff:
		fmt.Fprintln(w, "Shell integration is off ([shell] integration), so tuios adds no marks to new shells.")
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
		case p.PromptOnly:
			verdict = "prompt marks only, it ran a command without marking it: run refuses it with no_shell_integration"
			missing++
		case p.CommandMarkSeen != nil && !*p.CommandMarkSeen:
			verdict = "marks its prompts; no command has run yet to show it marks commands"
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
	case r.SetUpByTuios && r.Setup != "":
		fmt.Fprintf(w, "\nA pane with no marks started before the integration or with it off. Open a new pane.\nIf a new %s pane still has none, add these lines, then open a new pane:\n\n%s\n", r.Shell, r.Setup)
	case r.Setup != "":
		fmt.Fprintf(w, "\nTo turn the marks on for %s, add these lines, then open a new pane:\n\n%s\n", r.Shell, r.Setup)
	default:
		fmt.Fprintln(w, "\nA pane that is not running a shell never marks commands. For a shell, turn on its OSC 133 prompt integration.")
	}
	return nil
}
