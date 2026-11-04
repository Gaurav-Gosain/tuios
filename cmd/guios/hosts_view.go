package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

var hostIcon = glyph("f7-desktopcomputer")

// The sidebar's Machines section: each machine this machine's daemon links,
// with how its link stands, its load and its tuios version, and under it
// that machine's sessions, tagged with its name. A machine whose sessions
// can't show says why, and offers what fixes it: start the daemon there,
// install tuios, sign in, or try again.

// machinesSection draws the Machines section, when the daemon links any
// machine.
func (a *App) machinesSection(c *ui.Context, pal *palette, gi, n *int) {
	if len(a.hosts) < 2 {
		return
	}
	top := float32(18)
	if *gi == 0 {
		top = 2
	}
	inset := float32(rowInset)
	if pal.dark {
		inset++
	}
	ui.Row(c.Key("machines")).Height(28).Margin(top, 0, 3, 0).Padding(0, 6, 0, inset).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, locales.MachinesTitle()).FontSize(12.5).FontWeight(600).TextColor(pal.textMuted).SingleLine()
		ui.Textf(c, "%d", len(a.hosts)-1).FontSize(12.5).TextColor(pal.textFaint).FontFeatures("tnum")
		ui.Spacer(c)
		b := ui.ButtonBase(c.Key("add-machine")).Size(20, 20).Radius(5).Center().TextColor(pal.textFaint).
			Label(locales.MachineAdd()).Tooltip(locales.MachineAdd())
		if b.Hovered() {
			b.Background(pal.rowHover).TextColor(pal.text)
		}
		b.Children(func() { ui.Icon(c, plusIcon).Size(13, 13) })
		if b.Clicked() {
			a.openSettings("hosts")
		}
	})
	*gi++
	for _, h := range a.hosts[1:] {
		a.machineRow(c, pal, h)
		if a.prefs.Collapsed[machineKey(h)] {
			continue
		}
		// Its sessions, at the sidebar's one indent (council round 1,
		// visual 28).
		ui.Column(c.Key("in-" + h.name)).Children(func() {
			for i, g := range a.hostGroups(h) {
				g.first = i == 0
				a.groupView(c, pal, g, 1, n)
			}
		})
	}
}

func machineKey(h *host) string { return "machine:" + h.name }

// machineRow is a machine in the Machines section: a dot for its link, its
// name, its load, and under it its tuios version, or why its sessions don't
// show and what fixes it.
func (a *App) machineRow(c *ui.Context, pal *palette, h *host) {
	collapsed := a.prefs.Collapsed[machineKey(h)]
	ui.Column(c.Key("machine-"+h.name)).Margin(4, 0, 2, 0).Children(func() {
		hdr := ui.Row(c.Key("hdr")).Height(28).Padding(0, 6, 0, 8).Gap(7).AlignItems(ui.Center).Radius(6)
		hovered := hdr.Hovered()
		if hovered {
			hdr.Background(pal.rowHover)
		}
		if hdr.Clicked() {
			a.prefs.Collapsed[machineKey(h)] = !collapsed
			a.savePrefs()
			c.Invalidate()
		}
		pointerTooltip(c, hdr, "tip-machine-"+h.name, a.hostTip(h))
		hdr.ContextMenu(func(m *ui.Menu) { a.hostMenu(m, h) })
		hdr.Children(func() {
			col, label := a.hostDotColor(pal, h), a.hostStatusWord(h)
			if h.busy != "" {
				ui.Spinner(c).Size(10, 10).Shrink(0)
			} else {
				ui.Box(c).Size(8, 8).Radius(4).Background(col).Shrink(0).Label(label)
			}
			ui.Text(c, h.name).FontSize(13).FontWeight(650).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
			chev := ui.Icon(c, chevronIcon).Size(10, 10).TextColor(pal.textFaint)
			turn := chev.AnimateTo("turn", 90*(1-b2f(collapsed)), ui.Snappy)
			chev.Rotate(turn).Opacity(chev.Animate("shown", b2f(hovered || collapsed), 120*time.Millisecond))
			ui.Spacer(c)
			switch {
			case hovered && h.conn == connReady:
				b := ui.ButtonBase(c.Key("add")).Size(20, 20).Radius(5).Center().TextColor(pal.textMuted).
					Label(locales.NewSessionIn(h.name)).Tooltip(locales.NewSessionIn(h.name))
				if b.Hovered() {
					b.Background(pal.control).TextColor(pal.text)
				}
				b.Children(func() { ui.Icon(c, plusIcon).Size(13, 13) })
				if b.Clicked() {
					a.newSessionOn(h, "")
				}
			case h.rep.Status == client.HostUp && h.rep.LoadKnown():
				a.loadMeter(c, pal, h.rep)
			default:
				ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(col).SingleLine()
			}
		})
		// The second line: the version and the round trip of a machine
		// that is up, else what keeps its sessions from showing.
		ui.Column(c.Key("detail")).Padding(0, 6, 0, 23).Gap(6).Children(func() {
			if h.conn == connReady || h.rep.Status == client.HostUp && h.busy == "" && h.err == "" {
				ui.Text(c, a.hostSummary(h)).FontSize(11.5).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
				return
			}
			if t := a.hostProblem(h); t != "" {
				// A program's raw words (its stderr) go to the tooltip: the
				// line says in plain words what is wrong.
				say := t
				if rawReason(t) {
					say = plainReason(t, a.hostStatusWord(h))
				}
				line := ui.Text(c, say).FontSize(fsSmall).TextColor(pal.textMuted).LineHeight(1.35).Selectable()
				if say != t {
					line.Tooltip(t)
				}
			}
			a.hostFixes(c, pal, h)
		})
	})
}

