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

// Command keybindings, the [[keybindings.command]] entries. See
// config/command_keys.go for the entry and how its key reaches the
// dispatcher. This file runs one.
//
// Every type runs the command with sh -c, so a user can write pipes, globs and
// quotes as they would at a prompt, and a fish or nu login shell does not
// change what the line means. The command starts in the focused pane's folder
// (the scratch terminal's rule: a folder on this machine, else home), with
// four variables beside the pane's own environment:
//
//	TUIOS_SESSION           the session the key was pressed in
//	TUIOS_SOCKET            the daemon's socket (a pane has it already)
//	TUIOS_ACTIVE_PANE_ID    the pane that had the focus
//	TUIOS_ACTIVE_PANE_CWD   the folder the command starts in
//
// Where it runs:
//   - scratch, popup and pane ask the session's own daemon for the pane, so
//     in a session on another machine the command runs on that machine, as
//     the user there, in the focused pane's folder there. A popup or scratch
//     call goes through the host link, where that machine's [hosts] allow
//     list applies: it needs "open".
//   - shell runs from this client, on the machine the client runs on. For the
//     SSH and web servers that is the server.
//
// Only a key press or the command palette runs an entry. Nothing a pane can
// do reaches the dispatcher without the respond grant: send-keys writes into
// a pane, not into tuios, and run-command refuses key presses from a pane
// without it (refuseTapeTyping). Reloading config.toml rebuilds the key map
// and runs nothing.

// CommandRanMsg reports how a command entry ended, for the entries whose end
// the client waits for: a shell entry, and the call that opens a popup.
type CommandRanMsg struct {
	Label string
	Err   error
}

// commandShellRunner runs a shell entry. Tests replace it.
var commandShellRunner = runCommandShell

// commandPopupOpener opens a popup entry through the daemon. Tests replace it.
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
	var dir string
	if m.AttachedHost != "" {
		// Every pane is on the session's machine, so the folder is the
		// focused pane's own path there, and this machine's home means
		// nothing. scratchDir is not asked: it stats a folder here.
		dir = m.remotePaneDir()
	} else {
		dir = m.scratchDir()
	}
	env := m.commandEnv(dir)
	// scratch, popup and pane run on the session's machine, so their line is
	// built for its OS. A shell entry runs here.
	argv := commandArgvFor(m.sessionGOOS(), c.Command, env)
	if c.ResolvedType() == config.CommandTypeShell {
		argv = commandArgv(c.Command, env)
	}

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
		// A pane of the layout, next to the focused one, the way a new window
		// opens: the user can keep it, move it or zoom it, and it closes when
		// the command exits. The keyboard goes to it.
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

// handleCommandRan shows a failed command on the dock.
func (m *OS) handleCommandRan(msg CommandRanMsg) {
	if msg.Err == nil {
		return
	}
	m.ShowNotification(m.entryFailure(msg.Label, msg.Err), "error", m.Settings.NotificationDuration*2)
}

// errNoWindow is a pane entry whose window request did not reach the daemon
// on this machine.
var errNoWindow = errors.New("the request did not reach the daemon")

// errPaneNotStarted is a pane entry whose pane this client could not start.
var errPaneNotStarted = errors.New("the pane did not start")

// addWindowInChecked is AddWindowIn that says when the window was not asked
// for. In a daemon session the daemon makes the window later, so a nil error
// means the request went out, not that the command started.
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

// entryFailure is the dock message for a command entry that did not run or
// failed: what happened, and what to do. label names the entry.
func (m *OS) entryFailure(label string, err error) string {
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() >= 0 {
		return fmt.Sprintf("The command %s stopped with exit code %d.", label, exit.ExitCode())
	}
	if why := m.sessionCallFailure(err); why != "" {
		return "The command " + label + " did not run. " + why
	}
	return "The command " + label + " did not run. " + plainCause(err)
}

// sessionCallFailure says, in words a person acts on, why a call to the
// session's daemon failed: a refusal by the link's allow list, a link that
// is down, or a host that tuios does not know. It is "" for any other error.
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
		// The far daemon answered. Its own words say what is wrong.
		return ""
	}
	// No answer at all: the link dropped, or the call timed out on it.
	return linkDownNotice(host)
}

// linkDownNotice is the message for a call that did not reach host.
func linkDownNotice(host string) string {
	return "Cannot reach " + host + ". The link is reconnecting. Try again in a moment."
}

// plainCause is an error as a sentence: the daemon's message without its
// code, with a capital letter and a full stop.
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

// sessionGOOS is the OS of the machine that holds the session, as Go names
// it. For a session on another machine it is what that daemon's hello said
// when the client attached. Until that answer is in, or from a daemon too
// old to say, a focused pane in a drive folder means Windows, and anything
// else means a Unix.
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

