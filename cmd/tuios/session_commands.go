package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"golang.org/x/term"
)

func runAttach(sessionName string, createIfMissing bool) error {
	// Check the terminal before anything else: a session that cannot be
	// rendered is much harder to diagnose once the TUI has taken the screen.
	if err := checkTerminal(); err != nil {
		return err
	}

	// A daemon restores every saved session as it starts, so the sessions the
	// user is asking for are one process away. Refusing here and naming a
	// command that would create a different session is what made attach look
	// like it had lost them.
	if !session.IsDaemonRunning() {
		// Said before the daemon starts, because afterwards the sessions simply
		// exist and the user is left to work out where they came from.
		if reportSavedSessionsBeforeStart() == 0 && sessionName == "" {
			// An unnamed attach against an empty daemon opens a new session:
			// that is what the daemon has always done, and it is the only thing
			// left that gets the user to a terminal. Say so rather than let a
			// session appear unannounced.
			fmt.Println("No saved sessions to restore. Opening a new one.")
		}
		if err := ensureDaemon(); err != nil {
			return err
		}
	}

	if err := ensureAttachTarget(sessionName, createIfMissing); err != nil {
		return err
	}

	return runDaemonSession(sessionName, createIfMissing)
}

// reportSavedSessionsBeforeStart says what is about to be brought back, and
// returns how many. It is said before the daemon starts because afterwards the
// sessions simply exist, and the user is left to guess where they came from.
func reportSavedSessionsBeforeStart() int {
	infos, err := session.ListResurrectableInfos()
	if err != nil || len(infos) == 0 {
		return 0
	}

	names := make([]string, 0, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
	}
	noun := "sessions"
	if len(names) == 1 {
		noun = "session"
	}
	fmt.Printf("Restoring %d saved %s: %s.\n", len(names), noun, strings.Join(truncateList(names, 12), ", "))
	fmt.Printf("%s: %s.\n", session.RestoredTag, session.RestoredNote)
	return len(names)
}

// ensureAttachTarget verifies the named session exists before the TUI starts,
// so a typo produces a list of real names instead of an empty screen or a
// silently created session. It is a no-op when no name was given (attach picks
// the most recent session) or when the caller asked to create the session.
func ensureAttachTarget(sessionName string, createIfMissing bool) error {
	if sessionName == "" || createIfMissing {
		return nil
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	sessions, err := listSessionInfos(client)
	if err != nil {
		// Listing is a courtesy; if it fails, let the attach itself report.
		return nil
	}

	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.Name)
		if s.Name != sessionName {
			continue
		}
		if s.Attached {
			// Attaching to an already-attached session is supported, not an
			// error, but the shared screen size surprises people who expect
			// tmux's exclusive attach. Say so rather than letting them wonder
			// why their window shrank.
			fmt.Printf("Session %q already has a client attached. TUIOS shares the session and renders at the smallest client's size.\n", sessionName)
		}
		// Said before the attach, because the attach itself clears the mark. This
		// is the answer to "why is my session still here after I killed the
		// daemon", at the moment the user is asking it.
		if s.Restored {
			fmt.Printf("Session %q was %s: %s.\n", sessionName, session.RestoredTag, session.RestoredNote)
		}
		return nil
	}
	return explainMissingSession(sessionName, names)
}

// listSessionInfos returns the live sessions over the verb protocol.
func listSessionInfos(client *session.VerbClient) ([]session.SessionInfo, error) {
	raw, err := client.Call("list-sessions", nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Sessions []session.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}
	return listed.Sessions, nil
}

func runNewSession(sessionName string) error {
	if err := ensureDaemon(); err != nil {
		return err
	}

	if sessionName == "" {
		client := session.NewTUIClient()
		if err := client.Connect(version, 80, 24); err != nil {
			return explainDialError(err)
		}

		existingNames := client.AvailableSessionNames()
		_ = client.Close()

		sessionName = generateUniqueSessionName(existingNames)
		fmt.Printf("Creating session '%s'\n", sessionName)
	}

	return runDaemonSession(sessionName, true)
}

