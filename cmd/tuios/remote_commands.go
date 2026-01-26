package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// runSendKeys sends keystrokes to a running TUIOS session.
func runSendKeys(sessionName, keys string, literal bool, raw bool) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the keys command
	msg, err := session.NewMessage(session.MsgSendKeys, &session.SendKeysPayload{
		SessionName: sessionName,
		Keys:        keys,
		Literal:     literal,
		Raw:         raw,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResult(client, msg, requestID); err != nil {
		return err
	}

	return nil
}

// runCommand executes a tape command in a running TUIOS session.
func runCommand(sessionName, command string, args []string, jsonOutput bool) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the execute command
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		CommandType: command,
		Args:        args,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResultWithFormat(client, msg, requestID, jsonOutput); err != nil {
		return err
	}

	return nil
}

// queryWindows queries window list directly from daemon (doesn't require TUI).
func queryWindows(sessionName string, jsonOutput bool) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	msg, err := session.NewMessage(session.MsgQueryWindows, &session.QueryWindowsPayload{
		SessionName: sessionName,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResultWithFormat(client, msg, requestID, jsonOutput); err != nil {
		return err
	}

	return nil
}

// querySession queries session info directly from daemon (doesn't require TUI).
func querySession(sessionName string, jsonOutput bool) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	msg, err := session.NewMessage(session.MsgQuerySession, &session.QuerySessionPayload{
		SessionName: sessionName,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResultWithFormat(client, msg, requestID, jsonOutput); err != nil {
		return err
	}

	return nil
}

// runSetConfig sets a configuration option in a running TUIOS session.
func runSetConfig(sessionName, path, value string) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the set config command
	msg, err := session.NewMessage(session.MsgSetConfig, &session.SetConfigPayload{
		SessionName: sessionName,
		Path:        path,
		Value:       value,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResult(client, msg, requestID); err != nil {
		return err
	}

	return nil
}

// runTapeExec executes a tape file in a running TUIOS session.
func runTapeExec(sessionName, filePath string) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running. Start a session first with 'tuios new'")
	}

	// Read the tape file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}
	script := string(content)

	// Validate the script first
	lexer := tape.New(script)
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	if len(commands) == 0 {
		return fmt.Errorf("tape script has no commands or contains errors")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the execute command with tape script
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		TapeScript:  script,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResult(client, msg, requestID); err != nil {
		return err
	}

	return nil
}

// sendAndWaitForResult sends a message and waits for the result (human-readable output).
func sendAndWaitForResult(client *session.Client, msg *session.Message, requestID string) error {
	return sendAndWaitForResultWithFormat(client, msg, requestID, false)
}

// sendAndWaitForResultWithFormat sends a message and waits for the result.
// If jsonOutput is true, outputs machine-readable JSON.
func sendAndWaitForResultWithFormat(client *session.Client, msg *session.Message, requestID string, jsonOutput bool) error {
	resp, err := client.SendControlMessage(msg)
	if err != nil {
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to send command: %v", err),
			})
			return nil // Don't return error, we already output JSON
		}
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Check response type
	switch resp.Type {
	case session.MsgCommandResult:
		var result session.CommandResultPayload
		if err := resp.ParsePayloadWithCodec(&result, client.GetCodec()); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   fmt.Sprintf("failed to parse response: %v", err),
				})
				return nil
			}
			return fmt.Errorf("failed to parse response: %w", err)
		}
		if jsonOutput {
			output := map[string]any{
				"success": result.Success,
				"message": result.Message,
			}
			// Merge any additional data from the result
			maps.Copy(output, result.Data)
			outputJSON(output)
			if !result.Success {
				os.Exit(1)
			}
			return nil
		}
		if !result.Success {
			return fmt.Errorf("command failed: %s", result.Message)
		}
		fmt.Printf("Command executed successfully: %s\n", result.Message)
		return nil

	case session.MsgError:
		var errPayload session.ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, client.GetCodec()); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   "command failed with unknown error",
				})
				return nil
			}
			return fmt.Errorf("command failed with unknown error")
		}
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   errPayload.Message,
			})
			os.Exit(1)
			return nil
		}
		return fmt.Errorf("command failed: %s", errPayload.Message)

	default:
		// Command was sent, we got some response
		if jsonOutput {
			outputJSON(map[string]any{
				"success":    true,
				"request_id": requestID[:8],
			})
			return nil
		}
		fmt.Printf("Command sent (request ID: %s)\n", requestID[:8])
		return nil
	}
}

