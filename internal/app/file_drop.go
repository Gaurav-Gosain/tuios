package app

import (
	"image"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// # Dropping a file on a pane
//
// A row of the rail's files section can be carried to a pane with the mouse,
// or sent to the focused pane with a key, and its path is pasted there,
// quoted for the program that reads it. This file is the drop path that both
// use: what the pane under the pointer is, whether it may take the path, the
// text it gets, the outline and the label drawn while the gesture is held,
// and the paste itself. A drag between panes over the kitty drag and drop
// protocol (dnd_drag.go) goes through the same functions, dropTargetAt,
// paneTakesDrop, judgeDropPaths and deliverDrop, and draws the same ring and
// label, so a path is quoted and refused by one set of rules whatever carried
// it.
//
// It costs nothing at idle. The state is one struct, armed by a press on a
// listing row and cleared by the release, and motion reaches tuios only while
// a button is held (see program_options.go), so no frame is drawn and no
// process is read unless a drag is under way.
//
// It never moves or copies a file. A drop types a path, which is the
// authority a paste already has. Panes on another machine, and panes whose
// shell runs on another machine over ssh, are refused with the reason: the
// path names a file that is not there.

// dropSource is what a drop carries: one file or folder.
type dropSource struct {
	// Path is absolute, on Host.
	Path string
	// Name is what the label calls it.
	Name string
	Dir  bool
	// Host is the machine the file is on, empty for this one.
	Host string
}

// dropVerdict is what a drop of one source on one pane would do.
type dropVerdict struct {
	// Window is the pane, by id. Empty when the pointer is over no pane.
	Window string
	// Pane names the pane in the label.
	Pane string
	// Program is the program that reads the paste, empty when unknown.
	Program string
	// Text is what the paste sends, the quoted path and a trailing space.
	// Empty when the drop is refused.
	Text string
	// Refusal says why the pane may not take the path, as the label and the
	// notice show it.
	Refusal string
}

// fileDragState is the press-and-drag gesture on a row of the files section.
// The press arms it. While the pointer stays in the rail it is still a click,
// and the release does what a click on the row does. Once the pointer leaves
// the rail it is a drag, and the release over a pane drops the row there.
type fileDragState struct {
	Pressed bool
	// Dragging is set when the pointer first leaves the rail.
	Dragging bool
	// Canceled is set by esc. The gesture then swallows its own release, so a
	// pane under the pointer does not get a release for a press it never saw.
	Canceled bool
	// Secondary says the row was on the second rail, whose rows the click on
	// release has to be resolved against.
	Secondary bool
	Hit       sidebarRowHit
	Src       dropSource
	// Alt is whether alt was held at the last motion: a folder then types a
	// cd instead of its path.
	Alt bool
	// Verdict is the drop on the pane under the pointer, worked out when the
	// pointer reaches a new pane.
	Verdict dropVerdict
}

// FileDragActive reports whether a press on a files row owns the pointer.
func (m *OS) FileDragActive() bool { return m.fileDrag.Pressed }

// armFileDrag starts the gesture on a listing row, or reports false when the
// row cannot be dragged and the press must act as a click now.
func (m *OS) armFileDrag(hit sidebarRowHit) bool {
	if !m.Settings.SidebarFileDrag || m.learnFiles() {
		return false
	}
	t := m.fileMenuTargetFor(hit)
	if t.Name == "" || t.Dir == "" {
		return false
	}
	m.fileDrag = fileDragState{
		Pressed:   true,
		Secondary: m.sidebarDrawing,
		Hit:       hit,
		Src: dropSource{
			Path: filepath.Join(t.Dir, t.Name),
			Name: t.Name,
			Dir:  t.IsDir,
			Host: m.filesView.Host,
		},
	}
	return true
}

// inAnyRail reports whether a screen cell is on either rail.
func (m *OS) inAnyRail(x, y int) bool {
	return m.SidebarBandContains(x, y) || m.secondarySidebarBandContains(x, y)
}

// FileDragMotion follows the pointer. alt is whether alt is held.
func (m *OS) FileDragMotion(x, y int, alt bool) {
	d := &m.fileDrag
	if !d.Pressed || d.Canceled {
		return
	}
	if !d.Dragging {
		if m.inAnyRail(x, y) {
			return // still a click
		}
		d.Dragging = true
		m.tooltipClear()
	}
	d.Alt = alt
	w := m.dropTargetAt(x, y)
	switch {
	case w == nil:
		d.Verdict = dropVerdict{}
	case w.ID != d.Verdict.Window:
		d.Verdict = m.judgeDrop(w, d.Src)
	}
	m.dropPointer(d.Verdict)
}

// FileDragRelease ends the gesture. A drag drops the row on the pane under
// the pointer, and over anything else does nothing. A press that never left
// the rail is a click on the row. alt held on a folder types a cd instead of
// the path.
func (m *OS) FileDragRelease(x, y int, alt bool) {
	d := m.fileDrag
	m.fileDrag = fileDragState{}
	if !d.Pressed || d.Canceled {
		return
	}
	if !d.Dragging {
		click := func() bool {
			m.queueSidebarCmd(m.fileViewEnterRow(d.Hit.WindowIndex, d.Hit.WindowID))
			return true
		}
		if d.Secondary {
			m.withSecondaryRail(click)
		} else {
			click()
		}
		return
	}
	w := m.dropTargetAt(x, y)
	if w == nil {
		return
	}
	// Judged again: the program in the pane can have changed while the
	// pointer rested on it.
	v := m.judgeDrop(w, d.Src)
	m.deliverDrop(v, d.Src, alt && d.Src.Dir)
}

// CancelFileDrag is esc while a row is held. Nothing is dropped.
func (m *OS) CancelFileDrag() {
	if !m.fileDrag.Pressed {
		return
	}
	m.fileDrag = fileDragState{Pressed: true, Canceled: true}
	m.ResetPointerShape()
}

// SidebarFileInsert is the keyboard form of a drop: the path of the row
// under the cursor goes to the focused pane.
func (m *OS) SidebarFileInsert() {
	if !m.filesOn() {
		return
	}
	dir, e, ok := m.fileActionEntry()
	if !ok {
		return
	}
	w := m.GetFocusedWindow()
	if w == nil {
		m.ShowNotification("No pane has the focus. Select a pane first.", "warning", m.Settings.NotificationDuration)
		return
	}
	src := dropSource{Path: filepath.Join(dir, e.Name), Name: e.Name, Dir: e.Dir, Host: m.filesView.Host}
	m.deliverDrop(m.judgeDrop(w, src), src, false)
}

// dropTargetAt is the pane under the screen cell (x, y), or nil. A cell on
// a rail or on the dock is no pane, whatever is drawn under it.
func (m *OS) dropTargetAt(x, y int) *terminal.Window {
	if m.inAnyRail(x, y) || m.InDockBand(y) {
		return nil
	}
	lx, ly, _ := m.PointerToLayout(x, y)
	i := m.WindowAt(lx, ly)
	if i < 0 {
		return nil
	}
	return m.Windows[i]
}

// judgeDrop works out what a drop of src on w does: the text it pastes, or
// why it pastes nothing.
func (m *OS) judgeDrop(w *terminal.Window, src dropSource) dropVerdict {
	if src.Host != "" {
		return dropVerdict{Window: w.ID, Pane: pipPaneName(w),
			Refusal: "The file is on " + src.Host + ". tuios cannot paste a path from another machine yet."}
	}
	path := src.Path
	if src.Dir && filepath.Separator == '/' && !strings.HasSuffix(path, "/") {
		// A folder ends in a slash, so the next word typed can be a name in it.
		path += "/"
	}
	return m.judgeDropPaths(w, []string{path})
}

// paneTakesDrop is the one rule for whether a pane may take a path on this
// machine, whatever carried it: the pane's shell runs here, reports no other
// host, is not ssh or the like, and is not in copy mode. It returns the
// program that reads the paste, and the refusal when there is one.
func (m *OS) paneTakesDrop(w *terminal.Window) (program, refusal string) {
	if !m.paneIsLocal(w) {
		host := w.Host
		if host == "" {
			host = m.AttachedHost
		}
		return "", "This pane is on " + host + ". The file is on this machine."
	}
	if w.CwdHost != "" {
		return "", "The shell in this pane is on " + w.CwdHost + ". The file is not there."
	}
	program = paneProgram(w)
	if remoteShellPrograms[program] {
		return program, "This pane runs " + program + ". The file is not on that machine."
	}
	if w.CopyModeVisible() {
		return program, "This pane is in copy mode. Exit copy mode first."
	}
	return program, ""
}

// judgeDropPaths is the verdict of dropping paths on this machine on w: the
// pane's rules, then each path quoted for the program that reads it. One
// path that cannot be quoted refuses the whole drop, with its reason. With
// no paths yet, the verdict is the pane's alone, and Text is empty.
func (m *OS) judgeDropPaths(w *terminal.Window, paths []string) dropVerdict {
	v := dropVerdict{Window: w.ID, Pane: pipPaneName(w)}
	v.Program, v.Refusal = m.paneTakesDrop(w)
	if v.Refusal != "" || len(paths) == 0 {
		return v
	}
	d := pasteDialectOf(v.Program)
	quoted := make([]string, 0, len(paths))
	for _, p := range paths {
		text, refusal := quoteDropPath(d, p)
		if refusal != "" {
			v.Refusal = refusal
			return v
		}
		quoted = append(quoted, text)
	}
	v.Text = strings.Join(quoted, " ") + " "
	return v
}

// activeDropVerdict is the verdict of whichever drag is held: a row of the
// files section, or a drag between panes once its program started it. ok is
// false when neither is.
func (m *OS) activeDropVerdict() (v dropVerdict, ok bool) {
	if d := &m.fileDrag; d.Dragging && !d.Canceled {
		return d.Verdict, true
	}
	if d := m.dndDrag; d != nil && d.phase == dndStarted {
		return d.verdict, true
	}
	return dropVerdict{}, false
}

// dropPointer sets the pointer shape for a verdict: grabbing over no pane,
// not allowed over a pane that refuses, copy over one that takes the drop.
func (m *OS) dropPointer(v dropVerdict) {
	switch {
	case v.Window == "":
		m.SetPointerShape(PointerGrabbing)
	case v.Refusal != "":
		m.SetPointerShape(PointerNotAllowed)
	default:
		m.SetPointerShape(PointerCopy)
	}
}

// dropArrow is the arrow between what is dropped and where in a label.
func (m *OS) dropArrow() string {
	if m.Settings.UseASCIIOnly {
		return " -> "
	}
	return " → "
}

// dropTargetText names where a verdict lands: the pane and its program.
func dropTargetText(v dropVerdict) string {
	pane := v.Pane
	if v.Program != "" {
		pane += " (" + v.Program + ")"
	}
	return pane
}

// deliverDrop carries out a verdict: it pastes the text into the pane and
// gives the pane the focus, in the mode the person was in. cd types a cd to
// the folder instead, by the rules of every other cd tuios types. A refusal
// is said in a notice.
func (m *OS) deliverDrop(v dropVerdict, src dropSource, cd bool) {
	if v.Refusal != "" {
		m.ShowNotification(v.Refusal, "warning", m.Settings.NotificationDuration*2)
		return
	}
	w := m.windowByID(v.Window)
	if w == nil {
		m.ShowNotification("The pane closed. Nothing was pasted.", "warning", m.Settings.NotificationDuration)
		return
	}
	if i := m.windowIndex(w); i >= 0 && w != m.GetFocusedWindow() {
		m.FocusWindow(i)
	}
	if m.SidebarFocused {
		m.ExitSidebarFocus()
	}
	if cd {
		if why, ok := paneBusyReason(w); !ok {
			m.ShowNotification(why, "warning", m.Settings.NotificationDuration)
			return
		}
		if _, ok := cdLine(src.Path); !ok {
			m.ShowNotification(cdRefusedMessage, "warning", m.Settings.NotificationDuration)
			return
		}
		m.cdAtPrompt(w, src.Path, false, cdUnseenMessage)
		return
	}
	if m.DrawsTML(w) {
		if !m.TMLPaste(w, v.Text) {
			m.ShowNotification("Select a text field in the surface first.", "warning", m.Settings.NotificationDuration)
		}
		return
	}
	if w.InImplicitCopyMode() {
		w.ExitCopyMode()
	}
	// Window.Paste drops control characters and wraps the text in the
	// bracketed paste marks when the program turned them on.
	if err := w.Paste(v.Text); err != nil {
		m.ShowNotification("The path was not pasted: "+err.Error(), "error", m.Settings.NotificationDuration*2)
	}
}

// remoteShellPrograms are the programs whose pane reads its input on another
// machine, where a path on this one names nothing.
var remoteShellPrograms = map[string]bool{
	"ssh": true, "mosh": true, "mosh-client": true, "autossh": true, "et": true,
}

// paneProgram is the name of the program that holds a pane's terminal on
// this machine: the shell at its prompt, or what the shell runs. "" when it
// cannot be read.
//
// The daemon's ForegroundCmd leaves every shell out, so it can say neither
// which shell is at the prompt nor that a shell runs another shell. The
// process table on this machine says both, and the pane is on this machine.
func paneProgram(w *terminal.Window) string {
	if name := session.ForegroundProgram(w.ShellPgid); name != "" {
		return name
	}
	if name := w.ForegroundCommand(); name != "" {
		return strings.ToLower(filepath.Base(name))
	}
	return strings.ToLower(w.ForegroundCmd)
}

// paneDialect is how the program in w reads a quoted path. Only a pane on
// this machine can be asked, through the process table. A pane elsewhere has
// the daemon's ForegroundCmd, which is empty at a shell prompt, and then gets
// POSIX quoting.
func (m *OS) paneDialect(w *terminal.Window) pasteDialect {
	if m.paneIsLocal(w) {
		return pasteDialectOf(paneProgram(w))
	}
	return pasteDialectOf(strings.ToLower(w.ForegroundCmd))
}

// pasteDialect is how a program reads a quoted path.
type pasteDialect int

const (
	// dialectPOSIX is sh, bash, zsh, dash, ksh, and anything not known: single
	// quotes, a quote written as '\''.
	dialectPOSIX pasteDialect = iota
	// dialectFish reads \' and \\ inside single quotes as escapes.
	dialectFish
	// dialectNu has raw single quotes with no escape at all.
	dialectNu
	// dialectDoubled is PowerShell and elvish: single quotes, a quote doubled.
	dialectDoubled
	// dialectCmd is cmd.exe: double quotes, with no escape for " or %.
	dialectCmd
	// dialectCsh is POSIX quoting, but ! expands even inside single quotes.
	dialectCsh
)

// pasteDialectOf picks the dialect by program name.
func pasteDialectOf(program string) pasteDialect {
	switch strings.TrimSuffix(program, ".exe") {
	case "fish":
		return dialectFish
	case "nu", "nushell":
		return dialectNu
	case "pwsh", "powershell", "elvish":
		return dialectDoubled
	case "cmd":
		return dialectCmd
	case "csh", "tcsh":
		return dialectCsh
	}
	return dialectPOSIX
}

// pastePathRefusalControl is the refusal for a name no quoting can carry.
const pastePathRefusalControl = "The name holds a line break or a control character. tuios does not paste it."

// quoteDropPath quotes path for the dialect, or names why it cannot. A path
// that holds only characters no reader splits or expands is left bare.
func quoteDropPath(d pasteDialect, path string) (text, refusal string) {
	for _, r := range path {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == ' ' || r == ' ' {
			return "", pastePathRefusalControl
		}
	}
	switch d {
	case dialectFish:
		if bareWord(path, posixBare) {
			return path, ""
		}
		return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(path) + "'", ""
	case dialectNu:
		if bareWord(path, "") {
			return path, ""
		}
		if !strings.Contains(path, "'") {
			return "'" + path + "'", ""
		}
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(path) + `"`, ""
	case dialectDoubled:
		if bareWord(path, "") {
			return path, ""
		}
		// PowerShell takes the typographic single quotes as quotes too.
		return "'" + strings.NewReplacer("'", "''", "‘", "‘‘", "’", "’’",
			"‚", "‚‚", "‛", "‛‛").Replace(path) + "'", ""
	case dialectCmd:
		if strings.ContainsAny(path, `"%`) {
			return "", "The name holds a \" or a %. cmd cannot take it in quotes. tuios does not paste it."
		}
		if bareWord(path, `\:`) {
			return path, ""
		}
		return `"` + path + `"`, ""
	case dialectCsh:
		if strings.ContainsRune(path, '!') {
			return "", "The name holds a !. csh expands it in quotes too. tuios does not paste it."
		}
	}
	return pastePathText(path), ""
}

// posixBare is what a POSIX shell, and fish, leave alone in a bare word
// besides letters, digits, '/', '.', '_' and '-'. It is the set pastePathText
// uses.
const posixBare = "+:@%,="

// bareWord reports whether s holds only letters, digits, "/._-" and the
// runes in extra.
func bareWord(s, extra string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r) || strings.ContainsRune(extra, r) {
			continue
		}
		return false
	}
	return true
}

