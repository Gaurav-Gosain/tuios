package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Multi copy mode is copy mode on every pane of the multifocus set at once.
//
// Multifocus already means "the keys go to all of these panes" in terminal
// mode, so copy mode follows the same set: each pane gets its own cursor and
// selection, and every copy-mode key moves them all in lockstep, the way a
// multi-cursor editor does. Search moves each pane to its own match. A pane
// the search finds nothing in is parked: it keeps its cursor, is drawn dimmed,
// and stays out of the selection until a later search finds something in it.
// The yank takes exactly what is highlighted in each pane that has a
// selection and formats the lot as one block.
//
// The state is client-side, like the multifocus set it comes from. The key
// handling lives in the input package (copymode_multi.go); this file owns the
// set of panes, the format, and where the yank goes.

// paletteMultiCopyName is the palette entry that starts multi copy mode.
const paletteMultiCopyName = "Multi copy mode"

// multiCopyParkedDim is how far, in percent, a parked pane is dimmed toward
// its ground.
const multiCopyParkedDim = 45

// multiCopyLargeYank is the size, in bytes, past which a yank warns that the
// terminal may cut the OSC 52 write.
const multiCopyLargeYank = 100 * 1024

// MultiCopy is multi copy mode's state.
type MultiCopy struct {
	// IDs are the panes in the mode, in window-list order, which is the order
	// the yank lists them in.
	IDs []string
	// Format is what the next yank produces: one of config.MultiCopyFormats.
	Format string
	// Parked holds the panes the last search found nothing in.
	Parked map[string]bool
	// Save is the path prompt while one is open, or nil.
	Save *MultiCopySave
}

// MultiCopySave is the open "save to a file" prompt. The panes are captured
// when the prompt opens, so the file holds what was highlighted then even if a
// pane prints more while the path is typed.
type MultiCopySave struct {
	Path  string
	Panes []MultiCopyPane
	// Err is the last save's failure, shown under the prompt so the person can
	// fix the path and try again.
	Err string
}

// Has reports whether the pane with this id is in the mode.
func (mc *MultiCopy) Has(id string) bool {
	if mc == nil {
		return false
	}
	for _, v := range mc.IDs {
		if v == id {
			return true
		}
	}
	return false
}

// multiCopyCandidates is the panes multi copy mode would take: the multifocus
// set's panes that are on screen now, in window-list order.
func (m *OS) multiCopyCandidates() []*terminal.Window {
	if len(m.MultifocusSet) == 0 {
		return nil
	}
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w == nil || !m.MultifocusSet[w.ID] || w.Workspace != m.CurrentWorkspace || w.Minimized || w.Terminal == nil {
			continue
		}
		out = append(out, w)
	}
	return out
}

// MultiCopyEligible reports whether entering copy mode now enters multi copy
// mode: the focused pane is in the multifocus set and at least one other pane
// of the set is on screen with it. The pane count comes back for labels.
func (m *OS) MultiCopyEligible() (int, bool) {
	fw := m.GetFocusedWindow()
	if fw == nil || !m.MultifocusSet[fw.ID] {
		return 0, false
	}
	n := len(m.multiCopyCandidates())
	return n, n >= 2
}

// EnterCopyModeFocused is what the copy-mode key and the palette's "Enter copy
// mode" do: multi copy mode when the focused pane is in a multifocus set of two
// or more visible panes, and plain copy mode on the focused pane otherwise. A
// person who never uses multifocus sees no change.
func (m *OS) EnterCopyModeFocused() {
	if _, ok := m.MultiCopyEligible(); ok {
		m.EnterMultiCopyMode()
		return
	}
	if fw := m.GetFocusedWindow(); fw != nil {
		m.ExitMultiCopyMode()
		// No message says the keys: the dock's legend does, for as long as
		// copy mode is open (see mode_legend.go).
		m.enterCopyMode(fw)
	}
}

// EnterMultiCopyMode puts every visible pane of the multifocus set into copy
// mode together. It reports false, with a message that says what to do, when
// there are fewer than two such panes.
func (m *OS) EnterMultiCopyMode() bool {
	panes := m.multiCopyCandidates()
	if len(panes) < 2 {
		m.ShowNotification("Multi copy mode needs two or more panes in multifocus. Add panes with Ctrl+Shift+Click.", "warning", 2*m.Settings.NotificationDuration)
		return false
	}
	// The keys go to the focused pane first. Focus a pane of the set if the
	// focused one is not in it.
	if fw := m.GetFocusedWindow(); fw == nil || !m.MultifocusSet[fw.ID] {
		for i, w := range m.Windows {
			if w == panes[0] {
				m.FocusWindow(i)
				break
			}
		}
	}
	mc := &MultiCopy{Format: m.Settings.MultiCopyFormat}
	if mc.Format == "" {
		mc.Format = nextMultiCopyFormat("")
	}
	for _, w := range panes {
		m.enterCopyMode(w)
		mc.IDs = append(mc.IDs, w.ID)
	}
	// The mode pill says "MULTI" and the pane count, and the dock's legend
	// says the keys, so no message repeats either over the legend.
	m.MultiCopy = mc
	return true
}

