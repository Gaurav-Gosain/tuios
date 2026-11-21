package session

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

var daemonOwnedCommands = map[string]bool{
	"RenameWindow": true,
	"CloseWindow":  true,
	"NewWindow":    true,
}

const tapeResultTTL = 6 * time.Hour

var clientQueryCommands = map[string]bool{
	"ListWindows":    true,
	"GetSessionInfo": true,
	"GetWindow":      true,
	"ShareLook":      true,
}

func resolveCommandName(name string, args []string) (string, []string, bool) {
	if clientQueryCommands[name] {
		return name, args, true
	}
	if ct, ok := tape.ResolveCommandName(name); ok {
		return string(ct), args, true
	}
	for query := range clientQueryCommands {
		if strings.EqualFold(strings.ReplaceAll(name, "_", ""), query) {
			return query, args, true
		}
	}
	if tape.IsActionName(name) && (len(args) == 0 || strings.HasPrefix(name, config.UserActionPrefix)) {
		return string(tape.CommandTypeAction), append([]string{name}, args...), true
	}
	return "", nil, false
}

func unknownCommandMessage(name string) string {
	return fmt.Sprintf("unknown command %q. Run 'tuios run-command --list' for the command names, or 'tuios keybinds list' for the action names", name)
}

func (d *Daemon) handleExecuteCommand(cs *connState, msg *Message) error {
	var payload ExecuteCommandPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid execute command payload: %w", err)
	}

	LogBasic("Received execute command: %s (session=%s, args=%v)", payload.CommandType, payload.SessionName, payload.Args)

	session := d.findTargetSession(cs, payload.SessionName)
	if session == nil {
		LogBasic("Execute command: session not found")
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}
	LogBasic("Execute command: found session %s (ID=%s)", session.Name(), session.ID)

	if payload.TapeScript == "" {
		canonical, args, ok := resolveCommandName(payload.CommandType, payload.Args)
		if !ok {
			return d.sendCommandResult(cs, payload.RequestID, false, unknownCommandMessage(payload.CommandType))
		}
		payload.CommandType, payload.Args = canonical, args
	}
	if why := d.refuseTapeTyping(cs, &payload); why != "" {
		return d.sendCommandResult(cs, payload.RequestID, false, "run-command is refused for this pane: "+why)
	}
	var newWindowEnv []string
	if payload.SSHFrom != "" && payload.CommandType == "NewWindow" && len(payload.Args) <= 1 {
		argv, env, why, ok := session.sshFollowArgv(payload.SSHFrom)
		if why != "" && cs.isTUIClient {
			_ = d.sendInBroadcastOrder(cs, MsgRemoteCommand, &RemoteCommandPayload{CommandType: "notice", TapeArgs: []string{why}})
		}
		if ok {
			newWindowEnv = env
			name := ""
			if len(payload.Args) == 1 {
				name = payload.Args[0]
			}
			payload.Args = append([]string{name}, argv...)
			LogBasic("Execute command: following ssh of window %s: %v", payload.SSHFrom, redactSSHArgv(argv))
		}
	}
	if payload.FocusIfShown && payload.Cwd == "" && payload.CommandType == "NewWindow" {
		payload.Cwd = session.cwdFrom(payload.CwdFrom)
	}
	if why := d.refuseMultifocusInto(cs, session, payload.CommandType, payload.Args); why != "" {
		return d.sendCommandResult(cs, payload.RequestID, false, "run-command is refused for this pane: "+why)
	}

	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil || daemonOwnedCommands[payload.CommandType] {
		if payload.TapeScript != "" {
			return d.sendCommandResult(cs, payload.RequestID, false,
				"tape scripts need an attached client. A headless daemon has no renderer to run them")
		}
		keepOpen := payload.KeepOpen && payload.CommandType == "NewWindow" && len(payload.Args) > 1
		onExit := func(ptyID string) {
			if keepOpen {
				return
			}
			d.notifyPTYClosed(session.ID, ptyID)
		}
		var data map[string]any
		var err error
		if payload.FocusIfShown && payload.CommandType == "NewWindow" {
			var name string
			var command []string
			if len(payload.Args) > 0 {
				name, command = payload.Args[0], payload.Args[1:]
			}
			err = paneHoldForTest()
			var win WindowState
			if err == nil {
				win, err = session.AddDaemonWindowWith(NewWindowOptions{
					FocusIfShown: true, Cwd: payload.Cwd, Workspace: payload.Workspace, TabKey: payload.TabKey,
					Name: name, Command: command, Env: newWindowEnv,
					Cols: payload.Cols, Rows: payload.Rows,
				}, onExit)
			}
			switch {
			case errors.Is(err, ErrWorkspaceHasPane):
				err = nil
				data = map[string]any{"skipped": "the workspace has a pane already"}
			case err == nil:
				data = map[string]any{"window_id": win.ID, "name": win.Title}
			}
		} else {
			cs.mu.Lock()
			origin := cs.pushOrigin
			cs.mu.Unlock()
			if payload.CommandType == "NewWindow" {
				err = newWindowHoldForTest()
			}
			var closeBy string
			if payload.CommandType == "CloseWindow" {
				closeBy = d.askerOf(cs)
			}
			if err == nil {
				data, err = d.executeDaemonCommandEnv(session, payload.CommandType, payload.Args, NewWindowOptions{
					Cwd: payload.Cwd, Workspace: payload.Workspace, Env: newWindowEnv,
					Cols: payload.Cols, Rows: payload.Rows, keepOpen: keepOpen,
					focusAskedBy: origin, closeAskedBy: closeBy,
				}, onExit)
			}
		}
		if err != nil {
			return d.sendCommandResult(cs, payload.RequestID, false, err.Error())
		}
		return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
			RequestID: payload.RequestID,
			Success:   true,
			Message:   "command executed",
			Data:      data,
		})
	}
	LogBasic("Execute command: found TUI client %s", tuiClient.clientID)

	var remoteCmd *RemoteCommandPayload
	if payload.TapeScript != "" {
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_script",
			TapeScript:  payload.TapeScript,
		}
	} else {
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_command",
			TapeCommand: payload.CommandType,
			TapeArgs:    payload.Args,
		}
	}

	forwarded := cs.clientID != tuiClient.clientID
	if forwarded {
		d.pendingRequestsMu.Lock()
		pr := &pendingRequest{requester: cs, created: time.Now()}
		if payload.TapeScript != "" {
			pr.ttl = tapeResultTTL
		}
		d.pendingRequests[payload.RequestID] = pr
		d.pendingRequestsMu.Unlock()
		d.wakes.cleanup.Set()
	}

	if err := d.sendInBroadcastOrder(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		if forwarded {
			d.pendingRequestsMu.Lock()
			delete(d.pendingRequests, payload.RequestID)
			d.pendingRequestsMu.Unlock()
		}
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	return nil
}

