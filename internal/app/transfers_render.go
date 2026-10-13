package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// What the copies draw: the dock item, the mark on a machine's rail row, and
// the Copies panel.
//
// The bar is "[####--]": filled cells and empty cells are different glyphs,
// so it reads at 16 colours and with no colour at all. The colour only adds
// to it: the fill is the accent, a copy that waits is the warning token, a
// failed one the error token, a finished one the success token.

// transferDockBarCells is the dock bar's width inside its brackets.
const transferDockBarCells = 6

// transferPanelBarCells is the panel bar's width inside its brackets.
const transferPanelBarCells = 8

// transfersPanelWidth and transfersPanelRows size the Copies panel.
const (
	transfersPanelWidth = 86
	transfersPanelRows  = 10
)

// transferBar draws a bar of cells cells for pct percent, in two inks.
func transferBar(pct, cells int, fill, track, bg color.Color) string {
	n := min(max(pct*cells/100, 0), cells)
	st := func(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(c) }
	return st(track).Render("[") + st(fill).Render(strings.Repeat("#", n)) +
		st(track).Render(strings.Repeat("-", cells-n)) + st(track).Render("]")
}

// transferArrow is the mark for a copy on a machine's rail row.
func transferArrow() string {
	if overlay.UseASCII() {
		return "->"
	}
	return "→"
}

// transfersDockCell is the dock item, or "" while no copy is to be shown.
//
// One copy is a bar and its percent, several are a count and the percent of
// their bytes together. A failed copy the person has not seen outranks both,
// then a copy that waits for an answer, then copies that all wait for their
// machine.
func (m *OS) transfersDockCell() string {
	if !m.transfersShown() {
		return ""
	}
	pal := m.groundUI()
	bg := pal.Panel
	ink := func(c color.Color) lipgloss.Style {
		return lipgloss.NewStyle().Background(bg).Foreground(theme.Readable(c, bg))
	}
	pad := lipgloss.NewStyle().Background(bg).Render(" ")

	var failed, asks, waiting, active int
	var size, done int64
	var one session.TransferRow
	for _, r := range m.transfers.rows {
		switch {
		case r.State == "failed" && !m.transfers.seen[r.ID]:
			failed++
		case transferEnded(r.State):
		default:
			active++
			one = r
			size += r.Size
			done += min(r.Done, r.Size)
			switch r.State {
			case "conflict":
				asks++
			case "waiting":
				waiting++
			}
		}
	}
	var body string
	switch {
	case failed > 0:
		body = ink(pal.Warn).Render(strconv.Itoa(failed) + " failed")
	case asks > 0:
		word := " copy asks"
		if asks > 1 {
			word = " copies ask"
		}
		body = ink(pal.Warning).Render(strconv.Itoa(asks) + word)
	case active > 0 && waiting == active:
		body = ink(pal.Warning).Render("waiting")
	case active == 1:
		pct := transferPercent(one.Done, one.Size)
		tail := strconv.Itoa(pct) + "%"
		if one.State == "paused" {
			tail = "paused"
		}
		body = transferBar(pct, transferDockBarCells, theme.Readable(pal.Accent, bg), theme.Readable(pal.FgMute, bg), bg) +
			pad + ink(pal.FgDim).Render(tail)
	case active > 1:
		body = ink(pal.FgDim).Render(strconv.Itoa(active) + " copies " + strconv.Itoa(transferPercent(done, size)) + "%")
	default:
		// The last copy ended, and the item stays for the linger.
		if m.transfersAnyDoneLately() {
			body = transferBar(100, transferDockBarCells, theme.Readable(pal.Success, bg), theme.Readable(pal.FgMute, bg), bg) +
				pad + ink(pal.Success).Render("done")
		} else {
			body = ink(pal.FgMute).Render("ended")
		}
	}
	return pad + body + pad
}

// transfersAnyDoneLately reports whether a copy ended done within the linger.
func (m *OS) transfersAnyDoneLately() bool {
	cut := time.Now().Add(-transferLinger - time.Second).UnixMilli()
	for _, r := range m.transfers.rows {
		if r.State == "done" && r.Ended >= cut {
			return true
		}
	}
	return false
}

// transfersHostFigure is the mark a machine's rail row shows while it has a
// copy, "→ 61%", in the accent on the row's ground, and its width.
func (m *OS) transfersHostFigure(host string, rowBg color.Color, pal overlay.Palette) (string, int) {
	pct, ok := m.transfersHostMark(host)
	if !ok {
		return "", 0
	}
	text := transferArrow() + " " + strconv.Itoa(pct) + "%"
	return sidebarStyle(rowBg, pal.Accent).Render(text), lipgloss.Width(text)
}