// runNewSessionDetached creates a headless session in the daemon and returns
// without launching the TUI. The session holds an initial window, is usable by
// control verbs immediately, and can be attached later with 'tuios attach'.
func runNewSessionDetached(sessionName string) error {
	if err := ensureDaemon(); err != nil {
		return err
	}

	client := session.NewClient(&session.ClientConfig{Version: version})
	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	if sessionName == "" {
		sessions, err := client.ListSessions()
		if err != nil {
			return fmt.Errorf("failed to list sessions: %w", err)
		}
		existing := make([]string, len(sessions))
		for i, s := range sessions {
			existing[i] = s.Name
		}
		sessionName = generateUniqueSessionName(existing)
	}

	if err := client.CreateDetachedSession(sessionName, 80, 24); err != nil {
		return err
	}

	fmt.Printf("Created detached session '%s'. Attach with 'tuios attach %s'.\n", sessionName, sessionName)
	return nil
}

func generateUniqueSessionName(existingNames []string) string {
	existing := make(map[string]bool)
	for _, name := range existingNames {
		existing[name] = true
	}

	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !existing[name] {
			return name
		}
	}
}

func runDaemonSession(sessionName string, createNew bool) error {
	// Every path into the TUI funnels through here, so this is the one place
	// that guarantees the terminal can host it before the screen is taken over.
	if err := checkTerminal(); err != nil {
		return err
	}

	startPprofServer()

	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	userConfig := loadAndApplyConfig()

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	log.Printf("[CLIENT] Detecting terminal capabilities...")
	hostCaps := app.GetHostCapabilities()

	// Build client capabilities from detected host capabilities
	clientCaps := &session.ClientCapabilities{
		PixelWidth:     hostCaps.PixelWidth,
		PixelHeight:    hostCaps.PixelHeight,
		CellWidth:      hostCaps.CellWidth,
		CellHeight:     hostCaps.CellHeight,
		KittyGraphics:  hostCaps.KittyGraphics,
		KittyAnimation: hostCaps.KittyAnimation,
		SixelGraphics:  hostCaps.SixelGraphics,
		TerminalName:   hostCaps.TerminalName,
	}
	log.Printf("[CLIENT] Capabilities: cell=%dx%d, kitty=%v, sixel=%v, term=%s",
		clientCaps.CellWidth, clientCaps.CellHeight, clientCaps.KittyGraphics, clientCaps.SixelGraphics, clientCaps.TerminalName)

	log.Printf("[CLIENT] Connecting to daemon...")
	client := session.NewTUIClient()
	// The real host size, asked for here rather than left at a placeholder.
	//
	// The session's size is the minimum over its attached clients, and this is
	// the number this client contributes to that minimum. A placeholder 80x24
	// is not a neutral guess: it is smaller than almost any real terminal, so a
	// local client attaching beside a browser client used to collapse the whole
	// session to 80x24 for as long as it took Bubble Tea to deliver the first
	// WindowSizeMsg. Every other client clamped or scaled its panes into that
	// box and back out again, which is the resize storm an attach was reported
	// to cause. Bubble Tea still delivers the authoritative size a moment
	// later; this only stops the interval in between from being a lie.
	width, height := hostTerminalSize()

	if err := client.ConnectWithCapabilities(version, width, height, clientCaps); err != nil {
		return explainDialError(err)
	}
	log.Printf("[CLIENT] Connected to daemon")

	log.Printf("[CLIENT] Attaching to session '%s' (createNew=%v)", sessionName, createNew)
	state, err := client.AttachSession(sessionName, createNew, width, height)
	if err != nil {
		names := client.AvailableSessionNames()
		_ = client.Close()
		if !createNew && sessionName != "" {
			return explainMissingSession(sessionName, names)
		}
		return &diagnosticError{
			What:  fmt.Sprintf("Could not attach to session %q: %v.", sessionName, err),
			Cause: "the daemon refused the attach, usually because the session was killed between listing and attaching.",
			Fix:   "run 'tuios ls' to see live sessions, or 'tuios new' to create one.",
			Err:   err,
		}
	}
	log.Printf("[CLIENT] Attached to session, got state")

	log.Printf("[CLIENT] Starting read loop")
	client.StartReadLoop()

	prw := app.NewPostRenderWriter(os.Stdout)

	initialOS := app.NewOS(app.OSOptions{
		Client:          app.ClientLocal,
		KeybindRegistry: keybindRegistry,
		UserConfig:      userConfig,
		ShowKeys:        showKeys,
		// The size this client told the daemon, so the restored windows are
		// tiled into a real box rather than a zero one the first WindowSizeMsg
		// then has to undo. The servers have always passed theirs.
		Width:           width,
		Height:          height,
		IsDaemonSession: true,
		DaemonClient:    client,
		SessionName:     client.SessionName(),
		// One writer for the terminal: frames, kitty and sixel sequences all
		// serialize on it. Left nil, the passthroughs open their own /dev/tty
		// and nothing can order their writes against a frame.
		GraphicsOutput: prw,
	})
	initialOS.PostRenderWriter = prw

	// Everything the daemon sends an attached client, queued for Update. The
	// same call the SSH server and tuios-web make.
	initialOS.WireDaemonClient(client)

	initialOS.RestoreAttachedSession(state)

	// The shared list, then the one option that is this transport's: the
	// writer every frame and every graphics sequence serialize on.
	p := tea.NewProgram(initialOS, append(app.ProgramOptions(), tea.WithOutput(prw))...)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	reason := app.ExitNormal
	// The client may have switched sessions since it attached, so the name to
	// report at exit is the one it is in now, not the one on the command line.
	exitSession := sessionName
	// A deliberate quit in a daemon session kills it (leader q); a detach leaves
	// it running (leader d). Both exit normally, so QuitRequested is what tells
	// the two apart and picks the message the user sees.
	killed := false
	if finalOS, ok := finalModel.(*app.OS); ok {
		reason = finalOS.ExitReason
		if name := finalOS.SessionName; name != "" {
			exitSession = name
		}
		killed = finalOS.QuitRequested
		// Syncing state back is meaningful only for a detach, while the session
		// still exists. A quit already killed it, a kill from elsewhere left no
		// session to receive the state, and a lost connection cannot carry the
		// write, so skip it rather than block on a dead socket on the way out.
		if reason == app.ExitNormal && !killed {
			finalOS.SyncStateToDaemon()
		}
		finalOS.Cleanup()
	}

	_ = client.Close()

	terminal.ResetTerminal()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	return reportSessionExit(exitSession, reason, killed)
}

