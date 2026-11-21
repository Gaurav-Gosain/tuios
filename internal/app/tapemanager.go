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
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
	"github.com/Gaurav-Gosain/tuios/internal/units"
	"github.com/adrg/xdg"
)

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
	Mode  TapeManagerMode
	Files []TapeFile
	// List is the file list's cursor and window.
	List           listnav.State
	Name           textedit.Line // the name a new tape is saved under
	DeleteConfirm  bool          // Whether delete is confirmed
	ErrorMessage   string        // Error message to display
	SuccessMessage string        // Success message to display
	MessageTime    time.Time
}

// GetTapeDirectory returns the XDG data directory for tape files
func GetTapeDirectory() (string, error) {
	tapeDir, err := xdg.DataFile("tuios/tapes")
	if err != nil {
		return "", errfmt.Errorf("failed to get tape directory: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(tapeDir), 0750); err != nil {
		return "", errfmt.Errorf("failed to create tape directory: %w", err)
	}

	return filepath.Dir(tapeDir), nil
}

// LoadTapeFiles loads all tape files from the XDG data directory
func LoadTapeFiles() ([]TapeFile, error) {
	tapeDir, err := GetTapeDirectory()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(tapeDir, 0750); err != nil {
		return nil, errfmt.Errorf("failed to create tape directory: %w", err)
	}

	entries, err := os.ReadDir(tapeDir)
	if err != nil {
		return nil, errfmt.Errorf("failed to read tape directory: %w", err)
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

	if err := os.MkdirAll(tapeDir, 0750); err != nil {
		return "", errfmt.Errorf("failed to create tape directory: %w", err)
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405"))
	}
	if !strings.HasSuffix(name, ".tape") {
		name = name + ".tape"
	}

	path := filepath.Join(tapeDir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", errfmt.Errorf("failed to write tape file: %w", err)
	}

	return path, nil
}

// InitTapeManager initializes the tape manager state
func (m *OS) InitTapeManager() {
	m.TapeManager = &TapeManagerState{
		Mode:  TapeManagerList,
		Files: []TapeFile{},
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

	m.TapeManager.List.Fit(len(files), tapeManagerVisibleRows)
}

// ToggleTapeManager toggles the tape manager overlay
func (m *OS) ToggleTapeManager() {
	if !m.ShowTapeManager && m.learnOff(learnNoteTapes) {
		return
	}
	m.ShowTapeManager = !m.ShowTapeManager
	if m.ShowTapeManager {
		m.RefreshTapeFiles()
		if m.TapeManager != nil {
			m.TapeManager.Mode = TapeManagerList
			m.TapeManager.Name.Set("")
			m.TapeManager.DeleteConfirm = false
		}
	}
}

// TapeManagerMove moves the selection by delta under the list rule.
func (m *OS) TapeManagerMove(delta int) {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}
	m.TapeManager.List.Move(delta, len(m.TapeManager.Files), tapeManagerVisibleRows, m.listWraps(), nil)
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

	selected := m.TapeManager.Files[m.TapeManager.List.Cursor]
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
	if m.learnOff(learnNoteRecord) {
		return
	}
	if m.TapeManager == nil {
		m.InitTapeManager()
	}

	m.TapeManager.Mode = TapeManagerNaming
	m.TapeManager.Name.Set(fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405")))
}

// TapeManagerConfirmRecording confirms the name and starts recording
func (m *OS) TapeManagerConfirmRecording() {
	if m.TapeManager == nil {
		return
	}

	name := strings.TrimSpace(m.TapeManager.Name.String())
	if name == "" {
		name = fmt.Sprintf("recording_%s", time.Now().Format("20060102_150405"))
	}

	if m.TapeRecorder == nil {
		m.TapeRecorder = tape.NewRecorder()
	}

	m.TapeRecordingName = name

	mode := "window"
	if m.Mode == TerminalMode {
		mode = "terminal"
	}

	m.TapeRecorder.StartWithState(mode, m.CurrentWorkspace, m.AutoTiling)
	m.TapeManager.Mode = TapeManagerRecording
	m.ShowTapeManager = false

	if m.GetFocusedWindow() != nil {
		m.ChooseMode(TerminalMode, ModeCauseRecording)
	}

	m.ShowNotification("Recording started: "+name, "success", 2*time.Second)
}

// TapeManagerStopRecording stops recording and saves the tape
func (m *OS) TapeManagerStopRecording() {
	if m.TapeRecorder == nil || !m.TapeRecorder.IsRecording() {
		return
	}

	m.TapeRecorder.Stop()

	content := m.TapeRecorder.String(m.TapeRecordingName)
	path, err := SaveTape(m.TapeRecordingName, content)
	if err != nil {
		m.ShowNotification("Failed to save recording: "+err.Error(), "error", 3*time.Second)
	} else {
		m.ShowNotification(fmt.Sprintf("Recording saved: %s", filepath.Base(path)), "success", 2*time.Second)
	}

	m.TapeRecorder.Clear()
	m.TapeRecordingName = ""

	m.RefreshTapeFiles()
}

// TapeManagerPlaySelected plays the selected tape file
func (m *OS) TapeManagerPlaySelected() {
	if m.TapeManager == nil || len(m.TapeManager.Files) == 0 {
		return
	}

	selected := m.TapeManager.Files[m.TapeManager.List.Cursor]

	if m.scriptBusy() {
		m.TapeManager.ErrorMessage = "A tape is already playing"
		m.TapeManager.MessageTime = time.Now()
		return
	}

	script, err := tape.LoadFile(selected.Path)
	if err != nil {
		m.TapeManager.ErrorMessage = fmt.Sprintf("Failed to read tape: %s", err)
		m.TapeManager.MessageTime = time.Now()
		return
	}
	if len(script.Errors) > 0 {
		m.TapeManager.ErrorMessage = fmt.Sprintf("Tape has %s. The first is at %s", plural.Count(len(script.Errors), "error"), script.Errors[0])
		m.TapeManager.MessageTime = time.Now()
		return
	}
	if m.ScriptMode {
		m.exitScriptMode()
	}
	m.startTapePlayback(script.Commands, 0)

	m.ShowTapeManager = false
	m.ShowNotification("Playing: "+selected.Name, "info", 2*time.Second)
}

// RenderTapeManager renders the tape manager overlay
func (m *OS) RenderTapeManager() string {
	if m.TapeManager == nil {
		m.InitTapeManager()
	}

	pal := m.overlayUI()
	bg := pal.Surface
	width := m.panelWidth(tapeManagerWidth)

	text := func(fg color.Color) func(string) string {
		return func(s string) string { return overlay.Style(bg).Foreground(fg).Render(s) }
	}
	dim := text(pal.FgDim)

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
				m.tapeNameField(max(width-4, 1), bg, pal.Fg))
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "save"}, {Key: "esc", Label: "cancel"}}

	case TapeManagerConfirmDelete:
		if len(m.TapeManager.Files) > 0 {
			selected := m.TapeManager.Files[m.TapeManager.List.Cursor]
			lines = append(lines, text(pal.Warn)("Delete "+selected.Name+"?"))
			hints = []overlay.Hint{{Key: "y", Label: "delete"}, {Key: "n", Label: "keep"}}
		}

	case TapeManagerList:
		hints = []overlay.Hint{
			{Key: hintListMoveLetters, Label: "select"},
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
		startIdx := m.TapeManager.List.Scroll
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

const tapeManagerWidth = 56

const tapeMessageLinger = 3 * time.Second

func (m *OS) tapeFileRow(i, width int, pal overlay.Palette) string {
	file := m.TapeManager.Files[i]
	selected := i == m.TapeManager.List.Cursor

	st := overlay.RowState{Cursor: selected, Focused: true}
	rowBg := pal.Ground(st, pal.Surface)
	nameFg := pal.Fg

	marker := "  "
	if selected {
		marker = "› "
		if overlay.UseASCII() {
			marker = "> "
		}
	}

	size := units.Bytes(file.Size)
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
	return pal.Row(row+overlay.Style(rowBg).Render(strings.Repeat(" ", gap))+right, width, st, pal.Surface)
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
		}
		_, consumed := lineKey(&m.TapeManager.Name, key, "", cleanTapeName)
		return consumed

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
		if motion := m.ListKeys.Keys(key, true); motion != listnav.None {
			m.TapeManagerMove(listnav.Delta(motion, 10))
			return true
		}
		switch key {
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

func (m *OS) tapeNameField(width int, bg, fg color.Color) string {
	before, at, after := m.TapeManager.Name.Parts()
	return overlay.FieldLine(before, at, after, width, bg, fg)
}

// TapeNamePaste types a paste at the tape name's caret.
func (m *OS) TapeNamePaste(s string) {
	if m.TapeManager != nil && m.TapeManager.Mode == TapeManagerNaming {
		lineInsert(&m.TapeManager.Name, s, cleanTapeName)
	}
}

func cleanTapeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r > 126 {
			return -1
		}
		return r
	}, s)
}