// outputJSON outputs a value as JSON to stdout.
func outputJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// listAvailableCommands lists all available tape commands that can be executed remotely.
func listAvailableCommands() {
	commands := []struct {
		name        string
		description string
		example     string
	}{
		// Window management
		{"NewWindow [name]", "Create a new terminal window", "tuios run-command NewWindow \"My Terminal\""},
		{"CloseWindow [name]", "Close window(s) - all matching if name given", "tuios run-command CloseWindow \"Build\""},
		{"NextWindow", "Focus the next window", "tuios run-command NextWindow"},
		{"PrevWindow", "Focus the previous window", "tuios run-command PrevWindow"},
		{"FocusWindow <name>", "Focus a window by name", "tuios run-command FocusWindow \"Server\""},
		{"RenameWindow <name> | <old> <new>", "Rename focused or named window", "tuios run-command RenameWindow \"Old\" \"New\""},
		{"MinimizeWindow [name]", "Minimize focused or named window", "tuios run-command MinimizeWindow \"Server\""},
		{"RestoreWindow [name]", "Restore focused or named window", "tuios run-command RestoreWindow \"Server\""},

		// Mode switching
		{"TerminalMode", "Switch to terminal mode", "tuios run-command TerminalMode"},
		{"WindowManagementMode", "Switch to window management mode", "tuios run-command WindowManagementMode"},

		// Tiling
		{"ToggleTiling", "Toggle tiling mode", "tuios run-command ToggleTiling"},
		{"EnableTiling", "Enable tiling mode", "tuios run-command EnableTiling"},
		{"DisableTiling", "Disable tiling mode", "tuios run-command DisableTiling"},
		{"SnapLeft", "Snap focused window to left", "tuios run-command SnapLeft"},
		{"SnapRight", "Snap focused window to right", "tuios run-command SnapRight"},
		{"SnapFullscreen", "Snap focused window to fullscreen", "tuios run-command SnapFullscreen"},

		// BSP Tiling
		{"Split horizontal", "Split focused window horizontally", "tuios run-command Split horizontal"},
		{"Split vertical", "Split focused window vertically", "tuios run-command Split vertical"},
		{"RotateSplit", "Rotate the split direction", "tuios run-command RotateSplit"},
		{"EqualizeSplits", "Equalize all split ratios", "tuios run-command EqualizeSplits"},

		// Workspace
		{"SwitchWorkspace 1-9", "Switch to workspace N", "tuios run-command SwitchWorkspace 2"},
		{"MoveToWorkspace 1-9", "Move focused window to workspace N", "tuios run-command MoveToWorkspace 3"},

		// Animations
		{"EnableAnimations", "Enable UI animations", "tuios run-command EnableAnimations"},
		{"DisableAnimations", "Disable UI animations", "tuios run-command DisableAnimations"},
		{"ToggleAnimations", "Toggle UI animations", "tuios run-command ToggleAnimations"},

		// Config commands
		{"SetDockbarPosition top|bottom|left|right", "Change dockbar position", "tuios run-command SetDockbarPosition top"},
		{"SetBorderStyle style", "Change window border style", "tuios run-command SetBorderStyle rounded"},
		{"SetTheme themename", "Change the color theme", "tuios run-command SetTheme dracula"},
		{"ShowNotification message [type]", "Show a notification", "tuios run-command ShowNotification \"Hello!\" info"},

		// Inspection commands
		{"ListWindows", "List all windows (use --json)", "tuios list-windows --json"},
		{"GetWindow [id-or-name]", "Get window info (use --json)", "tuios get-window --json"},
		{"GetSessionInfo", "Get session info (use --json)", "tuios session-info --json"},
	}

	fmt.Println("Available commands for 'tuios run-command':")
	fmt.Println()

	for _, cmd := range commands {
		fmt.Printf("  %-35s %s\n", cmd.name, cmd.description)
	}

	fmt.Println()
	fmt.Println("Examples:")
	for _, cmd := range commands {
		if cmd.example != "" {
			fmt.Printf("  %s\n", cmd.example)
		}
	}
}

// Completion functions for shell autocompletion

