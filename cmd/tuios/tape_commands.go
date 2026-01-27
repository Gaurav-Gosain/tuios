package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/input"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func runTapeInteractive(tapeFile string) error {
	content, err := os.ReadFile(tapeFile)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}

	commands, parseErrors := tape.ParseFile(string(content))
	if len(parseErrors) > 0 {
		fmt.Fprintf(os.Stderr, "Tape parsing errors:\n")
		for _, err := range parseErrors {
			fmt.Fprintf(os.Stderr, "  %s\n", err)
		}
		return fmt.Errorf("failed to parse tape file")
	}

	fmt.Printf("Preparing tape script: %s\n", tapeFile)
	fmt.Printf("Total commands: %d\n", len(commands))
	fmt.Println("Press Ctrl+C to cancel, Ctrl+P to pause/resume playback")
	fmt.Println("\nStarting TUIOS with tape playback...")

	userConfig, err := config.LoadUserConfig()
	if err != nil {
		log.Printf("Warning: Failed to load config, using defaults: %v", err)
		userConfig = config.DefaultConfig()
	}

	if err := theme.Initialize(themeName); err != nil {
		log.Printf("Warning: Failed to load theme '%s': %v", themeName, err)
	}

	app.SetInputHandler(input.HandleInput)

	keybindRegistry := config.NewKeybindRegistry(userConfig)

	player := tape.NewPlayer(commands)
	converter := tape.NewScriptMessageConverter()

	initialOS := &app.OS{
		FocusedWindow:        -1,
		WindowExitChan:       make(chan string, 10),
		StateSyncChan:        make(chan *session.SessionState, 10),
		ClientEventChan:      make(chan app.ClientEvent, 10),
		MouseSnapping:        false,
		MasterRatio:          0.5,
		CurrentWorkspace:     1,
		NumWorkspaces:        9,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]app.WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		PendingResizes:       make(map[string][2]int),
		KeybindRegistry:      keybindRegistry,
		ShowKeys:             showKeys,
		RecentKeys:           []app.KeyEvent{},
		KeyHistoryMaxSize:    5,
		ScriptMode:           true,
		ScriptPlayer:         player,
		ScriptPaused:         false,
		ScriptConverter:      converter,
		ScriptExecutor:       tape.NewCommandExecutor(nil),
	}

	initialOS.ScriptExecutor = tape.NewCommandExecutor(initialOS)

	p := tea.NewProgram(
		initialOS,
		tea.WithFPS(config.NormalFPS),
		tea.WithoutSignalHandler(),
		tea.WithFilter(filterMouseMotion),
	)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		p.Send(tea.QuitMsg{})
	}()

	finalModel, err := p.Run()

	if finalOS, ok := finalModel.(*app.OS); ok {
		finalOS.Cleanup()
	}

	fmt.Print("\033c")
	fmt.Print("\033[?1000l")
	fmt.Print("\033[?1002l")
	fmt.Print("\033[?1003l")
	fmt.Print("\033[?1004l")
	fmt.Print("\033[?1006l")
	fmt.Print("\033[?25h")
	fmt.Print("\033[?47l")
	fmt.Print("\033[0m")
	fmt.Print("\r\n")
	_ = os.Stdout.Sync()

	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	return nil
}

func validateTapeFile(tapeFile string) error {
	content, err := os.ReadFile(tapeFile)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}

	commands, parseErrors := tape.ParseFile(string(content))
	if len(parseErrors) > 0 {
		fmt.Fprintf(os.Stderr, "Parsing errors found:\n")
		for _, err := range parseErrors {
			fmt.Fprintf(os.Stderr, "  ✗ %s\n", err)
		}
		return fmt.Errorf("tape file has parsing errors")
	}

	checkmark := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("✓")
	validText := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("Tape file is valid")

	fmt.Printf("%s %s\n", checkmark, validText)

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

	fmt.Printf("  %s: %s\n", labelStyle.Render("File"), valueStyle.Render(tapeFile))
	fmt.Printf("  %s: %s\n", labelStyle.Render("Commands"), valueStyle.Render(fmt.Sprintf("%d", len(commands))))

	if len(commands) > 0 {
		fmt.Print("\n  ")
		fmt.Print(headerStyle.Render("Command Summary:"))
		fmt.Println()

		numberStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		cmdNameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
		argsStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

		for i, cmd := range commands {
			parts := strings.Split(cmd.String(), " ")
			if len(parts) > 1 {
				cmdName := parts[0]
				args := strings.Join(parts[1:], " ")
				fmt.Printf("    %s %s %s\n",
					numberStyle.Render(fmt.Sprintf("[%d]", i+1)),
					cmdNameStyle.Render(cmdName),
					argsStyle.Render(args))
			} else {
				fmt.Printf("    %s %s\n",
					numberStyle.Render(fmt.Sprintf("[%d]", i+1)),
					cmdNameStyle.Render(parts[0]))
			}
		}
	}

	return nil
}

func listTapeFiles() error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	tapeDir, _ := app.GetTapeDirectory()

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	fmt.Printf("%s\n", headerStyle.Render("Tape Recordings"))
	fmt.Printf("%s\n\n", pathStyle.Render("Location: "+tapeDir))

	if len(files) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
		fmt.Printf("%s\n", dimStyle.Render("No tape recordings found"))
		fmt.Printf("%s\n", dimStyle.Render("Use Ctrl+B, T, r in TUIOS to start recording"))
		return nil
	}

	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	sizeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	dateStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	for _, file := range files {
		sizeStr := formatFileSize(file.Size)
		dateStr := file.Modified.Format("2006-01-02 15:04")
		fmt.Printf("  %s  %s  %s\n",
			nameStyle.Render(fmt.Sprintf("%-30s", file.Name)),
			sizeStyle.Render(fmt.Sprintf("%8s", sizeStr)),
			dateStyle.Render(dateStr))
	}

	fmt.Printf("\n%d tape(s) found\n", len(files))
	return nil
}

func showTapeDirectory() error {
	tapeDir, err := app.GetTapeDirectory()
	if err != nil {
		return fmt.Errorf("failed to get tape directory: %w", err)
	}
	fmt.Println(tapeDir)
	return nil
}

func deleteTapeFile(name string) error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	var targetFile *app.TapeFile
	for i := range files {
		if files[i].Name == name || files[i].Name == strings.TrimSuffix(name, ".tape") {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return fmt.Errorf("tape file '%s' not found", name)
	}

	fmt.Printf("Delete '%s'? (yes/no): ", targetFile.Name)
	var response string
	_, _ = fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	if response != "yes" && response != "y" {
		fmt.Println("Deletion cancelled.")
		return nil
	}

	if err := app.DeleteTapeFile(targetFile.Path); err != nil {
		return fmt.Errorf("failed to delete tape file: %w", err)
	}

	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	fmt.Printf("%s\n", successStyle.Render("Deleted: "+targetFile.Name))
	return nil
}

func showTapeFile(name string) error {
	files, err := app.LoadTapeFiles()
	if err != nil {
		return fmt.Errorf("failed to load tape files: %w", err)
	}

	var targetFile *app.TapeFile
	for i := range files {
		if files[i].Name == name || files[i].Name == strings.TrimSuffix(name, ".tape") {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return fmt.Errorf("tape file '%s' not found", name)
	}

	content, err := os.ReadFile(targetFile.Path)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	fmt.Printf("%s\n", headerStyle.Render(targetFile.Name+".tape"))
	fmt.Printf("%s\n\n", pathStyle.Render(targetFile.Path))

	fmt.Print(string(content))

	return nil
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}