// ExitMultiCopyMode takes every pane of the mode out of copy mode and ends the
// mode. It does nothing when the mode is off.
func (m *OS) ExitMultiCopyMode() {
	mc := m.MultiCopy
	if mc == nil {
		return
	}
	m.MultiCopy = nil
	for _, id := range mc.IDs {
		if w := m.windowByID(id); w != nil {
			if w.InCopyMode() {
				w.ExitCopyMode()
			}
			w.InvalidateCache()
		}
	}
}

// SettleMultiCopy keeps multi copy mode honest after anything the person did.
//
// A pane that was minimised or moved to another workspace is off screen, so it
// leaves the mode: it leaves copy mode and its id goes. Driving or yanking a
// pane the person cannot see is the one thing the mode must not do.
//
// The whole mode ends when the focused pane has left it: focus moved to a pane
// outside the mode, or the focused pane left copy mode some other way (a
// click, a scroll to the bottom). The other panes would otherwise stay in copy
// mode with nothing driving them.
//
// It runs after every input message (key, mouse, paste) and before a key is
// routed to copy mode.
func (m *OS) SettleMultiCopy() {
	mc := m.MultiCopy
	if mc == nil {
		return
	}
	kept := mc.IDs[:0]
	for _, id := range mc.IDs {
		w := m.windowByID(id)
		if w == nil {
			continue
		}
		if !m.multiCopyOnScreen(w) {
			if w.InCopyMode() {
				w.ExitCopyMode()
			}
			w.InvalidateCache()
			delete(mc.Parked, id)
			continue
		}
		kept = append(kept, id)
	}
	mc.IDs = kept
	fw := m.GetFocusedWindow()
	if fw == nil || !mc.Has(fw.ID) || !fw.InCopyMode() || fw.InImplicitCopyMode() {
		m.ExitMultiCopyMode()
	}
}

// multiCopyOnScreen reports whether a pane can take part in the mode: it is on
// the current workspace and not minimised.
func (m *OS) multiCopyOnScreen(w *terminal.Window) bool {
	return w.Workspace == m.CurrentWorkspace && !w.Minimized
}

// MultiCopyWindows is the panes of the mode that exist and are on screen, in
// order. It changes nothing, so the render path may ask; SettleMultiCopy is
// what takes a pane out.
func (m *OS) MultiCopyWindows() []*terminal.Window {
	if m.MultiCopy == nil {
		return nil
	}
	out := make([]*terminal.Window, 0, len(m.MultiCopy.IDs))
	for _, id := range m.MultiCopy.IDs {
		if w := m.windowByID(id); w != nil && m.multiCopyOnScreen(w) {
			out = append(out, w)
		}
	}
	return out
}

// MultiCopyLead is the pane whose state decides what a key means in multi copy
// mode: the focused pane, unless the last search found nothing in it, in which
// case the first pane the search did find something in. With every pane
// parked, it is the focused pane.
func (m *OS) MultiCopyLead(focused *terminal.Window) *terminal.Window {
	if focused == nil || !m.MultiCopyParked(focused.ID) {
		return focused
	}
	for _, w := range m.MultiCopyWindows() {
		if w.InCopyMode() && !m.MultiCopyParked(w.ID) {
			return w
		}
	}
	return focused
}

// MultiCopyAllParked reports whether the last search found nothing in any
// pane of the mode.
func (m *OS) MultiCopyAllParked() bool {
	ws := m.MultiCopyWindows()
	if len(ws) == 0 {
		return false
	}
	for _, w := range ws {
		if !m.MultiCopyParked(w.ID) {
			return false
		}
	}
	return true
}

// MultiCopyParked reports whether the pane is parked: the last search in multi
// copy mode found nothing in it.
func (m *OS) MultiCopyParked(id string) bool {
	return m.MultiCopy != nil && m.MultiCopy.Parked[id]
}

// SetMultiCopyParked records which panes the last search found nothing in, and
// redraws the ones whose state changed.
func (m *OS) SetMultiCopyParked(parked map[string]bool) {
	mc := m.MultiCopy
	if mc == nil {
		return
	}
	for _, id := range mc.IDs {
		if mc.Parked[id] != parked[id] {
			if w := m.windowByID(id); w != nil {
				w.InvalidateCache()
			}
		}
	}
	if len(parked) == 0 {
		parked = nil
	}
	mc.Parked = parked
}

