package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/remote"
	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

var (
	hostIcon = glyph("f7-desktopcomputer")
)

// The sidebar is flat: groups, a remote host's tagged with its name (see
// groupView). A host shows only by its groups, and has no row of its own
// unless something there wants the user: hostNeedsRow. One with no
// sessions shows nowhere; Open Project and Settings › Hosts reach it.

// hostNeedsRow reports whether a host has a row of its own in the
// sidebar, besides its groups: while it can't be reached, wants updating
// first, or is busy with what the user asked, as installing Kero.
func (a *App) hostNeedsRow(h *host) bool {
	return h.busy != "" || h.conn == connFailed || h.conn == connVersion
}

// hostLine is a host in the sidebar while it needs a row of its own: its
// header, which tells how the app stands with it, then what keeps its
// sessions from showing, and what to do about it.
func (a *App) hostLine(c *ui.Context, pal *palette, h *host, top float32) {
	a.hostHeader(c, pal, h, top)
	a.hostNote(c, pal, h)
}

// hostHeader is a remote host's name, with how the app stands with it: a
// dot while it connects or failed to, a chip when Kero there wants
// updating.
func (a *App) hostHeader(c *ui.Context, pal *palette, h *host, top float32) {
	hdr := ui.Row(c.Key("host-"+h.name)).Height(30).Margin(top, 0, 2, 0).Padding(0, 6, 0, 8).Gap(7).AlignItems(ui.Center).Radius(6)
	hovered := hdr.Hovered()
	pointerTooltip(c, hdr, "tip-host-"+h.name, a.hostTip(h))
	hdr.ContextMenu(func(m *ui.Menu) { a.hostMenu(m, h) })
	hdr.Children(func() {
		ui.Icon(c, hostIcon).Size(14, 14).TextColor(pal.hostColor(h)).Shrink(0)
		ui.Text(c, h.name).FontSize(13).FontWeight(650).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		ui.Spacer(c)
		a.hostState(c, pal, h)
		if hovered && h.conn == connReady {
			b := ui.ButtonBase(c.Key("add")).Size(20, 20).Radius(5).Center().TextColor(pal.textMuted).
				Label(locales.NewSessionIn(h.name)).Tooltip(locales.ActionNewSession())
			if b.Hovered() {
				b.Background(pal.rowHover).TextColor(pal.text)
			}
			b.Children(func() { ui.Icon(c, plusIcon).Size(13, 13) })
			if b.Clicked() {
				a.newSessionOn(h, "")
			}
		}
	})
}

// hostTag names a remote host in its color, after the name of one of its
// groups, faded while the app has lost it.
func (a *App) hostTag(c *ui.Context, pal *palette, h *host) {
	col := pal.hostColor(h)
	tag := ui.Row(c.Key("host")).Height(18).Padding(0, 6).Radius(5).AlignItems(ui.Center).Shrink(0).
		Background(col.Alpha(0.14)).Tooltip(a.hostTip(h))
	if h.conn != connReady {
		tag.Opacity(0.55)
	}
	tag.Children(func() {
		ui.Text(c, h.name).FontSize(11.5).FontWeight(600).TextColor(col).SingleLine()
	})
}

// hostState says how the app stands with a host, at the end of its row,
// or of its first group's header: a dot while it connects or failed to,
// a chip when Kero there wants updating or kerod restarting.
func (a *App) hostState(c *ui.Context, pal *palette, h *host) {
	switch {
	case h.busy != "":
		ui.Spinner(c).Size(12, 12)
	case h.conn == connConnecting && h.loaded:
		a.hostDot(c, pal, pal.attention, locales.HostReconnecting())
	case h.conn == connConnecting:
		a.hostDot(c, pal, pal.textFaint, locales.StatusConnecting())
	case h.conn == connFailed:
		a.hostDot(c, pal, pal.danger, a.hostProblem(h))
	case h.conn == connVersion || h.behind():
		b := ui.ButtonBase(c.Key("update")).Height(22).Padding(0, 7).Gap(5).Radius(11).AlignItems(ui.Center).
			Label(locales.HostUpdateAction(h.name)).Tooltip(a.hostVersionDetail(h))
		if b.Hovered() {
			b.Background(pal.rowHover)
		}
		b.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(pal.attention)
			ui.Text(c, locales.CommonUpdate()).FontSize(12).FontWeight(500).TextColor(pal.attention).SingleLine()
		})
		if b.Clicked() {
			a.offerHostUpdate(h)
		}
	case h.stale():
		b := ui.ButtonBase(c.Key("restart")).Height(22).Padding(0, 7).Gap(5).Radius(11).AlignItems(ui.Center).
			Label(locales.HostRestartAction(h.name)).Tooltip(locales.HostStaleTip(h.name, h.daemonApp, h.info.Kero))
		if b.Hovered() {
			b.Background(pal.rowHover)
		}
		b.Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(pal.attention)
			ui.Text(c, locales.CommonRestart()).FontSize(12).FontWeight(500).TextColor(pal.attention).SingleLine()
		})
		if b.Clicked() {
			a.offerHostRestart(h)
		}
	}
}

