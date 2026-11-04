package main

import (
	"errors"
	"runtime"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// webEntry is a browser pane's web view in the app: kerod keeps the pane's
// place and page, the app the page itself, which it loads where kerod
// last had it and tells kerod of as the user goes to others.
type webEntry struct {
	view *mygo.WebView
	err  string
	// url and title are the page's, as it last committed; told is what
	// kerod was last told.
	url, title string
	told       [2]string
	loading    bool
	back, fwd  bool
	// address is the address bar's text: the page's URL, unless the user
	// is typing in it (editing); focusAddress has it take the keyboard,
	// its text selected, as Command-L does.
	address      string
	editing      bool
	focusAddress bool
	// focused tells that the web view was given the keyboard since its
	// element took the focus, which a press on its page takes back.
	focused bool
	// The find bar's matches: a search runs off the main thread, and the
	// last asked for wins.
	found    mygo.FindResult
	findSeq  int
	findText string
	// loads are kero browser's calls waiting for the page's load to end.
	loads []chan error
	// iconURL is the URL of the icon the page told: until it does, its
	// site's last shows.
	iconURL string
	// staged lays the page out unseen, for kero browser screenshot of a
	// pane never shown, which has no size, and so no picture.
	staged bool
}

// browserUA makes the page look like Safari's on macOS: sites serve a bare
// WKWebView, whose user agent names no browser, older pages.
func browserUA() string {
	if runtime.GOOS == "darwin" {
		return "Version/18.0 Safari/605.1.15"
	}
	return ""
}

// ensureWeb makes the web view of a browser pane, on the page kerod has.
func (a *App) ensureWeb(p *proto.Pane) *webEntry {
	if e := a.webs[p.ID]; e != nil {
		return e
	}
	e := &webEntry{url: p.URL, title: p.Title, address: p.URL, told: [2]string{p.URL, p.Title}}
	if p.URL == proto.BlankURL {
		e.address, e.focusAddress = "", true
	}
	a.webs[p.ID] = e
	if a.win == nil {
		return e
	}
	v, err := a.win.NewWebView(mygo.WebViewOptions{
		Page: mygo.PageOptions{DevTools: mygo.DevToolsEnabled, UserAgentSuffix: browserUA(), ContentRules: a.adRules()},
	})
	if err != nil {
		e.err = err.Error()
		return e
	}
	e.view = v
	id := p.ID
	page := v.Page()
	// The page's events come on the main thread: they change what the
	// pane shows, and what kerod keeps.
	page.OnDidNavigate(func(url string) {
		a.update(func() {
			if a.webs[id] != e {
				return
			}
			e.url, e.err, e.iconURL = url, "", ""
			if !e.editing {
				e.address = url
			}
			e.syncNav()
			a.tellPage(id, e)
		})
	})
	page.OnPageTitleUpdated(func(t *mygo.TitleEvent) {
		a.update(func() {
			if a.webs[id] != e {
				return
			}
			e.title = t.Title
			// A page of an app changes its URL without navigating, and its
			// title with it.
			if url := page.URL(); url != "" {
				e.url = url
				if !e.editing {
					e.address = url
				}
			}
			e.syncNav()
			a.tellPage(id, e)
			// A page of an app changes its icon as it does its title.
			a.findFavicon(e)
		})
	})
	page.OnDidFinishLoad(func() {
		a.update(func() {
			e.syncNav()
			e.loadEnded(nil)
			a.findFavicon(e)
		})
	})
	page.OnDidFailLoad(func(err *mygo.LoadError) {
		a.update(func() {
			e.syncNav()
			// A navigation the user left before it ended fails too: the
			// one that left it ends later.
			if err.Code != -999 && err.Description != "" {
				e.err = err.Description
				e.loadEnded(errors.New(err.Description))
			}
		})
	})
	page.OnRenderProcessGone(func(string) { page.Reload() })
	if p.URL != proto.BlankURL {
		e.load(p.URL)
	}
	return e
}

// syncNav reads where the page can go and whether it loads.
func (e *webEntry) syncNav() {
	if e.view == nil || e.view.IsDestroyed() {
		return
	}
	page := e.view.Page()
	e.back, e.fwd, e.loading = page.CanGoBack(), page.CanGoForward(), page.IsLoading()
}

// load goes to what the user typed, as a URL or a search.
func (e *webEntry) load(text string) {
	url := proto.BrowserURL(text)
	e.url, e.address, e.err = url, url, ""
	if e.view == nil {
		return
	}
	if err := e.view.Page().LoadURL(url); err != nil {
		e.err = err.Error()
	}
	e.loading = true
}

// tellPage tells kerod the page a browser pane shows, which it keeps for
// a restore, and the title that names the pane.
func (a *App) tellPage(id string, e *webEntry) {
	if e.url == "" || e.told == [2]string{e.url, e.title} {
		return
	}
	e.told = [2]string{e.url, e.title}
	if s := a.sessionOfPane(id); s != nil {
		if p := s.PaneByID(id); p != nil {
			p.URL, p.Title = e.url, e.title
		}
	}
	a.call(id, "pane.page", proto.PageParams{ID: id, URL: e.url, Title: e.title}, nil)
}

func (a *App) dropWeb(id string) {
	if e := a.webs[id]; e != nil {
		if e.view != nil {
			e.view.Destroy()
		}
		delete(a.webs, id)
	}
}

// sessionOfPane returns the session a pane is of.
func (a *App) sessionOfPane(id string) *proto.Session {
	for i := range a.st.Sessions {
		if a.st.Sessions[i].PaneByID(id) != nil {
			return &a.st.Sessions[i]
		}
	}
	return nil
}

// isBrowser reports whether a pane is a browser's.
func (a *App) isBrowser(id string) bool {
	if s := a.sessionOfPane(id); s != nil {
		return s.PaneByID(id).Browser()
	}
	return false
}

// openBrowser opens a browser pane beside the pane the user is in, or the
// address bar of the browser they are in.
func (a *App) openBrowser(url string) {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	at := a.focusOf(s)
	if e := a.webs[at]; e != nil && url == "" {
		e.focusAddress = true
		return
	}
	if url == "" {
		url = proto.BlankURL
	}
	a.split(s, proto.CreateParams{Split: at, Direction: "right", URL: url})
}

// focusAddress gives the address bar of the browser the user is in the
// keyboard.
func (a *App) focusAddress() {
	if s := a.session(a.selected); s != nil {
		if e := a.webs[a.focusOf(s)]; e != nil {
			e.focusAddress = true
		}
	}
}

// paneBrowser is a browser pane: its toolbar, which is its header, over
// its page; it reports whether the pane is being dragged by its toolbar.
func (a *App) paneBrowser(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, p *proto.Pane, top, left, right, multi, focused, hovered bool) (dragging bool) {
	e := a.ensureWeb(p)
	ref := a.paneRef(p.ID)
	// What the page was as the build began: the address bar, which holds
	// the pane's handle while there is no page, may go to one as it
	// builds, and the page then takes the handle in the next build.
	blank := e.url == proto.BlankURL
	var tools ui.Element
	tools, dragging = a.browserBar(c, pal, bar, shown, s, p, e, top, left, right, multi, focused, hovered)
	if blank && e.url != proto.BlankURL {
		c.Invalidate()
	}
	ui.Box(c.Key("body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		switch {
		case blank:
			// Nothing to show yet: the address bar asks.
			ui.Column(c).Fill().Center().Gap(10).Children(func() {
				ui.Icon(c, globeIcon).Size(36, 36).TextColor(pal.textFaint)
				ui.Text(c, locales.BrowserBlank()).FontSize(fsBody).TextColor(pal.textMuted)
			})
		case e.view == nil:
			a.message(c, pal, locales.BrowserCantShow(), e.err, "", nil)
		default:
			web := ui.WebView(c.Key("web"), e.view).Grow(1).Bind(ref)
			// The page took the keyboard as it was clicked: the pane is
			// the one the user is in.
			if web.Hovered() && tools.PressedOutside() && a.focusOf(s) != p.ID {
				a.setFocus(s, p.ID)
			}
			// The focus came to its element, from the keyboard or the
			// app: the page takes it.
			switch {
			case ref.Focused(c) && !e.focused:
				e.focused = true
				e.view.Focus()
				if a.focusOf(s) != p.ID {
					a.setFocus(s, p.ID)
				}
			case !ref.Focused(c):
				e.focused = false
			}
			if e.err != "" {
				a.browserError(c, pal, e)
			}
			if a.find.pane == p.ID {
				a.browserFind(c, pal, e)
			}
		}
	})
	return dragging
}

