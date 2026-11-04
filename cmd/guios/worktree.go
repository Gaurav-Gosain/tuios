package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// worktreeAsk asks the name of the branch a new session checks out in a
// worktree of its own (plans/git.md, Phase 3), to start it on a host
// with p.
type worktreeAsk struct {
	open   bool
	branch string
	host   *host
	p      proto.CreateParams
}

// newWorktreeSession asks a branch's name for a session in a worktree of
// its own, of the repository of the session the user is in, else of its
// host's first project.
func (a *App) newWorktreeSession() {
	h := a.selectedHost()
	if h.cl == nil {
		a.toast(locales.HostNotConnected(h.label()))
		return
	}
	var p proto.CreateParams
	if cur := a.session(a.selected); cur != nil && a.hostOf(cur.ID) == h {
		p.Project, p.After, p.Dir = cur.Project, cur.ID, cur.WorkDir()
		p.Cols, p.Rows = cur.Cols, cur.Rows
	}
	if a.set.WorktreeBase == "current" {
		p.Base = "HEAD"
	}
	a.wtAsk = &worktreeAsk{open: true, host: h, p: p}
}

func (a *App) worktreeView(c *ui.Context, pal *palette) {
	w := a.wtAsk
	if w == nil {
		return
	}
	create := false
	ui.DialogBase(c.Key("worktree"), &w.open, func(backdrop, panel ui.Element) {
		backdrop.Background(pal.overlay)
		floating(panel.Width(420).Padding(22).Gap(8).Radius(14).Background(pal.popover), pal).Label(locales.WorktreeTitle())
		ui.Text(c, locales.WorktreeTitle()).FontSize(15).FontWeight(650).TextColor(pal.text)
		detail := locales.WorktreeDetailDefault()
		if w.p.Base == "HEAD" {
			detail = locales.WorktreeDetailCurrent()
		}
		ui.Text(c, detail).FontSize(13).TextColor(pal.textMuted).LineHeight(1.4)
		ui.Row(c).Margin(8, 0, 0, 0).Children(func() {
			in := textField(c, "branch", &w.branch, locales.WorktreeBranch(), 0, pal)
			in.AutoFocus()
			if in.Submitted() {
				create = true
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
			if button(c, "cancel", locales.CommonCancel(), "esc", false, pal) {
				w.open = false
			}
			if button(c, "ok", locales.WorktreeCreate(), "↩", true, pal) {
				create = true
			}
		})
	})
	if branch := strings.TrimSpace(w.branch); create && branch != "" {
		w.open = false
		w.p.Worktree = branch
		a.create(w.host, w.p, w.p.Project)
	}
	if !w.open {
		a.wtAsk = nil
		a.focusTerminal()
	}
}

// askCloseWorktree closes a session in a worktree of its own once the
// user says whether its worktree goes with it, as what kerod finds the
// removal would lose decides: Remove at hand when nothing, Keep when
// changes. A worktree another session works in stays, unasked.
func (a *App) askCloseWorktree(id string) {
	s, h := a.session(id), a.hostOf(id)
	cl := h.cl
	if s == nil || cl == nil {
		return
	}
	name := s.DisplayName(a.set.SessionName)
	go func() {
		var wc proto.WorktreeCheck
		err := cl.Call("worktree.check", proto.IDParams{ID: id}, &wc)
		a.update(func() {
			if a.session(id) == nil {
				return
			}
			keep := func() { a.endSession(id, proto.CloseParams{ID: id}) }
			if err != nil || wc.Shared {
				keep()
				return
			}
			remove := func(force bool) func() {
				return func() { a.endSession(id, proto.CloseParams{ID: id, RemoveWorktree: true, Force: force}) }
			}
			path := h.shortDir(wc.Path)
			cf := &confirmation{open: true, title: locales.WorktreeRemoveTitle(name),
				action: locales.WorktreeRemove(), run: remove(false), alt: locales.WorktreeKeep(), altRun: keep}
			switch {
			case wc.Files > 0:
				cf.detail = locales.WorktreeRemoveDirty(wc.Files, path, wc.Branch)
				cf.action, cf.run = locales.WorktreeKeep(), keep
				cf.alt, cf.altRun, cf.altDestroy = locales.WorktreeRemoveAnyway(), remove(true), true
			case wc.Merged:
				cf.detail = locales.WorktreeRemoveMerged(path, wc.Branch)
			default:
				cf.detail = locales.WorktreeRemoveUnmerged(path, wc.Branch)
			}
			a.confirm = cf
		})
	}()
}
