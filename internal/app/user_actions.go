package app

import (
	"maps"
	"runtime"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// User actions, the [[actions]] entries (config/user_actions.go). This file
// runs one on the focused pane: a built-in action through the action runner,
// a plugin action as a plugin_action command entry does, and a command as an
// argument list, in a popup, a pane, a scratch pane or silently, by the
// [[keybindings.command]] paths (runCommandEntry).
//
// A command's arguments are filled from the target's facts by
// config.ExpandRun, in one pass, and handed to the program as they are: no
// shell reads them. The variables a command entry has (commandEnv) are set
// around it, so a script that reads TUIOS_ACTIVE_PANE_ID keeps working.
//
// Only a key press, the palette or tuios actions run (which reaches the
// session's client through run-command, refused to a pane without respond)
// runs a user action. Reloading config.toml runs nothing.

// RunUserAction runs the user action behind action (user:NAME). extra holds
// facts the caller read and this file cannot, such as the selection. A name
// no [[actions]] entry has runs the [[keybindings.command]] entry of that
// name, so user:NAME names every command a person wrote.
func (m *OS) RunUserAction(action string, extra map[string]string) tea.Cmd {
	if m.UserConfig == nil {
		return nil
	}
	a, ok := m.UserConfig.UserActionFor(action)
	if !ok {
		name := strings.TrimPrefix(action, config.UserActionPrefix)
		if _, isCommand := m.UserConfig.Keybindings.CommandFor(config.CommandActionPrefix + name); isCommand {
			return m.RunCommandBinding(config.CommandActionPrefix + name)
		}
		m.ShowNotification("No action is called "+printableTitle(name)+". tuios actions list shows the actions.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	switch a.Body() {
	case "action":
		cmd, err := m.RunActionCmd(a.Action)
		if err != nil {
			m.userActionFailed(a, err.Error())
			return nil
		}
		return cmd
	case "plugin":
		return m.runPluginActionKey(a.AsCommand())
	case "run":
		dir := m.commandDir()
		args, err := config.ExpandRun(a.Run, m.userActionFacts(dir, extra))
		if err != nil {
			m.userActionFailed(a, err.Error())
			return nil
		}
		// A silent command runs on this machine, the others on the
		// session's: the command entry rule.
		goos := m.sessionGOOS()
		if a.ResolvedIn() == config.UserActionInSilent {
			goos = runtime.GOOS
		}
		return m.runCommandEntry(a.AsCommand(), dir, userActionArgv(goos, args, m.commandEnv(dir)))
	}
	return nil
}

// userActionFailed says on the dock that a did not run, and why.
func (m *OS) userActionFailed(a config.UserAction, why string) {
	m.ShowNotification("The action "+printableTitle(a.DisplayLabel())+" did not run: "+printableTitle(why)+".", "error", m.Settings.NotificationDuration*2)
}

// userActionFacts are the placeholder values of the focused pane. Text that
// came from a pane goes in as it is: config.ExpandRun puts it in one argument
// and never reads it for placeholders.
func (m *OS) userActionFacts(dir string, extra map[string]string) map[string]string {
	facts := map[string]string{
		"session":   m.SessionName,
		"workspace": strconv.Itoa(m.CurrentWorkspace),
		"pane.cwd":  dir,
		"host":      m.AttachedHost,
	}
	if w := m.GetFocusedWindow(); w != nil {
		facts["pane.id"] = w.ID
		facts["pane.program"] = w.ForegroundCmd
		facts["pane.title"] = contextMenuWindowName(m, m.FocusedWindow, "")
	}
	maps.Copy(facts, extra)
	return facts
}

// userActionArgv is args with the command entry variables set around it:
// env K=V ... args on a Unix machine. cmd has no env(1) and nothing that sets
// a variable for one program without a command line, so on Windows args run
// as they are, with the variables of the pane.
func userActionArgv(goos string, args []string, env map[string]string) []string {
	if goos == "windows" {
		return args
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// "--" ends env's options before the variables, so a program whose name
	// starts with a dash is run, not read as an option. A program name with =
	// in it would read as a variable: config refuses one.
	argv := make([]string, 0, len(keys)+len(args)+2)
	argv = append(argv, "env", "--")
	for _, k := range keys {
		argv = append(argv, k+"="+env[k])
	}
	return append(argv, args...)
}
