package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

// The questions a copy asks: cancel a copy, answer a file that is there,
// send a file from the rail to another machine, and send a dropped file to
// the machine of the pane it was dropped on. They are one centred dialog with
// a kind, drawn by overlay.Dialog like the rail's file prompts, because each
// is a sentence and a few answers.
//
// The conflict question opens from the Copies panel, on a copy in state
// conflict, and never by itself. A tuios cp in a pane asks the same question
// in its own terminal, and the daemon cannot say whether the copy has a
// person at a terminal behind it. A dialog that opened by itself would ask a
// second time for every such copy. The dock item says "1 copy asks" instead,
// and enter on the row in the panel opens the question.

// overlayKindCopyDialog is the copy dialog's overlay kind.
const overlayKindCopyDialog = "copydialog"

// overlayKindTransfers is the Copies panel's overlay kind.
const overlayKindTransfers = "transfers"

// copyDialogWidth is the dialog's preferred inner width.
const copyDialogWidth = 58

// transferReceiveDir is the folder a send from the rail goes into on the
// other machine unless the person types another.
const transferReceiveDir = "~/Downloads/tuios/"

// copyDialogKind is what the dialog asks.
type copyDialogKind int

const (
	// copyDialogCancel asks before a copy is cancelled.
	copyDialogCancel copyDialogKind = iota + 1
	// copyDialogConflict asks what to do with a file that is there.
	copyDialogConflict
	// copyDialogSendHost picks the machine a send goes to.
	copyDialogSendHost
	// copyDialogSendDir asks for the folder on that machine.
	copyDialogSendDir
	// copyDialogDrop asks whether to send dropped files or paste the text.
	copyDialogDrop
)

// copyConflictChoices are the conflict question's answers, in drawn order,
// and the choice each sends.
var copyConflictChoices = []string{"Replace", "Keep both", "Skip"}

var copyConflictWire = []string{"replace", "keep-both", "skip"}

// copyDialog is the open question.
type copyDialog struct {
	kind copyDialogKind
	// row is the copy a cancel or a conflict question is about.
	row session.TransferRow
	// rows are the answers, in drawn order.
	rows     []string
	selected int
	// all is the conflict question's check box: the answer is for every
	// file of the copy.
	all bool
	// input and err are the folder field of a send, and why it was refused.
	input string
	err   string
	// src, name and hosts are a send: what goes, its name, and the
	// machines it can go to ("" is this machine).
	src   session.Endpoint
	name  string
	hosts []string
	host  string
	// drop is a dropped file's question.
	drop *dropAsk
}

// dropAsk is a paste of paths into a pane on another machine.
type dropAsk struct {
	window string
	host   string
	text   string
	paths  []string
	size   int64
}

// pendingDrop is a send of dropped files whose copies have not all ended.
type pendingDrop struct {
	window string
	host   string
	paths  []string
	ids    []string
	done   map[string]bool
	failed bool
}

// CopyDialogOpen reports whether the copy dialog owns the keyboard.
func (m *OS) CopyDialogOpen() bool { return m.transfers.dialog != nil }

// openCopyDialog shows a question.
func (m *OS) openCopyDialog(d *copyDialog) {
	switch d.kind {
	case copyDialogCancel:
		d.rows = []string{"Keep copying", "Cancel the copy"}
	case copyDialogDrop:
		d.rows = []string{"Send", "Paste the text"}
	}
	m.transfers.dialog = d
	m.transfersChanged()
}

// CloseCopyDialog hides the dialog and does nothing else.
func (m *OS) CloseCopyDialog() {
	m.transfers.dialog = nil
	m.transfersChanged()
}

// copyDialogRows is how many answer rows the dialog draws, the check box
// included.
func (d *copyDialog) rowCount() int {
	n := len(d.rows)
	if d.kind == copyDialogConflict && d.row.ConflictCount > 1 {
		n++
	}
	return n
}

// CopyDialogMove steps the dialog's cursor.
func (m *OS) CopyDialogMove(delta int) {
	d := m.transfers.dialog
	if d == nil || d.kind == copyDialogSendDir {
		return
	}
	n := d.rowCount()
	if n == 0 {
		return
	}
	d.selected = ((d.selected+delta)%n + n) % n
}

// CopyDialogSelect puts the cursor on row idx.
func (m *OS) CopyDialogSelect(idx int) {
	if d := m.transfers.dialog; d != nil && idx >= 0 && idx < d.rowCount() {
		d.selected = idx
	}
}

