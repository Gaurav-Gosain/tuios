package main

import (
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/client"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// hostDraft is the Hosts page's form, which adds a host, or a host's,
// which edits its address.
type hostDraft struct {
	name, addr string
	err        string
	// editing is the name of the host edited, "" for none.
	editing string
	// busy says tuios hosts add runs for the form.
	busy bool
}

// hostsPage is Settings › Hosts: the machines this machine's tuios daemon
// links, as tuios hosts lists them. The page writes the [hosts] table of
// the tuios config through tuios hosts add and remove, and the daemon
// follows it at once.
func (a *App) hostsPage(c *ui.Context, pal *palette) {
	settingsGroup(c, pal, locales.SetHosts(), locales.SetHostsDetail(), func() {
		if len(a.hosts) == 1 {
			settingRow(c, pal, locales.SetHostsNone(), locales.SetHostsNoneDetail(), func() {})
		}
		for _, h := range a.hosts[1:] {
			a.hostRow(c, pal, h)
		}
	})
	settingsGroup(c, pal, locales.SetHostAdd(), locales.SetHostAddDetail(), func() {
		d := &a.ss.host
		ui.Row(c).Padding(10, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			add := func() { a.addHost() }
			textField(c, "host-addr", &d.addr, locales.SetHostAddressPlaceholder(), 0, pal).Font("monospace").OnSubmit(add)
			textField(c, "host-name", &d.name, locales.SetHostNamePlaceholder(defaultHostName(d.addr)), 130, pal).OnSubmit(add)
			if d.busy {
				ui.Spinner(c).Size(14, 14)
			} else if fieldButton(c, "host-add", locales.CommonAdd(), true, pal) {
				add()
			}
		})
		if d.err != "" {
			ui.Text(c, d.err).FontSize(12).TextColor(pal.dangerText).Padding(0, 0, 10, 0).Selectable()
		}
	})
	docLink(c, pal, "hosts-docs", locales.SetHostsDocs(), "https://tuios.dev/docs")
}

// hostRow is a host in Settings › Hosts: its address, how its link stands,
// and what the user may do about it.
func (a *App) hostRow(c *ui.Context, pal *palette, h *host) {
	if groupRows++; groupRows > 1 {
		ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
	}
	if d := &a.ss.hostEdit; d.editing == h.name {
		a.hostEditor(c, pal, h)
		return
	}
	ui.Column(c.Key("host-"+h.name)).Padding(11, 0).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(8, 8).Radius(4).Background(a.hostDotColor(pal, h)).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, h.name).FontSize(13.5).FontWeight(600).TextColor(pal.text).SingleLine()
					ui.Text(c, h.addr()).Font("monospace").FontSize(12).TextColor(pal.textMuted).SingleLine().Ellipsis("…").MinWidth(0).Selectable()
				})
				col := pal.textMuted
				if h.conn == connFailed {
					col = pal.danger
				}
				ui.Text(c, a.hostStatus(h)).FontSize(12).TextColor(col).LineHeight(1.35).Selectable()
			})
			iconButton(c, "edit", editIcon, locales.HostEdit(), pal, func() {
				a.ss.hostEdit = hostDraft{name: h.name, addr: h.addr(), editing: h.name}
			})
			iconButton(c, "reconnect", reloadIcon, locales.HostReconnect(), pal, func() { a.reconnectHost(h) })
			iconButton(c, "remove", closeIcon, locales.HostRemove(), pal, func() { a.ss.confirm = a.removeHostConfirmation(h) })
		})
		ui.Row(c).Padding(0, 0, 0, 18).Gap(8).Children(func() {
			if h.busy != "" {
				ui.Spinner(c).Size(14, 14)
				return
			}
			switch h.rep.Status {
			case client.HostNoDaemon:
				if button(c, "start", locales.HostStartDaemon(), "", true, pal) {
					a.startHostDaemon(h)
				}
			case client.HostNoTuios:
				if button(c, "install", locales.HostInstallTuios(), "", true, pal) {
					a.offerInstall(h)
				}
			case client.HostIncompatible:
				if button(c, "update", locales.HostUpdateAction(h.name), "", true, pal) {
					a.offerHostUpdate(h)
				}
			}
		})
	})
}