// hostDot says how the app stands with a host: a dot, and its words as
// the tooltip.
func (a *App) hostDot(c *ui.Context, pal *palette, col ui.Color, label string) {
	ui.Box(c).Size(7, 7).Radius(3.5).Background(col).Shrink(0).Margin(0, 4, 0, 0).Label(label).Tooltip(label)
}

// hostTip tells what a host is: its address, and Kero there.
func (a *App) hostTip(h *host) string {
	tip := h.addr.String()
	if i := h.info; i.OS != "" {
		tip += "\n" + locales.HostAbout(i.Kero, i.OS+"/"+i.Arch, i.Hostname, i.User)
	}
	if h.conn == connFailed {
		tip += "\n" + h.err
	}
	return tip
}

// hostProblem words why the app can't reach a host.
func (a *App) hostProblem(h *host) string {
	switch {
	case errors.Is(h.errKind, remote.ErrNotInstalled):
		return locales.HostNotInstalled(h.name)
	case errors.Is(h.errKind, remote.ErrAuth):
		return locales.HostRefused(h.name)
	}
	return locales.HostCantConnect(h.name)
}

// hostNote says, under a host's header, what keeps its sessions from
// showing, and what to do about it.
func (a *App) hostNote(c *ui.Context, pal *palette, h *host) {
	ui.Column(c.Key("note-"+h.name)).Margin(0, 0, 6, 0).Padding(8, 10).Gap(6).Radius(8).Background(pal.chip).Children(func() {
		var title, detail string
		switch {
		case h.busy == "install":
			title = locales.HostInstalling(h.name)
		case h.busy == "update":
			title = locales.HostUpdating(h.name)
		case h.busy == "restart":
			title = locales.HostRestarting(h.name)
		case h.conn == connVersion:
			title, detail = locales.HostVersionTitle(h.name), a.hostVersionDetail(h)
		case errors.Is(h.errKind, remote.ErrNotInstalled):
			// What the host said says it again.
			title = a.hostProblem(h)
		default:
			title, detail = a.hostProblem(h), h.err
			if errors.Is(h.errKind, remote.ErrAuth) && h.addr.Scheme == "ssh" {
				detail += "\n" + locales.HostAuthHint(h.addr.String())
			}
		}
		ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(pal.text)
		if detail != "" {
			ui.Text(c, detail).FontSize(12).TextColor(pal.textMuted).LineHeight(1.35).Selectable()
		}
		if h.busy != "" {
			return
		}
		ui.Row(c).Gap(6).Margin(2, 0, 0, 0).Children(func() {
			switch {
			case h.conn == connVersion:
				if smallButton(c, "update", locales.CommonUpdate(), true, pal) {
					a.offerHostUpdate(h)
				}
			case errors.Is(h.errKind, remote.ErrNotInstalled):
				if smallButton(c, "install", locales.HostInstall(), true, pal) {
					a.installOnHost(h)
				}
			}
			if h.conn == connFailed && smallButton(c, "retry", locales.CommonTryAgain(), !errors.Is(h.errKind, remote.ErrNotInstalled), pal) {
				a.reconnectHost(h)
			}
		})
	})
}

// smallButton is a button for a sidebar's note.
func smallButton(c *ui.Context, key, label string, primary bool, pal *palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 10).Radius(6).AlignItems(ui.Center).Label(label)
	bg, fg := pal.control, pal.text
	if primary {
		bg, fg = pal.accent, ui.Hex("#ffffff")
	}
	if b.Hovered() {
		bg = bg.Mix(pal.text, 0.06)
	}
	b.Background(bg).TextColor(fg).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine()
	})
	return b.Clicked()
}

