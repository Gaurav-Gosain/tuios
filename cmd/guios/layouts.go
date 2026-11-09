package main

import (
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Saved layouts, the Layout menu's Save layout… and Load layout…: the
// daemon's layout-template verb, so a layout is a file of the daemon's
// machine, the one the TUI's picker lists for the same session. A save
// keeps the workspace's panes, their names, folders and splits; a load puts
// them back, opening the panes it lacks.

// saveLayout asks for a name and saves the shown workspace under it.
func (a *App) saveLayout() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	sid := s.ID
	a.openPrompt(&promptState{open: true, title: locales.PromptSaveLayout(), detail: locales.PromptSaveLayoutDetail(),
		placeholder: locales.PromptSaveLayoutPlaceholder(),
		run: func(name string) {
			name = strings.TrimSpace(name)
			if name == "" {
				return
			}
			a.call(sid, "layout.save", proto.LayoutTemplateParams{Session: sid, Name: name}, func(err error) {
				if err != nil {
					a.toast(err.Error())
					return
				}
				a.toast(locales.ToastLayoutSaved(name))
			})
		}})
}

// openLayouts lists the saved layouts in the palette, each with its panes
// and when it was saved; a row loads its layout, and its menu deletes it.
func (a *App) openLayouts() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	sid := s.ID
	h := a.hostOf(sid)
	if h.cl == nil {
		a.toast(locales.HostNotConnected(h.label()))
		return
	}
	cl := h.cl
	go func() {
		var ls []proto.SavedLayout
		err := cl.Call("layout.list", proto.LayoutTemplateParams{Session: sid}, &ls)
		a.update(func() {
			if err != nil {
				a.toast(err.Error())
				return
			}
			if len(ls) == 0 {
				a.toast(locales.LayoutsNone())
				return
			}
			a.openPalette()
			a.pal.custom, a.pal.customLabel = a.layoutItems(sid, ls), locales.LayoutsLabel()
		})
	}()
}

// layoutItems are the palette's rows for the layouts ls of session sid.
func (a *App) layoutItems(sid string, ls []proto.SavedLayout) []paletteItem {
	now := time.Now()
	items := make([]paletteItem, 0, len(ls))
	for _, l := range ls {
		name := l.Name
		detail := locales.LayoutPanes(l.Panes)
		if l.SavedAt > 0 {
			detail += " · " + ago(l.SavedAt, now)
		}
		items = append(items, paletteItem{title: name, detail: detail, words: "layout",
			run: func() { a.loadLayout(sid, name) },
			menu: func(m *ui.Menu) {
				if m.Item(locales.LayoutLoad()).Chosen() {
					a.pal.open = false
					a.loadLayout(sid, name)
				}
				m.Separator()
				if m.Item(locales.LayoutDelete()).Chosen() {
					a.deleteLayout(sid, name)
				}
			}})
	}
	return items
}

// loadLayout loads the layout called name on the shown workspace of sid.
func (a *App) loadLayout(sid, name string) {
	a.call(sid, "layout.load", proto.LayoutTemplateParams{Session: sid, Name: name}, func(err error) {
		if err != nil {
			a.toast(err.Error())
			return
		}
		a.toast(locales.ToastLayoutLoaded(name))
	})
}

// deleteLayout asks, then deletes the layout called name and lists the
// layouts again.
func (a *App) deleteLayout(sid, name string) {
	a.confirm = &confirmation{open: true, title: locales.ConfirmDeleteLayout(name), detail: locales.ConfirmDeleteLayoutDetail(),
		action: locales.LayoutDelete(), destroy: true,
		run: func() {
			a.call(sid, "layout.delete", proto.LayoutTemplateParams{Session: sid, Name: name}, func(err error) {
				if err != nil {
					a.toast(err.Error())
					return
				}
				a.toast(locales.ToastLayoutDeleted(name))
				if a.pal.open && a.pal.customLabel == locales.LayoutsLabel() {
					a.openLayouts()
				}
			})
		}}
}