// fileDragLabel is the label at the pointer: the row's name and where a
// release would put it, or why it would not.
func (m *OS) fileDragLabel() string {
	d := &m.fileDrag
	name := d.Src.Name
	if d.Src.Dir {
		name += "/"
	}
	v := d.Verdict
	switch {
	case v.Window == "":
		return name + "  Move it to a pane. Esc cancels."
	case v.Refusal != "":
		return name + "  " + v.Refusal
	}
	what := name
	if d.Alt && d.Src.Dir {
		what = "cd " + name
	}
	return what + m.dropArrow() + dropTargetText(v)
}

// renderFileDragLabel is the label's layer, or nil when no drag is under way.
func (m *OS) renderFileDragLabel() *lipgloss.Layer {
	if !m.fileDrag.Dragging || m.fileDrag.Canceled {
		return nil
	}
	return m.renderDropLabel(m.fileDragLabel(), m.fileDrag.Verdict.Refusal != "", "file-drag-label")
}

// renderDropLabel is the layer of a drag's label at the pointer, in the
// warning colour when the drop is refused. The pointer is in screen cells,
// which is where the label goes whatever frame the drag's events are in.
func (m *OS) renderDropLabel(text string, refused bool, id string) *lipgloss.Layer {
	pal := theme.UI()
	renderW := m.GetRenderWidth()
	if refused {
		pal = overlay.Palette{Surface: pal.Surface, Fg: theme.ReadableAt(pal.Warn, pal.Surface, theme.ContrastFloor)}
	}
	label := tooltipLabel(sanitizeLinkText(text), max(renderW-2, 1), pal)
	x, y := m.LastMouseX+2, m.LastMouseY+1
	if y >= m.viewReserve().Top+m.ViewUsableHeight() {
		y = m.LastMouseY - 1
	}
	return tooltipLayer(label, x, y, renderW, id)
}