func (d *Daemon) handleCommandResult(cs *connState, msg *Message) error {
	var payload CommandResultPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid command result payload: %w", err)
	}

	if err := checkResultData(payload.Data); err != nil {
		LogError("Refused command result %s from %s: %v", payload.RequestID, cs.clientID, err)
		_ = d.replyError(cs, msg, ErrCodeInvalidMessage, "command result refused: "+err.Error())
		payload.Success, payload.Data = false, nil
		payload.Message = "the client's result was refused: " + err.Error()
	}

	if payload.Success {
		LogBasic("Command %s succeeded: %s (data keys: %v)", payload.RequestID, payload.Message, slices.Sorted(maps.Keys(payload.Data)))
	} else {
		LogBasic("Command %s failed: %s", payload.RequestID, payload.Message)
	}

	d.pendingRequestsMu.Lock()
	pending, found := d.pendingRequests[payload.RequestID]
	if found {
		delete(d.pendingRequests, payload.RequestID)
	}
	d.pendingRequestsMu.Unlock()

	if found && pending != nil && pending.resultCh != nil {
		result := payload
		select {
		case pending.resultCh <- &result:
		default:
		}
		return nil
	}

	if found && pending != nil && pending.requester != nil {
		requester := pending.requester
		LogBasic("Forwarding result to original requester %s", requester.clientID)
		return d.sendMessage(requester, MsgCommandResult, &payload)
	}

	return nil
}

