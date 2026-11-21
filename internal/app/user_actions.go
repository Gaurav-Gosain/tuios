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

// RunUserAction runs the user action behind action (user:NAME). extra holds
// facts the caller read and this file cannot, such as the selection, and
// given the values of its arguments the caller gave. A name no [[actions]]
// entry has runs the [[keybindings.command]] entry of that name, so
// user:NAME names every command a person wrote.
func (m *OS) RunUserAction(action string, extra, given map[string]string) tea.Cmd {
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
		if a.AsksFor(given) {
			return m.askUserAction(a, extra, given)
		}
		values, err := a.ResolveArgs(given, nil)
		if err != nil {
			m.userActionFailed(a, err.Error())
			return nil
		}
		return m.runUserActionWith(a, extra, values)
	}
	return nil
}

func (m *OS) runUserActionWith(a config.UserAction, extra, values map[string]string) tea.Cmd {
	dir := m.commandDir()
	facts := m.userActionFacts(dir, extra)
	maps.Copy(facts, values)
	args, err := config.ExpandRun(a.Run, facts)
	if err != nil {
		m.userActionFailed(a, err.Error())
		return nil
	}
	goos := m.sessionGOOS()
	if a.ResolvedIn() == config.UserActionInSilent {
		goos = runtime.GOOS
	}
	return m.runCommandEntry(a.AsCommand(), dir, userActionArgv(goos, args, m.commandEnv(dir)))
}

func (m *OS) userActionFailed(a config.UserAction, why string) {
	m.ShowNotification("The action "+printableTitle(a.DisplayLabel())+" did not run: "+printableTitle(why)+".", "error", m.Settings.NotificationDuration*2)
}

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

func userActionArgv(goos string, args []string, env map[string]string) []string {
	if goos == "windows" {
		return args
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	argv := make([]string, 0, len(keys)+len(args)+2)
	argv = append(argv, "env", "--")
	for _, k := range keys {
		argv = append(argv, k+"="+env[k])
	}
	return append(argv, args...)
}
