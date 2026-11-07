package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// runCommand executes a tape command in a running TUIOS session.
func runCommand(sessionName, command string, args []string, jsonOutput bool) error {
	return runCommandRendered(sessionName, command, args, jsonOutput, nil)
}

// runCommandRendered is runCommand with a printer for the human output. A
// command that answers with data, rather than only succeeding, passes one so the
// answer is shown instead of thrown away; render is nil for the rest.
func runCommandRendered(sessionName, command string, args []string, jsonOutput bool, render resultRenderer) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
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
		return errfmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResultWithFormat(client, msg, requestID, jsonOutput, render); err != nil {
		return err
	}

	return nil
}

// runTapeExec plays a tape file in a running TUIOS session and returns when
// the tape has ended. A tape that fails returns the failure, placed at the
// file and line it came from.
func runTapeExec(sessionName, filePath string, timeout time.Duration) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	script, err := tape.LoadFile(filePath)
	if err != nil {
		return errfmt.Errorf("failed to read tape file: %w", err)
	}
	if len(script.Errors) > 0 {
		for _, e := range script.Errors {
			fmt.Fprintf(os.Stderr, "%s: %s\n", filePath, e)
		}
		return errfmt.Errorf("the tape has errors, listed above. Run 'tuios tape validate %s' after you fix them", filePath)
	}
	if len(script.Commands) == 0 {
		return errfmt.Errorf("the tape has no commands")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// The tape goes with its Source lines resolved: the session cannot read
	// the files they name.
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		TapeScript:  script.Text,
		RequestID:   requestID,
	})
	if err != nil {
		return errfmt.Errorf("failed to create message: %w", err)
	}

	resp, err := client.SendControlMessageWait(msg, timeout)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return errfmt.Errorf("the tape did not finish in %s. It may still be playing. Use --timeout to wait longer", timeout)
		}
		return errfmt.Errorf("failed to run the tape: %w", err)
	}
	switch resp.Type {
	case session.MsgCommandResult:
		var result session.CommandResultPayload
		if err := resp.ParsePayload(&result); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		if !result.Success {
			return errfmt.Errorf("the tape failed: %s", script.Locate(result.Message))
		}
		fmt.Printf("Tape finished: %d commands\n", len(script.Commands))
		return nil
	case session.MsgError:
		var errPayload session.ErrorPayload
		if err := resp.ParsePayload(&errPayload); err == nil && errPayload.Message != "" {
			return errfmt.Errorf("the tape did not run: %s", errPayload.Message)
		}
		return errfmt.Errorf("the tape did not run, and the daemon did not say why. Run 'tuios logs' to read what it logged")
	default:
		return errfmt.Errorf("unexpected response from the daemon: %v", resp.Type)
	}
}

// runCommandCatalog is what 'tuios run-command --list' prints. A closed set of
// values is spelled from the list the executor checks against, so the listing
// cannot offer a value the command refuses.
func runCommandCatalog() []runCommandEntry {
	return []runCommandEntry{
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
		{"ArrangePanes tiled|even-horizontal|even-vertical [workspace [window...]]", "Lay out the panes of the workspace again", "tuios run-command ArrangePanes tiled 2"},
		{"SetMultifocus [window...]", "Put exactly these windows in multifocus, or clear it", "tuios run-command SetMultifocus build tests"},
		{"Screenshot", "Save the focused window as an image", "tuios run-command Screenshot"},

		// Master-stack
		{"SetMasterPosition " + strings.Join(config.MasterPositions, "|"), "Put the master panes on one side of the workspace", "tuios run-command SetMasterPosition center"},
		{"SetMasterCount 1-9", "Set how many panes are master panes", "tuios run-command SetMasterCount 2"},
		{"CycleMasterPosition", "Move the master panes to the next side", "tuios run-command CycleMasterPosition"},
		{"AddMaster", "Make one more pane a master pane", "tuios run-command AddMaster"},
		{"RemoveMaster", "Make one pane fewer a master pane", "tuios run-command RemoveMaster"},
		{"SwapWithMaster", "Swap the focused pane with the master pane", "tuios run-command SwapWithMaster"},
		{"FocusMaster", "Focus the master pane", "tuios run-command FocusMaster"},

		// Workspace
		{"SwitchWorkspace 1-9", "Switch to workspace N", "tuios run-command SwitchWorkspace 2"},
		{"MoveToWorkspace 1-9", "Move focused window to workspace N", "tuios run-command MoveToWorkspace 3"},

		// Animations
		{"EnableAnimations", "Enable UI animations", "tuios run-command EnableAnimations"},
		{"DisableAnimations", "Disable UI animations", "tuios run-command DisableAnimations"},
		{"ToggleAnimations", "Toggle UI animations", "tuios run-command ToggleAnimations"},

		// Config commands
		{"SetDockbarPosition " + strings.Join(config.DockbarPositions, "|"), "Change dockbar position", "tuios run-command SetDockbarPosition top"},
		{"SetBorderStyle style", "Change window border style", "tuios run-command SetBorderStyle rounded"},
		{"SetTheme themename", "Change the color theme", "tuios run-command SetTheme dracula"},
		{"ShowNotification message [type]", "Show a notification", "tuios run-command ShowNotification \"Hello!\" info"},

		// Any keybinding action, and keys through tuios's own key handling
		{"<action>", "Run a keybinding action by name (tuios keybinds list prints them)", "tuios run-command toggle_spotlight"},
		{"Action <action>", "The same, spelled as a tape writes it", "tuios run-command Action toggle_scratch"},
		{"Press <keys>", "Press keys as a person would: prefixes, copy mode, dialogs", "tuios run-command Press \"ctrl+b ?\""},
		{"Run <command line>", "Type a command line into the focused window and press Enter", "tuios run-command Run \"make test\""},

		// Inspection commands
		{"ListWindows", "List all windows (use --json)", "tuios list-windows --json"},
		{"GetWindow [id-or-name]", "Get window info (use --json)", "tuios get-window --json"},
		{"GetSessionInfo", "Get session info (use --json)", "tuios session-info --json"},
		{"ShareLook [name] [author]", "Share the look in the setups discussion on GitHub", "tuios look share"},
	}
}

