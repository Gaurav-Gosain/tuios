package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Copies between machines, as tuios cp makes them: the Files tab starts
// them, and the sidebar's Transfers section shows them, with their
// progress, until the person clears the ones that ended. This machine's
// daemon runs every copy and follows it across a dropped link.

// transfers is what the app knows of the copies: the daemon's list, newest
// first, and the ids whose end the app already told.
type transfers struct {
	rows  []client.Transfer
	told  map[string]bool
	known bool // the list came once: copies that ended before are not told
}

// watchTransfers follows the copies of this machine's daemon.
func (a *App) watchTransfers(cl *client.Client) {
	cl.WatchTransfers(func() { a.update(a.refreshTransfers) })
	a.refreshTransfers()
}

// refreshTransfers reads the copies again, off the main thread.
func (a *App) refreshTransfers() {
	cl := a.local().cl
	if cl == nil {
		return
	}
	go func() {
		rows, err := cl.Transfers()
		if err != nil {
			return
		}
		a.update(func() { a.applyTransfers(rows) })
	}()
}

// applyTransfers takes the daemon's list, and tells each copy that ended
// since the last one.
func (a *App) applyTransfers(rows []client.Transfer) {
	t := &a.xfer
	if t.told == nil {
		t.told = map[string]bool{}
	}
	slices.SortStableFunc(rows, func(x, y client.Transfer) int { return int(y.Created - x.Created) })
	for _, r := range rows {
		if !transferEnded(r.State) || t.told[r.ID] {
			continue
		}
		t.told[r.ID] = true
		if !t.known {
			continue
		}
		switch r.State {
		case "done":
			a.toast(locales.TransferDone(r.Name, transferPlace(r.Dst.Host)))
		case "failed":
			a.toast(locales.TransferFailed(r.Name, r.Error))
		}
	}
	t.rows, t.known = rows, true
}

func transferEnded(state string) bool {
	return state == "done" || state == "failed" || state == "cancelled"
}

// transferPlace names a machine of a copy: the host, "This computer" for
// this one.
func transferPlace(host string) string {
	if host == "" {
		return locales.HostThisComputer()
	}
	return host
}

// copyTo copies a file or folder of a host to another machine's receive
// folder (its ~/Downloads/tuios, unless its owner set files_roots).
func (a *App) copyTo(from *host, src string, to *host) {
	cl := a.local().cl
	if cl == nil {
		return
	}
	s := client.Endpoint{Host: from.name, Path: src}
	d := client.Endpoint{Host: to.name}
	name := path.Base(strings.ReplaceAll(src, `\`, "/"))
	go func() {
		_, err := cl.StartTransfer(s, d)
		a.update(func() {
			if err != nil {
				a.toast(locales.TransferFailed(name, err.Error()))
				return
			}
			a.toast(locales.HostCopyStarted(name, transferPlace(to.name)))
			a.refreshTransfers()
		})
	}()
}

// copyMenu adds to a file's menu a Copy to item for each other machine.
func (a *App) copyMenu(m *ui.Menu, from *host, src string) {
	if len(a.hosts) < 2 {
		return
	}
	m.Separator()
	for _, to := range a.hosts {
		if to == from {
			continue
		}
		ready := !to.remote() || to.rep.Status == client.HostUp
		if m.Item(locales.HostCopyTo(to.label())).Disabled(!ready).Chosen() {
			to := to
			a.defer_(func() { a.copyTo(from, src, to) })
		}
	}
}

// transfersSection is the sidebar's list of copies, while there are any.
func (a *App) transfersSection(c *ui.Context, pal *palette) {
	rows := a.xfer.rows
	if len(rows) == 0 {
		return
	}
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	ended := 0
	for _, r := range rows {
		if transferEnded(r.State) {
			ended++
		}
	}
	ui.Box(c).Height(1).Background(pal.text.Alpha(0.07)).Margin(0, 0, 4, 0)
	ui.Row(c.Key("transfers")).Height(26).Margin(0, 0, 2, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.TransfersTitle()).FontSize(12.5).FontWeight(600).TextColor(pal.textMuted).SingleLine()
		ui.Textf(c, "%d", len(rows)).FontSize(12.5).TextColor(pal.textFaint).FontFeatures("tnum")
		ui.Spacer(c)
		if ended > 0 {
			b := ui.ButtonBase(c.Key("clear")).Height(20).Padding(0, 6).Radius(5).AlignItems(ui.Center).Label(locales.TransferClear())
			col := pal.textFaint
			if b.Hovered() {
				b.Background(pal.rowHover)
				col = pal.text
			}
			b.Children(func() { ui.Text(c, locales.TransferClear()).FontSize(11.5).TextColor(col).SingleLine() })
			if b.Clicked() {
				a.clearTransfers()
			}
		}
	})
	for i, r := range rows {
		if i == 3 {
			break
		}
		a.transferRow(c, pal, r)
	}
}

// transferRow is one copy: its name, where it goes, its progress, and a
// button that stops it while it runs.
func (a *App) transferRow(c *ui.Context, pal *palette, r client.Transfer) {
	row := ui.Column(c.Key("xfer-"+r.ID)).Padding(5, 8, 6, rowInset).Gap(4).Radius(8)
	hovered := row.Hovered()
	if hovered {
		row.Background(pal.rowHover)
	}
	tip := r.Src.String() + " → " + transferPlace(r.Dst.Host) + ":" + r.Final
	if r.Error != "" {
		tip += "\n" + r.Error
	}
	pointerTooltip(c, row, "tip-xfer-"+r.ID, tip)
	row.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, r.Name).FontSize(12.5).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			ui.Text(c, "→ "+transferPlace(r.Dst.Host)).FontSize(11.5).TextColor(pal.textFaint).SingleLine().Shrink(0)
			ui.Spacer(c)
			if hovered && !transferEnded(r.State) {
				iconButton(c, "cancel", closeIcon, locales.TransferCancel(), pal, func() { a.cancelTransfer(r.ID) })
				return
			}
			ui.Text(c, a.transferState(r)).FontSize(11.5).TextColor(transferColor(pal, r)).SingleLine().Shrink(0).FontFeatures("tnum")
		})
		if !transferEnded(r.State) {
			frac := float32(0)
			if r.Size > 0 {
				frac = float32(r.Done) / float32(r.Size)
			}
			ui.Box(c).Height(3).Radius(1.5).Background(pal.text.Alpha(0.1)).Children(func() {
				ui.Box(c).Height(3).WidthPercent(100 * min(frac, 1)).Radius(1.5).Background(pal.accent)
			})
		}
	})
}

// transferState says in a word or a number how a copy stands.
func (a *App) transferState(r client.Transfer) string {
	switch r.State {
	case "running":
		if r.Size > 0 {
			return fmt.Sprintf("%d%%", r.Done*100/r.Size)
		}
	case "done":
		return "✓"
	}
	return r.State
}

func transferColor(pal *palette, r client.Transfer) ui.Color {
	switch r.State {
	case "failed":
		return pal.danger
	case "done":
		return pal.linkUp()
	case "waiting", "conflict", "paused":
		return pal.attention
	}
	return pal.textFaint
}

func (a *App) cancelTransfer(id string) {
	cl := a.local().cl
	if cl == nil {
		return
	}
	go func() {
		err := cl.CancelTransfer(id)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshTransfers()
		})
	}()
}

func (a *App) clearTransfers() {
	cl := a.local().cl
	if cl == nil {
		return
	}
	go func() {
		_ = cl.ClearTransfers()
		time.Sleep(50 * time.Millisecond)
		a.update(a.refreshTransfers)
	}()
}
