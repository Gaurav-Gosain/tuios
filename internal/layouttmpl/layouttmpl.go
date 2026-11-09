// Package layouttmpl reads and writes layout templates: a workspace's panes,
// their places, names, folders and commands, and how they were tiled, kept as
// JSON files under the config folder (~/.config/tuios/layouts/).
//
// The daemon saves and loads them (the layout-template verb), so every client
// of a session reads one list and a load is one change to the session's
// state. The TUI's standalone mode and the tuios layout commands read the
// same files.
package layouttmpl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/adrg/xdg"
)

// Version is the schema this package writes. Version 2 has no tree and no
// mode, and still loads.
const Version = 3

// Template is one saved layout.
type Template struct {
	// Metadata
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Version     int       `json:"version"`

	// Tiling configuration
	AutoTiling   bool    `json:"auto_tiling"`
	TilingScheme string  `json:"tiling_scheme,omitempty"` // "spiral", "alternate", "smart_split", etc.
	MasterRatio  float64 `json:"master_ratio,omitempty"`
	// Mode is the workspace's layout mode, "bsp", "master-stack",
	// "scrolling" or "floating" (version 3). Empty in a version 2 file.
	Mode string `json:"mode,omitempty"`
	// Tree is the split tree of a bsp layout (version 3). Its leaves are
	// indexes into Windows, from 1, so a load puts each pane back in its own
	// split rather than rebuilding the splits from the rectangles.
	Tree *Node `json:"tree,omitempty"`

	// Windows holds each window's configuration.
	Windows []Window `json:"windows"`

	// Screen dimensions at save time (for proportional scaling on different screens)
	ScreenWidth  int `json:"screen_width,omitempty"`
	ScreenHeight int `json:"screen_height,omitempty"`
}

// Node is one node of a template's split tree: a leaf (Window, an index into
// Template.Windows from 1) or a split of two children.
type Node struct {
	Window int     `json:"window,omitempty"`
	Split  int     `json:"split,omitempty"`
	Ratio  float64 `json:"ratio,omitempty"`
	Left   *Node   `json:"left,omitempty"`
	Right  *Node   `json:"right,omitempty"`
}

// Window stores one pane.
type Window struct {
	// Position and size (used in free-float mode or as fallback)
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`

	// Window identity
	Title      string `json:"title,omitempty"`       // Custom name
	CustomName string `json:"custom_name,omitempty"` // User-set name

	// Startup configuration
	Command    string   `json:"command,omitempty"`     // Shell command to run on creation (e.g., "vim", "htop")
	Args       []string `json:"args,omitempty"`        // Command arguments
	WorkingDir string   `json:"working_dir,omitempty"` // Working directory for the shell
	Shell      string   `json:"shell,omitempty"`       // Override shell (empty = default)

	// State
	Minimized bool `json:"minimized,omitempty"`
	Floating  bool `json:"floating,omitempty"`
}

// Dir is the folder the templates are kept in.
func Dir() string {
	return filepath.Join(xdg.ConfigHome, "tuios", "layouts")
}

// Path is the file template name is kept in.
func Path(name string) string {
	safe := strings.ReplaceAll(name, string(os.PathSeparator), "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	safe = strings.ReplaceAll(safe, "..", "_")
	if safe == "" {
		safe = "unnamed"
	}
	return filepath.Join(Dir(), safe+".json")
}

// Save writes t to its file, making the folder when there is none.
func Save(t Template) error {
	if err := os.MkdirAll(Dir(), 0o750); err != nil {
		return errfmt.Errorf("create layouts dir: %w", err)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return errfmt.Errorf("marshal: %w", err)
	}
	return os.WriteFile(Path(t.Name), data, 0o600)
}

// List reads every template in the folder, by name. A file that does not
// parse is left out.
func List() ([]Template, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(Dir(), e.Name())) // #nosec G304 -- a file in the layouts folder
		if err != nil {
			continue
		}
		var t Template
		if jsonx.Unmarshal(data, &t) != nil {
			continue
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load reads the template called name.
func Load(name string) (Template, error) {
	data, err := os.ReadFile(Path(name)) // #nosec G304 -- a file in the layouts folder
	if err != nil {
		return Template{}, err
	}
	var t Template
	if err := jsonx.Unmarshal(data, &t); err != nil {
		return Template{}, errfmt.Errorf("layout %q does not parse: %w", name, err)
	}
	return t, nil
}

// Delete removes the template called name. A template that is not there is
// not an error.
func Delete(name string) error {
	if err := os.Remove(Path(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Filter keeps the templates whose name holds query, ignoring case.
func Filter(ts []Template, query string) []Template {
	if query == "" {
		return ts
	}
	q := strings.ToLower(query)
	var out []Template
	for _, t := range ts {
		if strings.Contains(strings.ToLower(t.Name), q) {
			out = append(out, t)
		}
	}
	return out
}