// browserBar is a browser pane's header: back, forward, reload and the
// address bar, which the pane is dragged by, then the pane's buttons. As
// a terminal's header, it drags the window where it is empty, and keeps
// clear of the traffic lights and the sidebar's buttons.
func (a *App) browserBar(c *ui.Context, pal *palette, bar ui.TitleBar, shown float32, s *proto.Session, p *proto.Pane, e *webEntry, top, left, right, multi, focused, hovered bool) (ui.Element, bool) {
	pad := float32(16)
	if top && left {
		pad = a.chrome.headerPad
	}
	// The first button's icon starts where a header's title does.
	h := ui.Row(c.Key("hdr")).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, pad-6).Gap(2)
	if h.Pressed() && !focused {
		a.setFocus(s, p.ID)
		a.paneRef(p.ID).Focus()
	}
	dragging := false
	h.Children(func() {
		if top && left {
			a.sidebarButtons(c, pal, bar, shown)
		}
		navButton(c, "back", chevronLeftIcon, locales.BrowserBack(), e.back, pal, func() { e.view.Page().GoBack() })
		navButton(c, "forward", chevronIcon, locales.BrowserForward(), e.fwd, pal, func() { e.view.Page().GoForward() })
		if e.loading {
			navButton(c, "stop", closeIcon, locales.BrowserStop(), true, pal, func() { e.view.Page().Stop(); e.loading = false })
			// Loading ends with an event, which a page that never ends
			// loading sends late: look again meanwhile.
			c.After(500 * time.Millisecond)
			e.syncNav()
		} else {
			navButton(c, "reload", reloadIcon, locales.BrowserReload(), e.view != nil && e.url != proto.BlankURL, pal, func() {
				e.view.Page().Reload()
				e.loading = true
			})
		}
		gap := ui.Box(c.Key("drag-l")).Width(8).Shrink(0).AlignSelf(ui.Stretch)
		dragging = a.addressBar(c, pal, s, p, e, multi)
		// The room the address bar leaves moves the window, and zooms it
		// on a double click.
		rest := ui.Box(c.Key("drag")).Grow(1).MinWidth(8).AlignSelf(ui.Stretch)
		if top {
			gap.DragWindow()
			rest.DragWindow()
		}
		// The room past the address bar takes the buttons as they show.
		// The button that shows the right sidebar sits by the pane's own,
		// as one row of buttons.
		ui.Row(c.Key("buttons")).AlignItems(ui.Center).Children(func() {
			a.paneControls(c, pal, s, p, hovered && !dragging, multi)
			if top && right {
				a.showRightButton(c, pal)
			}
		})
		if top && right {
			a.controlsRoom(c, 2)
		}
	})
	return h, dragging
}

