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

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// daemonOwnedCommands are the commands the daemon executes itself whether or not
// a client is attached, because their entire effect is a change to a field the
// daemon owns and no renderer has to be consulted to make it. Routing one of
// these to the client instead is what made a remote rename report success while
// list-windows kept reporting the old name: the client renamed its own copy and
// the daemon, which every read verb answers from, never heard about it.
//
// Commands leave this set only when the daemon cannot produce the same result a
// renderer would. Everything still absent from it is routed as before.
var daemonOwnedCommands = map[string]bool{
	"RenameWindow": true,
	// Closing a window is removing it from the window set and killing its PTY,
	// both of which the daemon owns outright. The renderer has nothing to
	// contribute: it learns the window is gone from the state push and gives the
	// space back.
	"CloseWindow": true,
	// Creating one is the same trade in reverse. The daemon spawns the PTY and
	// adds the window; the one thing it cannot supply is where the window goes,
	// because it has no viewport. It says so with WindowState.Unplaced instead of
	// guessing, and the client that receives the push places it.
	"NewWindow": true,
}

// tapeResultTTL is how long the daemon keeps a tape exec's request open for
// the result. The CLI gives up sooner when its own --timeout is shorter.
const tapeResultTTL = 6 * time.Hour

// clientQueryCommands are the read-only names the attached client answers
// itself, beside the tape commands. They are commands a caller can name too.
var clientQueryCommands = map[string]bool{
	"ListWindows":    true,
	"GetSessionInfo": true,
	"GetWindow":      true,
}

// resolveCommandName turns the name a caller gave run-command into the one
// the client and the daemon dispatch on, or reports that there is no such
// command. The tape's name in any case and the keymap's name for the same
// action both resolve; see tape.ResolveCommandName.
//
// A name nothing dispatches on used to be routed anyway, and the client's
// executor ran it as nothing and reported success: run-command toggle_zoom
// said "command executed" and changed nothing, on the path the docs call the
// escape hatch for a binding that has no verb.
//
// Any other keybinding action, the name config.toml binds a key to, runs as
// the tape's Action command with the name as its argument, so every action a
// key can run is reachable here. It returns the arguments the command runs
// with, which change only for an action.
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
	if tape.IsActionName(name) && len(args) == 0 {
		return string(tape.CommandTypeAction), []string{name}, true
	}
	return "", nil, false
}

// unknownCommandMessage says what a caller can do about a name that is not a
// command.
func unknownCommandMessage(name string) string {
	return fmt.Sprintf("unknown command %q. Run 'tuios run-command --list' for the command names, or 'tuios keybinds list' for the action names", name)
}

