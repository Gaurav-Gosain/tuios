package main

import (
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/i18n"
	"github.com/Gaurav-Gosain/mygo/plugins/glass"
	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/mygo/plugins/updater"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/adblock"
	"github.com/Gaurav-Gosain/tuios/internal/guios/cli"
	"github.com/Gaurav-Gosain/tuios/internal/guios/client"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/settings"
	"github.com/Gaurav-Gosain/tuios/internal/guios/shell"
	"github.com/Gaurav-Gosain/tuios/internal/session"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// settingsPage is a page of the Settings window.
type settingsPage struct {
	id           string
	title        func() string
	icon         *ui.SVG
	experimental bool // tagged so beside its title
}

var settingsPages = []settingsPage{
	{"general", locales.PageGeneral, glyph("f7-gear-alt"), false},
	{"appearance", locales.PageAppearance, glyph("f7-paintbrush"), false},
	{"terminal", locales.PageTerminal, terminalIcon, false},
	{"shell", locales.PageShell, glyph("f7-chevron-left-slash-chevron-right"), false},
	{"browser", locales.PageBrowser, globeIcon, false},
	{"notifications", locales.PageNotifications, glyph("f7-bell"), false},
	{"keyboard", locales.PageKeyboard, glyph("f7-keyboard"), false},
	{"agents", locales.PageAgents, glyph("f7-sparkles"), false},
	{"hosts", locales.PageHosts, hostIcon, false},
	{"advanced", locales.PageAdvanced, glyph("f7-slider-horizontal-3"), false},
}

// settingsState is what the Settings window keeps besides the settings.
type settingsState struct {
	page         string
	recording    string // the action whose shortcut is being typed
	recordRef    ui.Handle
	filter       string // the keyboard page's search
	query        string // the sidebar's search, of every page
	searchRef    ui.Handle
	revealed     string // the query and page the search last scrolled to
	draft        map[string]string
	envDraft     string
	shellDraft   string
	shellEditing bool
	loginOpen    bool // open at login, as the system says
	updates      updatePrefs
	cliPath      string
	service      string // kerod, as last asked
	notice       string // a line under the keyboard page, as of a conflict
	toast        string
	menus        map[string]*searchMenu   // the search menus', by key
	problems     []string                 // what the configuration file got wrong
	scrolls      map[string]*smoothScroll // each page's, for its scroll edge
	host         hostDraft                // the Hosts page's form
	hostEdit     hostDraft                // a host's, as the user edits it
	confirm      *confirmation            // a question, asked in this window
}

// openSettings shows the Settings window, on a page.
func (a *App) openSettings(page string) {
	if page != "" {
		a.ss.page = page
	}
	if a.ss.page == "" {
		a.ss.page = "general"
	}
	if a.setWin != nil {
		a.setWin.Show()
		a.setWin.Focus()
		return
	}
	a.ss.loginOpen = mygo.App.OpenAtLogin()
	a.refreshUpdates()
	offUpdates := updater.OnChange(func() {
		a.refreshUpdates()
		if a.setWin != nil {
			a.setWin.Invalidate()
		}
	})
	a.ss.draft = map[string]string{}
	a.refreshAgents()
	a.refreshService()
	a.setWin = mygo.NewWindow(framed(mygo.WindowOptions{
		Title: locales.SettingsTitle(), Width: 820, Height: 620, MinWidth: 700, MinHeight: 480,
		StateKey: "settings",
		Vibrancy: vibrancyFor(a.set),
		Content:  ui.View(a.settingsView),
	}))
	a.setWin.OnClosed(func() {
		offUpdates()
		a.setWin = nil
		a.setRecording("") // the menus take their shortcuts back
		a.ss.query = ""
	})
}

// updatePrefs are the updater's, as last read: the page reads these, not
// the updater, which needs the running app.
type updatePrefs struct {
	enabled     bool // this copy of Kero can update itself
	automatic   bool // checks in the background
	download    bool // and installs what it finds without asking
	lastChecked time.Time
}

func (a *App) refreshUpdates() {
	a.ss.updates = updatePrefs{
		enabled:     mygo.Updater.Enabled(),
		automatic:   updater.AutomaticChecks(),
		download:    updater.AutomaticDownloads(),
		lastChecked: updater.LastCheck(),
	}
}

func (a *App) refreshAgents() {
	a.ss.cliPath = cliInstalled()
}

// refreshService asks kerod how it is, off the main thread.
func (a *App) refreshService() {
	go func() {
		line := locales.SvcNotRunning()
		if c, err := client.Connect(true); err == nil {
			var st proto.State
			c.Call("state", nil, &st)
			panes := 0
			for _, s := range st.Sessions {
				panes += len(s.Panes)
			}
			line = locales.SvcRunning(strconv.Itoa(c.Welcome.PID), c.Welcome.App, len(st.Sessions), panes)
			if kerodStale(c.Welcome.App, c.Welcome.Build) {
				line += locales.SvcStale()
			}
			c.Close()
		}
		a.update(func() {
			a.ss.service = line
			if a.setWin != nil {
				a.setWin.Invalidate()
			}
		})
	}()
}

func durationWords(d time.Duration) string {
	switch {
	case d < time.Minute:
		return locales.DurUnderMinute()
	case d < time.Hour:
		return locales.DurMinutes(int(d.Minutes()))
	case d < 48*time.Hour:
		return locales.DurHours(int(math.Round(d.Hours())))
	}
	return locales.DurDays(int(d.Hours() / 24))
}

// closeSettings closes the Settings window.
func (a *App) closeSettings() {
	if a.setWin != nil {
		a.setWin.Close()
	}
}

func (a *App) settingsView(c *ui.Context) {
	a.looks()
	pal := paletteFor(c)
	c.SetTheme(borderless(c, pal))
	if a.ss.toast != "" {
		c.Toast(a.ss.toast)
		a.ss.toast = ""
	}
	found := a.searchSettings()
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.ss.searchRef.Focus(c) })
	// Escape and Ctrl+W (Cmd+W on macOS, Ctrl+Shift+W too elsewhere)
	// close the window, as a dialog's do. Escape first empties the search,
	// and a shortcut being recorded takes every key.
	if a.ss.recording == "" {
		c.OnShortcut(0, ui.KeyEscape, func() {
			if a.ss.query != "" {
				a.ss.query = ""
				return
			}
			a.closeSettings()
		})
		c.OnShortcut(ui.Cmd, ui.KeyW, a.closeSettings)
		if runtime.GOOS != "darwin" {
			c.OnShortcut(ui.Ctrl|ui.Shift, ui.KeyW, a.closeSettings)
		}
	}
	row := ui.Row(c).Fill().AlignItems(ui.Stretch)
	backdrop(c, row)
	row.Children(func() {
		// Under the traffic lights on macOS; elsewhere the search starts
		// the sidebar, centered on barMid as the page's title and the
		// window controls are.
		top := float32(topBar)
		if ownControls {
			top = barMid - (controlH+2)/2
		}
		ui.Column(c).Width(212).Shrink(0).Padding(top, 10, 12, 10).Gap(2).DragWindow().Children(func() {
			a.settingsSearch(c, pal)
			for _, p := range settingsPages {
				if found == nil || found[p.id] != nil {
					a.settingsNav(c, pal, p)
				}
			}
			if found != nil && len(found) == 0 {
				ui.Text(c, locales.CommonNoResults()).FontSize(fsBody).TextColor(pal.textFaint).Padding(8, 10)
			}
			ui.Spacer(c)
			link := ui.ButtonBase(c.Key("open-config")).Height(hControl).Padding(0, 10).Gap(6).Radius(rRow).AlignItems(ui.Center).Label(locales.SettingsOpenConfigLabel())
			fg := pal.textMuted
			if link.Hovered() {
				fg = pal.text
				link.Background(pal.rowHover)
			}
			link.TextColor(fg).Tooltip(proto.ShortDir(settings.Path())).Children(func() {
				ui.Icon(c, glyph("f7-chevron-left-slash-chevron-right")).Size(14, 14).Shrink(0)
				oneLine(c, locales.SettingsOpenConfig(), fsSmall, 0)
			})
			if link.Clicked() {
				a.openConfig()
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Padding(cardGap, cardGap, cardGap, 0).Children(func() {
			card := paned(ui.Column(c).Grow(1).MinHeight(0).Radius(rPane).Background(pal.card), pal).Clip()
			card.Children(func() {
				page := settingsPages[0]
				for _, p := range settingsPages {
					if p.id == a.ss.page {
						page = p
					}
				}
				// The page scrolls under its header, which macOS 27's hard
				// scroll edge frosts it under, as System Settings does.
				// The edge shows, as AppKit's, once content is under the bar,
				// or while the pointer is over it.
				if a.ss.scrolls == nil {
					a.ss.scrolls = map[string]*smoothScroll{}
				}
				sc := a.ss.scrolls[page.id]
				if sc == nil {
					sc = &smoothScroll{}
					a.ss.scrolls[page.id] = sc
				}
				box := ui.Box(c.Key("page")).Grow(1).MinHeight(0)
				_, py, over := box.PointerPosition()
				edgeOn := sc.Y > 0.5 || over && py < headerH
				box.Children(func() {
					sc.track(c, ui.Scroll(c.Key("page-"+page.id))).Fill().Padding(headerH+16, 24, 28, 24).ScrollbarInsets(headerH, 0, 0).Children(func() {
						ui.Column(c).Gap(22).MaxWidth(620).Children(func() {
							switch page.id {
							case "general":
								a.generalPage(c, pal)
							case "appearance":
								a.appearancePage(c, pal)
							case "terminal":
								a.terminalPage(c, pal)
							case "shell":
								a.shellPage(c, pal)
							case "browser":
								a.browserPage(c, pal)
							case "notifications":
								a.notificationsPage(c, pal)
							case "keyboard":
								a.keyboardPage(c, pal)
							case "agents":
								a.agentsPage(c, pal)
							case "hosts":
								a.hostsPage(c, pal)
							case "advanced":
								a.advancedPage(c, pal)
							}
						})
					})
					edge := ui.Box(c.Key("edge")).Absolute().Top(0).Left(0).Right(0).Height(headerH).PassThrough().
						Material(glass.ScrollEdge{Hard: true, Background: pal.card})
					edge.Opacity(edge.Animate("on", b2f(edgeOn), dFade))
					ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(headerH).AlignItems(ui.Center).Padding(0, 10, 0, 24).DragWindow().Children(func() {
						ui.Text(c, page.title()).FontSize(fsTitle).FontWeight(wSemibold).TextColor(pal.text).SingleLine()
						if page.experimental {
							ui.Row(c.Key("experimental")).Height(18).Margin(0, 0, 0, 8).Padding(0, 6).Radius(rChip).AlignItems(ui.Center).Shrink(0).
								Background(pal.attention.Alpha(0.14)).Children(func() {
								ui.Text(c, locales.PageExperimental()).FontSize(fsSmall).FontWeight(wSemibold).TextColor(pal.attnText).SingleLine()
							})
						}
						ui.Spacer(c)
						windowControls(c, pal, a.setWin)
					})
				})
			})
		})
	})
	keepOverlay(c)
	toasts(c)
	if a.ss.confirm != nil {
		confirmDialog(c, pal, &a.ss.confirm)
	}
	// The controls edit a.set: what changed applies, and saves.
	if !reflect.DeepEqual(a.set, a.applied) {
		a.applySettings(a.set)
	}
}

// searchSettings applies the sidebar's search: it shows a page with
// finds, when the page shown has none, and sets what the pages highlight.
// It returns what searchResults does.
func (a *App) searchSettings() map[string][]string {
	found := searchResults(a.ss.query, a.set.Shortcuts)
	searchHits, searchReveal = nil, ""
	if found == nil {
		a.ss.revealed = ""
		return nil
	}
	if found[a.ss.page] == nil {
		for _, p := range settingsPages {
			if found[p.id] != nil {
				a.ss.page = p.id
				a.setRecording("")
				break
			}
		}
	}
	labels := found[a.ss.page]
	searchHits = map[string]bool{}
	for _, l := range labels {
		searchHits[l] = true
	}
	// The first find comes into view once for each query and page, after
	// which the page scrolls as the user likes.
	if at := a.ss.query + "\x00" + a.ss.page; len(labels) > 0 && a.ss.revealed != at {
		a.ss.revealed, searchReveal = at, labels[0]
	}
	return found
}

// settingsSearch is the sidebar's search field, which finds settings in
// every page by their labels, their keys in the configuration file, and
// words for them.
func (a *App) settingsSearch(c *ui.Context, pal *palette) {
	var in ui.Element
	frame := ui.Row(c.Key("search")).Cursor(ui.CursorText).Margin(0, 0, 10, 0)
	frame.Children(func() {
		ui.Icon(c, searchIcon).Size(14, 14).TextColor(pal.textFaint).Shrink(0)
		in = ui.TextInputBase(c.Key("in"), &a.ss.query).Bind(&a.ss.searchRef).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).FontSize(fsBody).
			Placeholder(locales.CommonSearch()).Label(locales.SettingsSearchLabel())
		// Escape empties the search, and with it empty closes the window.
		in.OnShortcut(0, ui.KeyEscape, func() {
			if a.ss.query == "" {
				a.closeSettings()
				return
			}
			a.ss.query = ""
		})
		if a.ss.query != "" {
			x := ui.ButtonBase(c.Key("clear")).Size(16, 16).Radius(8).Center().Shrink(0).Label(locales.SettingsClearSearch()).
				Background(pal.textFaint.Alpha(0.55)).TextColor(pal.card)
			if x.Hovered() {
				x.Background(pal.textMuted)
			}
			x.Children(func() { ui.Icon(c, closeIcon).Size(8, 8) })
			if x.Clicked() {
				a.ss.query = ""
				a.ss.searchRef.Focus(c)
			}
		}
	})
	field(c, frame, in.Focused(), pal)
}

func (a *App) settingsNav(c *ui.Context, pal *palette, p settingsPage) {
	sel := a.ss.page == p.id
	item := ui.Row(c.Key(p.id)).Height(hRow).Padding(0, 10).Gap(10).AlignItems(ui.Center).Radius(rRow)
	if pal.dark {
		item.Border(1, ui.Transparent) // room for the chosen item's edge
	}
	bg := ui.Transparent
	switch {
	case sel:
		bg = pal.rowSelected
		chosen(item, pal)
	case item.Hovered():
		bg = pal.rowHover
	}
	item.Background(item.AnimateColor("bg", bg, ui.Animation{Duration: dHoverOut}))
	// A quick tap can press and release between frames; keep its click too.
	if (item.Pressed() || item.Clicked()) && !sel {
		a.ss.page = p.id
		a.setRecording("")
	}
	item.Children(func() {
		ic := ui.Icon(c, p.icon).Size(16, 16).TextColor(pal.textMuted).Shrink(0)
		if sel {
			ic.TextColor(pal.accent)
		}
		oneLine(c, p.title(), fsBody, 0).TextColor(pal.text)
	})
}

// groupTitle is a group's title, which the search highlights as it does
// a setting.
func groupTitle(c *ui.Context, pal *palette, title string) {
	row := ui.Row(c.Key("title-"+title)).Padding(0, 4)
	row.Children(func() {
		searchHighlight(c, row, title, pal, 2, 3)
		oneLine(c, title, fsSmall, wMedium).TextColor(pal.textMuted)
	})
}

// group is a titled set of settings, on a soft panel.
func settingsGroup(c *ui.Context, pal *palette, title, note string, fn func()) {
	ui.Column(c).Gap(8).Children(func() {
		if title != "" {
			groupTitle(c, pal, title)
		}
		if fn != nil { // a group of a note alone has no panel
			groupRows = 0
			ui.Column(c).Radius(rCard).Background(pal.chip).Padding(4, 16).Children(fn)
		}
		if note != "" {
			ui.Text(c, note).FontSize(fsSmall).TextColor(pal.textFaint).LineHeight(1.4).Padding(0, 4)
		}
	})
}

// groupRows counts the rows of the group being built, so that a divider
// goes between them, as in macOS's Settings.
var groupRows int

// setting is a row of a group: a label, what it does, and its control.
func settingRow(c *ui.Context, pal *palette, label, detail string, control func()) {
	var fn func()
	if detail != "" {
		fn = func() {
			ui.Text(c, detail).FontSize(fsSmall).TextColor(pal.textMuted).LineHeight(1.4)
		}
	}
	settingRowWith(c, pal, label, fn, control)
}

// settingPathRow is settingRow for a file or a folder: its path on one
// line, cut at its start.
func settingPathRow(c *ui.Context, pal *palette, label, path string, control func()) {
	settingRowWith(c, pal, label, func() {
		ui.Row(c).MinWidth(0).Children(func() { pathLine(c, "path-"+label, path, fsSmall, pal.textMuted) })
	}, control)
}

func settingRowWith(c *ui.Context, pal *palette, label string, detail func(), control func()) {
	if groupRows++; groupRows > 1 {
		ui.Box(c).Height(1).Shrink(0).Background(pal.text.Alpha(0.07))
	}
	// The control goes under the label where a description would have
	// less than 300 DIPs beside it: a wide control never squeezes what it
	// is for into a column of words (DESIGN.md section 6).
	row := ui.Row(c.Key("row-"+label)).Padding(11, 0).GapX(16).GapY(10).AlignItems(ui.Center).Wrap()
	row.Children(func() {
		searchHighlight(c, row, label, pal, 10, -2)
		// A label alone needs only its own width beside the control; with
		// a description it keeps 300, so the description reads in lines
		// rather than a column of words.
		least := natural(c, flat(label), fsBody, 0) + 4
		if detail != nil {
			least = 300
		}
		ui.Column(c).Grow(1).Basis(0).MinWidth(least).Gap(3).Children(func() {
			oneLine(c, label, fsBody, 0).TextColor(pal.text)
			if detail != nil {
				detail()
			}
		})
		ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(control)
	})
}

// segmented chooses one of ids into value, as macOS's segmented control
// shows it: a soft track, the choice a lighter fill raised a hair, thin
// separators between the others, and labels of one weight.
func segmented(c *ui.Context, key string, value *string, ids, labels []string) {
	segmentedIn(c, key, value, ids, labels, false)
}

// segmentedIn is segmented, across the width it has when fill is set,
// its segments sharing it.
func segmentedIn(c *ui.Context, key string, value *string, ids, labels []string, fill bool) {
	pal := paletteFor(c)
	i := max(slices.Index(ids, *value), 0)
	track, thumb := ui.RGBA(0, 0, 0, 0.06), ui.Hex("#ffffff")
	if pal.dark {
		track, thumb = ui.RGBA(255, 255, 255, 0.07), ui.RGBA(255, 255, 255, 0.2)
	}
	seg := ui.SegmentedBase(c.Key(key), &i, len(labels))
	if fill {
		seg.Track.FillWidth()
	}
	seg.Track.Height(hControlSm).Padding(2).Radius(rControl).Background(track).AlignItems(ui.Center).Children(func() {
		for n, label := range labels {
			if n > 0 {
				// A separator, where neither segment beside it is chosen.
				sep := ui.Box(c).Width(1).Height(12).Shrink(0)
				if n != i && n-1 != i {
					sep.Background(pal.text.Alpha(0.14))
				}
			}
			s := seg.Segment(n).FillHeight().Padding(0, 12).Radius(rRow).Center()
			if fill {
				// Each its share of the width as its label's.
				s.Grow(float32(utf8.RuneCountInString(label))).Basis(0).MinWidth(0).Padding(0, 6)
			}
			if n == i {
				s.Background(thumb)
				if pal.dark {
					s.Shadow(0, 1, 1, 0, ui.RGBA(0, 0, 0, 0.25))
				} else {
					s.Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.16)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.04))
				}
			}
			s.Children(func() {
				oneLine(c, label, fsSmall, wMedium).TextColor(pal.text)
			})
		}
	})
	// The choice lands after this runs: take it then.
	seg.Track.OnChange(func() { *value = ids[i] })
}

