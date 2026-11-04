package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The last tuios prefix keys guios runs: e (the sidebar takes the keys), u
// (the copies between machines), Z (calm mode) and D (the debug card).

// --- e: the sidebar takes the keyboard, as the TUI's rail does.

// sidebarKeys is the sidebar's keyboard mode: a cursor over the sessions
// that the rail's keymap moves ([keybindings.sidebar]).
type sidebarKeys struct {
	on     bool
	cursor string // the session under the cursor
}

// exploreSidebar gives the sidebar the keyboard, or gives it back to the
// pane: the prefix and e.
func (a *App) exploreSidebar() {
	if a.sb.on {
		a.leaveSidebar()
		return
	}
	if a.prefs.SidebarHidden {
		a.toggleSidebar()
	}
	a.sb = sidebarKeys{on: true, cursor: a.selected}
}

func (a *App) leaveSidebar() {
	a.sb.on = false
	a.defer_(a.focusTerminal)
}

// sidebarKey takes a key in the sidebar's keyboard mode, as the keymap
// spells it.
func (a *App) sidebarKey(k string) {
	ids := a.visibleOrder()
	if len(ids) == 0 {
		a.leaveSidebar()
		return
	}
	i := max(slices.Index(ids, a.sb.cursor), 0)
	action := ""
	if a.tk.reg != nil {
		action = a.tk.reg.GetSidebarAction(k)
	}
	if k == "esc" {
		action = "exit"
	}
	switch {
	case action == "cursor_down":
		i = min(i+1, len(ids)-1)
	case action == "cursor_up":
		i = max(i-1, 0)
	case action == "first":
		i = 0
	case action == "last":
		i = len(ids) - 1
	case strings.HasPrefix(action, "jump_"):
		if n, err := strconv.Atoi(strings.TrimPrefix(action, "jump_")); err == nil && n <= len(ids) {
			i = n - 1
		}
	case action == "expand":
		a.selectSession(ids[i])
	case action == "activate":
		a.selectSession(ids[i])
		a.leaveSidebar()
		return
	case action == "rename":
		a.leaveSidebar()
		a.startRename(ids[i])
		return
	case action == "kill":
		a.closeSession(ids[i], true)
	case action == "new_session":
		a.leaveSidebar()
		a.newSession("")
		return
	case action == "palette":
		a.leaveSidebar()
		a.openPalette()
		return
	case action == "help":
		a.leaveSidebar()
		a.openHelp()
		return
	case action == "exit", action == "switch_edge":
		a.leaveSidebar()
		return
	}
	a.sb.cursor = ids[min(i, len(ids)-1)]
}

// sidebarKeysView is the element that holds the keyboard while the sidebar
// has it.
func (a *App) sidebarKeysView(c *ui.Context) {
	if !a.sb.on {
		return
	}
	box := ui.Box(c.Key("sidebar-keys")).Width(0).Height(0).Focusable().FocusRing(false).AutoFocus().Label(locales.KeysSidebarLabel())
	box.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputKeyDown:
			if typesText(ev.Mods, ev.Key) {
				return true
			}
			if k, ok := keyString(ev.Mods, ev.Key); ok {
				a.sidebarKey(k)
			}
			return true
		case ui.InputText:
			for _, r := range ev.Text {
				a.sidebarKey(string(r))
			}
			return true
		}
		return false
	})
	box.TextCaret(ui.Rect{})
}

// --- u: the copies between machines.

// transfersPanel is the panel of every copy (the prefix and u). The
// sidebar's Transfers section shows three.
type transfersPanel struct {
	open   bool
	scroll ui.ScrollState
}

func (a *App) transfersView(c *ui.Context, pal *palette) {
	if !a.xp.open {
		return
	}
	rows := a.xfer.rows
	ui.DialogBase(c.Key("transfers-panel"), &a.xp.open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal.overlay).Justify(ui.Start).Padding(84, 0, 0, 0)
		floating(panel.Width(520).MaxHeight(520).Padding(16).Gap(6).Radius(14).Background(pal.popover), pal).Label(locales.TransfersTitle())
		// Holds the keyboard, so Escape reaches the panel.
		ui.Box(c.Key("keys")).Width(0).Height(0).Focusable().FocusRing(false).AutoFocus()
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.TransfersTitle()).FontSize(15).FontWeight(650).TextColor(pal.text)
			ui.Textf(c, "%d", len(rows)).FontSize(13).TextColor(pal.textFaint)
		})
		if len(rows) == 0 {
			ui.Text(c, locales.KeysNoTransfers()).FontSize(13).TextColor(pal.textMuted).LineHeight(1.4)
			return
		}
		ui.Scroll(c.Key("rows")).Grow(1).MinHeight(0).TrackScroll(&a.xp.scroll).Children(func() {
			for _, r := range rows {
				a.transferRow(c, pal, r)
			}
		})
	})
	if !a.xp.open {
		a.focusTerminal()
	}
}