// handleExecuteCommand routes a tape command to the TUI client attached to the session.
func (d *Daemon) handleExecuteCommand(cs *connState, msg *Message) error {
	var payload ExecuteCommandPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid execute command payload: %w", err)
	}

	LogBasic("Received execute command: %s (session=%s, args=%v)", payload.CommandType, payload.SessionName, payload.Args)

	// Find the target session
	session := d.findTargetSession(payload.SessionName)
	if session == nil {
		LogBasic("Execute command: session not found")
		return d.sendCommandResult(cs, payload.RequestID, false, "session not found")
	}
	LogBasic("Execute command: found session %s (ID=%s)", session.Name(), session.ID)

	if why := d.refuseTapeTyping(cs, &payload); why != "" {
		return d.sendCommandResult(cs, payload.RequestID, false, "run-command is refused for this pane: "+why)
	}

	if payload.TapeScript == "" {
		canonical, args, ok := resolveCommandName(payload.CommandType, payload.Args)
		if !ok {
			return d.sendCommandResult(cs, payload.RequestID, false, unknownCommandMessage(payload.CommandType))
		}
		payload.CommandType, payload.Args = canonical, args
	}
	var newWindowEnv []string
	if payload.SSHFrom != "" && payload.CommandType == "NewWindow" && len(payload.Args) <= 1 {
		// The client asked for a pane on the machine another pane is ssh'd
		// into. The daemon reads that pane's processes, because it is the side
		// that owns them. With no ssh client there it is an ordinary window.
		if argv, env, ok := session.sshFollowArgv(payload.SSHFrom); ok {
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

	// Find the TUI client attached to this session. When one is present most
	// commands are routed to it (unchanged behavior). With no client attached,
	// structural verbs execute directly against daemon-owned state.
	tuiClient := d.findTUIClient(session.ID)
	if tuiClient == nil || daemonOwnedCommands[payload.CommandType] {
		if payload.TapeScript != "" {
			return d.sendCommandResult(cs, payload.RequestID, false,
				"tape scripts need an attached client. A headless daemon has no renderer to run them")
		}
		onExit := func(ptyID string) { d.notifyPTYClosed(session.ID, ptyID) }
		var data map[string]any
		var err error
		if payload.FocusIfShown && payload.CommandType == "NewWindow" {
			// The pane a client opens on an empty workspace. See
			// ExecuteCommandPayload.FocusIfShown. Its args are empty, or the
			// ssh the source pane runs when SSHFrom asked to follow it.
			var name string
			var command []string
			if len(payload.Args) > 0 {
				name, command = payload.Args[0], payload.Args[1:]
			}
			err = paneHoldForTest()
			var win WindowState
			if err == nil {
				win, err = session.AddDaemonWindowWith(NewWindowOptions{
					FocusIfShown: true, Cwd: payload.Cwd, Workspace: payload.Workspace,
					Name: name, Command: command, Env: newWindowEnv,
				}, onExit)
			}
			switch {
			case errors.Is(err, ErrWorkspaceHasPane):
				// Another request opened it first. Nothing to do, and the
				// shell this one started is closed already.
				err = nil
				data = map[string]any{"skipped": "the workspace has a pane already"}
			case err == nil:
				data = map[string]any{"window_id": win.ID, "name": win.Title}
			}
		} else {
			data, err = d.executeDaemonCommandEnv(session, payload.CommandType, payload.Args, payload.Cwd, payload.Workspace, newWindowEnv, onExit)
		}
		if err != nil {
			return d.sendCommandResult(cs, payload.RequestID, false, err.Error())
		}
		// The attached client, if any, has already been told: the mutation went
		// through Session.mutateState, whose state sink broadcasts to it.
		return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
			RequestID: payload.RequestID,
			Success:   true,
			Message:   "command executed",
			Data:      data,
		})
	}
	LogBasic("Execute command: found TUI client %s", tuiClient.clientID)

	// Forward the command to the TUI client
	var remoteCmd *RemoteCommandPayload
	if payload.TapeScript != "" {
		// Execute a full tape script
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_script",
			TapeScript:  payload.TapeScript,
		}
	} else {
		// Execute a single tape command
		remoteCmd = &RemoteCommandPayload{
			RequestID:   payload.RequestID,
			CommandType: "tape_command",
			TapeCommand: payload.CommandType,
			TapeArgs:    payload.Args,
		}
	}

	// The request is recorded before the command goes out, not after. The TUI
	// answers on its own connection, and it can do so in the time it takes this
	// goroutine to get from the write back to the map; a result that finds no
	// entry is dropped, and the requester then waited out its whole read
	// deadline for an answer the client had already given. On a loaded runner
	// that showed as `run-command` failing with an i/o timeout thirty seconds
	// after a ToggleTiling the client had run at once.
	forwarded := cs.clientID != tuiClient.clientID
	if forwarded {
		d.pendingRequestsMu.Lock()
		pr := &pendingRequest{requester: cs, created: time.Now()}
		if payload.TapeScript != "" {
			// The client answers a tape when it ends. The CLI waits as long
			// as its --timeout says; the daemon keeps the request that long.
			pr.ttl = tapeResultTTL
		}
		d.pendingRequests[payload.RequestID] = pr
		d.pendingRequestsMu.Unlock()
	}

	if err := d.sendMessage(tuiClient, MsgRemoteCommand, remoteCmd); err != nil {
		if forwarded {
			d.pendingRequestsMu.Lock()
			delete(d.pendingRequests, payload.RequestID)
			d.pendingRequestsMu.Unlock()
		}
		return d.sendCommandResult(cs, payload.RequestID, false, fmt.Sprintf("failed to send to TUI: %v", err))
	}

	// Don't send response here. Wait for TUI to send result via handleCommandResult
	return nil
}