// applyDropRing outlines the pane a release would drop on, for whichever
// drag is held: a heavy frame in the accent for a pane that takes the drop,
// in the warning colour for one that refuses it. The heavy glyphs make it
// read as a target at a glance, and unlike the pane's own border colour they
// survive every depth. At 16 colours the frame also takes the palette's
// cursor attributes (reverse video for a target that takes the drop, an
// underline for a refusal), since the accent is only an ANSI slot there.
func (m *OS) applyDropRing(canvas *frameCanvas, id string) {
	v, ok := m.activeDropVerdict()
	if !ok || v.Window != id {
		return
	}
	w := m.windowByID(id)
	if w == nil {
		return
	}
	rect := image.Rect(w.X, w.Y, w.X+w.Width, w.Y+w.Height)
	area := canvas.Bounds()
	if sv := m.sessionView; sv.on {
		rect = rect.Add(image.Pt(sv.dx, sv.dy))
		area = area.Intersect(sv.clip)
	}
	clip := rect.Intersect(area)
	if clip.Empty() {
		return
	}
	pal := theme.UI()
	look := m.dropZoneLook()
	ink, st := pal.Accent, overlay.RowState{Cursor: true, Focused: true}
	if v.Refusal != "" {
		ink, st = pal.Warn, overlay.RowState{Cursor: true}
	}
	ring := uv.Cell{Width: 1, Style: uv.Style{Fg: theme.ReadableAt(ink, look.bg, theme.MarkFloor), Bg: look.ringBg, Attrs: uv.AttrBold}}
	mark := pal.MarkCell(st)
	border := lipgloss.ThickBorder()
	if m.Settings.UseASCIIOnly {
		border = lipgloss.ASCIIBorder()
	}
	for y := clip.Min.Y; y < clip.Max.Y && y < len(canvas.Lines); y++ {
		row := canvas.Lines[y]
		for x := clip.Min.X; x < clip.Max.X && x < len(row); x++ {
			if g := dropRingGlyph(&border, rect, x, y); g != "" {
				cell := ring
				cell.Content = g
				if mark != nil {
					mark(&cell)
				}
				row.Set(x, &cell)
			}
		}
	}
}

// fileDragHeldKey is the key of the files row a drag is carrying, or "".
// The rail draws that row as picked up until the drop or the cancel.
func (m *OS) fileDragHeldKey() string {
	if d := &m.fileDrag; d.Dragging && !d.Canceled {
		return d.Hit.WindowID
	}
	return ""
}

// dropRingOn reports whether the compose pass has a ring to draw.
func (m *OS) dropRingOn() bool {
	v, ok := m.activeDropVerdict()
	return ok && v.Window != ""
}