// loadMeter draws a machine's one-minute load against its cores: a short
// bar and the number.
func (a *App) loadMeter(c *ui.Context, pal *palette, r client.HostReport) {
	frac := float32(min(r.Load1/float64(r.Cores), 1))
	col := pal.textFaint
	switch {
	case frac >= 0.9:
		col = pal.dangerText
	case frac >= 0.7:
		col = pal.attnText
	}
	// A labelled figure, in its role's colour past 70 % of the cores: an
	// unlabelled bar read as a progress bar (council round 1, visual 28).
	ui.Text(c.Key("load"), locales.HostLoadLabel(fmt.Sprintf("%.1f", r.Load1))).FontSize(fsSmall).TextColor(col).FontFeatures("tnum").SingleLine().
		Shrink(0).Tooltip(locales.HostLoad(fmt.Sprintf("%.2f", r.Load1), r.Cores))
}

// hostDotColor is the color of a machine's link dot.
func (a *App) hostDotColor(pal *palette, h *host) ui.Color {
	switch {
	case h.conn == connReady:
		return pal.linkUp()
	case h.conn == connVersion, h.rep.Status == client.HostSignIn, h.rep.Status == client.HostNoDaemon,
		h.rep.Status == client.HostNoTuios, h.rep.Status == client.HostReconnecting:
		return pal.attention
	case h.conn == connFailed:
		return pal.danger
	}
	return pal.textFaint
}

// hostStatusWord says in a word or two how a machine's link stands.
func (a *App) hostStatusWord(h *host) string {
	switch {
	case h.busy == "start":
		return locales.HostStarting()
	case h.busy == "install":
		return locales.HostInstallingShort()
	case h.conn == connReady:
		return locales.HostStatusUp()
	}
	switch h.rep.Status {
	case client.HostUp, client.HostConnecting, "":
		return locales.StatusConnecting()
	case client.HostReconnecting:
		return locales.HostStatusReconnecting()
	case client.HostNoDaemon:
		return locales.HostStatusNoDaemon()
	case client.HostNoTuios:
		return locales.HostStatusNoTuios()
	case client.HostIncompatible:
		return locales.HostStatusOtherVersion()
	case client.HostSignIn:
		return locales.HostStatusSignIn()
	}
	return locales.HostStatusUnreachable()
}