// reportSessionExit prints why the client stopped and returns an error for the
// cases that are not a normal exit, so a script or an agent driving tuios sees
// a non-zero status instead of a message that reads like success.
//
// killed distinguishes the two normal exits: a deliberate quit that killed the
// session (leader q) from a detach that left it running (leader d). They read
// differently on the way out so the user is not told a session was detached when
// it was in fact destroyed.
func reportSessionExit(sessionName string, reason app.ExitReason, killed bool) error {
	switch reason {
	case app.ExitSessionKilled:
		return &diagnosticError{
			What:  fmt.Sprintf("Session %q was terminated while you were attached.", sessionName),
			Cause: "the session was killed from another client, from 'tuios kill-session', or over the control plane.",
			Fix:   "run 'tuios ls' to see remaining sessions, or 'tuios new' to start another.",
		}

	case app.ExitDaemonLost:
		return &diagnosticError{
			What:  "The connection to the TUIOS daemon was lost.",
			Cause: "the daemon exited, crashed, or was stopped while this client was attached.",
			Fix:   "run 'tuios ls' to check the daemon, then 'tuios attach " + sessionName + "' to reconnect if the session survived.",
		}

	default:
		if killed {
			fmt.Printf("Killed session '%s'.\n", sessionName)
		} else {
			fmt.Printf("Detached from session '%s'.\n", sessionName)
		}
		return nil
	}
}

