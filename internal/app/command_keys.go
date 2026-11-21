package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/winpath"
)

// CommandRanMsg reports how a command entry ended, for the entries whose end
// the client waits for: a shell entry, and the call that opens a popup.
type CommandRanMsg struct {
	Label string
	Err   error
}

var commandShellRunner = runCommandShell

var commandPopupOpener = openCommandPopup

// RunCommandBinding runs the [[keybindings.command]] entry behind action.
func (m *OS) RunCommandBinding(action string) tea.Cmd {
	if m.UserConfig == nil {
		return nil
	}
	c, ok := m.UserConfig.Keybindings.CommandFor(action)
	if !ok {
		return nil
	}
	dir := m.commandDir()
	env := m.commandEnv(dir)
	argv := commandArgvFor(m.sessionGOOS(), c.Command, env)
	if c.ResolvedType() == config.CommandTypeShell {
		argv = commandArgv(c.Command, env)
	}
	return m.runCommandEntry(c, dir, argv)
}

func (m *OS) commandDir() string {
	if m.AttachedHost != "" {
		return m.remotePaneDir()
	}
	return m.scratchDir()
}

func (m *OS) runCommandEntry(c config.CommandBinding, dir string, argv []string) tea.Cmd {
	switch c.ResolvedType() {
	case config.CommandTypeScratch:
		return m.toggleScratch(m.commandScratchSpec(c, argv))

	case config.CommandTypePopup:
		if m.IsDaemonSession && m.DaemonClient != nil {
			req := commandPopupRequest{
				Session: m.SessionName, Title: c.Label(), Command: argv, Dir: dir,
				Width: c.WidthSpec(), Height: c.HeightSpec(), Workspace: m.CurrentWorkspace,
				Host: m.AttachedHost, Build: m.DaemonClient.ClientVersion(),
			}
			label := c.Label()
			return func() tea.Msg { return CommandRanMsg{Label: label, Err: commandPopupOpener(req)} }
		}
		if w := m.newLocalPopup(dir, c.Label(), c.WidthSpec(), c.HeightSpec(), argv); w != nil {
			m.FocusWindow(len(m.Windows) - 1)
			m.EnterTerminalMode()
			m.MarkAllDirty()
		}
		return nil

	case config.CommandTypePane:
		if err := m.addWindowInChecked(dir, c.Label(), argv...); err != nil {
			m.ShowNotification(m.entryFailure(c.Label(), err), "error", m.Settings.NotificationDuration*2)
			return nil
		}
		if m.IsDaemonSession && m.DaemonClient != nil {
			m.pendingStartTerminalMode = true
		} else {
			m.EnterTerminalMode()
		}
		return nil

	case config.CommandTypeShell:
		label := c.Label()
		return func() tea.Msg { return CommandRanMsg{Label: label, Err: commandShellRunner(argv, dir)} }

	case config.CommandTypePluginAction:
		return m.runPluginActionKey(c)
	}
	return nil
}

func (m *OS) handleCommandRan(msg CommandRanMsg) {
	if msg.Err == nil {
		return
	}
	m.ShowNotification(m.entryFailure(msg.Label, msg.Err), "error", m.Settings.NotificationDuration*2)
}

var errNoWindow = errors.New("the request did not reach the daemon")

var errPaneNotStarted = errors.New("the pane did not start")

func (m *OS) addWindowInChecked(dir, name string, command ...string) error {
	if m.IsDaemonSession && m.DaemonClient != nil {
		m.windowIntentErr = nil
		m.addDaemonWindow(dir, name, "", command)
		err := m.windowIntentErr
		m.windowIntentErr = nil
		if err != nil && m.AttachedHost == "" {
			return errNoWindow
		}
		return err
	}
	before := len(m.Windows)
	m.addLocalWindow(dir, name, command)
	if len(m.Windows) == before {
		return errPaneNotStarted
	}
	return nil
}

func (m *OS) entryFailure(label string, err error) string {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() >= 0 {
		return fmt.Sprintf("The command %s stopped with exit code %d.", label, exit.ExitCode())
	}
	if why := m.sessionCallFailure(err); why != "" {
		return "The command " + label + " did not run. " + why
	}
	return "The command " + label + " did not run. " + plainCause(err)
}

func (m *OS) sessionCallFailure(err error) string {
	host := m.AttachedHost
	if refusal := linkRefusal(host, err); refusal != "" {
		return refusal
	}
	if host == "" {
		if errors.Is(err, errNoWindow) {
			return "The daemon on this machine did not answer. Try again in a moment."
		}
		return ""
	}
	if connErr, ok := errors.AsType[*session.HostConnectError](err); ok {
		switch connErr.Code {
		case session.ErrVerbUnknownHost:
			return "This machine has no host named " + host + ". Add it to [hosts] in the config, then attach again."
		case session.ErrVerbProtocolMismatch, session.ErrVerbUnknownVerb:
			return plainCause(connErr)
		}
		return linkDownNotice(host)
	}
	if _, ok := errors.AsType[*session.ProtocolMismatchError](err); ok {
		return "The tuios on " + host + " is a different version. Update tuios on " + host + ", then try again."
	}
	if callErr, ok := errors.AsType[*session.VerbCallError](err); ok {
		if callErr.Code == session.ErrVerbHostUnreachable {
			return linkDownNotice(host)
		}
		return ""
	}
	return linkDownNotice(host)
}

func linkDownNotice(host string) string {
	return "Cannot reach " + host + ". The link is reconnecting. Try again in a moment."
}