// transfersKeyHint is a footer hint for a Copies action, from what the
// config binds. An action bound to nothing has no hint.
func (m *OS) transfersKeyHint(action, label string) (overlay.Hint, bool) {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	keys := reg.GetTransfersKeys(action)
	if len(keys) == 0 {
		return overlay.Hint{}, false
	}
	key := keys[0]
	if key == "enter" {
		key = overlay.EnterGlyph
	}
	return overlay.Hint{Key: key, Label: label}, true
}

// transfersKey is the first key bound now to a Copies action, as a sentence
// names it, or "" when the config binds it to nothing.
func (m *OS) transfersKey(action string) string {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	for _, k := range reg.GetTransfersKeys(action) {
		if k = strings.TrimSpace(k); k != "" {
			return k
		}
	}
	return ""
}

// transfersPressTo is "Press KEY to WHAT." for a Copies action, or "" when the
// action has no key: a sentence that names a dead key is worse than none.
func (m *OS) transfersPressTo(action, what string) string {
	if key := m.transfersKey(action); key != "" {
		return "Press " + key + " to " + what + "."
	}
	return ""
}

// railSendKey is the key bound now to send a file from the rail's files
// list, or "".
func (m *OS) railSendKey() string {
	reg := m.KeybindRegistry
	if reg == nil {
		reg = defaultInboxKeys()
	}
	for _, k := range reg.GetSidebarFilesKeys("file_send") {
		if k = strings.TrimSpace(k); k != "" {
			return k
		}
	}
	return ""
}

// transfersEmptyText is what the empty panel says: how to send a file.
func (m *OS) transfersEmptyText() string {
	if m.transfers.unsupported {
		return "This daemon cannot copy files to a host. Restart it with a newer tuios."
	}
	if key := m.railSendKey(); key != "" {
		return "No copies. Press " + key + " on a file in the rail to send it to a host."
	}
	return "No copies. Open the menu of a file in the rail to send it to a host."
}

// transfersHints is the panel's footer, for the copy under the cursor.
func (m *OS) transfersHints() []overlay.Hint {
	var out []overlay.Hint
	add := func(action, label string) {
		if h, ok := m.transfersKeyHint(action, label); ok {
			out = append(out, h)
		}
	}
	if row, ok := m.selectedTransfer(); ok {
		switch row.State {
		case "conflict":
			add(config.ActionTransfersAnswer, "answer")
		case "paused", "waiting":
			add(config.ActionTransfersPause, "go on")
		case "failed":
			add(config.ActionTransfersRetry, "retry")
		}
		if !transferEnded(row.State) && row.State != "paused" && row.State != "waiting" {
			add(config.ActionTransfersPause, "pause")
		}
		if !transferEnded(row.State) {
			add(config.ActionTransfersCancel, "cancel")
		}
	}
	add(config.ActionTransfersClear, "clear ended")
	add(config.ActionTransfersClose, "close")
	return out
}

// renderTransfers draws the Copies panel on the shared list overlay: one row
// per copy, newest first, through pal.Row like every list.
func (m *OS) renderTransfers() (string, overlay.Geometry, []overlayRowHit) {
	rows := m.transfers.rows
	m.clampTransfersSelection()
	empty := m.transfersEmptyText()
	return m.renderListOverlay(listOverlay{
		Title:      "Copies",
		Width:      transfersPanelWidth,
		MaxVisible: transfersPanelRows,
		Count:      len(rows),
		Selected:   m.transfers.selected,
		Scroll:     &m.transfers.scroll,
		EmptyMsg:   empty,
		Unfocused:  m.transfers.dialog != nil,
		Hints:      m.transfersHints(),
		RenderRow: func(i int, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
			return transferPanelRow(rows[i], selected, rowBg, pal, width)
		},
		DetailFor: func(width int) []string {
			row, ok := m.selectedTransfer()
			if !ok {
				return nil
			}
			return m.transferDetail(row, width)
		},
	})
}

// transferRoute is "this machine -> build": where a copy goes from and to.
func transferRoute(row session.TransferRow) string {
	return transferPlace(row.Src.Host) + " -> " + transferPlace(row.Dst.Host)
}