// commandScratchSpec is the scratch spec of a scratch entry.
func (m *OS) commandScratchSpec(c config.CommandBinding, argv []string) scratchSpec {
	return scratchSpec{
		Name: c.ResolvedName(), Title: c.Label(), Command: argv,
		Width: c.WidthSpec(), Height: c.HeightSpec(),
	}
}

// commandEnv is the variables a command entry runs with.
func (m *OS) commandEnv(dir string) map[string]string {
	env := map[string]string{
		"TUIOS_SESSION":         m.SessionName,
		"TUIOS_ACTIVE_PANE_CWD": dir,
	}
	if w := m.GetFocusedWindow(); w != nil {
		env["TUIOS_ACTIVE_PANE_ID"] = w.ID
	}
	// The socket is this machine's. A pane on another machine has its own
	// daemon's socket set already.
	if m.IsDaemonSession && m.AttachedHost == "" {
		if path, err := session.GetSocketPath(); err == nil {
			env[session.SocketEnv] = path
		}
	}
	return env
}

// remotePaneDir is the focused pane's folder in a session on another
// machine: the path the shell there reported (OSC 7), whatever host it
// names, or "".
func (m *OS) remotePaneDir() string {
	w := m.GetFocusedWindow()
	if w == nil {
		return ""
	}
	return remoteFolder(w.Cwd)
}

// remoteFolder is the path an OSC 7 report names, whatever host it names, or
// "". Nothing checks it: the folder is on the session's machine, not this one.
// That machine can be Windows, so a drive path is kept in either form: the
// C:/x a file:// URL names, and the C:\x a Windows daemon sends.
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

// commandArgv is the argv that runs a command line through sh -c with env
// set. An empty line is an empty argv, which runs the user's shell. The
// variables go through env(1) so the daemon, which spawns the pane, needs no
// new field to carry them.
func commandArgv(line string, env map[string]string) []string {
	return commandArgvFor(runtime.GOOS, line, env)
}

// commandArgvFor is commandArgv for the given GOOS, so a test can read the
// Windows form.
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
		// cmd has no env(1). set "K=V" sets one variable, and && runs the
		// next part only after it.
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

// runCommandShell runs a shell entry with no window and waits for it. Its
// output goes nowhere. A start failure or a non-zero exit is the error.
func runCommandShell(argv []string, dir string) error {
	if len(argv) == 0 {
		return errors.New("the command is empty")
	}
	cmd := exec.Command(argv[0], argv[1:]...) // #nosec G204 - the user's own config.toml names the command
	cmd.Dir = dir
	// Nil, not io.Discard: io.Discard makes Go copy from a pipe, and a child
	// the command put in the background holds the pipe open, so Run waited
	// for that child too. Nil is /dev/null, which nobody waits on.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.Env = os.Environ()
	// A child that still holds an output after the command exits is let go
	// after this long.
	cmd.WaitDelay = time.Second
	// A session of its own: no controlling terminal, so sudo or a write to
	// /dev/tty cannot draw on the tuios screen.
	detachFromTerminal(cmd)
	return cmd.Run()
}

// commandPopupRequest is what the popup call needs, copied off the model.
type commandPopupRequest struct {
	Session, Title string
	Command        []string
	Dir            string
	Width, Height  string
	Workspace      int
	// Host is the machine the session is on, "" for this one. Build is this
	// client's version, for the handshake through the link.
	Host, Build string
}

// openCommandPopup asks the session's daemon for a popup that closes when the
// command exits, as tuios popup does.
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

// dialSessionDaemon opens a verb connection to the daemon that holds a
// session: this machine's for host "", else host's through the link, where
// host's allow list for this machine applies. Nothing runs on this machine.
func dialSessionDaemon(host, build string) (*session.VerbClient, error) {
	if host == "" {
		return session.DialVerbClient()
	}
	c, _, err := session.DialVerbClientThroughHost(host, build)
	return c, err
}

// linkRefusal is the message for a call that the session's machine refused
// by its link policy, or "" for any other error. That machine names the
// capabilities it lacks and the config table that grants them. host is the
// machine, "" when it is not known.
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

// paletteCategoryCommands is the palette category of the command entries.
const paletteCategoryCommands = "Commands"

// commandPaletteItems is one palette row per command entry, named by its
// description, with its key as the shortcut.
func (m *OS) commandPaletteItems() []CommandPaletteItem {
	if m.UserConfig == nil {
		return nil
	}
	cmds := m.UserConfig.Keybindings.Commands()
	// A shadowed entry's key runs something else, so the row shows no key.
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

// commandShortcut is an entry's key as the palette spells the built-in keys:
// prefix+ and then the key in its canonical form.
func commandShortcut(c config.CommandBinding) string {
	key := config.CanonicalKey(c.BareKey())
	if c.Section() == config.SectionPrefixMode {
		return "prefix+" + key
	}
	return key
}
