package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
//   - scratch and popup ask the daemon on this machine for a popup, so they
//     refuse in a session on another machine, as the scratch terminal does.
//   - pane asks the session's own daemon for the window, so in a session on
//     another machine the command runs on that machine.
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
	dir := m.scratchDir()
	if m.AttachedHost != "" {
		// The folder is a path on this machine, and the pane would start on
		// the session's.
		dir = ""
	}
	env := m.commandEnv(dir)
	argv := commandArgv(c.Command, env)

	switch c.ResolvedType() {
	case config.CommandTypeScratch:
		return m.toggleScratch(m.commandScratchSpec(c, argv))

	case config.CommandTypePopup:
		if m.IsDaemonSession && m.DaemonClient != nil {
			if m.AttachedHost != "" {
				m.ShowNotification("A popup command works only in a session on this machine.", "warning", m.Settings.NotificationDuration)
				return nil
			}
			req := commandPopupRequest{
				Session: m.SessionName, Title: c.Label(), Command: argv, Dir: dir,
				Width: c.WidthSpec(), Height: c.HeightSpec(), Workspace: m.CurrentWorkspace,
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
		m.AddWindowIn(dir, c.Label(), argv...)
		if m.IsDaemonSession && m.DaemonClient != nil {
			m.pendingStartTerminalMode = true
		} else {
			m.EnterTerminalMode()
		}
		return nil

	case config.CommandTypeShell:
		label := c.Label()
		return func() tea.Msg { return CommandRanMsg{Label: label, Err: commandShellRunner(argv, dir)} }
	}
	return nil
}

// handleCommandRan shows a failed command on the dock.
func (m *OS) handleCommandRan(msg CommandRanMsg) {
	if msg.Err == nil {
		return
	}
	m.ShowNotification(fmt.Sprintf("The command %s failed: %v", msg.Label, msg.Err), "error", m.Settings.NotificationDuration)
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
	if m.IsDaemonSession {
		if path, err := session.GetSocketPath(); err == nil {
			env[session.SocketEnv] = path
		}
	}
	return env
}

// commandArgv is the argv that runs a command line through sh -c with env
// set. An empty line is an empty argv, which runs the user's shell. The
// variables go through env(1) so the daemon, which spawns the pane, needs no
// new field to carry them.
func commandArgv(line string, env map[string]string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C", line}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
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
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = os.Environ()
	return cmd.Run()
}

// commandPopupRequest is what the popup call needs, copied off the model.
type commandPopupRequest struct {
	Session, Title string
	Command        []string
	Dir            string
	Width, Height  string
	Workspace      int
}

// openCommandPopup asks the daemon for a popup that closes when the command
// exits, as tuios popup does.
func openCommandPopup(req commandPopupRequest) error {
	c, err := session.DialVerbClient()
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

// paletteCategoryCommands is the palette category of the command entries.
const paletteCategoryCommands = "Commands"

// commandPaletteItems is one palette row per command entry, named by its
// description, with its key as the shortcut.
func (m *OS) commandPaletteItems() []CommandPaletteItem {
	if m.UserConfig == nil {
		return nil
	}
	cmds := m.UserConfig.Keybindings.Commands()
	items := make([]CommandPaletteItem, 0, len(cmds))
	for _, c := range cmds {
		action := c.Action()
		items = append(items, CommandPaletteItem{
			Name:     c.Label(),
			Shortcut: strings.TrimSpace(c.Key),
			Category: paletteCategoryCommands,
			Action: func(m *OS) (*OS, tea.Cmd) {
				return m, m.RunCommandBinding(action)
			},
		})
	}
	return items
}