// --- Z: calm mode, as the TUI's (internal/app/calm.go): nothing moves or
// blinks, and only what needs the user notifies. guios holds the other
// notifications and says how many when calm goes off.

func (a *App) toggleCalm() {
	a.calm = !a.calm
	// The cursor's blink is the terminal's: they attach again with it.
	for id := range a.terms {
		a.dropTerm(id)
	}
	if a.calm {
		a.calmHeld = 0
		a.toast(locales.KeysCalmOn())
		return
	}
	if a.calmHeld > 0 {
		a.toast(locales.KeysCalmOffHeld(strconv.Itoa(a.calmHeld)))
	} else {
		a.toast(locales.KeysCalmOff())
	}
	a.calmHeld = 0
}

// --- D: the debug card: frame times, wakeups, memory, terminals.

// debugCard is what the card measures. Its numbers are sampled once a
// second while it shows, and nothing runs while it does not.
type debugCard struct {
	on      bool
	frames  []time.Duration // the last frames' build times
	count   int             // frames since the last sample
	last    time.Time       // the last sample
	fps     float64
	wakeups float64
	ctx     int64
	heap    uint64
	gor     int
}

// debugFrame records one frame's build time.
func (a *App) debugFrame(d time.Duration) {
	g := &a.dbg
	if !g.on {
		return
	}
	g.count++
	g.frames = append(g.frames, d)
	if len(g.frames) > 120 {
		g.frames = g.frames[len(g.frames)-120:]
	}
}

// ctxSwitches is the process's context switches, all its threads': its
// wakeups.
func ctxSwitches() int64 {
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return 0
	}
	var n int64
	for _, t := range tasks {
		b, err := os.ReadFile("/proc/self/task/" + t.Name() + "/status")
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "ctxt_switches") {
				f := strings.Fields(l)
				v, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
				n += v
			}
		}
	}
	return n
}

func (a *App) debugView(c *ui.Context, pal *palette) {
	g := &a.dbg
	if !g.on {
		return
	}
	now := c.Now()
	if el := now.Sub(g.last); el >= time.Second {
		ctx := ctxSwitches()
		if !g.last.IsZero() {
			g.fps = float64(g.count) / el.Seconds()
			g.wakeups = float64(ctx-g.ctx) / el.Seconds()
		}
		g.ctx, g.count, g.last = ctx, 0, now
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		g.heap, g.gor = ms.HeapInuse, runtime.NumGoroutine()
	}
	c.After(time.Second - now.Sub(g.last))
	var p50, p99, worst time.Duration
	if n := len(g.frames); n > 0 {
		s := slices.Clone(g.frames)
		slices.Sort(s)
		p50, p99, worst = s[n/2], s[min(n-1, n*99/100)], s[n-1]
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/1e6) }
	rows := [][2]string{
		{locales.DebugFps(), fmt.Sprintf("%.1f", g.fps)},
		{locales.DebugFrame(), ms(p50) + " · " + ms(p99) + " · " + ms(worst)},
		{locales.DebugWakeups(), fmt.Sprintf("%.1f /s", g.wakeups)},
		{locales.DebugHeap(), fmt.Sprintf("%.1f MB", float64(g.heap)/(1<<20))},
		{locales.DebugGoroutines(), strconv.Itoa(g.gor)},
		{locales.DebugTerminals(), strconv.Itoa(len(a.terms))},
	}
	card := ui.Column(c.Key("debug-card")).Absolute().Left(cardGap+12).Bottom(cardGap+12).Padding(12, 14).Gap(4).Radius(10).
		Background(pal.popover).Label(locales.DebugTitle())
	floating(card, pal)
	card.Children(func() {
		ui.Text(c, locales.DebugTitle()).FontSize(12).FontWeight(650).TextColor(pal.text)
		for _, r := range rows {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, r[0]).FontSize(11.5).TextColor(pal.textMuted).Width(150).SingleLine()
				ui.Text(c, r[1]).FontSize(11.5).TextColor(pal.text).FontFeatures("tnum").SingleLine()
			})
		}
	})
}
