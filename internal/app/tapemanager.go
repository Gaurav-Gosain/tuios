package app

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/adrg/xdg"
)

// tapeManagerVisibleRows is the number of tape files shown at once in the list.
const tapeManagerVisibleRows = 10

// TapeManagerMode represents the current mode of the tape manager
type TapeManagerMode int

const (
	// TapeManagerList shows the list of tape files
	TapeManagerList TapeManagerMode = iota
	// TapeManagerRecording is recording a new tape
	TapeManagerRecording
	// TapeManagerPlaying is playing back a tape
	TapeManagerPlaying
	// TapeManagerConfirmDelete asks for deletion confirmation
	TapeManagerConfirmDelete
	// TapeManagerNaming is entering a name for a new tape
	TapeManagerNaming
)

// TapeFile represents a tape file with metadata
type TapeFile struct {
	Name     string    // Display name (without extension)
	Path     string    // Full path to the file
	Size     int64     // File size in bytes
	Modified time.Time // Last modification time
}

// TapeManagerState holds the state for the tape manager UI
type TapeManagerState struct {
	Mode           TapeManagerMode
	Files          []TapeFile
	SelectedIndex  int
	ScrollOffset   int
	NameBuffer     string // Buffer for naming new tapes
	DeleteConfirm  bool   // Whether delete is confirmed
	ErrorMessage   string // Error message to display
	SuccessMessage string // Success message to display
	MessageTime    time.Time
}

// GetTapeDirectory returns the XDG data directory for tape files
func GetTapeDirectory() (string, error) {
	tapeDir, err := xdg.DataFile("tuios/tapes")
	if err != nil {
		return "", fmt.Errorf("failed to get tape directory: %w", err)
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(tapeDir), 0750); err != nil {
		return "", fmt.Errorf("failed to create tape directory: %w", err)
	}

	// Return the directory path (not the file path)
	return filepath.Dir(tapeDir), nil
}

// LoadTapeFiles loads all tape files from the XDG data directory
func LoadTapeFiles() ([]TapeFile, error) {
	tapeDir, err := GetTapeDirectory()
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	if err := os.MkdirAll(tapeDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create tape directory: %w", err)
	}

	entries, err := os.ReadDir(tapeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read tape directory: %w", err)
	}

	var files []TapeFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".tape") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, TapeFile{
			Name:     strings.TrimSuffix(name, ".tape"),
			Path:     filepath.Join(tapeDir, name),
			Size:     info.Size(),
			Modified: info.ModTime(),
		})
	}

	// Sort by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Modified.After(files[j].Modified)
	})

	return files, nil
}

// DeleteTapeFile deletes a tape file
func DeleteTapeFile(path string) error {
	return os.Remove(path)
}

// SaveTape saves tape content to a file in the XDG data directory
func SaveTape(name string, content string) (string, error) {
	tapeDir, err := GetTapeDirectory()
	if err != nil {
		return "", err
	}

	// Ensure directory exists
	if err := os.MkdirAll(tapeDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create tape directory: %w", err)
	}

	// Clean the name and add extension
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405"))
	}
	if !strings.HasSuffix(name, ".tape") {
		name = name + ".tape"
	}

	path := filepath.Join(tapeDir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", fmt.Errorf("failed to write tape file: %w", err)
	}

	return path, nil
}

// InitTapeManager initializes the tape manager state
func (m *OS) InitTapeManager() {
	m.TapeManager = &TapeManagerState{
		Mode:          TapeManagerList,
		Files:         []TapeFile{},
		SelectedIndex: 0,
		ScrollOffset:  0,
	}
}

// RefreshTapeFiles reloads the tape file list
func (m *OS) RefreshTapeFiles() {
	if m.TapeManager == nil {
		m.InitTapeManager()
	}

	files, err := LoadTapeFiles()
	if err != nil {
		m.TapeManager.ErrorMessage = err.Error()
		m.TapeManager.MessageTime = time.Now()
		return
	}

	m.TapeManager.Files = files

	// Adjust selection if necessary
	if m.TapeManager.SelectedIndex >= len(files) {
		m.TapeManager.SelectedIndex = max(0, len(files)-1)
	}
	m.clampTapeScroll()
}