// CopyDialogKey handles one key while the dialog is up. text is what the key
// types, for the folder field.
func (m *OS) CopyDialogKey(key, text string) tea.Cmd {
	d := m.transfers.dialog
	if d == nil {
		return nil
	}
	if key == "esc" {
		if d.kind == copyDialogSendDir {
			// Back to the machines, the step before.
			m.openSendHosts(d.src, d.name, d.hosts)
			return nil
		}
		m.CloseCopyDialog()
		return nil
	}
	if d.kind == copyDialogSendDir {
		if textedit.IsWordDeleteKey(key) {
			d.input = textedit.DropLastWord(d.input)
			d.err = ""
			return nil
		}
		switch key {
		case "enter":
			return m.CopyDialogActivate(0)
		case "backspace":
			if r := []rune(d.input); len(r) > 0 {
				d.input = string(r[:len(r)-1])
			}
			d.err = ""
		case "ctrl+u":
			d.input, d.err = "", ""
		case "space":
			d.input += " "
		default:
			if text != "" {
				d.input += printableRunes(text)
			} else if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
				d.input += key
			}
			d.err = ""
		}
		return nil
	}
	switch key {
	case "up", "k", "shift+tab":
		m.CopyDialogMove(-1)
	case "down", "j", "tab":
		m.CopyDialogMove(1)
	case "space":
		if d.kind == copyDialogConflict && d.row.ConflictCount > 1 {
			d.all = !d.all
		}
	case "enter":
		return m.CopyDialogActivate(d.selected)
	}
	return nil
}

// CopyDialogPaste types a paste into the folder field of a send. The other
// questions take no text, and drop it.
func (m *OS) CopyDialogPaste(text string) {
	if d := m.transfers.dialog; d != nil && d.kind == copyDialogSendDir {
		d.input += printableRunes(text)
		d.err = ""
	}
}

// CopyDialogActivate answers the dialog with row idx.
func (m *OS) CopyDialogActivate(idx int) tea.Cmd {
	d := m.transfers.dialog
	if d == nil {
		return nil
	}
	d.selected = idx
	switch d.kind {
	case copyDialogCancel:
		m.CloseCopyDialog()
		if idx != 1 {
			return nil
		}
		return m.transferCallCmd("transfer-cancel", d.row.Name, map[string]any{"id": d.row.ID})
	case copyDialogConflict:
		if idx >= len(d.rows) {
			// The check box row.
			d.all = !d.all
			return nil
		}
		m.CloseCopyDialog()
		params := map[string]any{"id": d.row.ID, "choice": copyConflictWire[idx]}
		if d.all || len(d.row.Conflicts) == 0 {
			params["all"] = true
		} else {
			params["rel"] = d.row.Conflicts[0].Rel
		}
		return m.transferCallCmd("transfer-answer", d.row.Name, params)
	case copyDialogSendHost:
		if idx < 0 || idx >= len(d.hosts) {
			return nil
		}
		m.transfers.dialog = &copyDialog{
			kind: copyDialogSendDir, src: d.src, name: d.name, hosts: d.hosts,
			host: d.hosts[idx], input: transferReceiveDir,
		}
		m.transfersChanged()
		return nil
	case copyDialogSendDir:
		dir := strings.TrimSpace(d.input)
		if dir == "" {
			d.err = "Type a folder."
			return nil
		}
		if !session.IsHostPath(dir) {
			d.err = "Type a full path, or one that starts with ~."
			return nil
		}
		m.CloseCopyDialog()
		return m.startSendCmd(d.src, d.name, d.host, dir)
	case copyDialogDrop:
		m.CloseCopyDialog()
		ask := d.drop
		if idx == 1 {
			if w := m.windowByID(ask.window); w != nil {
				m.PasteIntoWindow(w, ask.text)
			}
			return nil
		}
		return m.sendDropCmd(ask)
	}
	return nil
}

