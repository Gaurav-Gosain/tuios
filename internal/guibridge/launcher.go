package guibridge

import (
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/pkg/applist"
)

// LauncherEntry is one program.
type LauncherEntry struct {
	// Name is the program's name, what a person types. Path names the entry
	// in the launch command.
	Name string `json:"name"`
	Path string `json:"path"`
	// Display is a desktop entry's own name ("Firefox Web Browser"), and
	// Detail its generic name or comment.
	Display string `json:"display,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// Source is "desktop" for a desktop entry and empty for $PATH.
	Source string `json:"source,omitempty"`
	// Dir is the $PATH folder a program was found in.
	Dir string `json:"dir,omitempty"`
	// Words are other names the entry is found by.
	Words []string `json:"words,omitempty"`
	// Terminal marks a desktop entry that runs in a terminal.
	Terminal bool `json:"terminal,omitempty"`
}

func launcherEntries(es []applist.Entry) []LauncherEntry {
	out := make([]LauncherEntry, 0, len(es))
	for _, e := range es {
		out = append(out, LauncherEntry{Name: e.Name, Path: e.Path, Display: e.Display, Detail: e.Detail, Source: e.Source, Dir: e.Dir, Words: e.Aliases(), Terminal: e.Terminal})
	}
	return out
}

func (m *model) askLauncher() tea.Cmd {
	m.launcherWanted = true
	if cmd := m.os.ScanPathApps(); cmd != nil {
		return cmd
	}
	m.sendLauncher()
	return nil
}

func (m *model) sendLauncher() {
	m.launcherWanted = false
	m.out.JSON(Event{Type: "launcher", Launcher: launcherEntries(m.os.BridgeLauncherEntries())})
}

func (m *model) launch(path string, typeIt bool) (tea.Cmd, error) {
	if path == "" {
		return nil, errors.New("launch needs path, a program of the launcher event")
	}
	for _, e := range m.os.BridgeLauncherEntries() {
		if e.Path != path {
			continue
		}
		if typeIt {
			return m.os.TypeProgram(e), nil
		}
		return m.os.RunProgram(e), nil
	}
	return nil, errfmt.Errorf("no program %s in the launcher", path)
}

func isPathApps(msg tea.Msg) bool {
	_, ok := msg.(app.PathAppsMsg)
	return ok
}