// clampTapeScroll keeps ScrollOffset positioned so the selected file stays
// within the visible window and never scrolls past the end of the list.
func (m *OS) clampTapeScroll() {
	tm := m.TapeManager
	if tm == nil {
		return
	}

	if tm.SelectedIndex < tm.ScrollOffset {
		tm.ScrollOffset = tm.SelectedIndex
	} else if tm.SelectedIndex >= tm.ScrollOffset+tapeManagerVisibleRows {
		tm.ScrollOffset = tm.SelectedIndex - tapeManagerVisibleRows + 1
	}

	maxOffset := max(len(tm.Files)-tapeManagerVisibleRows, 0)
	if tm.ScrollOffset > maxOffset {
		tm.ScrollOffset = maxOffset
	}
	if tm.ScrollOffset < 0 {
		tm.ScrollOffset = 0
	}
}

// ToggleTapeManager toggles the tape manager overlay
func (m *OS) ToggleTapeManager() {
	m.ShowTapeManager = !m.ShowTapeManager
	if m.ShowTapeManager {
		m.RefreshTapeFiles()
		if m.TapeManager != nil {
			m.TapeManager.Mode = TapeManagerList
			m.TapeManager.NameBuffer = ""
			m.TapeManager.DeleteConfirm = false
		}
	}
}

// TapeManagerSelectNext moves selection down
func (m *OS) TapeManagerSelectNext() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	m.TapeManager.SelectedIndex++
	if m.TapeManager.SelectedIndex >= len(m.TapeManager.Files) {
		m.TapeManager.SelectedIndex = 0
	}
	m.clampTapeScroll()
}

// TapeManagerSelectPrev moves selection up
func (m *OS) TapeManagerSelectPrev() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	m.TapeManager.SelectedIndex--
	if m.TapeManager.SelectedIndex < 0 {
		m.TapeManager.SelectedIndex = len(m.TapeManager.Files) - 1
	}
	m.clampTapeScroll()
}

// TapeManagerDelete initiates delete confirmation
func (m *OS) TapeManagerDelete() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	m.TapeManager.Mode = TapeManagerConfirmDelete
}

// TapeManagerConfirmDeleteAction confirms and deletes the selected tape
func (m *OS) TapeManagerConfirmDeleteAction() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	selected := m.TapeManager.Files[m.TapeManager.SelectedIndex]
	if err := DeleteTapeFile(selected.Path); err != nil {
		m.TapeManager.ErrorMessage = fmt.Sprintf("Failed to delete: %s", err)
		m.TapeManager.MessageTime = time.Now()
	} else {
		m.TapeManager.SuccessMessage = fmt.Sprintf("Deleted '%s'", selected.Name)
		m.TapeManager.MessageTime = time.Now()
	}

	m.TapeManager.Mode = TapeManagerList
	m.RefreshTapeFiles()
}

// TapeManagerCancelDelete cancels delete confirmation
func (m *OS) TapeManagerCancelDelete() {
	if m.TapeManager == nil {
		return
	}
	m.TapeManager.Mode = TapeManagerList
}

// TapeManagerStartRecording starts recording a new tape
func (m *OS) TapeManagerStartRecording() {
	if m.TapeManager == nil {
		m.InitTapeManager()
	}

	m.TapeManager.Mode = TapeManagerNaming
	m.TapeManager.NameBuffer = fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405"))
}

// TapeManagerConfirmRecording confirms the name and starts recording
func (m *OS) TapeManagerConfirmRecording() {
	if m.TapeManager == nil {
		return
	}

	name := strings.TrimSpace(m.TapeManager.NameBuffer)
	if name == "" {
		name = fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405"))
	}

	// Initialize tape recorder if needed
	if m.TapeRecorder == nil {
		m.TapeRecorder = tape.NewRecorder()
	}

	// Store the tape name for later
	m.TapeRecordingName = name

	// Determine current mode for recording
	mode := "window"
	if m.Mode == TerminalMode {
		mode = "terminal"
	}

	// Start recording with initial state (mode, workspace, tiling)
	m.TapeRecorder.StartWithState(mode, m.CurrentWorkspace, m.AutoTiling)
	m.TapeManager.Mode = TapeManagerRecording
	m.ShowTapeManager = false // Close the manager UI

	// Switch to terminal mode if we have a focused window
	// This ensures keystrokes are recorded
	if m.GetFocusedWindow() != nil {
		m.Mode = TerminalMode
		m.TerminalModeEnteredAt = time.Now()
	}

	m.ShowNotification("Recording started: "+name, "success", 2*time.Second)
}

