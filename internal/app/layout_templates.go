package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

// LayoutTemplate v2  - comprehensive layout specification.
//
// A layout template captures everything needed to recreate a terminal
// workspace: window positions, BSP tree structure, per-window startup
// commands, working directories, and tiling configuration.
//
// Templates are stored as JSON in ~/.config/tuios/layouts/.
//
// Integration points:
//   - Command palette: "Save Layout", "Load Layout"
//   - Keybinding: prefix+L (load), command palette (save)
//   - Tape scripting: SaveLayout/LoadLayout commands
//   - CLI API: tuios layout save/load/list/delete
type LayoutTemplate struct {
	// Metadata
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Version     int       `json:"version"` // Schema version (2)

	// Tiling configuration
	AutoTiling  bool   `json:"auto_tiling"`
	TilingScheme string `json:"tiling_scheme,omitempty"` // "spiral", "alternate", "smart_split", etc.
	MasterRatio float64 `json:"master_ratio,omitempty"`

	// BSP tree structure (for tiled layouts)
	// When present, this is used instead of individual window coordinates.
	BSPTree *LayoutBSPNode `json:"bsp_tree,omitempty"`

	// Windows  - each window's configuration
	Windows []LayoutWindow `json:"windows"`

	// Screen dimensions at save time (for proportional scaling on different screens)
	ScreenWidth  int `json:"screen_width,omitempty"`
	ScreenHeight int `json:"screen_height,omitempty"`
}

// LayoutBSPNode is a serializable representation of a BSP tree node.
type LayoutBSPNode struct {
	SplitType  string          `json:"split_type"` // "vertical", "horizontal", "none"
	SplitRatio float64         `json:"split_ratio"`
	WindowIdx  int             `json:"window_idx,omitempty"` // Index into Windows array (-1 for internal nodes)
	Left       *LayoutBSPNode  `json:"left,omitempty"`
	Right      *LayoutBSPNode  `json:"right,omitempty"`
}