// handleCommandResult handles command results from TUI clients.
// Forwards results back to the original requester if there's a pending request.
func (d *Daemon) handleCommandResult(cs *connState, msg *Message) error {
	var payload CommandResultPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid command result payload: %w", err)
	}

	// Data is map[string]any and can nest to any depth. Everything below
	// walks it by recursion: the JSON reply of a verb, the gob encode that
	// forwards it. A result no real command makes is turned into a failure,
	// so the requester hears why instead of waiting out its timeout, and the
	// sender is told too. See wire_bounds.go.
	if err := checkResultData(payload.Data); err != nil {
		LogError("Refused command result %s from %s: %v", payload.RequestID, cs.clientID, err)
		_ = d.replyError(cs, msg, ErrCodeInvalidMessage, "command result refused: "+err.Error())
		payload.Success, payload.Data = false, nil
		payload.Message = "the client's result was refused: " + err.Error()
	}

	if payload.Success {
		// The keys only. Printing the values with %v walked them by recursion
		// and put whatever a client sent into the daemon log.
		LogBasic("Command %s succeeded: %s (data keys: %v)", payload.RequestID, payload.Message, slices.Sorted(maps.Keys(payload.Data)))
	} else {
		LogBasic("Command %s failed: %s", payload.RequestID, payload.Message)
	}

	// Check if there's a pending request from another client waiting for this result
	d.pendingRequestsMu.Lock()
	pending, found := d.pendingRequests[payload.RequestID]
	if found {
		delete(d.pendingRequests, payload.RequestID)
	}
	d.pendingRequestsMu.Unlock()

	// Deliver to a JSON verb handler blocked in routeToTUISync, if any.
	if found && pending != nil && pending.resultCh != nil {
		result := payload
		select {
		case pending.resultCh <- &result:
		default:
			// The waiter already gave up (timeout/disconnect); drop the result.
		}
		return nil
	}

	// Forward the result to the original requester
	if found && pending != nil && pending.requester != nil {
		requester := pending.requester
		LogBasic("Forwarding result to original requester %s", requester.clientID)
		return d.sendMessage(requester, MsgCommandResult, &payload)
	}

	return nil
}

// routeToTUISync sends a remote command to an attached TUI and blocks until the
// TUI replies with its result, a timeout elapses, or the daemon shuts down. It
// is the synchronous bridge the JSON verb front-end uses so a control verb that
// must be handled by the live renderer (WM keys, structural changes, live config)
// still returns a single request/response over the JSON connection. requestID
// must be unique per in-flight call.
func (d *Daemon) routeToTUISync(tui *connState, requestID string, cmd *RemoteCommandPayload, timeout time.Duration) (*CommandResultPayload, error) {
	// Stamp the request ID onto the outgoing command so the TUI echoes it back on
	// its result and handleCommandResult can match it to this pending waiter.
	cmd.RequestID = requestID

	ch := make(chan *CommandResultPayload, 1)

	d.pendingRequestsMu.Lock()
	d.pendingRequests[requestID] = &pendingRequest{resultCh: ch, created: time.Now()}
	d.pendingRequestsMu.Unlock()

	clearPending := func() {
		d.pendingRequestsMu.Lock()
		delete(d.pendingRequests, requestID)
		d.pendingRequestsMu.Unlock()
	}

	if err := d.sendMessage(tui, MsgRemoteCommand, cmd); err != nil {
		clearPending()
		return nil, fmt.Errorf("failed to reach the attached client: %w", err)
	}

	select {
	case res := <-ch:
		return res, nil
	case <-time.After(timeout):
		clearPending()
		return nil, fmt.Errorf("timed out waiting for the attached client")
	case <-d.ctx.Done():
		clearPending()
		return nil, fmt.Errorf("daemon shutting down")
	}
}