// SidebarFileSend is the rail's send action: the file or folder under the
// cursor goes to another machine. It asks which machine, then which folder.
func (m *OS) SidebarFileSend() tea.Cmd {
	if m.fileActionRefuse() {
		return nil
	}
	name, p, ok := m.fileActionTarget()
	if !ok {
		m.ShowNotification("Put the cursor on a file first.", "info", m.Settings.NotificationDuration)
		return nil
	}
	src := session.Endpoint{Path: p}
	switch {
	case m.filesView.Host != "" && m.AttachedHost != "":
		m.ShowNotification("tuios cannot send a file from a host that you reach through another host.", "warning", m.Settings.NotificationDuration)
		return nil
	case m.filesView.Host != "":
		src.Host = m.filesView.Host
	case m.AttachedHost != "":
		src.Host = m.AttachedHost
	}
	var hosts []string
	if src.Host != "" {
		hosts = append(hosts, "")
	}
	for _, h := range m.FederationHosts {
		if h.Name != src.Host && h.Name != federation.LocalHostName && h.Status == string(federation.StatusUp) {
			hosts = append(hosts, h.Name)
		}
	}
	if len(hosts) == 0 {
		m.ShowNotification("No other host is up. Add a host in Settings, or wait for its link.", "info", m.Settings.NotificationDuration)
		return nil
	}
	m.openSendHosts(src, name, hosts)
	return nil
}

// openSendHosts shows the machines a send can go to.
func (m *OS) openSendHosts(src session.Endpoint, name string, hosts []string) {
	rows := make([]string, len(hosts))
	for i, h := range hosts {
		rows[i] = transferPlace(h)
	}
	m.transfers.dialog = &copyDialog{kind: copyDialogSendHost, src: src, name: name, hosts: hosts, rows: rows}
	m.transfersChanged()
}

// startSendCmd makes the folder on the machine and starts the copy into it.
// A file that is there with other bytes keeps both: the copy gets the name
// "name (from HOST).ext". Nothing is replaced.
func (m *OS) startSendCmd(src session.Endpoint, name, host, dir string) tea.Cmd {
	call := m.transferCaller()
	onHost := m.verbOnHostCaller()
	params := map[string]any{
		"src":      map[string]any{"host": src.Host, "path": src.Path},
		"dst":      map[string]any{"host": host, "path": dir},
		"conflict": "merge",
		"each":     "keep-both",
		"place":    "into",
	}
	return func() tea.Msg {
		if _, err := onHost(host, "file-mkdir", map[string]any{"path": dir}); err != nil {
			return transferCallMsg{Verb: "file-mkdir", Name: name, Err: err}
		}
		raw, err := call("transfer-start", params, transferCallTimeout)
		msg := transferCallMsg{Verb: "transfer-start", Name: name, Err: err}
		if err == nil {
			var row session.TransferRow
			if json.Unmarshal(raw, &row) == nil && row.ID != "" {
				msg.Row = &row
			}
		}
		return msg
	}
}

// verbOnHostCaller calls a verb on a machine's daemon: this one for "", and a
// host's through this daemon's link.
func (m *OS) verbOnHostCaller() func(host, verb string, params map[string]any) (json.RawMessage, error) {
	if m.transfers.call != nil {
		call := m.transfers.call
		return func(host, verb string, params map[string]any) (json.RawMessage, error) {
			p := map[string]any{"host": host}
			maps.Copy(p, params)
			return call(verb, p, transferCallTimeout)
		}
	}
	build := ""
	if m.DaemonClient != nil {
		build = m.DaemonClient.ClientVersion()
	}
	return func(host, verb string, params map[string]any) (json.RawMessage, error) {
		client, err := session.DialVerbClientAs(build)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()
		if host != "" {
			if _, err := client.CallWithTimeout("open-host-connection", map[string]any{"host": host}, transferCallTimeout); err != nil {
				return nil, err
			}
		}
		return client.CallWithTimeout(verb, params, transferCallTimeout)
	}
}

// dropProbeMsg is what a paste of paths into a pane on another machine
// turned out to be: files on this machine, or text.
type dropProbeMsg struct {
	ask      dropAsk
	fallback bool
}

// dropSentMsg is the answer to drop-files.
type dropSentMsg struct {
	ask   dropAsk
	paths []string
	rows  []session.TransferRow
	err   error
}

// DropPaste looks at a bracketed paste before it reaches the focused pane.
// When the pane runs on another machine and the paste is only paths, it
// returns the command that checks the paths are files on this machine, and
// the paste waits for the answer. Otherwise it returns nil and the paste goes
// through as it always did. A pane on this machine is never touched.
func (m *OS) DropPaste(text string) tea.Cmd {
	w := m.GetFocusedWindow()
	if w == nil || m.BrowserClient || (m.RemoteClient && !m.SSHIsLoopback) || !m.IsDaemonSession {
		return nil
	}
	host := w.Host
	switch {
	case host != "" && m.AttachedHost != "":
		return nil
	case host == "":
		host = m.AttachedHost
	}
	if host == "" || host == federation.LocalHostName {
		return nil
	}
	paths, ok := parseDroppedPaths(text)
	if !ok {
		return nil
	}
	ask := dropAsk{window: w.ID, host: host, text: text, paths: paths}
	return func() tea.Msg {
		for _, p := range ask.paths {
			fi, err := os.Stat(p)
			if err != nil {
				return dropProbeMsg{ask: ask, fallback: true}
			}
			if fi.Mode().IsRegular() {
				ask.size += fi.Size()
			}
		}
		return dropProbeMsg{ask: ask}
	}
}