// MultiCopyMatched is how many panes of the mode are not parked, and how many
// panes there are.
func (m *OS) MultiCopyMatched() (matched, total int) {
	for _, w := range m.MultiCopyWindows() {
		total++
		if !m.MultiCopyParked(w.ID) {
			matched++
		}
	}
	return matched, total
}

// CycleMultiCopyFormat steps the yank format to the next one and says which.
// An open save prompt follows: its extension changes with the format when the
// path still ends in the old one.
func (m *OS) CycleMultiCopyFormat() {
	mc := m.MultiCopy
	if mc == nil {
		return
	}
	old := mc.Format
	mc.Format = nextMultiCopyFormat(mc.Format)
	if s := mc.Save; s != nil {
		oldExt, newExt := "."+multiCopyFormatExt(old), "."+multiCopyFormatExt(mc.Format)
		if base, ok := strings.CutSuffix(s.Path, oldExt); ok {
			s.Path = base + newExt
		}
	}
	m.ShowNotification("Multi copy format: "+mc.Format, "info", m.Settings.NotificationDuration)
}

// WindowIndex is the pane's position in the window list, or -1.
func (m *OS) WindowIndex(w *terminal.Window) int {
	for i, v := range m.Windows {
		if v == w {
			return i
		}
	}
	return -1
}

// MultiCopyPaneFor describes a pane for the yank, with the lines its selection
// covered.
func (m *OS) MultiCopyPaneFor(w *terminal.Window, text string) MultiCopyPane {
	return MultiCopyPane{
		Index:    m.WindowIndex(w),
		WindowID: w.ID,
		Title:    m.getWindowDisplayName(w),
		Lines:    strings.Split(text, "\n"),
	}
}

// YankMultiCopy puts the panes' selections on the clipboard in the current
// format, sweeps the copy light over each selection, and says what went. The
// selections must still be in place when it is called; the caller clears them
// afterwards. skipped is how many panes of the mode had no selection.
//
// The text goes through appearance.selection.copy_command when it is set.
func (m *OS) YankMultiCopy(panes []MultiCopyPane, skipped int) tea.Cmd {
	return m.yankMultiCopy(panes, skipped, nil)
}

// PipeMultiCopy is YankMultiCopy through the command of a copy-pipe entry.
// The command gets the selections once, joined in the current format, the
// text y would copy.
func (m *OS) PipeMultiCopy(panes []MultiCopyPane, skipped int, pipe config.CopyPipeBinding) tea.Cmd {
	return m.yankMultiCopy(panes, skipped, &pipe)
}

func (m *OS) yankMultiCopy(panes []MultiCopyPane, skipped int, pipe *config.CopyPipeBinding) tea.Cmd {
	mc := m.MultiCopy
	if mc == nil {
		return nil
	}
	if len(panes) == 0 {
		m.ShowNotification("No pane has a selection. Press v or V to select, then press y.", "warning", m.Settings.NotificationDuration)
		return nil
	}
	text := vt.StripSixelMarkers(FormatMultiCopy(mc.Format, panes))
	m.CancelPendingCopy()
	var flash []*terminal.Window
	for _, p := range panes {
		if w := m.windowByID(p.WindowID); w != nil {
			flash = append(flash, w)
		}
	}
	m.NoteCopyFlashMany(flash)
	msg := fmt.Sprintf("Copied %s as %s (%d characters).", paneCount(len(panes)), mc.Format, len(text))
	if skipped > 0 {
		msg += fmt.Sprintf(" %s had no selection.", paneCount(skipped))
	}
	if len(text) > multiCopyLargeYank {
		// OSC 52 carries the text base64 encoded in one escape sequence, and
		// many terminals cut a long one short or drop it.
		m.ShowNotification(msg+" The copy is large. Your terminal may cut it.", "warning", 2*m.Settings.NotificationDuration)
	} else {
		m.ShowNotification(msg, "success", m.Settings.NotificationDuration)
	}
	if pipe != nil {
		return m.PipeYank(text, pipe.Command, pipe.Label())
	}
	return m.Yank(text)
}

func paneCount(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return fmt.Sprintf("%d panes", n)
}

// OpenMultiCopySave opens the path prompt for saving the panes' selections to
// a file. The default path is in the home directory, named for the time, with
// the extension of the current format.
func (m *OS) OpenMultiCopySave(panes []MultiCopyPane) {
	mc := m.MultiCopy
	if mc == nil {
		return
	}
	if len(panes) == 0 {
		m.ShowNotification("No pane has a selection. Press v or V to select, then press Y.", "warning", m.Settings.NotificationDuration)
		return
	}
	name := fmt.Sprintf("tuios-copy-%s.%s", time.Now().Format("20060102-150405"), multiCopyFormatExt(mc.Format))
	mc.Save = &MultiCopySave{Path: "~/" + name, Panes: panes}
}