// hostEditor is a host's row as the user edits its address.
func (a *App) hostEditor(c *ui.Context, pal *palette, h *host) {
	d := &a.ss.hostEdit
	ui.Column(c.Key("edit-"+h.name)).Padding(10, 0).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			save := func() { a.saveHost(h) }
			ui.Text(c, h.name).FontSize(13.5).FontWeight(600).TextColor(pal.text).SingleLine().Shrink(0)
			textField(c, "edit-addr", &d.addr, locales.SetHostAddressPlaceholder(), 0, pal).Font("monospace").OnSubmit(save)
			if d.busy {
				ui.Spinner(c).Size(14, 14)
				return
			}
			if fieldButton(c, "edit-cancel", locales.CommonCancel(), false, pal) {
				*d = hostDraft{}
			}
			if fieldButton(c, "edit-save", locales.CommonSave(), true, pal) {
				save()
			}
		})
		if d.err != "" {
			ui.Text(c, d.err).FontSize(12).TextColor(pal.dangerText).Selectable()
		}
	})
}

// saveHost gives a host the address the user edited, as tuios hosts add
// with the host's name does. The tuios binary the link found there stays.
func (a *App) saveHost(h *host) {
	d := &a.ss.hostEdit
	addr := strings.TrimSpace(d.addr)
	if addr == "" {
		d.err = locales.SetHostNeedsAddress()
		return
	}
	if addr == h.addr() {
		*d = hostDraft{}
		return
	}
	args := []string{"hosts", "add", h.name, addr}
	if h.rep.Command != "" {
		args = append(args, "--command", h.rep.Command)
	}
	d.busy, d.err = true, ""
	a.hostsCLI(nil, func(err error) {
		d.busy = false
		if err != nil {
			d.err = err.Error()
			return
		}
		*d = hostDraft{}
		a.toast(locales.HostSaved(h.name))
	}, args...)
}

// hostStatus says how the app stands with a host, in a sentence.
func (a *App) hostStatus(h *host) string {
	if t := a.hostProblem(h); t != "" {
		return t
	}
	if h.conn != connReady {
		return a.hostStatusWord(h)
	}
	s := locales.HostStatusUp()
	if sum := a.hostSummary(h); sum != "" {
		s += " · " + sum
	}
	if h.rep.LoadKnown() {
		s += " · " + locales.HostLoad(fmt.Sprintf("%.2f", h.rep.Load1), h.rep.Cores)
	}
	return s
}

// defaultHostName is the name a host of an address takes when the user
// gives none: the machine's name in it.
func defaultHostName(addr string) string {
	addr = strings.TrimSpace(addr)
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	}
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		addr = addr[i+1:]
	}
	addr, _, _ = strings.Cut(addr, ":")
	addr, _, _ = strings.Cut(addr, "/")
	addr, _, _ = strings.Cut(addr, ".")
	var b strings.Builder
	for _, r := range strings.ToLower(addr) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "devbox"
	}
	return b.String()
}

// addHost adds the host of the Hosts page's form, as tuios hosts add does.
// The daemon opens its link at once, and the host shows in the sidebar.
func (a *App) addHost() {
	d := &a.ss.host
	addr := strings.TrimSpace(d.addr)
	name := strings.TrimSpace(d.name)
	if addr == "" {
		d.err = locales.SetHostNeedsAddress()
		return
	}
	if name == "" {
		name = defaultHostName(addr)
	}
	if a.hostNamed(name) != nil {
		d.err = locales.SetHostNameTaken(name)
		return
	}
	d.busy, d.err = true, ""
	a.hostsCLI(nil, func(err error) {
		d.busy = false
		if err != nil {
			d.err = err.Error()
			return
		}
		*d = hostDraft{}
		a.toast(locales.HostAdded(name))
	}, "hosts", "add", name, addr)
}