// navButton is a toolbar's button, faint while it can do nothing.
func navButton(c *ui.Context, key string, svg *ui.SVG, label string, enabled bool, pal *palette, fn func()) {
	b := ui.ButtonBase(c.Key(key)).Size(28, 26).Radius(rRow).Center().TextColor(pal.textMuted).Label(label).Tooltip(label).Disabled(!enabled)
	switch {
	case !enabled:
		b.TextColor(pal.textFaint.Alpha(0.6))
	case b.Pressed():
		b.Background(pal.rowHover.Alpha(0.12))
	case b.Hovered():
		b.Background(pal.rowHover).TextColor(pal.text)
	}
	b.Children(func() { ui.Icon(c, svg).Size(16, 16) })
	if enabled && b.Clicked() {
		fn()
	}
}

// addressBar is where the page's URL shows, and the user types another, or
// words to search for. With several panes, its globe is the pane's handle,
// as a terminal's title is: it reports whether it is being dragged.
func (a *App) addressBar(c *ui.Context, pal *palette, s *proto.Session, p *proto.Pane, e *webEntry, multi bool) (dragging bool) {
	blank := e.url == proto.BlankURL
	// It takes the room up to a width that reads well, and leaves the
	// rest to move the window by.
	frame := ui.Row(c.Key("address")).Grow(100).MinWidth(120).MaxWidth(760).Height(28).Padding(0, 10, 0, 4).Gap(3).Radius(rControl).
		AlignItems(ui.Center).Background(pal.chip).Cursor(ui.CursorText)
	var in ui.Element
	frame.Children(func() {
		grip := ui.Row(c.Key("grip")).Size(22, 22).Radius(rRow).Center().Shrink(0).TextColor(pal.textFaint)
		if multi {
			grip.Drag(paneDrag{s.ID, p.ID}).Tooltip(locales.PaneDragTip())
			dragging = grip.Dragging()
			switch {
			case dragging:
				floating(grip.Background(pal.popover), pal)
			case grip.Hovered():
				grip.Background(pal.rowHover).TextColor(pal.textMuted).Cursor(ui.CursorGrab)
			}
		}
		grip.Children(func() {
			if icon := a.favicon(e); icon != nil && !blank {
				ui.Image(c, icon).Size(14, 14).Radius(rChip)
			} else {
				ui.Icon(c, globeIcon).Size(14, 14)
			}
		})
		in = ui.TextInputBase(c.Key("in"), &e.address).Grow(1).MinWidth(0).FontSize(fsBody).
			Placeholder(locales.BrowserAddress()).Label(locales.BrowserAddress())
		if blank {
			// With no page, the pane's keyboard is the address bar's.
			in.Bind(a.paneRef(p.ID))
		}
	})
	focused := in.Focused()
	e.editing = focused
	glow := frame.Animate("glow", b2f(focused), dFade)
	if glow > 0 {
		frame.Shadow(0, 0, 0, 3*glow, pal.accent.Alpha(0.3*glow))
	}
	if frame.Pressed() && !focused {
		in.Focus()
	}
	if e.focusAddress {
		e.focusAddress = false
		in.Focus().SetTextSelection(0, utf8.RuneCountInString(e.address))
		if a.focusOf(s) != p.ID {
			a.setFocus(s, p.ID)
		}
	}
	if focused && a.focusOf(s) != p.ID {
		a.setFocus(s, p.ID)
	}
	if in.Submitted() && e.address != "" {
		e.load(e.address)
		// The page takes the keyboard, through its element, which the
		// field gives the focus up to.
		e.focused = false
		a.paneRef(p.ID).Focus()
		a.tellPage(p.ID, e)
	}
	// Escape gives the field back the page's URL, as a browser's does.
	if frame.Shortcut(0, ui.KeyEscape) {
		e.address = e.url
		if e.url == proto.BlankURL {
			e.address = ""
		}
	}
	if !focused && e.url != proto.BlankURL {
		e.address = e.url
	}
	return dragging
}