// lsEntry is a row of 'tuios ls --json'. It is the wire type plus the one fact
// the wire cannot carry: a session that exists only on disk, because no daemon
// is holding it. The flag is omitted for live sessions, so a listing from a
// running daemon is unchanged.
type lsEntry struct {
	session.SessionInfo
	Saved bool `json:"saved,omitempty"`
}

func runListSessions(jsonOutput bool) error {
	diag := session.DiagnoseDaemon()
	if !diag.Running() {
		return listSavedSessions(diag, jsonOutput)
	}

	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-sessions", nil)
	if err != nil {
		return explainVerbError("list-sessions", err)
	}
	var listed struct {
		Sessions []session.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		return fmt.Errorf("failed to parse sessions: %w", err)
	}
	sessions := listed.Sessions

	if jsonOutput {
		entries := make([]lsEntry, 0, len(sessions))
		for _, s := range sessions {
			entries = append(entries, lsEntry{SessionInfo: s})
		}
		return printJSON(entries)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions. Create one with 'tuios new', or run 'tuios resurrect' to see saved sessions.")
		return nil
	}

	rows := make([][]string, 0, len(sessions))
	anyRestored := false
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		// A restored session has by construction never been attached, so the tag
		// replaces "detached" rather than needing a column of its own.
		if s.Restored {
			status = session.RestoredTag
			anyRestored = true
		}

		rows = append(rows, []string{
			s.Name,
			fmt.Sprintf("%d", s.WindowCount),
			status,
			formatTimeAgo(s.Created),
			formatTimeAgo(s.LastActive),
		})
	}

	fmt.Println(renderSessionTable(rows))
	fmt.Printf("\n%d session(s)\n", len(sessions))
	if anyRestored {
		fmt.Printf("%s: %s.\n", session.RestoredTag, session.RestoredNote)
	}
	return nil
}

// listSavedSessions is 'tuios ls' with no daemon listening. The sessions are on
// disk and a daemon brings them back, so listing them is the truth; printing an
// empty list was what made attach's refusal incomprehensible.
//
// It reports the no-daemon status, which is what lets a script tell this apart
// from a daemon that is running and holds nothing.
func listSavedSessions(diag session.DaemonDiagnosis, jsonOutput bool) error {
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return err
	}

	if jsonOutput {
		entries := make([]lsEntry, 0, len(infos))
		for _, info := range infos {
			entries = append(entries, lsEntry{
				SessionInfo: session.SessionInfo{
					Name:        info.Name,
					WindowCount: info.WindowCount,
					LastActive:  savedUnix(info.SavedAt),
				},
				Saved: true,
			})
		}
		if err := printJSON(entries); err != nil {
			return err
		}
		return &statusError{code: noDaemonStatus}
	}

	if len(infos) > 0 {
		rows := make([][]string, 0, len(infos))
		for _, info := range infos {
			rows = append(rows, []string{
				info.Name,
				fmt.Sprintf("%d", info.WindowCount),
				session.SavedTag,
				"-",
				formatTimeAgo(savedUnix(info.SavedAt)),
			})
		}
		fmt.Println(renderSessionTable(rows))
		fmt.Printf("\n%d session(s)\n", len(infos))
		fmt.Printf("%s: %s.\n\n", session.SavedTag, session.SavedNote)
	}

	fmt.Println(diag.Explain())
	return &statusError{code: noDaemonStatus}
}