// LayoutWindow stores per-window configuration.
type LayoutWindow struct {
	// Position and size (used in free-float mode or as fallback)
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`

	// Window identity
	Title      string `json:"title,omitempty"`       // Custom name
	CustomName string `json:"custom_name,omitempty"` // User-set name

	// Startup configuration
	Command    string   `json:"command,omitempty"`    // Shell command to run on creation (e.g., "vim", "htop")
	Args       []string `json:"args,omitempty"`       // Command arguments
	WorkingDir string   `json:"working_dir,omitempty"` // Working directory for the shell
	Shell      string   `json:"shell,omitempty"`      // Override shell (empty = default)

	// State
	Minimized bool `json:"minimized,omitempty"`
}

// GetTemplatesDir returns the directory path for layout template files.
func GetTemplatesDir() string {
	return filepath.Join(xdg.ConfigHome, "tuios", "layouts")
}

func ensureTemplatesDir() error {
	return os.MkdirAll(GetTemplatesDir(), 0750)
}

func templateFilePath(name string) string {
	safe := strings.ReplaceAll(name, string(os.PathSeparator), "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	if safe == "" {
		safe = "unnamed"
	}
	return filepath.Join(GetTemplatesDir(), safe+".json")
}

// SaveLayoutTemplate saves the current workspace layout.
func SaveLayoutTemplate(name string, m *OS) error {
	if err := ensureTemplatesDir(); err != nil {
		return fmt.Errorf("create layouts dir: %w", err)
	}

	tmpl := LayoutTemplate{
		Name:         name,
		CreatedAt:    time.Now(),
		Version:      2,
		AutoTiling:   m.AutoTiling,
		MasterRatio:  m.MasterRatio,
		ScreenWidth:  m.GetRenderWidth(),
		ScreenHeight: m.GetRenderHeight(),
	}

	// Save tiling scheme
	if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil {
		tmpl.TilingScheme = tree.AutoScheme.String()
	}

	// Collect windows
	windowIdx := 0
	bspWindowMap := make(map[int]int) // BSP window ID → template window index
	for _, w := range m.Windows {
		if w.Workspace != m.CurrentWorkspace {
			continue
		}
		lw := LayoutWindow{
			X: w.X, Y: w.Y,
			Width: w.Width, Height: w.Height,
			Title:      w.Title,
			CustomName: w.CustomName,
			Minimized:  w.Minimized,
		}

		// Capture working directory from the terminal's CWD if available
		if w.Terminal != nil {
			// Try to get CWD from /proc if we have a shell PID
			if w.ShellPgid > 0 {
				if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", w.ShellPgid)); err == nil {
					lw.WorkingDir = cwd
				}
			}
		}

		tmpl.Windows = append(tmpl.Windows, lw)

		// Map BSP window IDs for tree serialization
		if bspID, ok := m.WindowToBSPID[w.ID]; ok {
			bspWindowMap[bspID] = windowIdx
		}
		windowIdx++
	}

	// Save BSP tree structure (for tiled mode)
	if m.AutoTiling {
		if tree := m.WorkspaceTrees[m.CurrentWorkspace]; tree != nil && tree.Root != nil {
			tmpl.BSPTree = serializeBSPNode(tree.Root, bspWindowMap)
		}
	}

	data, err := json.MarshalIndent(tmpl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(templateFilePath(name), data, 0600)
}

func serializeBSPNode(node *layout.TileNode, windowMap map[int]int) *LayoutBSPNode {
	if node == nil {
		return nil
	}
	n := &LayoutBSPNode{
		SplitRatio: node.SplitRatio,
		WindowIdx:  -1,
	}
	switch node.SplitType {
	case layout.SplitVertical:
		n.SplitType = "vertical"
	case layout.SplitHorizontal:
		n.SplitType = "horizontal"
	default:
		n.SplitType = "none"
	}

	if node.WindowID >= 0 {
		if idx, ok := windowMap[node.WindowID]; ok {
			n.WindowIdx = idx
		}
	}

	n.Left = serializeBSPNode(node.Left, windowMap)
	n.Right = serializeBSPNode(node.Right, windowMap)
	return n
}

// LoadLayoutTemplates reads all templates from the layouts directory.
func LoadLayoutTemplates() ([]LayoutTemplate, error) {
	dir := GetTemplatesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var templates []LayoutTemplate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// #nosec G304
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
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

// ApplyLayoutTemplate recreates a workspace from a template.
func ApplyLayoutTemplate(tmpl LayoutTemplate, m *OS) {
	// Collect existing windows in current workspace (reuse them instead of killing)
	var existingWindows []*terminal.Window
	for _, w := range m.Windows {
		if w.Workspace == m.CurrentWorkspace && !w.Minimized {
			existingWindows = append(existingWindows, w)
		}
	}

	// Count non-minimized template slots
	var templateSlots []LayoutWindow
	for _, tw := range tmpl.Windows {
		if !tw.Minimized {
			templateSlots = append(templateSlots, tw)
		}
	}

	// Disable auto-tiling during layout to prevent retiling
	m.AutoTiling = false

	// Scale factor for different screen sizes
	scaleX, scaleY := 1.0, 1.0
	if tmpl.ScreenWidth > 0 && tmpl.ScreenHeight > 0 {
		scaleX = float64(m.GetRenderWidth()) / float64(tmpl.ScreenWidth)
		scaleY = float64(m.GetRenderHeight()) / float64(tmpl.ScreenHeight)
	}

	// Assign existing windows to template slots
	for i, tw := range templateSlots {
		var win *terminal.Window
		if i < len(existingWindows) {
			// Reuse existing window
			win = existingWindows[i]
		} else {
			// Need more windows than we have  - create new ones
			title := tw.CustomName
			if title == "" {
				title = tw.Title
			}
			m.AddWindow(title)
			if len(m.Windows) > 0 {
				win = m.Windows[len(m.Windows)-1]
			}
		}
		if win == nil {
			continue
		}

		// Apply scaled positions
		win.X = int(float64(tw.X) * scaleX)
		win.Y = int(float64(tw.Y) * scaleY)
		win.Resize(
			max(int(float64(tw.Width)*scaleX), 10),
			max(int(float64(tw.Height)*scaleY), 5),
		)
		win.Minimized = false

		if tw.CustomName != "" {
			win.CustomName = tw.CustomName
		}

		// If template specifies a working directory, cd to it
		if tw.WorkingDir != "" {
			if win.Pty != nil {
				cdCmd := fmt.Sprintf("cd %q && clear\n", tw.WorkingDir)
				_, _ = win.Pty.Write([]byte(cdCmd))
			} else if win.DaemonWriteFunc != nil {
				cdCmd := fmt.Sprintf("cd %q && clear\n", tw.WorkingDir)
				_ = win.DaemonWriteFunc([]byte(cdCmd))
			}
		}

		// If template specifies a startup command, run it (only for newly created windows)
		if i >= len(existingWindows) && tw.Command != "" {
			cmd := tw.Command
			if len(tw.Args) > 0 {
				cmd += " " + strings.Join(tw.Args, " ")
			}
			if win.Pty != nil {
				_, _ = win.Pty.Write([]byte(cmd + "\n"))
			} else if win.DaemonWriteFunc != nil {
				_ = win.DaemonWriteFunc([]byte(cmd + "\n"))
			}
		}

		win.InvalidateCache()
	}

	// If we have MORE existing windows than template slots, minimize the extras
	for i := len(templateSlots); i < len(existingWindows); i++ {
		existingWindows[i].Minimized = true
	}

	// Restore tiling configuration
	m.AutoTiling = tmpl.AutoTiling
	if tmpl.MasterRatio > 0 {
		m.MasterRatio = tmpl.MasterRatio
	}

	// If tiled, rebuild BSP tree from the loaded window positions
	// instead of retiling (which would override the loaded layout)
	if m.AutoTiling {
		if m.UseScrollingLayout {
			m.TileAllWindows()
		} else {
			if tmpl.TilingScheme != "" {
				if tree := m.GetOrCreateBSPTree(); tree != nil {
					tree.AutoScheme = layout.ParseAutoScheme(tmpl.TilingScheme)
				}
			}
			m.RebuildBSPTreeFromPositions()
		}
	} else {
		// Always clamp windows after loading - handles resolution differences
		m.ClampWindowsToView()
	}

	if len(m.Windows) > 0 {
		m.FocusWindow(0)
	}

	m.MarkAllDirty()
}

// DeleteLayoutTemplate removes a template file.
func DeleteLayoutTemplate(name string) error {
	err := os.Remove(templateFilePath(name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// FilterLayoutTemplates filters by name substring.
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

// GenerateTapeScript converts a layout template to a tape script that
// recreates the layout. This enables layout templates to be shared,
// version-controlled, and executed in CI/headless environments.
func GenerateTapeScript(tmpl LayoutTemplate) string {
	var sb strings.Builder
	sb.WriteString("# Auto-generated layout script: " + tmpl.Name + "\n")
	sb.WriteString("# Created: " + tmpl.CreatedAt.Format(time.RFC3339) + "\n\n")

	if tmpl.AutoTiling {
		sb.WriteString("EnableTiling\n")
	} else {
		sb.WriteString("DisableTiling\n")
	}

	for i, w := range tmpl.Windows {
		if i > 0 {
			sb.WriteString("NewWindow\n")
		}
		if w.CustomName != "" {
			sb.WriteString(fmt.Sprintf("RenameWindow %s\n", w.CustomName))
		}
		if w.WorkingDir != "" {
			sb.WriteString(fmt.Sprintf("Type cd %s\nEnter\n", w.WorkingDir))
		}
		if w.Command != "" {
			cmd := w.Command
			if len(w.Args) > 0 {
				cmd += " " + strings.Join(w.Args, " ")
			}
			sb.WriteString(fmt.Sprintf("Type %s\nEnter\n", cmd))
		}
		sb.WriteString("Sleep 200ms\n")
	}

	return sb.String()
}