func (a *App) generalPage(c *ui.Context, pal *palette) {
	s := &a.set
	settingsGroup(c, pal, locales.SetSessions(), locales.SetSessionsDetail(a.tip(strings.TrimSuffix(actionTitle("app.quitAll"), "…"), "app.quitAll")), func() {
		settingRow(c, pal, locales.SetNewSessionsStartIn(), "", func() {
			segmented(c, "newdir", &s.NewSessionDir, []string{"current", "project", "home"}, []string{locales.SetDirCurrent(), locales.SetDirProject(), locales.SetDirHome()})
		})
		settingRow(c, pal, locales.SetSessionName(), locales.SetSessionNameDetail(), func() {
			segmented(c, "sessionname", &s.SessionName, []string{proto.NameFocused, proto.NameLead, proto.NameFolder},
				[]string{locales.SetNameFocused(), locales.SetNameLead(), locales.SetNameFolder()})
		})
		settingRow(c, pal, locales.SetSidebarRows(), locales.SetSidebarRowsDetail(), func() {
			segmented(c, "sidebar-rows", &s.SidebarRows, []string{"detailed", "compact"}, []string{locales.SetRowsDetailed(), locales.SetRowsCompact()})
		})
		settingRow(c, pal, locales.SetFocusFollowsMouse(), locales.SetFocusFollowsMouseDetail(), func() {
			toggle(c.Key("focusmouse"), &s.FocusFollowsMouse, pal)
		})
		settingRow(c, pal, locales.SetAskBeforeClosing(), locales.SetAskBeforeClosingDetail(), func() {
			toggle(c.Key("confirm"), &s.ConfirmClose, pal)
		})
		settingRow(c, pal, locales.SetKeepHistory(), locales.SetKeepHistoryDetail(), func() {
			toggle(c.Key("keep-history"), &s.KeepHistory, pal)
		})
		settingRow(c, pal, locales.SetResumeAgents(), locales.SetResumeAgentsDetail(), func() {
			segmented(c, "resume", &s.ResumeAgents, []string{"ask", "auto", "off"},
				[]string{locales.SetResumeAsk(), locales.SetResumeAuto(), locales.SetResumeOff()})
		})
		settingRow(c, pal, locales.SetSessionHistory(), locales.SetSessionHistoryDetail(), func() {
			toggle(c.Key("session-history"), &s.SessionHistory, pal)
		})
	})
	settingsGroup(c, pal, locales.SetGit(), locales.SetGitDetail(), func() {
		settingRow(c, pal, locales.SetGitStatus(), locales.SetGitStatusDetail(), func() {
			toggle(c.Key("git-status"), &s.GitStatus, pal)
		})
		settingRow(c, pal, locales.SetDiffLayout(), locales.SetDiffLayoutDetail(), func() {
			segmented(c, "diff-layout", &s.DiffLayout, []string{"auto", "unified", "split"},
				[]string{locales.SetDiffAuto(), locales.ChangesUnified(), locales.ChangesSplit()})
		})
	})
	// Windows and Linux always show the icon, in the notification area.
	if runtime.GOOS == "darwin" {
		detail := ""
		if s.AppIcon != "dock" {
			detail = locales.SetAppIconDetail()
		}
		settingsGroup(c, pal, "", "", func() {
			settingRow(c, pal, locales.SetAppIcon(), detail, func() {
				popup(c, "app-icon", &s.AppIcon, settings.AppIcons, appIconName, 180, pal)
			})
		})
	}
	settingsGroup(c, pal, locales.SetStartup(), "", func() {
		settingRow(c, pal, locales.SetOpenAtLogin(), "", func() {
			toggle(c.Key("login"), &a.ss.loginOpen, pal).OnChange(func() {
				if err := mygo.App.SetOpenAtLogin(a.ss.loginOpen); err != nil {
					a.ss.loginOpen = !a.ss.loginOpen
					a.ss.toast = locales.SetLoginFailed(err.Error())
				}
			})
		})
	})
	a.updatesGroup(c, pal)
}

