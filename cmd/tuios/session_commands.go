package main

import (
	"encoding/json"
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
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

func runAttach(sessionName string, createIfMissing bool) error {
	// Check the terminal before anything else: a session that cannot be
	// rendered is much harder to diagnose once the TUI has taken the screen.
	if err := checkTerminal(); err != nil {
		return err
	}

	diag := session.DiagnoseDaemon()
	if !diag.Running() {
		if !createIfMissing {
			return explainAttachWithoutDaemon(sessionName, diag)
		}
		fmt.Println("Starting TUIOS daemon...")
		if err := startDaemonBackground(); err != nil {
			return &diagnosticError{
				What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
				Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
				Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
				Err:   err,
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err := ensureAttachTarget(sessionName, createIfMissing); err != nil {
		return err
	}

	return runDaemonSession(sessionName, createIfMissing)
}

// explainAttachWithoutDaemon reports that attach found no daemon, and adds the
// one thing a user in that state most wants to know: whether the session they
// asked for is saved and can be brought back.
func explainAttachWithoutDaemon(sessionName string, diag session.DaemonDiagnosis) error {
	e := &diagnosticError{What: diag.Explain(), Err: diag.Err}

	if sessionName == "" {
		return e
	}
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return e
	}
	for _, info := range infos {
		if info.Name == sessionName {
			e.Extra = append(e.Extra, fmt.Sprintf("Session %q has saved state (%d window(s)) and can be restored.", sessionName, info.WindowCount))
			e.Fix = fmt.Sprintf("run 'tuios resurrect %s' to restore it and attach.", sessionName)
			return e
		}
	}
	return e
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
			fmt.Printf("Session %q already has a client attached; TUIOS shares it between clients and renders at the smallest client's size.\n", sessionName)
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
	if !session.IsDaemonRunning() {
		fmt.Println("Starting TUIOS daemon...")
		if err := startDaemonBackground(); err != nil {
			return &diagnosticError{
				What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
				Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
				Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
				Err:   err,
			}
		}
		time.Sleep(500 * time.Millisecond)
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
	if !session.IsDaemonRunning() {
		fmt.Println("Starting TUIOS daemon...")
		if err := startDaemonBackground(); err != nil {
			return &diagnosticError{
				What:  fmt.Sprintf("The TUIOS daemon could not be started: %v.", err),
				Cause: "the tuios binary could not be re-executed, or the socket directory is not writable.",
				Fix:   "run 'tuios daemon' in another terminal to see why it fails to start.",
				Err:   err,
			}
		}
		time.Sleep(500 * time.Millisecond)
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

	if debugMode {
		_ = os.Setenv("TUIOS_DEBUG_INTERNAL", "1")
		fmt.Println("Debug mode enabled")
	}

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	// Apply the config appearance globals as the baseline before CLI flags win.
	// LoadUserConfig no longer applies globals itself.
	config.ApplyAppearanceConfig(userConfig)

	config.ApplyOverrides(config.Overrides{
		ASCIIOnly:           asciiOnly,
		BorderStyle:         borderStyle,
		DockbarPosition:     dockbarPosition,
		HideWindowButtons:   hideWindowButtons,
		HideScrollbar:       hideScrollbar,
		WindowTitlePosition: windowTitlePosition,
		HideClock:           hideClock,
		ShowClock:           showClock,
		ShowCPU:             showCPU,
		ShowRAM:             showRAM,
		SharedBorders:       sharedBorders,
		ZoomMaxWidth:        zoomMaxWidth,
		ScrollbackLines:     scrollbackLines,
		NoAnimations:        noAnimations,
		ThemeName:           themeName,
	}, userConfig)

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	log.Printf("[CLIENT] Detecting terminal capabilities...")
	hostCaps := app.GetHostCapabilities()

	// Build client capabilities from detected host capabilities
	clientCaps := &session.ClientCapabilities{
		PixelWidth:    hostCaps.PixelWidth,
		PixelHeight:   hostCaps.PixelHeight,
		CellWidth:     hostCaps.CellWidth,
		CellHeight:    hostCaps.CellHeight,
		KittyGraphics: hostCaps.KittyGraphics,
		SixelGraphics: hostCaps.SixelGraphics,
		TerminalName:  hostCaps.TerminalName,
	}
	log.Printf("[CLIENT] Capabilities: cell=%dx%d, kitty=%v, sixel=%v, term=%s",
		clientCaps.CellWidth, clientCaps.CellHeight, clientCaps.KittyGraphics, clientCaps.SixelGraphics, clientCaps.TerminalName)

	log.Printf("[CLIENT] Connecting to daemon...")
	client := session.NewTUIClient()
	width, height := 80, 24

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
		KeybindRegistry:           keybindRegistry,
		UserConfig:                userConfig,
		ShowKeys:                  showKeys,
		IsDaemonSession:           true,
		DaemonClient:              client,
		SessionName:               client.SessionName(),
		EnableGraphicsPassthrough: true,
	})
	initialOS.PostRenderWriter = prw

	windowCount := 0
	if state != nil {
		windowCount = len(state.Windows)
	}
	log.Printf("[CLIENT] State from daemon: %v, windows: %d", state != nil, windowCount)
	if state != nil && len(state.Windows) > 0 {
		log.Printf("[CLIENT] Restoring %d windows from session state", len(state.Windows))
		if err := initialOS.RestoreFromState(state); err != nil {
			log.Printf("Warning: Failed to restore session state: %v", err)
		}
		log.Printf("[CLIENT] RestoreFromState complete")

		log.Printf("[CLIENT] Restoring terminal states from daemon")
		if err := initialOS.RestoreTerminalStates(); err != nil {
			log.Printf("Warning: Failed to restore terminal states: %v", err)
		}

		log.Printf("[CLIENT] Setting up PTY output handlers")
		if err := initialOS.SetupPTYOutputHandlers(); err != nil {
			log.Printf("Warning: Failed to set up PTY handlers: %v", err)
		}

		// Re-tile to set correct dimensions for current screen size
		if initialOS.AutoTiling {
			log.Printf("[CLIENT] Re-tiling windows for current screen")
			initialOS.TileAllWindows()
		}

		// Sync daemon PTY dimensions to match tiled layout
		log.Printf("[CLIENT] Syncing daemon PTY dimensions")
		initialOS.SyncDaemonPTYDimensions()

		log.Printf("[CLIENT] Restore complete, %d windows in OS", len(initialOS.Windows))
	} else {
		log.Printf("[CLIENT] No existing state to restore")
	}

	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.MaxFPSCap),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filterMouseMotion),
		tea.WithOutput(prw),
	)

	// Set up remote command handler for CLI-initiated commands
	// This handler sends messages to the Bubble Tea program which processes them in the main loop
	log.Printf("[CLIENT] Setting up remote command handler")
	client.OnRemoteCommand(func(payload *session.RemoteCommandPayload) error {
		// Send a Bubble Tea message to handle the command in the main loop
		// Use Send which is safe to call from any goroutine
		go func() {
			p.Send(app.RemoteCommandMsg{
				CommandType:  payload.CommandType,
				TapeCommand:  payload.TapeCommand,
				TapeArgs:     payload.TapeArgs,
				TapeScript:   payload.TapeScript,
				Keys:         payload.Keys,
				Literal:      payload.Literal,
				Raw:          payload.Raw,
				WindowTarget: payload.WindowTarget,
				ConfigPath:   payload.ConfigPath,
				ConfigValue:  payload.ConfigValue,
				RequestID:    payload.RequestID,
			})
		}()
		return nil // Don't report error here - it will be handled by the Update loop
	})

	// Set up multi-client handlers for state sync, join/leave notifications, and resize
	log.Printf("[CLIENT] Setting up multi-client handlers")

	// Handle state sync from other clients
	client.OnStateSync(func(state *session.SessionState, triggerType, sourceID string) {
		// Send a message to apply state in the main loop
		go func() {
			p.Send(app.StateSyncMsg{State: state, TriggerType: triggerType, SourceID: sourceID})
		}()
	})

	// Handle client join notifications
	client.OnClientJoined(func(clientID string, clientCount int, width, height int) {
		go func() {
			p.Send(app.ClientJoinedMsg{ClientID: clientID, ClientCount: clientCount, Width: width, Height: height})
		}()
	})

	// Handle client leave notifications
	client.OnClientLeft(func(clientID string, clientCount int) {
		go func() {
			p.Send(app.ClientLeftMsg{ClientID: clientID, ClientCount: clientCount})
		}()
	})

	// Handle session resize (min of all clients)
	client.OnSessionResize(func(width, height, clientCount int) {
		go func() {
			p.Send(app.SessionResizeMsg{Width: width, Height: height, ClientCount: clientCount})
		}()
	})

	// Handle force refresh
	client.OnForceRefresh(func(reason string) {
		go func() {
			p.Send(app.ForceRefreshMsg{Reason: reason})
		}()
	})

	// Handle unexpected daemon disconnect (crash/reset/desync): quit cleanly
	// instead of leaving the TUI frozen.
	client.OnDisconnect(func(err error) {
		go func() {
			p.Send(app.DaemonDisconnectedMsg{Err: err})
		}()
	})

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.SyncStateToDaemon()
		finalOS.Cleanup()
	}

	_ = client.Close()

	terminal.ResetTerminal()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	fmt.Printf("[detached from session '%s']\n", sessionName)
	return nil
}