// getSendKeysCompletions returns completions for send-keys key names.
func getSendKeysCompletions(toComplete string) []string {
	keys := []string{
		// Special tokens
		"$PREFIX\tConfigured leader/prefix key",
		"PREFIX\tConfigured leader/prefix key",
		// Special keys
		"Enter\tPress Enter/Return",
		"Return\tPress Enter/Return",
		"Space\tPress Space",
		"Tab\tPress Tab",
		"Escape\tPress Escape",
		"Esc\tPress Escape",
		"Backspace\tPress Backspace",
		"Delete\tPress Delete",
		// Arrow keys
		"Up\tPress Up arrow",
		"Down\tPress Down arrow",
		"Left\tPress Left arrow",
		"Right\tPress Right arrow",
		// Navigation
		"Home\tPress Home",
		"End\tPress End",
		"PageUp\tPress Page Up",
		"PageDown\tPress Page Down",
		"Insert\tPress Insert",
		// Function keys
		"F1\tPress F1",
		"F2\tPress F2",
		"F3\tPress F3",
		"F4\tPress F4",
		"F5\tPress F5",
		"F6\tPress F6",
		"F7\tPress F7",
		"F8\tPress F8",
		"F9\tPress F9",
		"F10\tPress F10",
		"F11\tPress F11",
		"F12\tPress F12",
		// Common key combos
		"ctrl+b\tPrefix key (default)",
		"ctrl+c\tInterrupt/cancel",
		"ctrl+d\tEOF/logout",
		"ctrl+z\tSuspend",
		"alt+1\tWorkspace 1",
		"alt+2\tWorkspace 2",
		"alt+3\tWorkspace 3",
		// Mode keys
		"i\tEnter terminal mode",
		"n\tNew window (in window mode)",
		"q\tQuit (in window mode)",
		"h\tMove left",
		"j\tMove down",
		"k\tMove up",
		"l\tMove right",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, key := range keys {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(key), toComplete) {
			filtered = append(filtered, key)
		}
	}
	return filtered
}

// getRunCommandCompletions returns completions for run-command command names.
func getRunCommandCompletions(toComplete string) []string {
	commands := []string{
		"NewWindow\tCreate a new terminal window",
		"CloseWindow\tClose the focused window",
		"NextWindow\tFocus the next window",
		"PrevWindow\tFocus the previous window",
		"RenameWindow\tRename the focused window",
		"MinimizeWindow\tMinimize the focused window",
		"RestoreWindow\tRestore the focused window",
		"TerminalMode\tSwitch to terminal mode",
		"WindowManagementMode\tSwitch to window management mode",
		"ToggleTiling\tToggle tiling mode",
		"EnableTiling\tEnable tiling mode",
		"DisableTiling\tDisable tiling mode",
		"SnapLeft\tSnap window to left",
		"SnapRight\tSnap window to right",
		"SnapFullscreen\tSnap window fullscreen",
		"Split\tSplit window (horizontal/vertical)",
		"RotateSplit\tRotate split direction",
		"EqualizeSplits\tEqualize all splits",
		"SwitchWorkspace\tSwitch to workspace N",
		"MoveToWorkspace\tMove window to workspace N",
		"MoveAndFollowWorkspace\tMove and follow to workspace N",
		"EnableAnimations\tEnable animations",
		"DisableAnimations\tDisable animations",
		"ToggleAnimations\tToggle animations",
		"SetConfig\tSet a config option",
		"SetTheme\tChange theme",
		"SetDockbarPosition\tChange dockbar position",
		"SetBorderStyle\tChange border style",
		"ShowNotification\tShow a notification",
		"FocusDirection\tFocus window in direction",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, cmd := range commands {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(cmd), toComplete) {
			filtered = append(filtered, cmd)
		}
	}
	return filtered
}

// getRunCommandArgCompletions returns completions for run-command arguments.
func getRunCommandArgCompletions(command string, argIndex int, toComplete string) []string {
	switch command {
	case "Split":
		if argIndex == 1 {
			return []string{"horizontal\tSplit top/bottom", "vertical\tSplit left/right"}
		}
	case "SwitchWorkspace", "MoveToWorkspace", "MoveAndFollowWorkspace":
		if argIndex == 1 {
			return []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
		}
	case "SetDockbarPosition":
		if argIndex == 1 {
			return []string{"top", "bottom", "hidden"}
		}
	case "SetBorderStyle":
		if argIndex == 1 {
			return []string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii"}
		}
	case "FocusDirection":
		if argIndex == 1 {
			return []string{"left", "right", "up", "down"}
		}
	case "ShowNotification":
		if argIndex == 2 {
			return []string{"info", "success", "warning", "error"}
		}
	case "SetConfig":
		if argIndex == 1 {
			return getConfigPathCompletions(toComplete)
		}
		if argIndex == 2 {
			// Would need the first arg to determine values
			return nil
		}
	}
	return nil
}