// browserPage is Settings › Browser: what browser panes block.
func (a *App) browserPage(c *ui.Context, pal *palette) {
	s := &a.set
	lists := ""
	if s.BlockAds {
		lists = locales.SetAdListsNote()
	}
	settingsGroup(c, pal, locales.SetAdBlocking(), lists, func() {
		settingRow(c, pal, locales.SetBlockAds(), locales.SetBlockAdsDetail(), func() {
			toggle(c.Key("browser-block-ads"), &s.BlockAds, pal)
		})
		if s.BlockAds {
			adListRows(c, pal, s)
		}
	})
}

// adListRows are the filter lists to block with: those Kero offers, and
// those of other URLs the configuration names.
func adListRows(c *ui.Context, pal *palette, s *settings.Settings) {
	sources := slices.Clone(adblock.Catalog)
	for _, id := range s.AdLists {
		if src, ok := adblock.Find(id); ok && !slices.ContainsFunc(sources, func(c adblock.Source) bool { return c.ID == id }) {
			sources = append(sources, src)
		}
	}
	for _, src := range sources {
		on := slices.Contains(s.AdLists, src.ID)
		settingRow(c, pal, src.Name, adListDetail(src), func() {
			toggle(c.Key("adlist-"+src.ID), &on, pal).OnChange(func() {
				// In the order of the rows, whatever order the file had.
				var lists []string
				for _, other := range sources {
					if other.ID == src.ID && on || other.ID != src.ID && slices.Contains(s.AdLists, other.ID) {
						lists = append(lists, other.ID)
					}
				}
				if lists == nil {
					lists = []string{}
				}
				s.AdLists = lists
			})
		})
	}
}