// hostSummary is a machine's second line while it is up: its tuios
// version, its round trip and how many sessions it holds.
func (a *App) hostSummary(h *host) string {
	parts := []string{}
	if v := h.version(); v != "" {
		parts = append(parts, "tuios "+v)
	}
	if h.rep.RTTMillis > 0 {
		parts = append(parts, fmt.Sprintf("%d ms", h.rep.RTTMillis))
	}
	if h.loaded {
		parts = append(parts, locales.HostSessions(len(a.sessionsOf(h))))
	}
	return strings.Join(parts, " · ")
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

// hostState says how the app stands with a host at the end of one of its
// groups' headers: a dot while its link is not up.
func (a *App) hostState(c *ui.Context, pal *palette, h *host) {
	if h.conn == connReady {
		return
	}
	label := a.hostStatusWord(h)
	ui.Box(c).Size(7, 7).Radius(3.5).Background(a.hostDotColor(pal, h)).Shrink(0).Margin(0, 4, 0, 0).Label(label).Tooltip(label)
}

// hostTip tells what a host is: its address, its tuios, its load, and how
// its link last went.
func (a *App) hostTip(h *host) string {
	r := h.rep
	lines := []string{h.name + " · " + r.Addr}
	if v := h.version(); v != "" {
		lines = append(lines, locales.HostRunsTuios(v))
	}
	if r.LoadKnown() {
		lines = append(lines, locales.HostLoad(fmt.Sprintf("%.2f", r.Load1), r.Cores))
	}
	if r.Status != client.HostUp && r.Reason != "" {
		lines = append(lines, r.Reason)
	}
	if r.Detail != "" {
		lines = append(lines, r.Detail)
	}
	if r.Drops > 0 && r.DropReason != "" {
		lines = append(lines, locales.HostLastDrop(r.DropReason))
	}
	if r.Queued > 0 {
		lines = append(lines, locales.HostQueued(r.Queued))
	}
	return strings.Join(lines, "\n")
}

// hostProblem words why a host's sessions don't show, or what the app does
// on it now.
func (a *App) hostProblem(h *host) string {
	switch {
	case h.busy == "start":
		return locales.HostStartingOn(h.name)
	case h.busy == "install":
		return locales.HostInstalling(h.name)
	case h.busy == "update":
		return locales.HostUpdating(h.name)
	case h.rep.Status == client.HostUp && h.err != "":
		// The link is up and its daemon did not take the app.
		return h.err
	case h.rep.Status == client.HostUp, h.rep.Status == "" && !a.hostsKnown:
		return ""
	}
	t := h.rep.Reason
	if h.rep.Status == client.HostUnreachable && h.rep.Detail != "" {
		t = h.rep.Detail
	}
	if t == "" {
		t = a.hostStatusWord(h)
	}
	return t
}

// hostFixes are the buttons under a machine that fix what keeps its
// sessions from showing.
func (a *App) hostFixes(c *ui.Context, pal *palette, h *host) {
	if h.busy != "" {
		return
	}
	ui.Row(c.Key("fixes")).Gap(6).Children(func() {
		switch h.rep.Status {
		case client.HostNoDaemon:
			if smallButton(c, "start", locales.HostStartDaemon(), false, pal) {
				a.startHostDaemon(h)
			}
		case client.HostNoTuios:
			if smallButton(c, "install", locales.HostInstallTuios(), false, pal) {
				a.offerInstall(h)
			}
		case client.HostIncompatible:
			if smallButton(c, "update", locales.CommonUpdate(), false, pal) {
				a.offerHostUpdate(h)
			}
		case client.HostSignIn:
			if h.rep.ApprovalURL != "" && smallButton(c, "signin", locales.HostSignIn(), false, pal) {
				mygo.Shell.OpenExternal(h.rep.ApprovalURL)
			}
		}
		switch h.rep.Status {
		case client.HostConnecting, client.HostReconnecting:
		default:
			if smallButton(c, "retry", locales.CommonTryAgain(), false, pal) {
				a.reconnectHost(h)
			}
		}
	})
}

// rawReason tells that a machine's reason is a program's own words, as
// "sh: tuios: command not found", not a sentence of the app's.
func rawReason(t string) bool {
	return strings.Contains(t, ": ") || strings.Contains(t, "\n") || strings.Contains(t, "exit status")
}

// plainReason says a program's raw reason in the app's words, or word
// where it knows no better.
func plainReason(raw, word string) string {
	l := strings.ToLower(raw)
	switch {
	case strings.Contains(l, "command not found") || strings.Contains(l, "not found") && strings.Contains(l, "tuios"):
		return locales.HostReasonNoTuios()
	case strings.Contains(l, "connection refused"):
		return locales.HostReasonRefused()
	case strings.Contains(l, "timed out") || strings.Contains(l, "no route to host"):
		return locales.HostReasonTimeout()
	case strings.Contains(l, "permission denied") || strings.Contains(l, "host key"):
		return locales.HostReasonAuth()
	}
	return word
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

// hostMenu is a machine's context menu.
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
	if h.rep.Status == client.HostNoDaemon {
		if m.Item(locales.HostStartDaemon()).Disabled(h.busy != "").Chosen() {
			a.startHostDaemon(h)
		}
	}
	if h.rep.Status == client.HostNoTuios {
		if m.Item(locales.HostInstallTuios()).Disabled(h.busy != "").Chosen() {
			a.offerInstall(h)
		}
	}
	if m.Item(locales.HostUpdateAction(h.name)).Disabled(h.busy != "" || h.rep.Status != client.HostIncompatible && h.rep.Status != client.HostUp).Chosen() {
		a.offerHostUpdate(h)
	}
	a.colorMenu(m, h)
	m.Separator()
	if m.Item(locales.HostCopyAddress()).Chosen() {
		a.copyText = h.addr()
	}
	if m.Item(locales.HostSettings()).Chosen() {
		a.openSettings("hosts")
	}
	if m.Item(locales.HostRemove()).Chosen() {
		a.confirm = a.removeHostConfirmation(h)
	}
}

// reconnectHost has this machine's daemon dial a host's link again now, and
// the app connect to its daemon again once the link is up. The panes keep
// their last frame meanwhile.
func (a *App) reconnectHost(h *host) {
	local := a.local().cl
	if local == nil {
		return
	}
	if h.cl != nil {
		go h.cl.Close()
		h.cl = nil
	}
	h.retries, h.err = 0, ""
	h.conn = connConnecting
	name := h.name
	go func() {
		err := local.RetryHost(name)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
			}
			a.refreshHosts()
		})
	}()
}