// listAvailableCommands lists all available tape commands that can be executed remotely.
func listAvailableCommands() {
	commands := runCommandCatalog()

	fmt.Println("Available commands for 'tuios run-command':")
	fmt.Println()

	width := 0
	for _, cmd := range commands {
		width = max(width, len(cmd.name))
	}
	for _, cmd := range commands {
		fmt.Printf("  %-*s  %s\n", width, cmd.name, cmd.description)
	}

	fmt.Println()
	fmt.Println("Examples:")
	for _, cmd := range commands {
		if cmd.example != "" {
			fmt.Printf("  %s\n", cmd.example)
		}
	}
}

// fixedCompletions completes a flag whose values are a closed set the daemon
// will reject anything outside of, so the shell can offer them all.
func fixedCompletions(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

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
		"ArrangePanes\tLay out the panes again (tiled/even-horizontal/even-vertical)",
		"SetMultifocus\tSet the multifocus windows",
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
		"Screenshot\tSave the focused window as an image",
		"SetMasterPosition\tPut the master panes on one side",
		"SetMasterCount\tSet how many panes are master panes",
		"CycleMasterPosition\tMove the master panes to the next side",
		"AddMaster\tMake one more pane a master pane",
		"RemoveMaster\tMake one pane fewer a master pane",
		"SwapWithMaster\tSwap the focused pane with the master pane",
		"FocusMaster\tFocus the master pane",
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
			return slices.Clone(config.DockbarPositions)
		}
	case "SetMasterPosition":
		if argIndex == 1 {
			return slices.Clone(config.MasterPositions)
		}
	case "SetMasterCount":
		if argIndex == 1 {
			return masterCountNames()
		}
	case "SetBorderStyle":
		if argIndex == 1 {
			return slices.Clone(config.BorderStyles)
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

// runGetLogs retrieves and displays daemon logs.
func runGetLogs(count int, clear bool, follow bool) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	if follow {
		// Follow mode: continuously poll for new logs
		return followLogs(client, count)
	}

	// Single retrieval
	_, err := displayLogs(client, count, clear)
	return err
}

// displayLogs fetches and displays logs once.
// displayLogs prints up to count entries and returns the newest timestamp shown
// (0 when none), which follow mode uses to seed its poll cursor.
func displayLogs(client *session.Client, count int, clear bool) (int64, error) {
	msg, err := session.NewMessage(session.MsgGetLogs, &session.GetLogsPayload{
		Count: count,
		Clear: clear,
	})
	if err != nil {
		return 0, errfmt.Errorf("failed to create message: %w", err)
	}

	resp, err := client.SendControlMessage(msg)
	if err != nil {
		return 0, errfmt.Errorf("failed to get logs: %w", err)
	}

	if resp.Type == session.MsgError {
		var errPayload session.ErrorPayload
		if err := resp.ParsePayload(&errPayload); err != nil {
			return 0, errfmt.Errorf("failed to get logs")
		}
		return 0, errfmt.Errorf("failed to get logs: %s", errPayload.Message)
	}

	if resp.Type != session.MsgLogsData {
		return 0, errfmt.Errorf("unexpected response type: %d", resp.Type)
	}

	var logsData session.LogsDataPayload
	if err := resp.ParsePayload(&logsData); err != nil {
		return 0, errfmt.Errorf("failed to parse logs: %w", err)
	}

	if len(logsData.Entries) == 0 {
		fmt.Println("No log entries")
		return 0, nil
	}

	var newest int64
	for _, entry := range logsData.Entries {
		ts := time.UnixMilli(entry.Timestamp)
		fmt.Printf("[%s] [%s] %s\n", ts.Format("15:04:05.000"), entry.Level, entry.Message)
		if entry.Timestamp > newest {
			newest = entry.Timestamp
		}
	}

	fmt.Printf("\n--- %d log entries ---\n", len(logsData.Entries))

	if clear {
		fmt.Println("(logs cleared)")
	}

	return newest, nil
}

// followLogs continuously polls for new logs.
func followLogs(client *session.Client, initialCount int) error {
	// First, display existing logs and seed the poll cursor from the newest one
	// shown, so the first tick does not reprint what we just displayed.
	lastTimestamp, err := displayLogs(client, initialCount, false)
	if err != nil {
		return err
	}

	fmt.Println("\n--- Following logs (Ctrl+C to stop) ---")

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
			if err := resp.ParsePayload(&logsData); err != nil {
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