// adListDetail says what a filter list blocks, or where it is.
func adListDetail(src adblock.Source) string {
	switch src.ID {
	case "easylist":
		return locales.SetAdListEasylist()
	case "easyprivacy":
		return locales.SetAdListEasyprivacy()
	case "peter-lowe":
		return locales.SetAdListPeterLowe()
	case "easylist-china":
		return locales.SetAdListEasylistChina()
	case "fanboy-cookie":
		return locales.SetAdListFanboyCookie()
	case "fanboy-social":
		return locales.SetAdListFanboySocial()
	case "fanboy-annoyance":
		return locales.SetAdListFanboyAnnoyance()
	}
	return src.URL
}

func (a *App) updatesGroup(c *ui.Context, pal *palette) {
	u := &a.ss.updates
	version := "guios " + mygo.App.Version()
	switch {
	case !u.enabled:
		version += locales.SetVersionNoUpdate()
	case u.lastChecked.IsZero():
		version += locales.SetVersionNotChecked()
	default:
		version += locales.SetVersionLastChecked(durationWords(time.Since(u.lastChecked)))
	}
	settingsGroup(c, pal, locales.SetUpdates(), "", func() {
		settingRow(c, pal, locales.SetVersion(), version, func() {
			if button(c, "check-updates", locales.SetCheckForUpdates(), "", false, pal) {
				updater.CheckForUpdates()
			}
		})
		settingRow(c, pal, locales.SetAutoCheck(), locales.SetAutoCheckDetail(), func() {
			toggle(c.Key("auto-check"), &u.automatic, pal).OnChange(func() { updater.SetAutomaticChecks(u.automatic) })
		})
		settingRow(c, pal, locales.SetAutoInstall(), locales.SetAutoInstallDetail(), func() {
			toggle(c.Key("auto-download"), &u.download, pal).OnChange(func() { updater.SetAutomaticDownloads(u.download) })
		})
	})
}

func (a *App) appearancePage(c *ui.Context, pal *palette) {
	s := &a.set
	settingsGroup(c, pal, "", "", func() {
		settingRow(c, pal, locales.SetAppearance(), "", func() {
			segmented(c, "appearance", &s.Appearance, []string{"system", "light", "dark"}, []string{locales.SetThemeSystem(), locales.SetThemeLight(), locales.SetThemeDark()})
		})
		settingRow(c, pal, locales.SetLanguage(), "", func() {
			popup(c, "language", &s.Language, append([]string{"system"}, settings.Languages...), languageName, 180, pal)
		})
		settingRow(c, pal, locales.SetTranslucent(), locales.SetTranslucentDetail(), func() {
			toggle(c.Key("vibrancy"), &s.Vibrancy, pal)
		})
	})
	ui.Column(c).Gap(8).Children(func() {
		groupTitle(c, pal, locales.SetBackground())
		// A theme for each appearance: the tiles of one show its colors.
		for _, dark := range []bool{false, true} {
			label := locales.SetLightWindows()
			if dark {
				label = locales.SetDarkWindows()
			}
			ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textFaint).Padding(4, 4, 0, 4)
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for _, t := range settings.KeroThemes {
					a.backgroundTile(c, pal, t.ID, backgroundName(t.ID, t.Name), dark)
				}
			})
		}
	})
	// A Ghostty theme in place of kero's: none goes back to kero's default.
	none := locales.SetThemeNone()
	// tuios: the theme the TUI has, Omarchy's while it follows Omarchy.
	themes := append([]string{none, tuiosThemeName}, terminal.GhosttyThemes()...)
	ghostty := func(key, value string, choose func(string)) {
		if keroID(value) != "" {
			value = none
		}
		a.searchPopup(c, key, value, themes, 240, nil, pal, func(v string) {
			if v == none {
				v = settings.Default().LightTheme
			}
			choose(v)
		})
	}
	settingsGroup(c, pal, locales.SetColors(), locales.SetColorsDetail(), func() {
		settingRow(c, pal, locales.SetLightWindows(), "", func() {
			ghostty("light", s.LightTheme, func(v string) { a.set.LightTheme = v })
		})
		settingRow(c, pal, locales.SetDarkWindows(), "", func() {
			ghostty("dark", s.DarkTheme, func(v string) { a.set.DarkTheme = v })
		})
	})
}

// languageName names a language in itself, as "简体中文", so that whoever
// reads it finds theirs; "system" in the language the app shows.
func languageName(tag string) string {
	if tag == "system" {
		return locales.SetLanguageSystem()
	}
	return i18n.Autonym(tag)
}

// appIconName names a place the app's icon shows (settings.AppIcons).
func appIconName(id string) string {
	switch id {
	case "dock":
		return locales.SetAppIconDock()
	case "menubar":
		return locales.SetAppIconMenuBar()
	}
	return locales.SetAppIconBoth()
}

// localeTag is the tag of a language setting for App.SetLocale: "" follows
// the system's languages.
func localeTag(language string) string {
	if language == "system" {
		return ""
	}
	return language
}

// backgroundName is a wash's name in the language the app shows.
func backgroundName(id, name string) string {
	if t, ok := map[string]func() string{"rex": locales.BgPetal, "mist": locales.BgMist, "meadow": locales.BgMeadow, "dusk": locales.BgDusk,
		"tide": locales.BgTide, "apricot": locales.BgApricot, "aurora": locales.BgAurora, "dune": locales.BgDune, "paper": locales.BgPaper, "plain": locales.BgPlain}[id]; ok {
		return t()
	}
	return name
}