// removeHostConfirmation asks to remove a host, which leaves its sessions
// running there.
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

// removeHost removes a host from this machine's tuios config, as tuios
// hosts remove does. The daemon closes its link at once.
func (a *App) removeHost(name string) {
	a.hostsCLI(nil, func(err error) {
		if err != nil {
			a.toast(err.Error())
		}
	}, "hosts", "remove", name)
}

// hostsCLI runs the tuios binary with args off the main thread, then done
// with its error (with what it printed), and refreshes the hosts.
func (a *App) hostsCLI(h *host, done func(error), args ...string) {
	go func() {
		out, err := cli.Output(args...)
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				msg = err.Error()
			}
			err = errors.New(lastLines(msg, 3))
		}
		a.update(func() {
			if h != nil {
				h.busy = ""
			}
			if done != nil {
				done(err)
			}
			if a.setWin != nil {
				a.setWin.Invalidate()
			}
			// The daemon follows a change of its config in a moment.
			a.refreshHosts()
			time.AfterFunc(time.Second, func() { a.update(a.refreshHosts) })
		})
	}()
}

// lastLines keeps the last n lines of s.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// startHostDaemon starts the tuios daemon on a host that has tuios and no
// daemon, as tuios hosts test --start does. Starting one ends nothing, so
// it needs no question.
func (a *App) startHostDaemon(h *host) {
	h.busy = "start"
	name := h.name
	a.hostsCLI(h, func(err error) {
		if err != nil {
			a.toast(locales.HostStartFailed(name, err.Error()))
			return
		}
		if local := a.local().cl; local != nil {
			go local.RetryHost(name)
		}
	}, "hosts", "test", name, "--start")
}

// offerInstall installs this machine's tuios on a host that has none, then
// starts its daemon, as tuios hosts sync NAME --start does, asking first.
func (a *App) offerInstall(h *host) {
	name := h.name
	a.confirm = &confirmation{
		open:   true,
		title:  locales.HostInstallTitle(name),
		detail: locales.HostInstallDetail(name),
		action: locales.CommonInstall(),
		run: func() {
			h.busy = "install"
			a.hostsCLI(h, func(err error) {
				if err != nil {
					a.toast(locales.HostInstallFailed(name, err.Error()))
					return
				}
				a.toast(locales.HostInstalled(name))
			}, "hosts", "sync", name, "--start", "--yes")
		},
	}
	if a.win != nil {
		a.win.Focus()
	}
}

// hostVersionDetail says how tuios on a host differs from this machine's.
func (a *App) hostVersionDetail(h *host) string {
	v := h.version()
	if v == "" {
		v = "?"
	}
	return locales.HostVersionDetail(h.name, v, a.local().version())
}

// offerHostUpdate installs this machine's tuios on a host and restarts its
// daemon into it, as tuios hosts sync NAME --restart does, asking first: the
// restart ends the programs in that daemon's panes.
func (a *App) offerHostUpdate(h *host) {
	name := h.name
	a.confirm = &confirmation{
		open:    true,
		title:   locales.HostUpdateTitle(name, a.local().version()),
		detail:  withRunning(locales.HostUpdateDetail(name), a.busyCount(h)),
		action:  locales.CommonUpdate(),
		destroy: true,
		run: func() {
			h.busy = "update"
			a.hostsCLI(h, func(err error) {
				if err != nil {
					a.toast(fmt.Sprintf("%s: %v", name, err))
					return
				}
				a.toast(locales.HostUpdated(name, a.local().version()))
			}, "hosts", "sync", name, "--restart", "--yes")
		},
	}
	if a.win != nil {
		a.win.Focus()
	}
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