// hostMenu is a host header's context menu.
func (a *App) hostMenu(m *ui.Menu, h *host) {
	ready := h.conn == connReady
	if m.Item(locales.ActionNewSession()).Disabled(!ready).Chosen() {
		a.newSessionOn(h, "")
	}
	if m.Item(locales.ActionOpenProject()).Disabled(!ready).Chosen() {
		a.openProjectOn(h)
	}
	m.Separator()
	if m.Item(locales.HostReconnect()).Chosen() {
		a.reconnectHost(h)
	}
	if m.Item(locales.HostRestartAction(h.name)).Disabled(h.rc == nil || h.busy != "").Chosen() {
		a.offerHostRestart(h)
	}
	if h.info.Server || h.conn == connVersion {
		if m.Item(locales.HostUpdateAction(h.name)).Disabled(h.rc == nil || h.busy != "" || !released(mygo.App.Version())).Chosen() {
			a.offerHostUpdate(h)
		}
	}
	if errors.Is(h.errKind, remote.ErrNotInstalled) {
		if m.Item(locales.HostInstall()).Disabled(h.busy != "").Chosen() {
			a.installOnHost(h)
		}
	}
	a.colorMenu(m, h)
	m.Separator()
	if m.Item(locales.HostCopyAddress()).Chosen() {
		a.copyText = h.addr.String()
	}
	if m.Item(locales.HostSettings()).Chosen() {
		a.openSettings("hosts")
	}
	if m.Item(locales.HostRemove()).Chosen() {
		a.confirm = a.removeHostConfirmation(h)
	}
}

// reconnectHost connects to a host again, now, with a new connection.
func (a *App) reconnectHost(h *host) {
	a.dropTerms(h)
	if h.cl != nil {
		go h.cl.Close()
		h.cl = nil
	}
	if h.rc != nil {
		go h.rc.Close() // ssh takes a moment to end: never on the main thread
		h.rc = nil
	}
	h.retries, h.err, h.errKind = 0, "", nil
	h.conn = connConnecting
	a.connectHost(h)
}

// removeHostConfirmation asks to remove a host from Settings › Hosts,
// which leaves its sessions running there.
func (a *App) removeHostConfirmation(h *host) *confirmation {
	name := h.name
	return &confirmation{
		open:    true,
		title:   locales.HostRemoveTitle(name),
		detail:  locales.HostRemoveDetail(name),
		action:  locales.CommonRemove(),
		destroy: true,
		run:     func() { a.removeHost(name) },
	}
}

func (a *App) removeHost(name string) {
	s := a.set.Clone()
	var keep = s.Hosts[:0]
	for _, h := range s.Hosts {
		if !strings.EqualFold(h.Name, name) {
			keep = append(keep, h)
		}
	}
	s.Hosts = keep
	a.applySettings(s)
}

// installOnHost asks, then installs Kero on a host the app reaches over
// SSH or WSL, as Kero's install script does, and connects.
func (a *App) installOnHost(h *host) {
	a.confirm = &confirmation{
		open:   true,
		title:  locales.HostInstallTitle(h.name),
		detail: locales.HostInstallDetail(h.name),
		action: locales.CommonInstall(),
		run:    func() { a.install(h) },
	}
	if a.win != nil {
		a.win.Focus()
	}
}

func (a *App) install(h *host) {
	version := mygo.App.Version()
	if !released(version) {
		version = ""
	}
	cmd, err := remote.InstallCommand(h.addr, version, true)
	if err != nil {
		a.toast(err.Error())
		return
	}
	h.busy = "install"
	go func() {
		out, err := cmd.CombinedOutput()
		a.update(func() {
			h.busy = ""
			if !slices.Contains(a.hosts, h) {
				return
			}
			if err != nil {
				msg := strings.TrimSpace(string(out))
				if i := strings.LastIndex(msg, "\n"); i >= 0 {
					msg = msg[i+1:]
				}
				if msg == "" {
					msg = err.Error()
				}
				a.toast(locales.HostInstallFailed(h.name, msg))
				return
			}
			a.toast(locales.HostInstalled(h.name))
			a.reconnectHost(h)
		})
	}()
}

// hostVersionDetail says how Kero on a host differs from this app, and
// what updates which.
func (a *App) hostVersionDetail(h *host) string {
	v, app := h.kero(), mygo.App.Version()
	switch {
	case h.ahead():
		return locales.HostNewerDetail(h.name, v, app)
	case !h.info.Server && !h.info.Updates:
		return locales.HostDesktopDetail(h.name, v, app)
	case !h.info.Updates:
		return locales.HostManualDetail(h.name, v, app)
	}
	return locales.HostVersionDetail(h.name, v, app)
}