// backgroundTile previews one of kero's themes as windows of an
// appearance show it, choosing it for them on a click: a card of its
// colors on its wash, with text, a prompt, the cursor and the programs'
// colors.
func (a *App) backgroundTile(c *ui.Context, pal *palette, id, name string, dark bool) {
	slot, key, windows := &a.set.LightTheme, "bg-light-", locales.SetLightWindows()
	if dark {
		slot, key, windows = &a.set.DarkTheme, "bg-dark-", locales.SetDarkWindows()
	}
	sel := keroID(*slot) == id || *slot == tuiosThemeName && id == "paper" && tuiosNamesNone[b2i(dark)].Load()
	tile := ui.ButtonBase(c.Key(key + id)).Column().Width(124).Gap(8).AlignItems(ui.Stretch).Label(name + ", " + windows)
	if tile.Clicked() {
		*slot = keroName(id)
	}
	tile.Children(func() {
		l := keroLook(id, dark)
		th := l.term
		ui.Box(c).Children(func() {
			sw := raised(ui.Box(c).Size(124, 80).Radius(rCard), pal).Clip()
			sw.LinearGradient(l.base)
			sw.Children(func() {
				ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).LinearGradient(l.glow)
				ui.Column(c).Absolute().Top(8).Left(8).Right(8).Bottom(8).Radius(rControl).Background(th.Background).
					Padding(8, 9).Justify(ui.SpaceBetween).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
						ui.Text(c, "Aa").FontSize(17).TextColor(th.Foreground).Grow(1)
						ui.Box(c).Radius(rChip).Padding(1, 4).Background(th.Selection).Children(func() {
							ui.Text(c, "~/").Font(a.termFamily()).FontSize(fsCaption).TextColor(th.Foreground)
						})
						ui.Box(c).Size(6, 13).Radius(1.5).Background(th.Cursor)
					})
					ui.Row(c).Gap(3).Children(func() {
						for i := range 8 {
							ui.Box(c).Size(8, 8).Radius(4).Background(th.Palette[i])
						}
					})
				})
			})
			if sel {
				// The ring, a gap off the tile and around it, so that
				// nothing in the tile moves for it.
				ui.Box(c).Absolute().Top(-5).Left(-5).Right(-5).Bottom(-5).Radius(rCard+5).Border(2, l.pal.accent)
			}
		})
		lbl := ui.Text(c, name).FontSize(fsSmall).TextColor(pal.textMuted).TextAlign(ui.Center)
		if sel {
			lbl.TextColor(pal.text).FontWeight(wSemibold)
		}
	})
}

// fontChoices are the fonts the picker offers: kero's own first, then
// the system's, then common coding fonts the user may have installed.
var fontChoices = []string{bundledFont, "System", "SF Mono", "Menlo", "Monaco", "JetBrains Mono", "Fira Code", "Cascadia Code",
	"Iosevka", "Berkeley Mono", "Geist Mono", "IBM Plex Mono", "Source Code Pro", "Hack", "Maple Mono"}

// fontOptions are the families the font menu offers: kero's own, the
// system's, the common coding fonts, and every monospaced family the
// machine has, so that a font installed with the system is there to pick.
// Fonts installed while kero runs show after a restart, as MyGo lists
// them once. The system's is named in the language the app shows now.
func fontOptions() []string {
	return append([]string{fontChoices[0], locales.SetFontSystem()}, otherFonts()...)
}

// otherFonts are the families of fontOptions after kero's own and the
// system's, listed once.
var otherFonts = sync.OnceValue(func() []string {
	var opts []string
	seen := map[string]bool{fontChoices[0]: true, bundledFont: true}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		opts = append(opts, name)
	}
	for _, name := range fontChoices[2:] {
		add(name)
	}
	for _, f := range ui.FontFamilies() {
		if f.Monospaced {
			add(f.Name)
		}
	}
	return opts
})

func (a *App) terminalPage(c *ui.Context, pal *palette) {
	s := &a.set
	a.terminalPreview(c, pal)
	settingsGroup(c, pal, locales.SetText(), "", func() {
		fontDetail := locales.SetFontDetail()
		if s.FontFamily != "" && usableFamily(s.FontFamily) == "" {
			fontDetail = locales.SetFontMissing(s.FontFamily) + " " + fontDetail
		}
		settingRow(c, pal, locales.SetFont(), fontDetail, func() {
			// The picker's names for the settings' values: "" is kero's
			// font, "monospace" the system's.
			family := s.FontFamily
			switch family {
			case "":
				family = bundledFont
			case "monospace":
				family = locales.SetFontSystem()
			}
			opts := fontOptions()
			if !slices.Contains(opts, family) {
				opts = append([]string{family}, opts...)
			}
			a.searchPopup(c, "font", family, opts, 240, locales.SetUseFont, pal, func(v string) {
				switch v {
				case bundledFont:
					v = ""
				case locales.SetFontSystem():
					v = "monospace"
				}
				a.set.FontFamily = v
			})
		})
		settingRow(c, pal, locales.SetSize(), locales.SetSizeDetail(a.keys("text.bigger"), a.keys("text.smaller")), func() {
			sizes := []float32{10, 11, 12, 13, 14, 15, 16, 18, 20, 22, 24, 28}
			if !slices.Contains(sizes, s.FontSize) {
				sizes = append(sizes, s.FontSize)
				slices.Sort(sizes)
			}
			popup(c, "size", &s.FontSize, sizes, func(v float32) string { return strconv.FormatFloat(float64(v), 'f', -1, 32) + " pt" }, 90, pal)
		})
		settingRow(c, pal, locales.SetLineHeight(), "", func() {
			v := float64(s.LineHeight)
			slider(c, "lh", &v, 1, 2, 160, pal).OnChange(func() { a.set.LineHeight = float32(int(v*100+0.5)) / 100 })
			ui.Textf(c, "%.2f×", s.LineHeight).FontSize(fsSmall).TextColor(pal.textMuted).Width(40).FontFeatures("tnum")
		})
		settingRow(c, pal, locales.SetWeight(), "", func() {
			popup(c, "weight", &s.FontWeight, []int{300, 400, 500, 600}, func(w int) string {
				return map[int]string{300: locales.SetWeightLight(), 400: locales.SetWeightRegular(), 500: locales.SetWeightMedium(), 600: locales.SetWeightSemibold()}[w]
			}, 140, pal)
		})
		settingRow(c, pal, locales.SetLigatures(), locales.SetLigaturesDetail(), func() {
			toggle(c.Key("liga"), &s.Ligatures, pal)
		})
		settingRow(c, pal, locales.SetThicken(), locales.SetThickenDetail(), func() {
			toggle(c.Key("thicken"), &s.Thicken, pal)
		})
	})
	settingsGroup(c, pal, "", "", func() {
		settingRow(c, pal, locales.SetDimUnfocused(), locales.SetDimUnfocusedDetail(), func() {
			toggle(c.Key("dim-unfocused"), &s.DimUnfocused, pal)
		})
	})
	settingsGroup(c, pal, locales.SetCursor(), "", func() {
		settingRow(c, pal, locales.SetShape(), locales.SetShapeDetail(), func() {
			segmented(c, "cursor", &s.Cursor, []string{"block", "bar", "underline"}, []string{locales.SetCursorBlock(), locales.SetCursorBar(), locales.SetCursorUnderline()})
		})
		settingRow(c, pal, locales.SetBlink(), "", func() { toggle(c.Key("blink"), &s.CursorBlink, pal) })
	})
	settingsGroup(c, pal, locales.SetInput(), "", func() {
		settingRow(c, pal, locales.SetOptionMeta(), locales.SetOptionMetaDetail(), func() {
			toggle(c.Key("optalt"), &s.OptionAsAlt, pal)
		})
		settingRow(c, pal, locales.SetCopyOnSelect(), locales.SetCopyOnSelectDetail(), func() {
			toggle(c.Key("cos"), &s.CopyOnSelect, pal)
		})
		settingRow(c, pal, locales.SetRightClick(), "", func() {
			popup(c, "rc", &s.RightClick, []string{"menu", "paste", "copyOrPaste"}, func(id string) string {
				return map[string]string{"menu": locales.SetRightClickMenu(), "paste": locales.SetRightClickPaste(), "copyOrPaste": locales.SetRightClickCopyOrPaste()}[id]
			}, 170, pal)
		})
		settingRow(c, pal, locales.SetClipboardRead(), locales.SetClipboardReadDetail(), func() {
			segmented(c, "clip", &s.Clipboard, []string{"ask", "allow", "deny"}, []string{locales.SetAsk(), locales.SetAllow(), locales.SetDeny()})
		})
	})
	// mouse-scroll-multiplier's discrete: and precision:.
	settingsGroup(c, pal, locales.SetScrolling(), "", func() {
		settingRow(c, pal, locales.SetWheelScroll(), locales.SetWheelScrollDetail(), func() {
			rows := []float32{1, 2, 3, 4, 5, 6, 8, 10}
			if !slices.Contains(rows, s.ScrollDiscrete) {
				rows = append(rows, s.ScrollDiscrete)
				slices.Sort(rows)
			}
			popup(c, "wheel", &s.ScrollDiscrete, rows, func(n float32) string {
				if n == float32(int(n)) {
					return locales.SetScrollRows(int(n))
				}
				return strconv.FormatFloat(float64(n), 'f', -1, 32)
			}, 110, pal)
		})
		settingRow(c, pal, locales.SetTrackpadScroll(), locales.SetTrackpadScrollDetail(), func() {
			v := float64(s.ScrollPrecision)
			slider(c, "trackpad", &v, 0.25, 3, 160, pal).OnChange(func() { a.set.ScrollPrecision = float32(int(v*20+0.5)) / 20 })
			ui.Textf(c, "%.2f×", s.ScrollPrecision).FontSize(fsSmall).TextColor(pal.textMuted).Width(40).FontFeatures("tnum")
		})
	})
}