func (d *Daemon) routeToTUISync(tui *connState, requestID string, cmd *RemoteCommandPayload, timeout time.Duration) (*CommandResultPayload, error) {
	cmd.RequestID = requestID

	ch := make(chan *CommandResultPayload, 1)

	d.pendingRequestsMu.Lock()
	d.pendingRequests[requestID] = &pendingRequest{resultCh: ch, created: time.Now()}
	d.pendingRequestsMu.Unlock()
	d.wakes.cleanup.Set()

	clearPending := func() {
		d.pendingRequestsMu.Lock()
		delete(d.pendingRequests, requestID)
		d.pendingRequestsMu.Unlock()
	}

	if err := d.sendInBroadcastOrder(tui, MsgRemoteCommand, cmd); err != nil {
		clearPending()
		return nil, errfmt.Errorf("failed to reach the attached client: %w", err)
	}

	select {
	case res := <-ch:
		return res, nil
	case <-time.After(timeout):
		clearPending()
		return nil, errfmt.Errorf("timed out waiting for the attached client")
	case <-d.ctx.Done():
		clearPending()
		return nil, errfmt.Errorf("daemon shutting down")
	}
}

func (d *Daemon) findTargetSession(cs *connState, sessionName string) *Session {
	if sessionName != "" {
		sess, _ := d.manager.ResolveSession(sessionName)
		return sess
	}
	if sess := d.callerSession(cs); sess != nil {
		return sess
	}
	return d.manager.MostRecentSession()
}

func (d *Daemon) findTUIClient(sessionID string) *connState {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	type pick struct {
		cs    *connState
		input time.Time
		seq   uint64
	}
	var person, viewer pick
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached && !cs.passive
		input, seq, viewOnly := cs.lastActivity, cs.attachSeq, cs.viewOnly
		cs.mu.Unlock()
		if !match {
			continue
		}
		if in := cs.lastInput.Load(); in != 0 {
			if t := time.Unix(0, in); t.After(input) {
				input = t
			}
		}
		best := &person
		if viewOnly {
			best = &viewer
		}
		if best.cs == nil || input.After(best.input) || (input.Equal(best.input) && seq > best.seq) {
			*best = pick{cs, input, seq}
		}
	}
	if person.cs != nil {
		return person.cs
	}
	return viewer.cs
}

func (d *Daemon) sendCommandResult(cs *connState, requestID string, success bool, message string) error {
	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
	})
}

func (d *Daemon) handleGetLogs(cs *connState, msg *Message) error {
	var payload GetLogsPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return errfmt.Errorf("invalid get logs payload: %w", err)
	}

	entries := GetLogEntries(payload.Count)

	if payload.Clear {
		ClearLogBuffer()
	}

	return d.sendMessage(cs, MsgLogsData, &LogsDataPayload{
		Entries: entries,
	})
}

func paneHoldForTest() error {
	return holdForTest("hold_pane")
}

func newWindowHoldForTest() error {
	return holdForTest("hold_new_window")
}

func holdForTest(knob string) error {
	path := testknob.String(knob)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value := strings.TrimSpace(string(data))
	if value == "refuse" {
		return errors.New("the test hold refused the pane")
	}
	ms, err := strconv.Atoi(value)
	if err != nil || ms <= 0 {
		return nil
	}
	time.Sleep(min(time.Duration(ms)*time.Millisecond, 10*time.Second))
	return nil
}