// CancelMultiCopySave closes the path prompt without writing anything.
func (m *OS) CancelMultiCopySave() {
	if m.MultiCopy == nil || m.MultiCopy.Save == nil {
		return
	}
	m.MultiCopy.Save = nil
	m.ShowNotification("Save cancelled. Nothing was written.", "info", m.Settings.NotificationDuration)
}

// expandHome turns a leading ~ into the home directory.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// homeRelative writes a path under the home directory with ~, so the dock can
// show the file name.
func homeRelative(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return p
}

// MultiCopySaveType adds text to the save prompt's path. A paste arrives
// here: control characters and line breaks are removed, so a pasted path with
// a newline stays a path and never reaches a shell.
func (m *OS) MultiCopySaveType(text string) {
	if m.MultiCopy == nil || m.MultiCopy.Save == nil {
		return
	}
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || session.InvisibleFormatRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	m.MultiCopy.Save.Path += b.String()
	m.MultiCopy.Save.Err = ""
}

// multiCopySaveBase is the directory a relative save path is taken from: the
// focused pane's working directory when its shell reported one on this
// machine (OSC 7), and the home directory otherwise. The tuios process's own
// directory means nothing to the person typing the path.
func (m *OS) multiCopySaveBase() string {
	if fw := m.GetFocusedWindow(); fw != nil && fw.Cwd != "" && filepath.IsAbs(fw.Cwd) {
		return fw.Cwd
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// MultiCopySaveResolved is the full path the save prompt's text names. The
// prompt shows it, so a relative path is never a guess.
func (m *OS) MultiCopySaveResolved() (string, error) {
	if m.MultiCopy == nil || m.MultiCopy.Save == nil {
		return "", errors.New("no save prompt")
	}
	raw := strings.TrimSpace(m.MultiCopy.Save.Path)
	if raw == "" {
		return "", errors.New("empty path")
	}
	path, err := expandHome(raw)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		base := m.multiCopySaveBase()
		if base == "" {
			return "", errors.New("no base directory")
		}
		path = filepath.Join(base, path)
	}
	return filepath.Clean(path), nil
}

// multiCopySaveHost is " on <machine>" when the person is not at the machine
// the file is written on (an SSH or browser client), and empty otherwise.
func (m *OS) multiCopySaveHost() string {
	if !m.RemoteClient || m.SSHIsLoopback {
		return ""
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return ""
	}
	return " on " + host
}

// CommitMultiCopySave writes the captured panes to the prompt's path in the
// current format. It never overwrites a file: a path that exists keeps the
// prompt open with a message, so the person can type another. A write that
// fails removes what it wrote, so the retry is not refused. It reports whether
// the file was written.
func (m *OS) CommitMultiCopySave() bool {
	mc := m.MultiCopy
	if mc == nil || mc.Save == nil {
		return false
	}
	s := mc.Save
	if strings.TrimSpace(s.Path) == "" {
		s.Err = "Type a path."
		return false
	}
	path, err := m.MultiCopySaveResolved()
	if err != nil {
		s.Err = "Cannot find the home directory. Type a full path."
		return false
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		s.Err = homeRelative(path) + " is a folder. Type a file name."
		return false
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		s.Err = "The folder " + homeRelative(dir) + " does not exist."
		return false
	}
	f, err := multiCopyCreate(path)
	switch {
	case errors.Is(err, fs.ErrExist):
		s.Err = "A file is already at this path. Type a different path."
		return false
	case errors.Is(err, fs.ErrPermission):
		s.Err = "You cannot write to " + homeRelative(dir) + "."
		return false
	case err != nil:
		s.Err = "Cannot save: " + err.Error()
		return false
	}
	text := FormatMultiCopy(mc.Format, s.Panes)
	_, werr := f.WriteString(text)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		// The file is new (O_EXCL), so it is ours to remove. Leaving the
		// partial file would make the retry fail as "a file is already here".
		_ = os.Remove(path)
		s.Err = "Cannot save: " + errors.Join(werr, cerr).Error()
		return false
	}
	mc.Save = nil
	m.ShowNotification(fmt.Sprintf("Saved to %s%s (%s, %s).", homeRelative(path), m.multiCopySaveHost(), paneCount(len(s.Panes)), mc.Format),
		"success", 2*m.Settings.NotificationDuration)
	return true
}

// multiCopyFile is what a save writes to. It is an interface so a test can
// make the write fail part way.
type multiCopyFile interface {
	WriteString(string) (int, error)
	Close() error
}

// multiCopyCreate creates the save file. It never opens one that exists.
var multiCopyCreate = func(path string) (multiCopyFile, error) {
	// A failed open returns a nil *os.File. Returned as it is, it would be a
	// non-nil multiCopyFile holding a nil pointer.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return f, nil
}