// applyDropProbe asks about the files, or pastes the text that was not files.
func (m *OS) applyDropProbe(msg dropProbeMsg) {
	if msg.fallback {
		if w := m.windowByID(msg.ask.window); w != nil {
			m.PasteIntoWindow(w, msg.ask.text)
		}
		return
	}
	ask := msg.ask
	m.openCopyDialog(&copyDialog{kind: copyDialogDrop, drop: &ask})
}

// sendDropCmd sends the dropped files to the pane's machine.
func (m *OS) sendDropCmd(ask *dropAsk) tea.Cmd {
	call := m.transferCaller()
	a := *ask
	return func() tea.Msg {
		raw, err := call("drop-files", map[string]any{"host": a.host, "paths": a.paths}, transferCallTimeout)
		if err != nil {
			return dropSentMsg{ask: a, err: err}
		}
		var res struct {
			Paths     []string              `json:"paths"`
			Transfers []session.TransferRow `json:"transfers"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return dropSentMsg{ask: a, err: err}
		}
		return dropSentMsg{ask: a, paths: res.Paths, rows: res.Transfers}
	}
}

// applyDropSent waits for the copies of a drop. The far paths are pasted when
// every copy is checked.
func (m *OS) applyDropSent(msg dropSentMsg) tea.Cmd {
	if msg.err != nil {
		m.ShowNotification(transferCallFailed(transferCallMsg{Verb: "drop-files", Name: dropName(msg.ask.paths), Err: msg.err})+" Nothing was pasted.", "error", m.Settings.NotificationDuration*2)
		return nil
	}
	pd := &pendingDrop{window: msg.ask.window, host: msg.ask.host, paths: msg.paths, done: map[string]bool{}}
	for _, r := range msg.rows {
		pd.ids = append(pd.ids, r.ID)
	}
	m.transfers.drops = append(m.transfers.drops, pd)
	var cmds []tea.Cmd
	for _, r := range msg.rows {
		cmds = append(cmds, m.applyTransferRow(r, false))
	}
	m.transfersChanged()
	if len(pd.ids) == 0 {
		cmds = append(cmds, m.finishDrop(pd))
	}
	return tea.Batch(append(cmds, m.transfersLingerCmd())...)
}

// dropCopyEnded counts a copy of a drop that ended, and reports whether the
// copy belongs to one. When the last copy of a drop ends checked, the far
// paths are pasted.
func (m *OS) dropCopyEnded(row session.TransferRow) (tea.Cmd, bool) {
	for _, pd := range m.transfers.drops {
		if !slices.Contains(pd.ids, row.ID) {
			continue
		}
		pd.done[row.ID] = true
		if row.State != "done" {
			pd.failed = true
			if row.State == "failed" {
				m.ShowNotification(transferFailedText(row)+" Nothing was pasted.", "error", m.Settings.NotificationDuration*2)
			}
		}
		if len(pd.done) < len(pd.ids) {
			return nil, true
		}
		return m.finishDrop(pd), true
	}
	return nil, false
}

// finishDrop pastes a drop's far paths once its copies all ended checked.
func (m *OS) finishDrop(pd *pendingDrop) tea.Cmd {
	m.transfers.drops = slices.DeleteFunc(m.transfers.drops, func(x *pendingDrop) bool { return x == pd })
	if pd.failed {
		return nil
	}
	w := m.windowByID(pd.window)
	if w == nil {
		m.ShowNotification("The pane closed before the path was pasted. The file is on "+transferPlace(pd.host)+".", "warning", m.Settings.NotificationDuration)
		return nil
	}
	quoted := make([]string, len(pd.paths))
	for i, p := range pd.paths {
		quoted[i] = pastePathText(p)
	}
	if !m.PasteIntoWindow(w, strings.Join(quoted, " ")) {
		m.ShowNotification("The file is on "+transferPlace(pd.host)+", but its path was not pasted. Exit copy mode first.", "warning", m.Settings.NotificationDuration*2)
		return nil
	}
	what := "path"
	if len(pd.paths) > 1 {
		what = "paths"
	}
	m.ShowNotification("Copied to "+transferPlace(pd.host)+" and checked. Pasted the "+what+" on "+transferPlace(pd.host)+".", "success", m.Settings.NotificationDuration)
	return nil
}

// dropName names what a drop sends: the file, or how many.
func dropName(paths []string) string {
	if len(paths) == 1 {
		return filepath.Base(paths[0])
	}
	return countOf(len(paths), "file")
}

// parseDroppedPaths reads a paste as the paths a terminal sends for files
// dropped on it, and reports whether the paste is only paths.
//
// The forms are the ones the terminals use: iTerm2, Terminal.app, Ghostty and
// WezTerm escape each space and shell character with a backslash; kitty and
// GNOME Terminal put a path in single quotes; WezTerm can use double quotes;
// some terminals send file:// URLs, one a line. Paths are separated by
// spaces or line breaks. Each must be absolute.
func parseDroppedPaths(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64<<10 || strings.ContainsRune(s, 0) {
		return nil, false
	}
	var words []string
	var cur strings.Builder
	in := false
	end := func() {
		if in {
			words = append(words, cur.String())
			cur.Reset()
			in = false
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			end()
			i++
		case c == '\\':
			if i+1 < len(s) {
				cur.WriteByte(s[i+1])
			}
			i += 2
			in = true
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 2
			in = true
		case c == '"':
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					cur.WriteByte(s[i+1])
					i += 2
					continue
				}
				if s[i] == '"' {
					closed = true
					i++
					break
				}
				cur.WriteByte(s[i])
				i++
			}
			if !closed {
				return nil, false
			}
			in = true
		default:
			cur.WriteByte(c)
			i++
			in = true
		}
	}
	end()
	if len(words) == 0 || len(words) > 64 {
		return nil, false
	}
	for i, w := range words {
		if strings.HasPrefix(w, "file://") {
			u, err := url.Parse(w)
			if err != nil || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
				return nil, false
			}
			w = u.Path
		}
		if !strings.HasPrefix(w, "/") || strings.ContainsAny(w, "\r\n") {
			return nil, false
		}
		words[i] = path.Clean(w)
	}
	return words, true
}

// transferBytes writes a size in the binary units, as tuios cp does: 2.00
// GiB.
func transferBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for q := n / unit; q >= unit && exp < 4; q /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	units := "KMGTP"[exp : exp+1]
	switch {
	case v >= 100:
		return fmt.Sprintf("%.0f %siB", v, units)
	case v >= 10:
		return fmt.Sprintf("%.1f %siB", v, units)
	}
	return fmt.Sprintf("%.2f %siB", v, units)
}

// copyDialogTitle is the word in the dialog's top border.
func (d *copyDialog) title() string {
	switch d.kind {
	case copyDialogCancel:
		return "cancel copy"
	case copyDialogConflict:
		return "file is there"
	case copyDialogSendHost:
		return "send to host"
	case copyDialogSendDir:
		return "send to folder"
	case copyDialogDrop:
		return "dropped file"
	}
	return ""
}

// lines is the dialog's sentences, first the question.
func (d *copyDialog) lines() []string {
	name := printableTitle(d.row.Name)
	switch d.kind {
	case copyDialogCancel:
		where := transferPlace(d.row.Dst.Host)
		return []string{"Cancel the copy of " + name + "?", "The parts on " + where + " are removed."}
	case copyDialogConflict:
		return conflictLines(d.row)
	case copyDialogSendHost:
		return []string{"Send " + printableTitle(d.name) + " to which host?", "Only hosts whose link is up are listed."}
	case copyDialogSendDir:
		return []string{"Send " + printableTitle(d.name) + " to " + transferPlace(d.host) + ".", "Type the folder. tuios makes it when it is not there."}
	case copyDialogDrop:
		a := d.drop
		what := printableTitle(dropName(a.paths))
		return []string{
			"Send " + what + " (" + transferBytes(a.size) + ") to " + transferPlace(a.host) + "?",
			"The path on " + transferPlace(a.host) + " is pasted when the copy is checked.",
		}
	}
	return nil
}

// conflictLines says which file is there, and how it differs from the one
// the copy brings.
func conflictLines(row session.TransferRow) []string {
	if len(row.Conflicts) == 0 {
		return []string{"A file is there on " + transferPlace(row.Dst.Host) + "."}
	}
	c := row.Conflicts[0]
	name := printableTitle(path.Base(filepath.ToSlash(c.Rel)))
	first := name + " is there on " + transferPlace(row.Dst.Host) + "."
	var age string
	switch {
	case c.DstMTime > c.SrcMTime:
		age = "newer"
	case c.DstMTime < c.SrcMTime:
		age = "older"
	}
	var size string
	switch diff := c.DstSize - c.SrcSize; {
	case diff > 0:
		size = transferBytes(diff) + " larger"
	case diff < 0:
		size = transferBytes(-diff) + " smaller"
	default:
		size = "the same size"
	}
	second := "The file there is "
	if age != "" {
		second += age + " and "
	}
	second += size + "."
	out := []string{first, second}
	if row.ConflictCount > 1 {
		if others := row.ConflictCount - 1; others == 1 {
			out = append(out, "1 other file of this copy is there too.")
		} else {
			out = append(out, countOf(others, "other file")+" of this copy are there too.")
		}
	}
	return out
}

// renderCopyDialog draws the open question, with its clickable rows.
func (m *OS) renderCopyDialog() (string, overlay.Geometry, []overlayRowHit) {
	d := m.transfers.dialog
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(copyDialogWidth, m.GetRenderWidth())

	var body []string
	for i, l := range d.lines() {
		for _, wrapped := range wrapPlain(l, max(width-2, 1)) {
			ink := pal.FgDim
			if i == 0 {
				body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
					overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(wrapped), width, bg))
				continue
			}
			body = append(body, dialogBodyLine(wrapped, width, ink, bg))
		}
	}

	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "choose"}, {Key: "esc", Label: "close"}}
	var hits []overlayRowHit
	if d.kind == copyDialogSendDir {
		field := fieldTail(printableRunes(d.input), max(width-4, 1))
		body = append(body, overlay.Fill(
			overlay.Style(bg).Render(" ")+
				overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(overlay.Sigil())+
				overlay.Style(bg).Foreground(pal.Fg).Render(field)+
				overlay.Cursor(" ", bg, pal.Fg), width, bg))
		note, ink := "A file that is there stays. The copy gets a new name.", pal.FgMute
		if d.err != "" {
			note, ink = d.err, pal.Warn
		}
		body = append(body, dialogBodyLine(note, width, ink, bg))
		hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "send"}, {Key: "esc", Label: "back"}}
	} else {
		body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg))
		top := len(body)
		for i := range d.rowCount() {
			body = append(body, m.copyDialogRow(d, i, width, pal))
		}
		if d.kind == copyDialogConflict && d.row.ConflictCount > 1 {
			hints = []overlay.Hint{{Key: overlay.EnterKey(), Label: "choose"}, {Key: "space", Label: "all files"}, {Key: "esc", Label: "close"}}
		}
		content, geo := overlay.Dialog{Title: d.title(), Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
		for i := range d.rowCount() {
			y := geo.BodyY + top + i
			hits = append(hits, overlayRowHit{Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1}, Idx: i})
		}
		return content, geo, hits
	}
	content, geo := overlay.Dialog{Title: d.title(), Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
	return content, geo, nil
}

// copyDialogRow draws one answer, or the check box. The cursor row is on
// the Surface ground with the sigil, as the rail's confirmations are; a
// destructive answer takes the warn ink under the cursor.
func (m *OS) copyDialogRow(d *copyDialog, idx, width int, pal overlay.Palette) string {
	bg := pal.Canvas
	cursor := d.selected == idx
	if cursor {
		bg = pal.Surface
	}
	marker := " "
	if cursor {
		marker = overlay.SigilMark()
	}
	var label string
	if idx < len(d.rows) {
		label = d.rows[idx]
	} else {
		box := "[ ]"
		if d.all {
			box = "[x]"
		}
		label = box + " Do this for the other files in this copy"
	}
	ink := pal.FgDim
	if cursor {
		ink = pal.Fg
		if d.kind == copyDialogCancel && idx == 1 {
			ink = pal.Warn
		}
	}
	row := overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(pal.AccentBright).Bold(true).Render(marker) +
		overlay.Style(bg).Render(" ") +
		overlay.Style(bg).Foreground(ink).Bold(cursor).Render(overlay.Truncate(label, max(width-4, 1)))
	return pal.Mark(overlay.Fill(row, width, bg), overlay.RowState{Cursor: cursor, Focused: true})
}