// savedUnix converts a save time to the epoch seconds the listing formats,
// keeping zero as "unknown" rather than turning it into 1970.
func savedUnix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// renderSessionTable draws the session listing. Both listings use it so a
// session reads the same whether a daemon is holding it or a disk is.
func renderSessionTable(rows [][]string) string {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("NAME", "WINDOWS", "STATUS", "CREATED", "LAST ACTIVE").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			baseStyle := lipgloss.NewStyle().Padding(0, 1)

			if row == table.HeaderRow {
				return baseStyle.Bold(true).Foreground(lipgloss.Color("12"))
			}

			switch col {
			case 0:
				return baseStyle.Foreground(lipgloss.Color("3")).Bold(true)
			case 2:
				if rows[row][col] == "attached" {
					return baseStyle.Foreground(lipgloss.Color("10"))
				}
				return baseStyle.Foreground(lipgloss.Color("8"))
			case 3, 4:
				return baseStyle.Foreground(lipgloss.Color("8"))
			default:
				return baseStyle
			}
		}).Render()
}

func formatTimeAgo(unixTime int64) string {
	if unixTime == 0 {
		return "-"
	}

	t := time.Unix(unixTime, 0)
	diff := time.Since(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", mins)
	case diff < 24*time.Hour:
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case diff < 7*24*time.Hour:
		days := int(diff.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}

func runKillSession(sessionName string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Call("kill-session", map[string]any{"session": sessionName}); err != nil {
		return explainVerbError("kill-session", err)
	}

	fmt.Printf("Killed session '%s'.\n", sessionName)
	return nil
}

// runResurrect lists resurrectable sessions (no name) or restores one on demand
// and attaches to it (name given).
func runResurrect(sessionName string) error {
	if sessionName == "" {
		return listResurrectableSessions()
	}

	// Ensure the daemon is running so it can hold the restored session.
	if err := ensureDaemon(); err != nil {
		return err
	}

	// Ask the daemon to restore the session from saved state. This is a no-op if
	// the daemon already auto-restored it on start.
	client := session.NewClient(&session.ClientConfig{Version: version})
	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	if err := client.ResurrectSession(sessionName); err != nil {
		_ = client.Close()
		return explainResurrectFailure(sessionName, err)
	}
	_ = client.Close()

	fmt.Printf("Resurrected session '%s'\n", sessionName)

	// Attach to the now-live session.
	return runDaemonSession(sessionName, false)
}

// explainResurrectFailure turns a failed restore into a message that says which
// of the several reasons applies: no saved state at all, or state that exists
// but cannot be read by this build. The daemon archives unreadable state rather
// than deleting it, so the message says where it went.
func explainResurrectFailure(sessionName string, err error) error {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "was written by a newer TUIOS"):
		return &diagnosticError{
			What:  fmt.Sprintf("Session %q has saved state that this build of TUIOS cannot read.", sessionName),
			Cause: "the state was written by a newer TUIOS, so its format is not understood here.",
			Extra: []string{
				"The state file was moved out of the way so it is not retried: " + session.ResurrectionArchiveDir(),
				"Detail: " + msg,
			},
			Fix: "upgrade TUIOS to restore it, or run 'tuios new " + sessionName + "' to start fresh.",
			Err: err,
		}

	case strings.Contains(msg, "is corrupt"):
		return &diagnosticError{
			What:  fmt.Sprintf("Session %q has saved state that is corrupt and cannot be restored.", sessionName),
			Cause: "the state file was truncated or damaged, usually by an unclean shutdown or a full disk.",
			Extra: []string{
				"The damaged file was moved out of the way so it is not retried: " + session.ResurrectionArchiveDir(),
				"Detail: " + msg,
			},
			Fix: "run 'tuios new " + sessionName + "' to start a fresh session.",
			Err: err,
		}

	case strings.Contains(msg, "no resurrection data"):
		e := &diagnosticError{
			What:  fmt.Sprintf("Session %q has no saved state to restore.", sessionName),
			Cause: "the name does not match any saved session. Sessions killed with 'tuios kill-session' are removed from saved state deliberately.",
			Fix:   "run 'tuios resurrect' to list restorable sessions, or 'tuios new " + sessionName + "' to create it.",
			Err:   err,
		}
		if infos, listErr := session.ListResurrectableInfos(); listErr == nil && len(infos) > 0 {
			names := make([]string, 0, len(infos))
			for _, info := range infos {
				names = append(names, info.Name)
			}
			if closest := closestName(sessionName, names); closest != "" {
				e.Extra = append(e.Extra, fmt.Sprintf("Did you mean %q?", closest))
			}
			e.Extra = append(e.Extra, "Restorable: "+strings.Join(truncateList(names, 12), ", ")+".")
		}
		return e
	}

	return &diagnosticError{
		What:  fmt.Sprintf("Session %q could not be restored: %v.", sessionName, err),
		Cause: "the daemon refused the restore.",
		Fix:   "run 'tuios resurrect' to list restorable sessions.",
		Err:   err,
	}
}