// TapeManagerStopRecording stops recording and saves the tape
func (m *OS) TapeManagerStopRecording() {
	if m.TapeRecorder == nil || !m.TapeRecorder.IsRecording() {
		return
	}

	m.TapeRecorder.Stop()

	// Save the recording
	content := m.TapeRecorder.String(m.TapeRecordingName)
	path, err := SaveTape(m.TapeRecordingName, content)
	if err != nil {
		m.ShowNotification("Failed to save recording: "+err.Error(), "error", 3*time.Second)
	} else {
		m.ShowNotification(fmt.Sprintf("Recording saved: %s", filepath.Base(path)), "success", 2*time.Second)
	}

	// Clear recorder
	m.TapeRecorder.Clear()
	m.TapeRecordingName = ""

	// Refresh file list
	m.RefreshTapeFiles()
}

// TapeManagerPlaySelected plays the selected tape file
func (m *OS) TapeManagerPlaySelected() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	selected := m.TapeManager.Files[m.TapeManager.SelectedIndex]

	// Read the tape file
	content, err := os.ReadFile(selected.Path)
	if err != nil {
		m.TapeManager.ErrorMessage = fmt.Sprintf("Failed to read tape: %s", err)
		m.TapeManager.MessageTime = time.Now()
		return
	}

	// Parse the tape
	lexer := tape.New(string(content))
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	// Create and start player
	player := tape.NewPlayer(commands)
	m.ScriptPlayer = player
	m.ScriptMode = true
	m.ScriptPaused = false
	m.ScriptFinishedTime = time.Time{}
	m.ScriptAwaitWindows = 0
	m.ScriptAwaitDeadline = time.Time{}

	// Create executor
	m.ScriptExecutor = tape.NewCommandExecutor(m)

	// Close the manager UI
	m.ShowTapeManager = false
	m.ShowNotification("Playing: "+selected.Name, "info", 2*time.Second)
}

// RenderTapeManager renders the tape manager overlay
func (m *OS) RenderTapeManager() string {
	if m.TapeManager == nil {
		m.InitTapeManager()
	}

	pal := theme.UI()
	bg := pal.Surface
	width := m.panelWidth(tapeManagerWidth)

	text := func(fg color.Color) func(string) string {
		return func(s string) string { return overlay.Style(bg).Foreground(fg).Render(s) }
	}
	dim, body := text(pal.FgDim), text(pal.Fg)

	title := "tapes"
	if m.TapeRecorder != nil && m.TapeRecorder.IsRecording() {
		title = "recording " + m.TapeRecordingName
	}

	var lines []string
	var hints []overlay.Hint

	switch m.TapeManager.Mode {
	case TapeManagerNaming:
		lines = append(lines,
			dim("Name this tape"),
			"",
			overlay.Style(bg).Foreground(pal.Accent).Bold(true).Render(overlay.Sigil())+
				body(truncateString(m.TapeManager.NameBuffer, max(width-4, 1)))+
				overlay.Cursor(" ", bg, pal.Fg))
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "save"}, {Key: "esc", Label: "cancel"}}

	case TapeManagerConfirmDelete:
		if len(m.TapeManager.Files) > 0 {
			selected := m.TapeManager.Files[m.TapeManager.SelectedIndex]
			lines = append(lines, text(pal.Warn)("Delete "+selected.Name+"?"))
			hints = []overlay.Hint{{Key: "y", Label: "delete"}, {Key: "n", Label: "keep"}}
		}

	case TapeManagerList:
		hints = []overlay.Hint{
			{Key: "↑↓", Label: "select"},
			{Key: overlay.EnterKey(), Label: "play"},
			{Key: "r", Label: "record"},
			{Key: "d", Label: "delete"},
			{Key: "esc", Label: "close"},
		}
		if overlay.UseASCII() {
			hints[0].Key = "jk"
		}

		if m.TapeManager.ErrorMessage != "" && time.Since(m.TapeManager.MessageTime) < tapeMessageLinger {
			lines = append(lines, text(pal.Warn)(m.TapeManager.ErrorMessage), "")
		} else if m.TapeManager.SuccessMessage != "" && time.Since(m.TapeManager.MessageTime) < tapeMessageLinger {
			lines = append(lines, text(pal.Success)(m.TapeManager.SuccessMessage), "")
		}

		if len(m.TapeManager.Files) == 0 {
			lines = append(lines,
				overlay.Style(bg).Foreground(pal.FgDim).Italic(true).Render("  No tapes recorded yet"))
			break
		}

		rows, trimmed := m.panelBody(len(m.TapeManager.Files), len(lines)+1, width, nil, hints)
		hints = trimmed
		startIdx := m.TapeManager.ScrollOffset
		endIdx := min(startIdx+rows, len(m.TapeManager.Files))

		for i := startIdx; i < endIdx; i++ {
			lines = append(lines, m.tapeFileRow(i, width, pal))
		}
		if len(m.TapeManager.Files) > rows {
			lines = append(lines, "",
				overlay.Style(bg).Foreground(pal.FgDim).Italic(true).
					Render(fmt.Sprintf("  %d-%d of %d", startIdx+1, endIdx, len(m.TapeManager.Files))))
		}
	}

	panel := overlay.Panel{
		Title: title,
		Width: width,
		Body:  clipStyledLines(strings.Join(lines, "\n"), width),
		Hints: hints,
	}
	content, _ := panel.Render(pal)
	return content
}