// browserError shows why the page failed to load, over its top.
func (a *App) browserError(c *ui.Context, pal *palette, e *webEntry) {
	bar := ui.Row(c.Key("error")).Absolute().Top(8).Left(12).Right(12).Padding(8, 8, 8, 12).Gap(8).
		AlignItems(ui.Center).Radius(rCard).Background(pal.popover)
	floating(bar, pal).Children(func() {
		ui.Text(c, e.err).FontSize(fsSmall).TextColor(pal.dangerText).Grow(1).MinWidth(0).Ellipsis("…").SingleLine()
		if button(c, "retry", locales.CommonTryAgain(), "", false, pal) {
			e.load(e.url)
		}
		iconButton(c, "dismiss", closeIcon, locales.FindClose(), pal, func() { e.err = "" })
	})
}

// browserFind is the find bar over a browser's page, as over a terminal.
func (a *App) browserFind(c *ui.Context, pal *palette, e *webEntry) {
	f := &a.find
	if e.findText != f.query {
		a.findInPage(e, false, false)
	}
	bar := ui.Row(c.Key("find")).Absolute().Top(8).Right(12).Height(36).Padding(0, 4, 0, 12).Gap(4).
		AlignItems(ui.Center).Radius(rCard).Background(pal.popover).Label(locales.FindLabel())
	floating(bar, pal).Children(func() {
		ui.Icon(c, searchIcon).Size(14, 14).TextColor(pal.textMuted).Shrink(0)
		in := ui.TextInputBase(c.Key("q"), &f.query).Width(180).FontSize(fsBody).Placeholder(locales.FindPlaceholder()).Bind(&f.input)
		if f.focus {
			f.focus = false
			in.Focus().SetTextSelection(0, utf8.RuneCountInString(f.query))
		}
		if in.Submitted() {
			a.findInPage(e, true, c.Modifiers()&ui.Shift != 0)
		}
		count := ""
		switch {
		case f.query == "":
		case e.found.Matches == 0:
			count = locales.FindNoMatches()
		default:
			count = locales.FindCount(itoa(e.found.Active), itoa(e.found.Matches))
		}
		ui.Text(c, count).FontSize(fsSmall).FontFeatures("tnum").TextColor(pal.textFaint).SingleLine().MinWidth(56).TextAlign(ui.End).Margin(0, 4, 0, 0)
		iconButton(c, "previous", chevronUpIcon, a.tip(locales.ActionFindPrevious(), "find.previous"), pal, func() { a.findInPage(e, true, true) })
		iconButton(c, "next", chevronDownIcon, a.tip(locales.ActionFindNext(), "find.next"), pal, func() { a.findInPage(e, true, false) })
		iconButton(c, "close", closeIcon, locales.FindClose(), pal, func() { a.defer_(a.closeFind) })
	})
	if bar.Shortcut(0, ui.KeyEscape) {
		a.defer_(a.closeFind)
	}
}

// findInPage searches the page for the find bar's query, off the main
// thread, where the page answers: next goes on from the match shown.
func (a *App) findInPage(e *webEntry, next, back bool) {
	if e.view == nil {
		return
	}
	q := a.find.query
	e.findText = q
	e.findSeq++
	seq, page := e.findSeq, e.view.Page()
	if q == "" {
		e.found = mygo.FindResult{}
		go page.StopFindInPage()
		return
	}
	go func() {
		res, err := page.FindInPage(q, mygo.FindOptions{FindNext: next, Backward: back})
		a.update(func() {
			if err == nil && e.findSeq == seq {
				e.found = res
			}
		})
	}()
}