// listResurrectableSessions prints the sessions that can be restored from saved
// state on disk.
func listResurrectableSessions() error {
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return err
	}

	// Sessions currently live in the daemon are already available via attach;
	// still list them so the user sees the full set, but mark their status.
	liveNames := make(map[string]bool)
	if session.IsDaemonRunning() {
		client := session.NewClient(&session.ClientConfig{Version: version})
		if err := client.Connect(); err == nil {
			if sessions, err := client.ListSessions(); err == nil {
				for _, s := range sessions {
					liveNames[s.Name] = true
				}
			}
			_ = client.Close()
		}
	}

	if len(infos) == 0 {
		fmt.Println("No resurrectable sessions.")
		fmt.Printf("Saved state lives in %s. Unreadable state is moved to %s.\n",
			session.ResurrectionStateDir(), session.ResurrectionArchiveDir())
		return nil
	}

	rows := make([][]string, 0, len(infos))
	for _, info := range infos {
		status := "restorable"
		if liveNames[info.Name] {
			status = "live"
		}
		saved := "-"
		if !info.SavedAt.IsZero() {
			saved = formatTimeAgo(info.SavedAt.Unix())
		}
		rows = append(rows, []string{
			info.Name,
			fmt.Sprintf("%d", info.WindowCount),
			status,
			saved,
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("NAME", "WINDOWS", "STATUS", "SAVED").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			baseStyle := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return baseStyle.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 0:
				return baseStyle.Foreground(lipgloss.Color("3")).Bold(true)
			case 2:
				if rows[row][col] == "live" {
					return baseStyle.Foreground(lipgloss.Color("10"))
				}
				return baseStyle.Foreground(lipgloss.Color("11"))
			default:
				return baseStyle.Foreground(lipgloss.Color("8"))
			}
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%d resurrectable session(s). Use 'tuios resurrect <name>' to restore.\n", len(infos))
	return nil
}

func runDaemon(foreground, disableAutoRestore bool) error {
	if session.IsDaemonRunning() {
		pid := session.GetDaemonPID()
		if pid > 0 {
			return fmt.Errorf("daemon already running (PID %d)", pid)
		}
		return fmt.Errorf("daemon already running")
	}

	if !foreground {
		return startDaemonBackground()
	}

	// The file's daemon settings first, the same way every other starter maps
	// them, then what only this command knows.
	var daemonCfg *session.DaemonConfig
	if userConfig, err := config.LoadUserConfig(); err == nil {
		daemonCfg = session.DaemonConfigFromUser(userConfig)
	} else {
		daemonCfg = session.DaemonConfigFromUser(nil)
	}
	daemonCfg.Version = version
	daemonCfg.DisableAutoRestore = disableAutoRestore
	// A terminal on stderr is what makes this daemon a foreground one for
	// logging. `tuios daemon` is also the command startDaemonBackground
	// spawns for a detached daemon, with stderr pointed at the log file, so
	// echoing there would write every line to that file twice.
	daemonCfg.Foreground = term.IsTerminal(int(os.Stderr.Fd()))

	// Install the log sinks before the daemon is built. NewDaemon already logs,
	// and the level is settled by now, so the file header names the right one.
	session.InstallDaemonLogging(daemonCfg.Foreground, daemonCfg.LogFile)

	// One log line per firing, at the level that already logs connections. It
	// is what answers "did my hook run at all". The sinks are in place above,
	// so the line reaches the ring and the file in a background daemon.
	hooks.SetVerbose(session.GetDebugLevel() >= session.DebugBasic)

	daemon := session.NewDaemon(daemonCfg)

	return daemon.Run()
}

func runKillDaemon() error {
	diag := session.DiagnoseDaemon()

	switch diag.State {
	case session.DaemonRunning:
		pid := diag.PID
		if pid == 0 {
			pid = session.GetDaemonPID()
		}
		if pid > 0 {
			if err := killDaemonProcess(pid); err != nil {
				return err
			}
			return awaitDaemonShutdown(pid, diag.SocketPath)
		}
		return &diagnosticError{
			What:  "The TUIOS daemon is running but its process id could not be determined.",
			Cause: "the pid file is missing or unreadable, which happens when the daemon was started by an older build or by another user.",
			Fix:   fmt.Sprintf("find it with 'pgrep -f \"tuios daemon\"' and stop it with 'kill <pid>', or remove %s.", diag.SocketPath),
		}

	case session.DaemonStaleSocket:
		// kill-server is the command every other message points at for this
		// state, so it has to actually clear it rather than report "not
		// running" and leave the socket in place.
		if err := os.Remove(diag.SocketPath); err != nil && !os.IsNotExist(err) {
			return &diagnosticError{
				What:  fmt.Sprintf("A stale daemon socket at %s could not be removed: %v.", diag.SocketPath, err),
				Cause: "the socket belongs to another user, or its directory is not writable.",
				Fix:   fmt.Sprintf("remove it manually with 'rm %s'.", diag.SocketPath),
				Err:   err,
			}
		}
		fmt.Printf("TUIOS daemon was not running. Removed a stale socket at %s.\n", diag.SocketPath)
		return nil

	default:
		fmt.Println("TUIOS daemon is not running.")
		return nil
	}
}

// killServerTimeout bounds how long kill-server waits for the daemon to finish
// persisting and exit. A shutdown that is going to succeed takes milliseconds;
// the slow case is Daemon.shutdown's own 5s cap on draining goroutines, so this
// leaves headroom past that before declaring the daemon wedged.
const killServerTimeout = 10 * time.Second

// awaitDaemonShutdown blocks until the signalled daemon has finished writing its
// resurrection state and removed its socket, so that kill-server returning means
// the next command can safely start a fresh daemon. Reporting success while the
// old daemon is still saving is what lets 'kill-server && start-server' race:
// the new daemon reads state the old one has not finished writing, or the old
// one's final write lands on top of the new one's.
func awaitDaemonShutdown(pid int, socketPath string) error {
	err := session.WaitForDaemonShutdown(killServerTimeout)
	if err == nil {
		fmt.Println("TUIOS daemon stopped. Session state was saved.")
		return nil
	}
	if !errors.Is(err, session.ErrShutdownTimeout) {
		return &diagnosticError{
			What:  fmt.Sprintf("Could not confirm the TUIOS daemon (PID %d) shut down: %v.", pid, err),
			Cause: "the daemon socket path could not be resolved, so there is no signal to wait on.",
			Fix:   fmt.Sprintf("check the daemon exited with 'ps -p %d', then retry.", pid),
			Err:   err,
		}
	}
	return &diagnosticError{
		What: fmt.Sprintf("The TUIOS daemon (PID %d) was asked to stop but had not finished after %s.",
			pid, killServerTimeout),
		Cause: "the daemon is wedged, or a session is taking an unusually long time to write its saved state.",
		Fix: fmt.Sprintf("wait and run 'tuios kill-server' again to re-check. If it stays stuck, force it with 'kill -9 %d' and remove %s. Force killing loses any session state that was not yet written.",
			pid, socketPath),
		Err: err,
	}
}

// killDaemonProcess is defined in platform-specific files:
// - session_commands_unix.go for Unix/Linux/macOS
// - session_commands_windows.go for Windows