// tapeManagerWidth is the panel's preferred inner width: a name, a size and a
// timestamp with room to breathe.
const tapeManagerWidth = 56

// tapeMessageLinger is how long a tape's own success or error line stays up.
const tapeMessageLinger = 3 * time.Second

// tapeFileRow renders one tape as a full-width row. The fill spans the panel so
// the selection reads as a row rather than as a slab painted around the text.
func (m *OS) tapeFileRow(i, width int, pal overlay.Palette) string {
	file := m.TapeManager.Files[i]
	selected := i == m.TapeManager.SelectedIndex

	rowBg := pal.Surface
	nameFg := pal.Fg
	if selected {
		rowBg, nameFg = pal.RowSel, pal.Fg
	}

	marker := "  "
	if selected {
		marker = "› "
		if overlay.UseASCII() {
			marker = "> "
		}
	}

	// The name gives way first, then the timestamp: a row that keeps its whole
	// name at the cost of the panel's edge is not a row anyone can read.
	size := formatFileSize(file.Size)
	stamp := file.Modified.Format("Jan 02 15:04")
	nameW := max(width-lipgloss.Width(size)-lipgloss.Width(stamp)-6, 6)
	if width < 40 {
		stamp = ""
		nameW = max(width-lipgloss.Width(size)-4, 6)
	}

	row := overlay.Style(rowBg).Foreground(pal.Accent).Bold(true).Render(marker) +
		overlay.Style(rowBg).Foreground(nameFg).Bold(selected).
			Render(overlay.Truncate(file.Name, nameW))
	right := overlay.Style(rowBg).Foreground(pal.FgDim).Render(size)
	if stamp != "" {
		right += overlay.Style(rowBg).Foreground(pal.FgMute).Render("  " + stamp)
	}

	gap := max(width-lipgloss.Width(row)-lipgloss.Width(right), 1)
	return overlay.Fill(row+overlay.Style(rowBg).Render(strings.Repeat(" ", gap))+right, width, rowBg)
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%dB", size)
	} else if size < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
}

func truncateString(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	// Too small to fit an ellipsis: hard-truncate on a rune boundary.
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// HandleTapeManagerInput handles keyboard input for the tape manager
func (m *OS) HandleTapeManagerInput(key string) bool {
	if m.TapeManager == nil {
		return false
	}

	switch m.TapeManager.Mode {
	case TapeManagerNaming:
		switch key {
		case "enter":
			m.TapeManagerConfirmRecording()
			return true
		case "esc":
			m.TapeManager.Mode = TapeManagerList
			return true
		case "backspace":
			if len(m.TapeManager.NameBuffer) > 0 {
				m.TapeManager.NameBuffer = m.TapeManager.NameBuffer[:len(m.TapeManager.NameBuffer)-1]
			}
			return true
		default:
			// Add printable characters to buffer
			if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
				m.TapeManager.NameBuffer += key
				return true
			}
		}

	case TapeManagerConfirmDelete:
		switch key {
		case "y", "Y":
			m.TapeManagerConfirmDeleteAction()
			return true
		case "n", "N", "esc":
			m.TapeManagerCancelDelete()
			return true
		}

	case TapeManagerList:
		switch key {
		case "up", "k":
			m.TapeManagerSelectPrev()
			return true
		case "down", "j":
			m.TapeManagerSelectNext()
			return true
		case "enter":
			if len(m.TapeManager.Files) > 0 {
				m.TapeManagerPlaySelected()
			}
			return true
		case "r":
			m.TapeManagerStartRecording()
			return true
		case "d":
			if len(m.TapeManager.Files) > 0 {
				m.TapeManagerDelete()
			}
			return true
		case "esc", "q":
			m.ShowTapeManager = false
			return true
		}
	}

	return false
}
