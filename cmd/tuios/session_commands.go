package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/localclient"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

var attachTerminalMode bool

var attachFocusWindow string

func runAttach(sessionName string, createIfMissing bool) error {
	if !nestedAllowed() {
		if err := session.CheckNestedAttach(sessionName); err != nil {
			nested, _ := session.AsNestedAttach(err)
			return explainNestedAttach(nested, nil)
		}
	}

	if err := checkTerminal(); err != nil {
		return err
	}

	if !session.IsDaemonRunning() {
		if reportSavedSessionsBeforeStart() == 0 && sessionName == "" {
			fmt.Println("No saved sessions to restore. Opening a new one.")
		}
		if err := ensureDaemon(); err != nil {
			return err
		}
	}

	if err := ensureAttachTarget(sessionName, createIfMissing); err != nil {
		return err
	}

	if attachFocusWindow != "" && sessionName != "" {
		focusBeforeAttach(sessionName, attachFocusWindow)
	}

	return runDaemonSession(sessionName, createIfMissing)
}

func focusBeforeAttach(sessionName, window string) {
	client, err := dialVerb()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Call("focus-window", map[string]any{
		"session": sessionName,
		"window":  window,
	}); err != nil {
		log.Printf("startup.window: cannot focus %q in session %q: %v", window, sessionName, err)
	}
}

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

var attachNotices []string

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
		return nil
	}

	names := make([]string, 0, len(sessions))
	for _, s := range sessions {
		names = append(names, s.Name)
		if s.Name != sessionName {
			continue
		}
		if s.Attached {
			attachNotices = append(attachNotices, sharedSessionNotice(sessionName, attachWindowSize(client, sessionName)))
		}
		if s.Restored {
			attachNotices = append(attachNotices, fmt.Sprintf("Session %q was %s: %s.", sessionName, session.RestoredTag, session.RestoredNote))
		}
		return nil
	}
	return explainMissingSession(sessionName, names)
}

func attachWindowSize(client *session.VerbClient, name string) string {
	raw, err := client.Call("session-info", map[string]any{"session": name})
	if err != nil {
		return ""
	}
	var res struct {
		WindowSize string `json:"window_size"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return ""
	}
	return res.WindowSize
}

func sharedSessionNotice(name, policy string) string {
	head := fmt.Sprintf("Session %q already has a client attached. TUIOS shares the session.", name)
	switch policy {
	case config.WindowSizeLargest:
		return head + " The session uses the size of the largest client (window_size largest). A smaller client shows part of it."
	case config.WindowSizeLatest:
		return head + " The session uses the size of the client that last had input (window_size latest). A smaller client shows part of it."
	default:
		return head + " The session uses the size of the smallest client (window_size smallest)."
	}
}

func listSessionInfos(client *session.VerbClient) ([]session.SessionInfo, error) {
	raw, err := client.Call("list-sessions", nil)
	if err != nil {
		return nil, err
	}
	var listed struct {
		Sessions []session.SessionInfo `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &listed); err != nil {
		return nil, err
	}
	return listed.Sessions, nil
}

var newSessionDir string