// offerHostUpdate updates the server build of Kero on a host to this
// app's version, and restarts kerod there into it, asking first, as the
// restart ends the programs in its sessions. A host newer than the app,
// or one that updates otherwise, gets told what to do instead.
func (a *App) offerHostUpdate(h *host) {
	switch {
	case h.ahead():
		updater.CheckForUpdates()
		a.toast(a.hostVersionDetail(h))
		return
	case !h.info.Updates:
		a.toast(a.hostVersionDetail(h))
		return
	case !released(mygo.App.Version()):
		a.toast(locales.HostUpdateDev())
		return
	}
	detail := locales.HostUpdateDetail(h.name)
	if !h.info.Server {
		detail = locales.HostUpdateAppDetail(h.name)
	}
	a.confirm = &confirmation{
		open:    true,
		title:   locales.HostUpdateTitle(h.name, mygo.App.Version()),
		detail:  withRunning(detail, a.busyCount(h)),
		action:  locales.CommonUpdate(),
		destroy: true,
		run:     func() { a.hostRequest(h, "update") },
	}
	if a.win != nil {
		a.win.Focus()
	}
}

// offerHostRestart restarts kerod on a host, asking first while programs
// run in its sessions.
func (a *App) offerHostRestart(h *host) {
	a.confirm = &confirmation{
		open:    true,
		title:   locales.HostRestartTitle(h.name),
		detail:  withRunning(locales.HostRestartDetail(h.name), a.busyCount(h)),
		action:  locales.CommonRestart(),
		destroy: true,
		run:     func() { a.hostRequest(h, "restart") },
	}
	if a.win != nil {
		a.win.Focus()
	}
}

// hostRequest asks a host to update Kero, then restart kerod into it, or
// only to restart it; the app connects again as kerod comes back, its
// sessions with it, or, should it fail, as it stands.
func (a *App) hostRequest(h *host, what string) {
	rc := h.rc
	if rc == nil {
		a.toast(locales.HostNotConnected(h.name))
		return
	}
	h.busy = what
	version := mygo.App.Version()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		var err error
		if what == "update" {
			err = rc.Host(ctx, proto.HostRequest{Kind: proto.KindUpdate, App: version})
		}
		if err == nil {
			err = rc.Host(ctx, proto.HostRequest{Kind: proto.KindRestart})
		}
		a.update(func() {
			h.busy = ""
			if !slices.Contains(a.hosts, h) {
				return
			}
			if err != nil {
				a.toast(fmt.Sprintf("%s: %v", h.name, err))
				// kerod may have stopped all the same: the connection
				// that disconnected waited for this.
				a.reconnect(h)
				return
			}
			if what == "update" {
				a.toast(locales.HostUpdated(h.name, version))
			}
			// Through the bridge, which stays, or through a new
			// connection to the listener of the kerod that follows.
			time.AfterFunc(time.Second, func() { a.update(func() { a.reconnectHost(h) }) })
		})
	}()
}

// openRemotePath opens what a path printed in a remote pane names, which
// the host tells: a folder in a new session there, a file in the editor
// of the host, in a pane beside, as nothing of this machine is the host's.
func (a *App) openRemotePath(h *host, id, link string) {
	path := lineSuffix.ReplaceAllString(link, "")
	cl := h.cl
	if cl == nil {
		return
	}
	base := a.workDir(id)
	go func() {
		var st proto.FSStat
		err := cl.Call("fs.stat", proto.FSStatParams{Path: path, Base: base}, &st)
		a.update(func() {
			switch {
			case err != nil:
				a.toast(err.Error())
			case !st.Exists:
				a.toast(locales.HostNoSuchFile(h.name, path))
			case st.Dir:
				a.newSessionIn(h, st.Path)
			default:
				s := a.session(id)
				if s == nil {
					for i := range a.st.Sessions {
						if a.st.Sessions[i].PaneByID(id) != nil {
							s = &a.st.Sessions[i]
						}
					}
				}
				if s == nil {
					return
				}
				// The host's editor, as its shell would run it.
				p := proto.CreateParams{Split: id, Direction: "down", Command: editorCommand(h, st.Path)}
				a.split(s, p)
			}
		})
	}()
}

// withRunning adds to what a restart does how many sessions run programs
// it ends, when any do.
func withRunning(detail string, n int) string {
	if n == 0 {
		return detail
	}
	return detail + " " + locales.RestartRunning(n) + "."
}

// editorCommand edits a file of a host in a pane, with the editor its
// shell names, $VISUAL or $EDITOR, else vi, or on Windows edit.
func editorCommand(h *host, file string) []string {
	if !h.windows() {
		return []string{"sh", "-c", `exec ${VISUAL:-${EDITOR:-vi}} "$1"`, "sh", file}
	}
	return []string{"powershell.exe", "-NoProfile", "-Command",
		`$e = if ($env:VISUAL) { $env:VISUAL } elseif ($env:EDITOR) { $env:EDITOR } else { 'edit' }; Invoke-Expression ($e + ' ' + ` + shell.PSQuote(shell.PSQuote(file)) + `)`}
}