// getConfigPathCompletions returns completions for set-config paths.
func getConfigPathCompletions(toComplete string) []string {
	paths := []string{
		"dockbar_position\tDockbar position (top/bottom/hidden)",
		"border_style\tWindow border style",
		"animations\tEnable/disable animations (true/false/toggle)",
		"hide_window_buttons\tHide window buttons (true/false)",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, path := range paths {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(path), toComplete) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// getConfigValueCompletions returns completions for set-config values.
func getConfigValueCompletions(path, _ string) []string {
	switch path {
	case "dockbar_position", "appearance.dockbar_position":
		return []string{"top", "bottom", "hidden"}
	case "border_style", "appearance.border_style":
		return []string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii"}
	case "animations", "appearance.animations_enabled", "animations_enabled":
		return []string{"true", "false", "toggle", "on", "off"}
	case "hide_window_buttons", "appearance.hide_window_buttons":
		return []string{"true", "false"}
	}
	return nil
}

// runGetLogs retrieves and displays daemon logs.
func runGetLogs(count int, clear bool, follow bool) error {
	if !session.IsDaemonRunning() {
		return fmt.Errorf("TUIOS daemon is not running")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer func() { _ = client.Close() }()

	if follow {
		// Follow mode: continuously poll for new logs
		return followLogs(client, count)
	}

	// Single retrieval
	return displayLogs(client, count, clear)
}

// displayLogs fetches and displays logs once.
func displayLogs(client *session.Client, count int, clear bool) error {
	msg, err := session.NewMessage(session.MsgGetLogs, &session.GetLogsPayload{
		Count: count,
		Clear: clear,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	resp, err := client.SendControlMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to get logs: %w", err)
	}

	if resp.Type == session.MsgError {
		var errPayload session.ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, client.GetCodec()); err != nil {
			return fmt.Errorf("failed to get logs")
		}
		return fmt.Errorf("failed to get logs: %s", errPayload.Message)
	}

	if resp.Type != session.MsgLogsData {
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}

	var logsData session.LogsDataPayload
	if err := resp.ParsePayloadWithCodec(&logsData, client.GetCodec()); err != nil {
		return fmt.Errorf("failed to parse logs: %w", err)
	}

	if len(logsData.Entries) == 0 {
		fmt.Println("No log entries")
		return nil
	}

	// Display logs
	for _, entry := range logsData.Entries {
		ts := time.UnixMilli(entry.Timestamp)
		fmt.Printf("[%s] [%s] %s\n", ts.Format("15:04:05.000"), entry.Level, entry.Message)
	}

	fmt.Printf("\n--- %d log entries ---\n", len(logsData.Entries))

	if clear {
		fmt.Println("(logs cleared)")
	}

	return nil
}

// followLogs continuously polls for new logs.
func followLogs(client *session.Client, initialCount int) error {
	// First, display existing logs
	if err := displayLogs(client, initialCount, false); err != nil {
		return err
	}

	fmt.Println("\n--- Following logs (Ctrl+C to stop) ---")

	// Keep track of the last timestamp we've seen
	var lastTimestamp int64

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	for {
		select {
		case <-sigChan:
			fmt.Println("\nStopped following logs")
			return nil
		case <-ticker.C:
			// Fetch new logs
			msg, err := session.NewMessage(session.MsgGetLogs, &session.GetLogsPayload{
				Count: 100, // Fetch last 100 entries to check for new ones
				Clear: false,
			})
			if err != nil {
				continue
			}

			resp, err := client.SendControlMessage(msg)
			if err != nil {
				continue
			}

			if resp.Type != session.MsgLogsData {
				continue
			}

			var logsData session.LogsDataPayload
			if err := resp.ParsePayloadWithCodec(&logsData, client.GetCodec()); err != nil {
				continue
			}

			// Display only new entries
			for _, entry := range logsData.Entries {
				if entry.Timestamp > lastTimestamp {
					ts := time.UnixMilli(entry.Timestamp)
					fmt.Printf("[%s] [%s] %s\n", ts.Format("15:04:05.000"), entry.Level, entry.Message)
					lastTimestamp = entry.Timestamp
				}
			}
		}
	}
}

// completeSessionNames returns available session names for shell completion.
func completeSessionNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !session.IsDaemonRunning() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	sessions, err := client.ListSessions()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		names = append(names, fmt.Sprintf("%s\t%s (%d windows)", s.Name, status, s.WindowCount))
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}