func plainCause(err error) string {
	msg := err.Error()
	if callErr, ok := errors.AsType[*session.VerbCallError](err); ok {
		msg = callErr.Message
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "Try again."
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

func (m *OS) sessionGOOS() string {
	if m.AttachedHost == "" {
		return runtime.GOOS
	}
	if v, ok := m.attachedView(); ok && v.OS != "" {
		return v.OS
	}
	if w := m.GetFocusedWindow(); w != nil {
		if dir := remoteFolder(w.Cwd); dir != "" {
			if _, ok := winpath.ForOS(dir, "windows"); ok && !strings.HasPrefix(dir, "/") {
				return "windows"
			}
		}
	}
	return "linux"
}

func (m *OS) commandScratchSpec(c config.CommandBinding, argv []string) scratchSpec {
	return scratchSpec{
		Name: c.ResolvedName(), Title: c.Label(), Command: argv,
		Width: c.WidthSpec(), Height: c.HeightSpec(),
	}
}

func (m *OS) commandEnv(dir string) map[string]string {
	env := map[string]string{
		"TUIOS_SESSION":         m.SessionName,
		"TUIOS_ACTIVE_PANE_CWD": dir,
	}
	if w := m.GetFocusedWindow(); w != nil {
		env["TUIOS_ACTIVE_PANE_ID"] = w.ID
	}
	if m.IsDaemonSession && m.AttachedHost == "" {
		if path, err := session.GetSocketPath(); err == nil {
			env[session.SocketEnv] = path
		}
	}
	return env
}

func (m *OS) remotePaneDir() string {
	w := m.GetFocusedWindow()
	if w == nil {
		return ""
	}
	return remoteFolder(w.Cwd)
}

func remoteFolder(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "file://") {
		_, p, _ := winpath.FileURL(raw)
		return p
	}
	if _, ok := winpath.ForOS(raw, "windows"); ok || filepath.IsAbs(raw) {
		return raw
	}
	return ""
}

func commandArgv(line string, env map[string]string) []string {
	return commandArgvFor(runtime.GOOS, line, env)
}

func commandArgvFor(goos, line string, env map[string]string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if goos == "windows" {
		var b strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&b, "set \"%s=%s\" && ", k, env[k])
		}
		b.WriteString(line)
		return []string{"cmd", "/C", b.String()}
	}
	argv := []string{"env"}
	for _, k := range keys {
		argv = append(argv, k+"="+env[k])
	}
	return append(argv, "sh", "-c", line)
}

func runCommandShell(argv []string, dir string) error {
	if len(argv) == 0 {
		return errors.New("the command is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 - the user's own config.toml names the command
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = os.Environ()
	cmd.WaitDelay = time.Second
	detachFromTerminal(cmd)
	return cmd.Run()
}

type commandPopupRequest struct {
	Session, Title string
	Command        []string
	Dir            string
	Width, Height  string
	Workspace      int
	Host, Build    string
}

func openCommandPopup(req commandPopupRequest) error {
	c, err := dialSessionDaemon(req.Host, req.Build)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	params := map[string]any{
		"session":   req.Session,
		"name":      req.Title,
		"command":   req.Command,
		"width":     req.Width,
		"height":    req.Height,
		"workspace": req.Workspace,
	}
	if req.Dir != "" {
		params["cwd"] = req.Dir
	}
	_, err = c.Call("popup", params)
	return err
}

func dialSessionDaemon(host, build string) (*session.VerbClient, error) {
	if host == "" {
		return session.DialVerbClient()
	}
	c, _, err := session.DialVerbClientThroughHost(host, build)
	return c, err
}

func linkRefusal(host string, err error) string {
	callErr, ok := errors.AsType[*session.VerbCallError](err)
	if !ok || callErr.Code != session.ErrVerbForbidden || callErr.Hint == nil || len(callErr.Hint.Missing) == 0 {
		return ""
	}
	if callErr.Hint.Host != "" {
		host = callErr.Hint.Host
	}
	where := "The other machine"
	on := "on the other machine"
	if host != "" {
		where = "Host " + host
		on = "on " + host
	}
	quoted := make([]string, 0, len(callErr.Hint.Missing))
	for _, c := range callErr.Hint.Missing {
		quoted = append(quoted, `"`+c+`"`)
	}
	table := callErr.Hint.Table
	if table == "" {
		table = "[hosts]"
	}
	return where + " does not allow " + strings.Join(callErr.Hint.Missing, " and ") + " from this machine. Add " +
		strings.Join(quoted, " and ") + " to the allow list in " + table + " " + on + "."
}

const paletteCategoryCommands = "Commands"

func (m *OS) commandPaletteItems() []CommandPaletteItem {
	if m.UserConfig == nil {
		return nil
	}
	cmds := m.UserConfig.Keybindings.Commands()
	dead := map[string]bool{}
	if m.KeybindRegistry != nil {
		for _, b := range m.KeybindRegistry.Bindings() {
			if b.Section == config.SectionCommand && b.Shadowed {
				dead[b.Action] = true
			}
		}
	}
	items := make([]CommandPaletteItem, 0, len(cmds))
	for _, c := range cmds {
		action := c.Action()
		shortcut := ""
		if !dead[action] {
			shortcut = commandShortcut(c)
		}
		items = append(items, CommandPaletteItem{
			Name:     c.Label(),
			Shortcut: shortcut,
			Category: paletteCategoryCommands,
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.RunCommandBinding(action)
			},
		})
	}
	return items
}

func commandShortcut(c config.CommandBinding) string {
	key := config.CanonicalKey(c.BareKey())
	if c.Section() == config.SectionPrefixMode {
		return "prefix+" + key
	}
	return key
}
