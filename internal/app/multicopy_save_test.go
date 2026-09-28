package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The save prompt's paths and messages.

func saveOS(t *testing.T) (*OS, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := flashOS(t)
	a := liveWindow(t, "wa")
	m.Windows = []*terminal.Window{a}
	m.FocusedWindow = 0
	m.MultiCopy = &MultiCopy{IDs: []string{"wa"}, Format: config.MultiCopyFormatPlain}
	m.MultiCopy.Save = &MultiCopySave{Panes: []MultiCopyPane{{Index: 0, WindowID: "wa", Lines: []string{"x"}}}}
	return m, home
}

// failingFile writes part of the text to a real file and then fails, the way
// a full disk does.
type failingFile struct{ f *os.File }

func (ff failingFile) WriteString(s string) (int, error) {
	n, _ := ff.f.WriteString(s[:len(s)/2])
	return n, errors.New("no space left on device")
}
func (ff failingFile) Close() error { return ff.f.Close() }

// A write that fails part way removes its file, so the retry is not refused
// as "a file is already at this path".
//
// Negative control: removing the os.Remove on the failure branch fails this.
func TestMultiCopySaveFailureLeavesNoFile(t *testing.T) {
	m, home := saveOS(t)
	path := filepath.Join(home, "out.txt")
	m.MultiCopy.Save.Path = "~/out.txt"

	real := multiCopyCreate
	t.Cleanup(func() { multiCopyCreate = real })
	multiCopyCreate = func(p string) (multiCopyFile, error) {
		f, err := real(p)
		if err != nil {
			return nil, err
		}
		return failingFile{f.(*os.File)}, nil
	}
	if m.CommitMultiCopySave() {
		t.Fatal("a failed write reported success")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the failed write left a file behind: %v", err)
	}
	if !strings.Contains(m.MultiCopy.Save.Err, "no space") {
		t.Errorf("the prompt says %q, want the reason", m.MultiCopy.Save.Err)
	}

	multiCopyCreate = real
	if !m.CommitMultiCopySave() {
		t.Fatalf("the retry was refused: %q", m.MultiCopy.Save.Err)
	}
	if data, _ := os.ReadFile(path); string(data) != "x\n" {
		t.Errorf("the retry wrote %q", data)
	}
}

func TestMultiCopySavePathMessages(t *testing.T) {
	m, home := saveOS(t)

	// A relative path is taken from the focused pane's directory, not from
	// wherever tuios was started.
	cwd := filepath.Join(home, "rack")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	m.Windows[0].Cwd = cwd
	m.MultiCopy.Save.Path = "lldp.txt"
	if got, _ := m.MultiCopySaveResolved(); got != filepath.Join(cwd, "lldp.txt") {
		t.Errorf("a relative path resolves to %q, want it in the pane's directory %q", got, cwd)
	}
	// Without a directory from the pane, the home directory.
	m.Windows[0].Cwd = ""
	if got, _ := m.MultiCopySaveResolved(); got != filepath.Join(home, "lldp.txt") {
		t.Errorf("with no pane directory a relative path resolves to %q, want the home directory", got)
	}

	m.MultiCopy.Save.Path = "~/no/such/dir/x.txt"
	m.CommitMultiCopySave()
	if got, want := m.MultiCopy.Save.Err, "The folder ~/no/such/dir does not exist."; got != want {
		t.Errorf("missing folder: %q, want %q", got, want)
	}

	m.MultiCopy.Save.Path = "~/rack"
	m.CommitMultiCopySave()
	if got, want := m.MultiCopy.Save.Err, "~/rack is a folder. Type a file name."; got != want {
		t.Errorf("folder path: %q, want %q", got, want)
	}

	// The prompt shows the full path.
	m.MultiCopy.Save.Err = ""
	m.MultiCopy.Save.Path = "~/rack/a.txt"
	layer := m.multiCopySaveLayer()
	if layer == nil || !strings.Contains(stripANSIForTrace(layer.GetContent()), filepath.Join(home, "rack", "a.txt")) {
		t.Errorf("the prompt does not show the full path")
	}

	// A remote client is told which machine the file is on.
	m.RemoteClient = true
	host, _ := os.Hostname()
	m.MultiCopy.Save.Path = "~/remote.txt"
	if !m.CommitMultiCopySave() {
		t.Fatalf("save failed: %q", m.MultiCopy.Save.Err)
	}
	if got := lastNotificationText(t, m); !strings.Contains(got, "Saved to ~/remote.txt on "+host) {
		t.Errorf("remote save message %q does not name the machine %q", got, host)
	}
}

// A yank past what OSC 52 carries well says so.
func TestMultiCopyLargeYankWarns(t *testing.T) {
	m, _ := saveOS(t)
	m.MultiCopy.Save = nil
	big := strings.Repeat("x", multiCopyLargeYank+1)
	m.YankMultiCopy([]MultiCopyPane{{WindowID: "wa", Lines: []string{big}}}, 0)
	if got := lastNotificationText(t, m); !strings.Contains(got, "The copy is large. Your terminal may cut it.") {
		t.Errorf("a %d byte yank said %q", len(big), got)
	}
	m.YankMultiCopy([]MultiCopyPane{{WindowID: "wa", Lines: []string{"small"}}}, 0)
	if got := lastNotificationText(t, m); strings.Contains(got, "large") {
		t.Errorf("a small yank warned: %q", got)
	}
}

// The palette offers multi copy mode only with a multifocus set.
func TestMultiCopyPaletteEntryNeedsMultifocus(t *testing.T) {
	m := flashOS(t)
	has := func() bool {
		m.rebuildPaletteItems()
		for _, it := range m.PaletteItems {
			if it.Name == paletteMultiCopyName {
				return true
			}
		}
		return false
	}
	if has() {
		t.Error("the palette offers multi copy mode with no multifocus set")
	}
	m.MultifocusSet = map[string]bool{m.Windows[0].ID: true}
	if !has() {
		t.Error("the palette does not offer multi copy mode with a multifocus set")
	}
}

// The dock help follows the lead pane: with the focused pane parked and the
// others selecting, it shows the visual keys.
func TestMultiCopyHelpFollowsTheLead(t *testing.T) {
	m := flashOS(t)
	a, b := selectedWindowID("wa"), selectedWindowID("wb")
	a.CopyMode.State = terminal.CopyModeNormal
	m.Windows = []*terminal.Window{a, b}
	m.FocusedWindow = 0
	m.MultiCopy = &MultiCopy{IDs: []string{"wa", "wb"}, Format: config.MultiCopyFormatPlain,
		Parked: map[string]bool{"wa": true}}
	if help := hintsText(m.copyModeHelp(a)[0]); !strings.Contains(help, "hjkl extend") {
		t.Errorf("help %q does not follow the lead pane in visual mode", help)
	}
}