// findTargetSession finds a session by name, or returns the most recently active session.
// A name a session was renamed from still finds it: a pane started before the
// rename keeps the old name in TUIOS_SESSION, and its commands send that.
func (d *Daemon) findTargetSession(sessionName string) *Session {
	if sessionName != "" {
		sess, _ := d.manager.ResolveSession(sessionName)
		return sess
	}

	// Find the most recently active session
	sessions := d.manager.ListSessions()
	if len(sessions) == 0 {
		return nil
	}

	var mostRecent *Session
	var mostRecentTime int64 = 0

	for _, info := range sessions {
		if info.LastActive > mostRecentTime {
			mostRecentTime = info.LastActive
			mostRecent = d.manager.GetSession(info.Name)
		}
	}

	return mostRecent
}

// findTUIClient finds the TUI client attached to a session, and never one that
// is still attaching: a routed command is an unsolicited message, and a client
// inside its attach call has no read loop to tell one from its own reply. See
// connState.attached.
//
// With several clients attached it is the one the person used last: the
// newest key typed into a pane or activity reported, and with no input at any
// of them, the one that attached last. A command routed here acts as that
// person, and some of what it sets, multifocus for one, is the client's own.
// It used to be whichever client the map gave first, so tuios xpanes turned
// multifocus on at the other client half the time, and Enter at the client
// that ran it reached one pane.
func (d *Daemon) findTUIClient(sessionID string) *connState {
	d.clientsMu.RLock()
	defer d.clientsMu.RUnlock()

	var best *connState
	var bestInput time.Time
	var bestSeq uint64
	for _, cs := range d.clients {
		cs.mu.Lock()
		match := cs.sessionID == sessionID && cs.isTUIClient && cs.attached
		input, seq := cs.lastActivity, cs.attachSeq
		cs.mu.Unlock()
		if !match {
			continue
		}
		if in := cs.lastInput.Load(); in != 0 {
			if t := time.Unix(0, in); t.After(input) {
				input = t
			}
		}
		if best == nil || input.After(bestInput) || (input.Equal(bestInput) && seq > bestSeq) {
			best, bestInput, bestSeq = cs, input, seq
		}
	}

	return best
}

// sendCommandResult sends a command result to a client.
func (d *Daemon) sendCommandResult(cs *connState, requestID string, success bool, message string) error {
	return d.sendMessage(cs, MsgCommandResult, &CommandResultPayload{
		RequestID: requestID,
		Success:   success,
		Message:   message,
	})
}

// handleGetLogs retrieves recent log entries from the daemon's log buffer.
func (d *Daemon) handleGetLogs(cs *connState, msg *Message) error {
	var payload GetLogsPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return fmt.Errorf("invalid get logs payload: %w", err)
	}

	entries := GetLogEntries(payload.Count)

	if payload.Clear {
		ClearLogBuffer()
	}

	return d.sendMessage(cs, MsgLogsData, &LogsDataPayload{
		Entries: entries,
	})
}

// paneHoldForTest lets the end-to-end tests hold the pane a client opens on
// an empty workspace, so they can send the switches that follow while the
// request waits, as a slow daemon would, or refuse it, as a failed request
// would be.
//
// It does anything only with TUIOS_E2E=1 and TUIOS_E2E_HOLD_PANE naming a
// file, which ordinary runs never set. A file that holds a number of
// milliseconds makes each request wait that long, ten seconds at most. A file
// that holds "refuse" makes each request fail. Messages are read one at a time
// per connection, so the client's later messages wait behind a held request.
func paneHoldForTest() error {
	if os.Getenv("TUIOS_E2E") != "1" {
		return nil
	}
	path := os.Getenv("TUIOS_E2E_HOLD_PANE")
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