func runListSessions(jsonOutput bool) error {
	diag := session.DiagnoseDaemon()
	if !diag.Running() {
		if jsonOutput {
			fmt.Println("[]")
		} else {
			fmt.Println(diag.Explain())
		}
		return nil
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
		data, err := json.MarshalIndent(sessions, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions. Create one with 'tuios new', or run 'tuios resurrect' to see saved sessions.")
		return nil
	}

	rows := make([][]string, 0, len(sessions))
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}

		rows = append(rows, []string{
			s.Name,
			fmt.Sprintf("%d", s.WindowCount),
			status,
			formatTimeAgo(s.Created),
			formatTimeAgo(s.LastActive),
		})
	}

	t := table.New().
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
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%d session(s)\n", len(sessions))
	return nil
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

	fmt.Printf("Killed session: %s\n", sessionName)
	return nil
}

// runResurrect lists resurrectable sessions (no name) or restores one on demand
// and attaches to it (name given).
func runResurrect(sessionName string) error {
	if sessionName == "" {
		return listResurrectableSessions()
	}

	// Ensure the daemon is running so it can hold the restored session.
	if !session.IsDaemonRunning() {
		fmt.Println("Starting TUIOS daemon...")
		if err := startDaemonBackground(); err != nil {
			return fmt.Errorf("failed to start daemon: %w", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Ask the daemon to restore the session from saved state. This is a no-op if
	// the daemon already auto-restored it on start.
	client := session.NewClient(&session.ClientConfig{Version: version})
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	if err := client.ResurrectSession(sessionName); err != nil {
		_ = client.Close()
		return err
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
		fmt.Printf("Saved state lives in %s; unreadable state is moved to %s.\n",
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

	if session.GetDebugLevel() == session.DebugOff {
		userConfig, err := config.LoadUserConfig()
		if err == nil && userConfig.Daemon.LogLevel != "" {
			session.SetDebugLevel(session.ParseDebugLevel(userConfig.Daemon.LogLevel))
		}
	}

	daemon := session.NewDaemon(&session.DaemonConfig{
		Version:            version,
		DisableAutoRestore: disableAutoRestore,
	})

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
			return killDaemonProcess(pid)
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

// startDaemonBackground is defined in platform-specific files:
// - session_commands_unix.go for Unix/Linux/macOS
// - session_commands_windows.go for Windows