func newSessionStartDir(flag string) (string, error) {
	if flag != "" {
		return checkSessionDir(flag)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", nil
	}
	return wd, nil
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

func runNewSessionDetached(sessionName string) error {
	return newSessionDetached(sessionName, false)
}

func runNewGlobalSessionDetached(sessionName string) error {
	return newSessionDetached(sessionName, true)
}

func newSessionDetached(sessionName string, global bool) error {
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
			return errfmt.Errorf("failed to list sessions: %w", err)
		}
		existing := make([]string, len(sessions))
		for i, s := range sessions {
			existing[i] = s.Name
		}
		sessionName = generateUniqueSessionName(existing)
	}

	var err error
	if global {
		err = client.CreateGlobalSession(sessionName, 80, 24)
	} else {
		err = client.CreateDetachedSessionIn(sessionName, 80, 24, newSessionDir)
	}
	if err != nil {
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

func clientLogf(format string, args ...any) {
	if debugMode || debuglog.Enabled() {
		log.Printf(format, args...)
	}
}

func runNewSessionInSpace(sessionName, space string, detach bool) error {
	if !detach {
		if err := checkTerminal(); err != nil {
			return err
		}
	}
	if err := ensureDaemon(); err != nil {
		return err
	}
	params := map[string]any{"space": space, "window": detach}
	if sessionName != "" {
		params["name"] = sessionName
	}
	if newSessionDir != "" {
		params["cwd"] = newSessionDir
	}
	raw, err := localVerb("new-session", params, 0, false)
	if err != nil {
		return err
	}
	var res struct {
		Session string `json:"session"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil || res.Session == "" {
		return errfmt.Errorf("failed to parse the new session's name: %v: %s", err, raw)
	}

	if detach {
		fmt.Printf("Created detached session '%s' in space '%s'. Attach with 'tuios attach %s'.\n", res.Session, space, res.Session)
		return nil
	}
	return runDaemonSession(res.Session, false)
}

func runDaemonSession(sessionName string, createNew bool) error {
	return runDaemonSessionOn("", sessionName, createNew)
}

func runDaemonSessionOn(host, sessionName string, createNew bool) error {
	if err := checkTerminal(); err != nil {
		return err
	}

	var probe session.NestProbe
	if host == "" && session.NestProbeSafe(os.Getenv("TERM")) {
		probe = session.WriteNestProbe(os.Stdout)
	}

	startPprofServer()
	if err := startCPUProfile(); err != nil {
		return err
	}
	defer stopCPUProfile()

	if debugMode {
		debuglog.Enable()
		fmt.Println("Debug mode enabled")
	}

	userConfig, settings := loadAndApplyConfig()

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	clientLogf("[CLIENT] Detecting terminal capabilities...")
	hostCaps := app.GetHostCapabilities()

	clientCaps := app.ClientCapabilitiesOf(hostCaps, settings.ImageSymbols)
	clientLogf("[CLIENT] Capabilities: cell=%dx%d, kitty=%v, sixel=%v, term=%s",
		clientCaps.CellWidth, clientCaps.CellHeight, clientCaps.KittyGraphics, clientCaps.SixelGraphics, clientCaps.TerminalName)

	clientLogf("[CLIENT] Connecting to daemon...")
	client := session.NewTUIClient()
	client.AllowNested = nestedAllowed()
	client.DetachOthers = attachDetachOthers
	client.SetNestProbe(probe)
	client.SSHAuthSock = os.Getenv("SSH_AUTH_SOCK")
	client.Display = session.ClientDisplayFromEnv()
	client.UseCallerTerm()
	client.UpgradeDialog = true
	if host == "" && createNew {
		client.StartDir = newSessionDir
	}
	if host == "" && sessionName == "" && client.StartDir == "" {
		client.StartDir, _ = os.Getwd()
	}
	width, height := hostTerminalSize()

	if host == "" {
		if err := client.ConnectWithCapabilities(version, width, height, clientCaps); err != nil {
			return explainDialError(err)
		}
	} else {
		if _, err := client.ConnectThroughHost(host, version, width, height, clientCaps); err != nil {
			return explainHostConnectError(host, err)
		}
	}
	clientLogf("[CLIENT] Connected to daemon")

	if host != "" && sessionName == "" && createNew {
		sessionName = generateUniqueSessionName(client.AvailableSessionNames())
	}

	clientLogf("[CLIENT] Attaching to session '%s' (createNew=%v)", sessionName, createNew)
	state, err := client.AttachSession(sessionName, createNew, width, height)
	if err != nil {
		names := client.AvailableSessionNames()
		_ = client.Close()
		if nested, ok := session.AsNestedAttach(err); ok {
			return explainNestedAttach(nested, names)
		}
		if host != "" {
			return explainMissingHostSession(host, sessionName, names, err)
		}
		if current, ok := session.RenamedSessionTarget(err); ok {
			return &diagnosticError{
				What:  fmt.Sprintf("Session %q was renamed to %q.", sessionName, current),
				Cause: "the session has a new name, and attach takes the new name only.",
				Fix:   fmt.Sprintf("run 'tuios attach %s'.", current),
				Err:   err,
			}
		}
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
	clientLogf("[CLIENT] Attached to session, got state")
	for _, notice := range attachNotices {
		fmt.Println(notice)
	}
	attachNotices = nil

	clientLogf("[CLIENT] Starting read loop")
	client.StartReadLoop()

	c := localclient.New(localclient.Stdio(), app.OSOptions{
		Client:              app.ClientLocal,
		KeybindRegistry:     keybindRegistry,
		UserConfig:          userConfig,
		ShowKeys:            interfaceFlags.ShowKeys,
		Overrides:           flagOverrides(),
		Settings:            settings,
		Width:               width,
		Height:              height,
		IsDaemonSession:     true,
		DaemonClient:        client,
		SessionName:         client.SessionName(),
		StartInTerminalMode: host == "" && attachTerminalMode,
		AfterRestart:        host == "" && attachAfterRestart,
		AfterUpgrade:        afterUpgradeFor(host),
		FollowUpgrades:      host == "",
		AttachedHost:        host,
	}, state)

	runReturned, finish := armSignalQuit(c.Program)

	restoreTabs := withoutHardTabs()
	finalModel, err := c.Run()
	runReturned()
	restoreTabs()
	c.Close()

	reason := app.ExitNormal
	exitSession := sessionName
	killed := false
	exitHost := host
	exitClient := client
	var restartExec *app.RestartExec
	var restartFail *app.RestartFailure
	var upgradeExec *app.UpgradeExec
	if finalOS, ok := finalModel.(*app.OS); ok {
		reason = finalOS.ExitReason
		restartExec, restartFail, upgradeExec = finalOS.RestartExec, finalOS.RestartFailure, finalOS.UpgradeExec
		if finalOS.DaemonClient != nil {
			exitClient = finalOS.DaemonClient
		}
		if name := finalOS.SessionName; name != "" {
			exitSession = name
		}
		exitHost = finalOS.AttachedHost
		killed = finalOS.QuitRequested
		if reason == app.ExitNormal && !killed {
			finalOS.SyncStateToDaemon()
		}
		c.Finish(finalOS)
	}

	_ = client.Close()

	terminal.ResetTerminal()
	finish()

	if err != nil {
		return errfmt.Errorf("program error: %w", err)
	}

	if reason == app.ExitRestarted && restartExec != nil {
		return reattachAfterRestart(restartExec)
	}
	if reason == app.ExitRestartFailed && restartFail != nil {
		return appRestartFailure(restartFail)
	}
	if reason == app.ExitUpgradeFollowed && upgradeExec != nil {
		return followUpgrade(upgradeExec)
	}

	if reason == app.ExitNestedRefused {
		return &diagnosticError{What: client.NestedRefusal()}
	}

	if reason == app.ExitDetached {
		fmt.Println(exitClient.DetachedReason())
		where := ""
		if exitHost != "" {
			where = " on " + exitHost
		}
		fmt.Printf("Detached from session '%s'%s.\n", exitSession, where)
		return nil
	}

	if reason == app.ExitDaemonUpgraded {
		to, _ := exitClient.UpgradedTo()
		if to == "" {
			to = "a new version"
		}
		fmt.Printf("The daemon is being upgraded to %s. Your programs are still running. Run 'tuios attach %s' to continue.\n", to, exitSession)
		return nil
	}

	return reportSessionExit(exitSession, exitHost, reason, killed)
}

func reportSessionExit(sessionName, host string, reason app.ExitReason, killed bool) error {
	switch reason {
	case app.ExitHostLost:
		return &diagnosticError{
			What:  fmt.Sprintf("The link to %s closed and did not come back.", host),
			Cause: "ssh to the host dropped, or its daemon stopped. tuios dialed again and stopped. The session keeps running on " + host + ".",
			Fix:   "run 'tuios hosts' to see the link, then 'tuios attach --host " + host + " " + sessionName + "' to attach again.",
		}

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
		where := ""
		if host != "" {
			where = " on " + host
		}
		if killed {
			fmt.Printf("Killed session '%s'%s.\n", sessionName, where)
		} else {
			fmt.Printf("Detached from session '%s'%s.\n", sessionName, where)
		}
		return nil
	}
}

type lsEntry struct {
	session.SessionInfo
	Saved      bool   `json:"saved,omitempty"`
	Server     string `json:"server,omitempty"`
	AgentTitle string `json:"agent_title,omitempty"`
}

const lsAgentTitleCells = 40

func lsAgentTitle(s session.SessionInfo) string {
	if s.DisplayName != "" || !session.IsGeneratedSessionName(s.Name) {
		return ""
	}
	return session.SessionAgentTitle(s.Windows)
}

func printServerLine(socketPath string) {
	if name := serverscope.Name(); name != "" {
		fmt.Printf("Server: %s (%s)\n", name, socketPath)
	}
}

func runListClients(jsonOutput bool) error {
	raw, err := localVerb("list-clients", nil, 0, false)
	if err != nil {
		return err
	}
	var listed struct {
		Clients []session.ClientInfo `json:"clients"`
	}
	if err := jsonx.Unmarshal(raw, &listed); err != nil {
		return errfmt.Errorf("failed to parse clients: %w", err)
	}
	if jsonOutput {
		return printJSON(listed.Clients)
	}

	rows := make([][]string, 0, len(listed.Clients))
	for _, c := range listed.Clients {
		rows = append(rows, []string{c.ClientID, fmt.Sprintf("%d", c.PID), c.Session})
	}
	fmt.Println(table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("CLIENT ID", "PID", "SESSION").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			baseStyle := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return baseStyle.Bold(true).Foreground(lipgloss.Color("12"))
			}
			if col == 2 {
				return baseStyle.Foreground(lipgloss.Color("3")).Bold(true)
			}
			return baseStyle.Foreground(lipgloss.Color("8"))
		}).Render())
	fmt.Printf("\n%s\n", plural.Count(len(rows), "client"))
	return nil
}

func runListSessions(jsonOutput bool) error {
	diag := session.DiagnoseDaemon()
	if !diag.Running() {
		return listSavedSessions(diag, jsonOutput)
	}

	raw, err := localVerb("list-sessions", nil, 0, false)
	if err != nil {
		return err
	}
	var listed struct {
		Sessions []session.SessionInfo `json:"sessions"`
	}
	if err := jsonx.Unmarshal(raw, &listed); err != nil {
		return errfmt.Errorf("failed to parse sessions: %w", err)
	}
	sessions := listed.Sessions

	if jsonOutput {
		entries := make([]lsEntry, 0, len(sessions))
		for _, s := range sessions {
			entries = append(entries, lsEntry{SessionInfo: s, Server: serverscope.Name(), AgentTitle: lsAgentTitle(s)})
		}
		return printJSON(entries)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions. Create one with 'tuios new', or run 'tuios resurrect' to see saved sessions.")
		printServerLine(diag.SocketPath)
		return nil
	}

	rowsBySpace := map[string][][]string{}
	anyRestored := false
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		if s.Restored {
			status = session.RestoredTag
			anyRestored = true
		}

		name := s.Name
		if s.DisplayName != "" && s.DisplayName != s.Name {
			name = fmt.Sprintf("%s (%s)", s.Name, s.DisplayName)
		} else if t := lsAgentTitle(s); t != "" {
			name = fmt.Sprintf("%s (%s)", s.Name, ansi.Truncate(t, lsAgentTitleCells, "..."))
		}
		rowsBySpace[s.Space] = append(rowsBySpace[s.Space], []string{
			name,
			fmt.Sprintf("%d", s.WindowCount),
			status,
			formatTimeAgo(s.Created),
			formatTimeAgo(s.LastActive),
		})
	}

	fmt.Println(renderSpaceGroups(rowsBySpace))
	fmt.Printf("\n%s\n", plural.Count(len(sessions), "session"))
	if anyRestored {
		fmt.Printf("%s: %s.\n", session.RestoredTag, session.RestoredNote)
	}
	printServerLine(diag.SocketPath)
	return nil
}

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
					Space:       info.Space,
				},
				Saved:  true,
				Server: serverscope.Name(),
			})
		}
		if err := printJSON(entries); err != nil {
			return err
		}
		return &statusError{code: noDaemonStatus}
	}

	if len(infos) > 0 {
		rowsBySpace := map[string][][]string{}
		for _, info := range infos {
			rowsBySpace[info.Space] = append(rowsBySpace[info.Space], []string{
				info.Name,
				fmt.Sprintf("%d", info.WindowCount),
				session.SavedTag,
				"-",
				formatTimeAgo(savedUnix(info.SavedAt)),
			})
		}
		fmt.Println(renderSpaceGroups(rowsBySpace))
		fmt.Printf("\n%s\n", plural.Count(len(infos), "session"))
		fmt.Printf("%s: %s.\n\n", session.SavedTag, session.SavedNote)
	}

	printServerLine(diag.SocketPath)
	fmt.Println(diag.Explain())
	return &statusError{code: noDaemonStatus}
}

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

func renderSpaceGroups(rowsBySpace map[string][][]string) string {
	spaces := make([]string, 0, len(rowsBySpace))
	for space := range rowsBySpace {
		if space != "" {
			spaces = append(spaces, space)
		}
	}
	slices.Sort(spaces)

	heading := lipgloss.NewStyle().Bold(true)
	groups := make([]string, 0, len(rowsBySpace))
	for _, space := range spaces {
		groups = append(groups, heading.Render(space)+"\n"+renderSessionTable(rowsBySpace[space]))
	}
	if loose, ok := rowsBySpace[""]; ok {
		groups = append(groups, renderSessionTable(loose))
	}
	return strings.Join(groups, "\n\n")
}

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
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	if _, err := t.client.Call("kill-session", t.params(nil)); err != nil {
		return t.explain("kill-session", err)
	}

	fmt.Printf("Killed session '%s'%s.\n", t.session, t.on())
	return nil
}

func runResurrect(sessionName string, asJSON bool) error {
	if sessionName == "" {
		return listResurrectableSessions(asJSON)
	}
	if asJSON {
		return errors.New("--json lists the saved sessions. Run 'tuios resurrect --json' with no session name")
	}

	if err := ensureDaemon(); err != nil {
		return err
	}

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

	return runDaemonSession(sessionName, false)
}

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
			if closest := session.ClosestMatch(sessionName, names); closest != "" {
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

func listResurrectableSessions(asJSON bool) error {
	infos, err := session.ListResurrectableInfos()
	if err != nil {
		return err
	}

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

	if asJSON {
		type savedRow struct {
			Name    string `json:"name"`
			Windows int    `json:"windows"`
			Status  string `json:"status"`
			SavedAt int64  `json:"saved_at"`
		}
		rows := make([]savedRow, 0, len(infos))
		for _, info := range infos {
			status := "restorable"
			if liveNames[info.Name] {
				status = "live"
			}
			rows = append(rows, savedRow{Name: info.Name, Windows: info.WindowCount, Status: status, SavedAt: savedUnix(info.SavedAt)})
		}
		return printJSON(rows)
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
	fmt.Printf("\n%s. Use 'tuios resurrect <name>' to restore.\n", plural.Count(len(infos), "resurrectable session"))
	return nil
}

func runDaemon(foreground, disableAutoRestore bool) error {
	if err := session.CheckSocketEnv(); err != nil {
		return err
	}
	if session.IsDaemonRunning() {
		pid := session.GetDaemonPID()
		if pid > 0 {
			return errfmt.Errorf("a daemon is already running (PID %d). Stop it with 'tuios kill-server' first, or use it as it is", pid)
		}
		return errfmt.Errorf("a daemon is already running. Stop it with 'tuios kill-server' first, or use it as it is")
	}

	if !foreground {
		if pprofAddr != "" {
			return startDaemonBackground("--pprof", pprofAddr)
		}
		return startDaemonBackground()
	}

	daemonCfg := foregroundDaemonConfig()
	daemonCfg.DisableAutoRestore = disableAutoRestore
	daemon := session.NewDaemon(daemonCfg)

	err := daemon.Run()
	session.RemoveTempFolders()
	return err
}

func foregroundDaemonConfig() *session.DaemonConfig {
	var daemonCfg *session.DaemonConfig
	if userConfig, err := config.LoadUserConfig(); err == nil {
		daemonCfg = session.DaemonConfigFromUser(userConfig)
	} else {
		daemonCfg = session.DaemonConfigFromUser(nil)
	}
	daemonCfg.Version = version
	daemonCfg.HandoffArgs = func(fd int) []string {
		stopCPUProfile()
		return handoffArgs(fd)
	}
	daemonCfg.Foreground = term.IsTerminal(int(os.Stderr.Fd()))

	session.InstallDaemonLogging(daemonCfg.Foreground, daemonCfg.LogFile)

	hooks.SetVerbose(session.GetDebugLevel() >= session.DebugBasic)
	return daemonCfg
}

func runKillDaemon(restart bool) error {
	diag := session.DiagnoseDaemon()
	if restart && diag.State == session.DaemonRunning {
		if err := session.MarkRestart(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: the clients will not wait for the new daemon: %v\n", err)
		}
	}
	printServerLine(diag.SocketPath)

	switch diag.State {
	case session.DaemonRunning:
		pid := diag.PID
		if pid == 0 {
			pid = session.GetDaemonPID()
		}
		if pid > 0 {
			return stopDaemon(pid, diag.SocketPath)
		}
		return &diagnosticError{
			What:  "The TUIOS daemon is running but its process id could not be determined.",
			Cause: "the pid file is missing or unreadable, which happens when the daemon was started by an older build or by another user.",
			Fix:   fmt.Sprintf("find it with 'pgrep -f \"tuios daemon\"' and stop it with 'kill <pid>', or remove %s.", diag.SocketPath),
		}

	case session.DaemonStaleSocket:
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

const killServerTimeout = 10 * time.Second

func awaitDaemonShutdown(pid int, socketPath string) error {
	err := session.WaitForDaemonShutdown(socketPath, killServerTimeout)
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
		Fix: fmt.Sprintf("wait and run 'tuios kill-server' again to re-check. If it stays stuck, force it with '%s' and remove %s. Force killing loses any session state that was not yet written.",
			forceKillCommand(pid), socketPath),
		Err: err,
	}
}
