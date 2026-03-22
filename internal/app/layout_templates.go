package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// LayoutTemplate represents a saved window layout configuration.
type LayoutTemplate struct {
	Name       string                 `json:"name"`
	CreatedAt  time.Time              `json:"created_at"`
	Windows    []LayoutTemplateWindow `json:"windows"`
	AutoTiling bool                   `json:"auto_tiling"`
}

// LayoutTemplateWindow stores the position and size of a single window in a layout template.
type LayoutTemplateWindow struct {
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Title  string `json:"title,omitempty"`
}

// GetTemplatesDir returns the directory path for layout template files.
// It uses the XDG config directory under tuios/layouts/.
func GetTemplatesDir() string {
	configDir := filepath.Join(xdg.ConfigHome, "tuios", "layouts")
	return configDir
}

// ensureTemplatesDir creates the templates directory if it does not exist.
func ensureTemplatesDir() error {
	dir := GetTemplatesDir()
	return os.MkdirAll(dir, 0750)
}

// templateFilePath returns the file path for a layout template with the given name.
func templateFilePath(name string) string {
	// Sanitize the name: replace path separators and whitespace
	safe := strings.ReplaceAll(name, string(os.PathSeparator), "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	if safe == "" {
		safe = "unnamed"
	}
	return filepath.Join(GetTemplatesDir(), safe+".json")
}

// SaveLayoutTemplate saves the current window layout of the OS to a JSON file.
func SaveLayoutTemplate(name string, m *OS) error {
	if err := ensureTemplatesDir(); err != nil {
		return fmt.Errorf("failed to create layouts directory: %w", err)
	}

	tmpl := LayoutTemplate{
		Name:       name,
		CreatedAt:  time.Now(),
		AutoTiling: m.AutoTiling,
	}

	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized && !w.Minimizing {
			tw := LayoutTemplateWindow{
				X:      w.X,
				Y:      w.Y,
				Width:  w.Width,
				Height: w.Height,
				Title:  w.CustomName,
			}
			tmpl.Windows = append(tmpl.Windows, tw)
		}
	}

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal layout template: %w", err)
	}

	path := templateFilePath(name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write layout template: %w", err)
	}

	return nil
}

// LoadLayoutTemplates reads all layout template JSON files from the templates directory.
func LoadLayoutTemplates() ([]LayoutTemplate, error) {
	dir := GetTemplatesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read layouts directory: %w", err)
	}

	var templates []LayoutTemplate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// #nosec G304 - reading user layout files from known config directory
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var tmpl LayoutTemplate
		if err := json.Unmarshal(data, &tmpl); err != nil {
			continue
		}
		templates = append(templates, tmpl)
	}

	return templates, nil
}

// ApplyLayoutTemplate closes all windows in the current workspace and creates new ones
// at the positions defined by the template.
func ApplyLayoutTemplate(tmpl LayoutTemplate, m *OS) {
	// Close all windows in current workspace
	for i := len(m.Windows) - 1; i >= 0; i-- {
		if m.Windows[i].Workspace == m.CurrentWorkspace {
			m.DeleteWindow(i)
		}
	}

	m.AutoTiling = tmpl.AutoTiling

	// Create windows at saved positions
	for _, tw := range tmpl.Windows {
		title := tw.Title
		m.AddWindow(title)
		// The newly added window is at the end of the Windows slice
		if len(m.Windows) > 0 {
			newWin := m.Windows[len(m.Windows)-1]
			newWin.X = tw.X
			newWin.Y = tw.Y
			newWin.Resize(tw.Width, tw.Height)
			if tw.Title != "" {
				newWin.CustomName = tw.Title
			}
			newWin.InvalidateCache()
		}
	}

	if m.AutoTiling {
		m.TileAllWindows()
	}

	if len(m.Windows) > 0 {
		m.FocusedWindow = len(m.Windows) - 1
	}
}

// DeleteLayoutTemplate removes a layout template file by name.
func DeleteLayoutTemplate(name string) error {
	path := templateFilePath(name)
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete layout template: %w", err)
	}
	return nil
}

// FilterLayoutTemplates filters layout templates by a query string.
// It performs case-insensitive substring matching on the Name field.
func FilterLayoutTemplates(templates []LayoutTemplate, query string) []LayoutTemplate {
	if query == "" {
		return templates
	}
	q := strings.ToLower(query)
	var filtered []LayoutTemplate
	for _, tmpl := range templates {
		if strings.Contains(strings.ToLower(tmpl.Name), q) {
			filtered = append(filtered, tmpl)
		}
	}
	return filtered
}