// terminalPreview shows text as the terminals would draw it: the font,
// its size and line height, and the colors of the window's appearance.
func (a *App) terminalPreview(c *ui.Context, pal *palette) {
	light, dark := a.termThemes()
	th := light
	if pal.dark {
		th = dark
	}
	font := a.termFamily()
	features := ""
	if !a.set.Ligatures {
		features = "-calt,-liga"
	}
	line := func(spans ...ui.Span) {
		for i := range spans {
			spans[i].Font, spans[i].Size, spans[i].Weight, spans[i].Features = font, a.set.FontSize, a.set.FontWeight, features
		}
		ui.RichText(c, spans...).LineHeight(a.set.LineHeight).SingleLine()
	}
	box := raised(ui.Column(c).Radius(rCard).Padding(16, 18).Gap(0).Background(th.Background), pal)
	box.Children(func() {
		// Nerd Fonts' folder and branch icons: fonts without them show
		// boxes, which is worth knowing before picking one.
		line(ui.Span{Text: "\uf07b ~/dev/tuios ", Color: th.Palette[4]}, ui.Span{Text: "\ue0a0 main ", Color: th.Palette[5]}, ui.Span{Text: "❯ ", Color: th.Palette[2]}, ui.Span{Text: "git status", Color: th.Foreground})
		line(ui.Span{Text: "modified:   ", Color: th.Foreground}, ui.Span{Text: "settings_view.go", Color: th.Palette[1]})
		line(ui.Span{Text: "func ", Color: th.Palette[5]}, ui.Span{Text: "main", Color: th.Palette[4]}, ui.Span{Text: "() { x := a != b && c >= d => ok }", Color: th.Foreground})
		ui.Row(c).Gap(4).Margin(10, 0, 0, 0).Children(func() {
			for i := range 16 {
				ui.Box(c).Size(16, 10).Radius(rChip).Background(th.Palette[i])
			}
		})
	})
}

func (a *App) shellPage(c *ui.Context, pal *palette) {
	s := &a.set
	// The shell a pane runs: the daemon takes $SHELL before the
	// account's shell, as panes started by guios inherit it.
	login := os.Getenv("SHELL")
	if login == "" {
		login = shell.Login()
	}
	note := locales.SetShellGroupDetail()
	if len(a.hosts) > 1 {
		note += " " + locales.SetShellThisComputer()
	}
	settingsGroup(c, pal, locales.SetShellGroup(), note, func() {
		settingRow(c, pal, locales.SetShell(), locales.SetShellDetail(proto.ShortDir(login)), func() {
			if !a.ss.shellEditing {
				a.ss.shellDraft = s.Shell
			}
			in := textField(c, "shell", &a.ss.shellDraft, proto.ShortDir(login), 220, pal)
			a.ss.shellEditing = in.Focused()
			// Kept as it is submitted, or as the field loses the focus.
			in.OnSubmit(func() { a.set.Shell = strings.TrimSpace(a.ss.shellDraft) })
			if !a.ss.shellEditing && strings.TrimSpace(a.ss.shellDraft) != s.Shell {
				a.set.Shell = strings.TrimSpace(a.ss.shellDraft)
			}
		})
		settingRow(c, pal, locales.SetScrollback(), locales.SetScrollbackDetail(), func() {
			sizes := []int{0, 1, 4, 16, 64}
			if !slices.Contains(sizes, s.Scrollback) {
				sizes = append(sizes, s.Scrollback)
			}
			popup(c, "sb", &s.Scrollback, sizes, func(mb int) string {
				if mb == 0 {
					return locales.CommonNone()
				}
				return strconv.Itoa(mb) + " MB"
			}, 130, pal)
		})
	})
	settingsGroup(c, pal, locales.SetEnvironment(), locales.SetEnvironmentDetail(), func() {
		for i, kv := range s.Env {
			k, v, _ := strings.Cut(kv, "=")
			ui.Row(c.Key("env"+strconv.Itoa(i))).Padding(8, 0).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, k).Font("monospace").FontSize(fsBody).FontWeight(wSemibold).TextColor(pal.text)
				ui.Text(c, v).Font("monospace").FontSize(fsSmall).TextColor(pal.textMuted).Grow(1).SingleLine().Ellipsis("…").MinWidth(0)
				idx := i
				iconButton(c, "rm", closeIcon, locales.CommonRemove(), pal, func() {
					a.set.Env = slices.Delete(slices.Clone(a.set.Env), idx, idx+1)
				})
			})
		}
		ui.Row(c).Padding(8, 0).Gap(8).AlignItems(ui.Center).Children(func() {
			addEnv := func() {
				if kv := strings.TrimSpace(a.ss.envDraft); strings.Contains(kv, "=") {
					a.set.Env = append(slices.Clone(a.set.Env), kv)
					a.ss.envDraft = ""
				}
			}
			textField(c, "env-new", &a.ss.envDraft, "NAME=value", 0, pal).Font("monospace").OnSubmit(addEnv)
			if fieldButton(c, "env-add", locales.CommonAdd(), false, pal) {
				addEnv()
			}
		})
	})
}

func (a *App) notificationsPage(c *ui.Context, pal *palette) {
	s := &a.set
	note := ""
	if !mygo.NotificationsSupported() {
		note = locales.SetNotifyUnpackaged()
	}
	settingsGroup(c, pal, locales.SetNotifyWhen(), note, func() {
		settingRow(c, pal, locales.SetNotifyWaiting(), locales.SetNotifyWaitingDetail(), func() {
			toggle(c.Key("nw"), &s.NotifyWaiting, pal)
		})
		settingRow(c, pal, locales.SetNotifyDone(), locales.SetNotifyDoneDetail(), func() {
			toggle(c.Key("nd"), &s.NotifyDone, pal)
		})
		settingRow(c, pal, locales.SetNotifyFailed(), locales.SetNotifyFailedDetail(), func() {
			toggle(c.Key("nf"), &s.NotifyFailed, pal)
		})
	})
	settingsGroup(c, pal, locales.SetHow(), "", func() {
		settingRow(c, pal, locales.SetPlaySound(), "", func() { toggle(c.Key("ns"), &s.NotifySound, pal) })
		// The Dock is macOS's: elsewhere these rows would set nothing.
		if runtime.GOOS == "darwin" {
			settingRow(c, pal, locales.SetDockBadge(), locales.SetDockBadgeDetail(), func() { toggle(c.Key("nb"), &s.DockBadge, pal) })
			settingRow(c, pal, locales.SetDockBounce(), locales.SetDockBounceDetail(), func() { toggle(c.Key("nbo"), &s.BounceDock, pal) })
		}
	})
	if button(c, "test-notif", locales.SetTestNotification(), "", false, pal) {
		n := mygo.NewNotification(mygo.NotificationOptions{Title: "guios", Body: locales.SetTestNotificationBody(), Silent: !s.NotifySound})
		go func() {
			if err := n.Show(); err != nil {
				a.update(func() {
					a.ss.toast = locales.SetNotifyError(err.Error())
					if a.setWin != nil {
						a.setWin.Invalidate()
					}
				})
			}
		}()
	}
}

