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

type dropSource struct {
	Path string
	Name string
	Dir  bool
	Host string
}

type dropVerdict struct {
	Window  string
	Pane    string
	Program string
	Text    string
	Refusal string
}

type fileDragState struct {
	Pressed        bool
	Dragging       bool
	PressX, PressY int
	Copy           bool
	Canceled       bool
	Secondary      bool
	Hit            sidebarRowHit
	Src            dropSource
	Alt            bool
	Verdict        dropVerdict
}

// FileDragActive reports whether a press on a files row owns the pointer.
func (m *OS) FileDragActive() bool { return m.fileDrag.Pressed }

func (m *OS) armFileDrag(hit sidebarRowHit, x, y int) bool {
	if !m.Settings.SidebarFileDrag || m.learnFiles() {
		return false
	}
	t := m.fileMenuTargetFor(hit)
	if t.Name == "" || t.Dir == "" {
		return false
	}
	m.fileDrag = fileDragState{
		Pressed:   true,
		PressX:    x,
		PressY:    y,
		Copy:      m.sidebarPressCopy,
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
		if abs(x-d.PressX)+abs(y-d.PressY) < m.DragThreshold() {
			return
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
// the pointer, and over anything else does nothing. A press that never moved
// past the drag threshold is a click on the row. alt held on a folder types a
// cd instead of the path.
func (m *OS) FileDragRelease(x, y int, alt bool) {
	d := m.fileDrag
	m.fileDrag = fileDragState{}
	if !d.Pressed || d.Canceled {
		return
	}
	if !d.Dragging {
		click := func() bool {
			m.fileRowClick(d.Hit, d.Copy)
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
	v := m.judgeDrop(w, d.Src)
	m.deliverDrop(v, d.Src, alt && d.Src.Dir)
}

func (m *OS) fileRowClick(hit sidebarRowHit, copyPath bool) {
	t := m.fileMenuTargetFor(hit)
	switch {
	case t.Name == "" || t.Dir == "":
		m.queueSidebarCmd(m.fileViewEnterRow(hit.WindowIndex, hit.WindowID))
	case copyPath:
		m.queueSidebarCmd(m.copyFilePath(filepath.Join(t.Dir, t.Name)))
	case t.IsDir || m.learnFiles():
		m.queueSidebarCmd(m.fileViewEnterRow(hit.WindowIndex, hit.WindowID))
	default:
		m.queueSidebarCmd(m.quickLookMenu(t))
	}
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

func (m *OS) judgeDrop(w *terminal.Window, src dropSource) dropVerdict {
	if src.Host != "" {
		return dropVerdict{Window: w.ID, Pane: pipPaneName(w),
			Refusal: "The file is on " + src.Host + ". tuios cannot paste a path from another machine yet."}
	}
	path := src.Path
	if src.Dir && filepath.Separator == '/' && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return m.judgeDropPaths(w, []string{path})
}

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

func (m *OS) activeDropVerdict() (v dropVerdict, ok bool) {
	if d := &m.fileDrag; d.Dragging && !d.Canceled {
		return d.Verdict, true
	}
	if d := m.dndDrag; d != nil && d.phase == dndStarted {
		return d.verdict, true
	}
	return dropVerdict{}, false
}

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

func (m *OS) dropArrow() string {
	if m.Settings.UseASCIIOnly {
		return " -> "
	}
	return " → "
}

func dropTargetText(v dropVerdict) string {
	pane := v.Pane
	if v.Program != "" {
		pane += " (" + v.Program + ")"
	}
	return pane
}

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
	if err := w.Paste(v.Text); err != nil {
		m.ShowNotification("The path was not pasted: "+err.Error(), "error", m.Settings.NotificationDuration*2)
	}
}

var remoteShellPrograms = map[string]bool{
	"ssh": true, "mosh": true, "mosh-client": true, "autossh": true, "et": true,
}

func paneProgram(w *terminal.Window) string {
	if name := session.ForegroundProgram(w.ShellPgid); name != "" {
		return name
	}
	if name := w.ForegroundCommand(); name != "" {
		return strings.ToLower(filepath.Base(name))
	}
	return strings.ToLower(w.ForegroundCmd)
}

func (m *OS) paneDialect(w *terminal.Window) pasteDialect {
	if m.paneIsLocal(w) {
		return pasteDialectOf(paneProgram(w))
	}
	return pasteDialectOf(strings.ToLower(w.ForegroundCmd))
}

type pasteDialect int

const (
	dialectPOSIX pasteDialect = iota
	dialectUnknown
	dialectFish
	dialectNu
	dialectDoubled
	dialectCmd
	dialectCsh
)

func pasteDialectOf(program string) pasteDialect {
	switch strings.TrimSuffix(program, ".exe") {
	case "":
		return dialectPOSIX
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
	case "sh", "bash", "rbash", "zsh", "dash", "ksh", "ksh93", "mksh", "lksh",
		"oksh", "loksh", "pdksh", "posh", "yash", "ash", "hush":
		return dialectPOSIX
	}
	return dialectUnknown
}

const pastePathRefusalControl = "The name holds a line break or a control character. tuios does not paste it."

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
	case dialectUnknown:
		if !pastePathIsWindows(path) && (strings.Contains(path, `\\`) || strings.Contains(path, `\'`) || strings.HasSuffix(path, `\`)) {
			return quoteOutsideSingles(path), ""
		}
	case dialectCsh:
		if strings.ContainsRune(path, '!') {
			return "", "The name holds a !. csh expands it in quotes too. tuios does not paste it."
		}
	}
	return pastePathText(path), ""
}

func quoteOutsideSingles(path string) string {
	var b strings.Builder
	run := 0
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c != '\'' && c != '\\' {
			continue
		}
		if i > run {
			b.WriteByte('\'')
			b.WriteString(path[run:i])
			b.WriteByte('\'')
		}
		b.WriteByte('\\')
		b.WriteByte(c)
		run = i + 1
	}
	if run < len(path) {
		b.WriteByte('\'')
		b.WriteString(path[run:])
		b.WriteByte('\'')
	}
	return b.String()
}

const posixBare = "+:@%,="

func bareWord(s, extra string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-", r) || strings.ContainsRune(extra, r) {
			continue
		}
		return false
	}
	return true
}

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

func (m *OS) renderFileDragLabel() *lipgloss.Layer {
	if !m.fileDrag.Dragging || m.fileDrag.Canceled {
		return nil
	}
	return m.renderDropLabel(m.fileDragLabel(), m.fileDrag.Verdict.Refusal != "", "file-drag-label")
}

func (m *OS) renderDropLabel(text string, refused bool, id string) *lipgloss.Layer {
	pal := theme.UI()
	renderW := m.GetRenderWidth()
	if refused {
		pal = overlay.Palette{Surface: pal.Surface, Fg: theme.ReadableAt(pal.Warn, pal.Surface, theme.ContrastFloor)}
	}
	label := tooltipLabel(sanitizeLinkText(text), max(renderW-2, 1), pal)
	return m.pointerLabelLayer(label, id)
}

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

func (m *OS) fileDragHeldKey() string {
	if d := &m.fileDrag; d.Dragging && !d.Canceled {
		return d.Hit.WindowID
	}
	return ""
}

func (m *OS) dropRingOn() bool {
	v, ok := m.activeDropVerdict()
	return ok && v.Window != ""
}