// transferStatus is the right-hand part of a panel row, and its ink.
func transferStatus(row session.TransferRow, pal overlay.Palette, bg color.Color) string {
	st := func(c color.Color) lipgloss.Style { return overlay.Style(bg).Foreground(c) }
	pct := transferPercent(row.Done, row.Size)
	bytes := transferBytes(row.Done) + "/" + transferBytes(row.Size)
	switch row.State {
	case "running", "verifying", "queued", "paused":
		bar := transferBar(pct, transferPanelBarCells, pal.Accent, pal.FgMute, bg)
		text := " " + padLeft(strconv.Itoa(pct)+"%", 4) + "  " + bytes
		switch row.State {
		case "running":
			if row.Rate > 0 {
				text += "  " + transferBytes(int64(row.Rate)) + "/s"
			}
			if row.ETAms > 0 {
				text += "  " + units.Duration(time.Duration(row.ETAms)*time.Millisecond)
			}
		case "verifying":
			text += "  checking"
		case "queued":
			text += "  queued"
		case "paused":
			text += "  paused"
		}
		return bar + st(pal.FgDim).Render(text)
	case "waiting":
		text := "waiting for " + transferPlace(transferFarHost(row))
		if row.RetryInMs > 0 {
			text += ", next try in " + units.Duration(time.Duration(row.RetryInMs)*time.Millisecond)
		}
		return st(pal.Warning).Render(text)
	case "conflict":
		n := max(row.ConflictCount, 1)
		return st(pal.Warning).Render(countOf(n, "file") + " there, waits for an answer")
	case "done":
		text := "done"
		if row.Kind == "dir" {
			text += ", " + countOf(row.Files, "file")
		}
		if row.Verified {
			text += ", checked"
		}
		return st(pal.Success).Render(text)
	case "failed":
		return st(pal.Warn).Render("failed")
	case "cancelled":
		return st(pal.FgMute).Render("cancelled")
	}
	return st(pal.FgMute).Render(row.State)
}

// transferFarHost is the machine of a copy that is not this one.
func transferFarHost(row session.TransferRow) string {
	if row.Dst.Host != "" {
		return row.Dst.Host
	}
	return row.Src.Host
}

// transferPanelRow draws one copy: its name and route on the left, keeping
// their room, and its progress or its end on the right.
func transferPanelRow(row session.TransferRow, selected bool, rowBg color.Color, pal overlay.Palette, width int) string {
	nameInk := pal.FgDim
	if selected {
		nameInk = pal.Fg
	}
	name := printableTitle(row.Name)
	if row.Kind == "dir" {
		name += "/"
	}
	status := transferStatus(row, pal, rowBg)
	route := transferRoute(row)
	statusW := ansi.StringWidth(status)
	room := max(width-statusW-ansi.StringWidth(route)-8, 8)
	left := overlay.Style(rowBg).Foreground(nameInk).Bold(selected).Render(overlay.Truncate(name, min(room, 28))) +
		overlay.Style(rowBg).Render("  ") +
		overlay.Style(rowBg).Foreground(pal.FgMute).Render(route)
	return listRowSpans(width, listRowMarker(selected), left, status, rowBg, pal)
}

// transferDetail is the lines under the list about the copy under the
// cursor: where it goes, and what to do next.
func (m *OS) transferDetail(row session.TransferRow, width int) []string {
	cut := func(s string) string { return " " + overlay.Truncate(s, max(width-2, 1)) }
	where := row.Dst.Path
	if row.Final != "" {
		where = row.Final
	} else if row.Target != "" {
		where = row.Target
	}
	// Paths are cut from the front: the tail names the file.
	at := func(label, p string) string {
		label += ": "
		return " " + label + truncPathLeft(printableTitle(p), max(width-2-len(label), 1))
	}
	lines := []string{at("From "+transferPlace(row.Src.Host), row.Src.Path)}
	lines = append(lines, at("To "+transferPlace(row.Dst.Host), where))
	switch row.State {
	case "failed":
		text := transferFailedText(row)
		if press := m.transfersPressTo(config.ActionTransfersRetry, "try again"); press != "" {
			text += " " + press
		}
		for _, l := range wrapPlain(text, max(width-2, 1)) {
			lines = append(lines, " "+l)
		}
	case "waiting":
		why := "The link to " + transferPlace(transferFarHost(row)) + " is down. The copy goes on when it is back."
		lines = append(lines, cut(why))
	case "conflict":
		if press := m.transfersPressTo(config.ActionTransfersAnswer, "say what to do with the files that are there"); press != "" {
			lines = append(lines, cut(press))
		}
	case "running", "verifying":
		if row.Kind == "dir" && row.Current != "" {
			lines = append(lines, cut("Now: "+printableTitle(row.Current)+" ("+strconv.Itoa(row.FilesDone)+" of "+strconv.Itoa(row.Files)+" files)"))
		}
	case "done":
		if row.SHA256 != "" {
			lines = append(lines, cut("sha256 "+row.SHA256))
		}
	}
	return lines
}