func (a *App) agentsPage(c *ui.Context, pal *palette) {
	s := &a.set
	settingsGroup(c, pal, locales.SetRemoteControl(), "", func() {
		settingRow(c, pal, locales.SetRemoteControlRow(), locales.SetRemoteControlDetail(), func() {
			toggle(c.Key("remote"), &s.RemoteControl, pal)
		})
	})
	codeBlock(c, pal, "tuios new-window\ntuios list-agents\ntuios --help        # every command")
	settingsGroup(c, pal, locales.SetCli(), locales.SetCliDetail(), func() {
		detail := locales.SetCliNotInstalled()
		if a.ss.cliPath != "" {
			detail = a.ss.cliPath
		}
		settingRow(c, pal, locales.SetCliRow(), detail, func() {
			label := locales.CommonInstall()
			if a.ss.cliPath != "" {
				label = locales.CommonReinstall()
			}
			if button(c, "cli", label, "", a.ss.cliPath == "", pal) {
				a.installCLI()
				time.AfterFunc(1500*time.Millisecond, func() { a.update(a.refreshAgents) })
			}
		})
	})
	settingsGroup(c, pal, locales.SetSkills(), locales.SetSkillsDetail(), func() {
		for _, st := range cli.SkillStates() {
			name := st.Name
			detail := map[string]string{"current": locales.SetSkillInstalled(), "outdated": locales.SetSkillOutdated(), "missing": locales.SetSkillMissing()}[st.State] + " · " + proto.ShortDir(st.Dir)
			settingRow(c, pal, name, detail, func() {
				agent := st.Agent
				if st.State != "missing" && button(c, "rm-"+agent, locales.CommonRemove(), "", false, pal) {
					if err := cli.RemoveSkill(agent); err != nil {
						a.toastIn(c, err.Error())
					}
				}
				if st.State != "current" {
					label := locales.CommonInstall()
					if st.State == "outdated" {
						label = locales.CommonUpdate()
					}
					if button(c, "in-"+agent, label, "", true, pal) {
						if err := cli.InstallSkill(agent); err != nil {
							a.toastIn(c, err.Error())
						}
					}
				}
			})
		}
	})
	settingsGroup(c, pal, locales.SetProgramStatus(), locales.SetProgramStatusDetail(), nil)
	codeBlock(c, pal, "tuios status working --msg \"Deploying\" --progress 40\ntuios status blocked --kind permission --msg \"Approve the migration?\"\ntuios status done --msg \"Deployed\"")
}

// docLink opens a page of Kero's documentation in the browser: the
// accent's words and an arrow, under the group it tells more of.
func docLink(c *ui.Context, pal *palette, key, label, url string) {
	b := ui.ButtonBase(c.Key(key)).AlignSelf(ui.Start).Padding(2, 4).Gap(3).Radius(rChip).AlignItems(ui.Center).Label(label).Tooltip(url)
	col := pal.accent
	if b.Hovered() {
		col = col.Mix(pal.text, 0.25)
	}
	b.TextColor(col).Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium)
		ui.Icon(c, linkIcon).Size(12, 12)
	})
	if b.Clicked() {
		mygo.Shell.OpenExternal(url)
	}
}

func codeBlock(c *ui.Context, pal *palette, code string) {
	ui.Text(c, code).Font("monospace").FontSize(fsSmall).LineHeight(1.55).TextColor(pal.text).
		Padding(12, 16).Radius(rControl).Background(pal.chip).Selectable()
}

func (a *App) advancedPage(c *ui.Context, pal *palette) {
	settingsGroup(c, pal, locales.SetConfiguration(), locales.SetConfigurationDetail(), func() {
		settingPathRow(c, pal, locales.SetConfigFile(), proto.ShortDir(settings.Path()), func() {
			if button(c, "reveal-config", locales.CommonShowIn(fileManager()), "", false, pal) {
				a.ensureConfig()
				revealPath(settings.Path())
			}
			if button(c, "edit-config", locales.CommonOpen(), "", true, pal) {
				a.openConfig()
			}
		})
		for i, p := range a.ss.problems {
			ui.Row(c.Key("problem"+strconv.Itoa(i))).Padding(0, 0, 8, 0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(6, 6).Radius(3).Background(pal.attention).Shrink(0)
				ui.Text(c, p).FontSize(fsSmall).TextColor(pal.attnText).Grow(1).MinWidth(0)
			})
		}
	})
	settingsGroup(c, pal, locales.SetBackgroundService(), locales.SetBackgroundServiceDetail(), func() {
		settingRow(c, pal, locales.SetDaemonStatus(), a.ss.service, func() {
			if button(c, "svc-restart", locales.CommonRestart(), "", false, pal) {
				a.ss.confirm = &confirmation{
					open: true, title: locales.ConfirmRestartServiceTitle(),
					detail: locales.RestartServiceDetail(),
					action: locales.CommonRestart(), destroy: true,
					run: func() { a.restartDaemon(); time.AfterFunc(2*time.Second, func() { a.update(a.refreshService) }) },
				}
			}
		})
		settingPathRow(c, pal, locales.SetLog(), proto.ShortDir(session.DefaultDaemonLogPath()), func() {
			if button(c, "log", locales.CommonShow(), "", false, pal) {
				revealPath(session.DefaultDaemonLogPath())
			}
		})
	})
	settingsGroup(c, pal, locales.SetData(), locales.SetDataDetail(), func() {
		settingPathRow(c, pal, locales.SetFolder(), proto.ShortDir(proto.Dir()), func() {
			if button(c, "reveal", locales.CommonShowIn(fileManager()), "", false, pal) {
				openPath(proto.Dir())
			}
		})
		settingRow(c, pal, locales.SetReset(), locales.SetResetDetail(), func() {
			if button(c, "reset", locales.SetResetButton(), "", false, pal) {
				a.ss.confirm = &confirmation{
					open: true, title: locales.SetResetTitle(), detail: locales.SetResetConfirmDetail(), action: locales.SetResetAction(), destroy: true,
					run: func() { a.applySettings(settings.Default()) },
				}
			}
		})
	})
}

// ensureConfig writes the configuration file, with its header, when
// there is none yet, so that there is a file to open.
func (a *App) ensureConfig() {
	if _, err := os.Stat(settings.Path()); err != nil {
		settings.Save(a.set)
	}
}

// openConfig opens the configuration in the user's text editor.
func (a *App) openConfig() {
	a.ensureConfig()
	openTextFile(settings.Path())
}

// toastIn shows a short message in the window c is of.
func (a *App) toastIn(c *ui.Context, msg string) { c.Toast(msg) }

// --- keyboard ---

func (a *App) keyboardPage(c *ui.Context, pal *palette) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		textField(c, "kb-search", &a.ss.filter, locales.SetKeyboardSearch(), 0, pal)
		if len(a.set.Shortcuts) > 0 && button(c, "kb-reset", locales.SetRestoreDefaults(), "", false, pal) {
			a.set.Shortcuts = nil
			a.ss.notice = ""
		}
	})
	if a.ss.notice != "" {
		ui.Text(c, a.ss.notice).FontSize(fsSmall).TextColor(pal.attnText).Padding(0, 4)
	}
	q := strings.ToLower(strings.TrimSpace(a.ss.filter))
	if q == "" {
		settingsGroup(c, pal, "tuios", "", func() {
			settingRow(c, pal, locales.SetTuiosKeys(), locales.SetTuiosKeysDetail(), func() {
				toggle(c.Key("tuios-keys"), &a.set.TuiosKeys, pal)
			})
		})
	}
	menus := []string{"Kero", "Session", "Edit", "Pane", "Layout", "Workspace", "View"}
	for _, menu := range menus {
		var acts []actions.Action
		for _, act := range actions.All {
			keys := actions.Glyphs(actions.Keys(act, a.set.Shortcuts))
			found := q == "" || strings.Contains(strings.ToLower(actionTitle(act.ID)), q) || strings.Contains(strings.ToLower(act.Title), q) || strings.Contains(strings.ToLower(keys), q) || strings.Contains(act.ID, q)
			if searchHits != nil {
				found = found && searchHits[strings.TrimSuffix(actionTitle(act.ID), "…")]
			}
			if act.Menu == menu && found {
				acts = append(acts, act)
			}
		}
		if len(acts) == 0 {
			continue
		}
		title := map[string]func() string{"Kero": locales.MenuApp, "Session": locales.MenuSession, "Edit": locales.MenuEdit, "Pane": locales.MenuPane, "Layout": locales.MenuLayout, "Workspace": locales.MenuWorkspace, "View": locales.MenuView}[menu]()
		settingsGroup(c, pal, title, "", func() {
			for _, act := range acts {
				a.shortcutRow(c, pal, act)
			}
		})
	}
	ui.Text(c, locales.SetKeyboardHelp()).FontSize(fsSmall).TextColor(pal.textFaint).Padding(0, 4)
}

func (a *App) shortcutRow(c *ui.Context, pal *palette, act actions.Action) {
	keys := actions.Keys(act, a.set.Shortcuts)
	_, custom := a.set.Shortcuts[act.ID]
	ui.Row(c.Key(act.ID)).Padding(7, 0).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, strings.TrimSuffix(actionTitle(act.ID), "…")).FontSize(fsBody).TextColor(pal.text).SingleLine()
			if act.Global {
				ui.Text(c, locales.SetShortcutGlobal()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
			}
		})
		if custom && keys != act.Keys {
			b := ui.ButtonBase(c.Key("undo")).Size(22, 22).Radius(rRow).Center().TextColor(pal.textFaint).Label(locales.SetUseDefault(actions.Glyphs(act.Keys))).Tooltip(locales.SetDefaultKeys(actions.Glyphs(act.Keys)))
			if b.Hovered() {
				b.Background(pal.rowHover).TextColor(pal.text)
			}
			b.Children(func() { ui.Icon(c, returnIcon).Size(12, 12) })
			if b.Clicked() {
				delete(a.set.Shortcuts, act.ID)
				if len(a.set.Shortcuts) == 0 {
					a.set.Shortcuts = nil
				}
			}
		}
		recording := a.ss.recording == act.ID
		chip := ui.ButtonBase(c.Key("keys")).Height(26).MinWidth(84).Padding(0, 10).Radius(rRow).Center().Label(locales.SetShortcutFor(strings.TrimSuffix(actionTitle(act.ID), "…")))
		bg := pal.control
		switch {
		case recording:
			bg = pal.accent.Alpha(0.18)
		case chip.Hovered():
			bg = pal.controlHover
		}
		face(chip, pal, bg)
		if chip.Clicked() && !recording {
			a.setRecording(act.ID)
			a.ss.recordRef.Focus()
		}
		if recording {
			chip.Bind(&a.ss.recordRef).HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputKeyDown {
					return ev.Kind == ui.InputText
				}
				return a.recordKey(act, ev.Mods, ev.Key)
			})
			if !a.ss.recordRef.Focused(c) && !chip.Pressed() {
				// Focus went elsewhere: the shortcut stays.
				c.After(200 * time.Millisecond)
			}
		}
		chip.Children(func() {
			switch {
			case recording:
				ui.Text(c, locales.SetTypeShortcut()).FontSize(fsBody).TextColor(pal.accent).SingleLine()
			case keys == "":
				ui.Text(c, locales.CommonNone()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
			default:
				ui.Text(c, actions.Glyphs(keys)).FontSize(fsBody).TextColor(pal.text).SingleLine().FontFeatures("tnum")
			}
		})
	})
}

// setRecording starts or ends typing a shortcut. While one is typed, the
// menus give up their key equivalents, which macOS gives keys to first:
// else typing one bound already, as ⌘D, would split a pane, not record.
func (a *App) setRecording(id string) {
	if a.ss.recording == id {
		return
	}
	was := a.ss.recording != ""
	a.ss.recording = id
	if was != (id != "") && a.win != nil {
		mygo.App.SetMenu(a.menu())
		a.applyGlobalShortcuts()
	}
}

// shortcutNeeds tells what a shortcut needs, typed without it.
func shortcutNeeds() string {
	if runtime.GOOS == "darwin" {
		return locales.SetShortcutNeedsMac()
	}
	return locales.SetShortcutNeedsPc()
}

// recordKey takes a key typed for a shortcut, reporting whether it took it.
func (a *App) recordKey(act actions.Action, mods ui.Modifiers, key ui.Key) bool {
	switch {
	case key == ui.KeyEscape && mods == 0:
		a.setRecording("")
		return true
	case (key == ui.KeyBackspace || key == ui.KeyDelete) && mods == 0:
		a.setShortcut(act.ID, "")
		a.setRecording("")
		return true
	}
	name, ok := keyNames[key]
	if !ok {
		return false // a modifier alone, or a key menus can't take
	}
	if mods&(ui.Super|ui.Ctrl|ui.Alt) == 0 && !strings.HasPrefix(name, "F") {
		a.ss.notice = shortcutNeeds()
		return true
	}
	var parts []string
	if mods&ui.Super != 0 && runtime.GOOS == "darwin" {
		parts = append(parts, "CmdOrCtrl")
	} else if mods&ui.Super != 0 {
		parts = append(parts, "Super")
	}
	if mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	accel := strings.Join(append(parts, name), "+")
	a.setShortcut(act.ID, accel)
	a.setRecording("")
	return true
}

// setShortcut binds an action, taking the keys from any other that had
// them.
func (a *App) setShortcut(id, accel string) {
	sc := map[string]string{}
	for k, v := range a.set.Shortcuts {
		sc[k] = v
	}
	a.ss.notice = ""
	if accel != "" {
		for _, other := range actions.All {
			if other.ID != id && strings.EqualFold(actions.Keys(other, sc), accel) {
				sc[other.ID] = ""
				a.ss.notice = locales.SetShortcutTaken(actions.Glyphs(accel), strings.TrimSuffix(actionTitle(other.ID), "…"))
			}
		}
	}
	act, _ := actions.Find(id)
	if accel == act.Keys {
		delete(sc, id)
	} else {
		sc[id] = accel
	}
	if len(sc) == 0 {
		sc = nil
	}
	a.set.Shortcuts = sc
}

// keyNames are the keys a shortcut takes, by their accelerator names.
var keyNames = func() map[ui.Key]string {
	m := map[ui.Key]string{
		ui.KeyEnter: "Enter", ui.KeyTab: "Tab", ui.KeySpace: "Space", ui.KeyBackspace: "Backspace", ui.KeyDelete: "Delete",
		ui.KeyEscape: "Escape", ui.KeyHome: "Home", ui.KeyEnd: "End", ui.KeyPageUp: "PageUp", ui.KeyPageDown: "PageDown",
		ui.KeyLeft: "Left", ui.KeyRight: "Right", ui.KeyUp: "Up", ui.KeyDown: "Down",
		ui.KeyMinus: "-", ui.KeyEqual: "=", ui.KeyComma: ",", ui.KeyPeriod: ".", ui.KeySlash: "/", ui.KeySemicolon: ";",
		ui.KeyQuote: "'", ui.KeyBracketLeft: "[", ui.KeyBracketRight: "]", ui.KeyBackslash: "\\", ui.KeyBackquote: "`",
	}
	fs := []ui.Key{ui.KeyF1, ui.KeyF2, ui.KeyF3, ui.KeyF4, ui.KeyF5, ui.KeyF6, ui.KeyF7, ui.KeyF8, ui.KeyF9, ui.KeyF10, ui.KeyF11, ui.KeyF12}
	for i, k := range fs {
		m[k] = "F" + strconv.Itoa(i+1)
	}
	digits := []ui.Key{ui.Key0, ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	for i, k := range digits {
		m[k] = strconv.Itoa(i)
	}
	letters := []ui.Key{ui.KeyA, ui.KeyB, ui.KeyC, ui.KeyD, ui.KeyE, ui.KeyF, ui.KeyG, ui.KeyH, ui.KeyI, ui.KeyJ, ui.KeyK, ui.KeyL, ui.KeyM,
		ui.KeyN, ui.KeyO, ui.KeyP, ui.KeyQ, ui.KeyR, ui.KeyS, ui.KeyT, ui.KeyU, ui.KeyV, ui.KeyW, ui.KeyX, ui.KeyY, ui.KeyZ}
	for i, k := range letters {
		m[k] = string(rune('A' + i))
	}
	return m
}()
