package main

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/mygo/ui/editor"

	"github.com/Gaurav-Gosain/tuios/internal/guios/git"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// The right sidebar stands at the window's right edge, on its wash as the
// left sidebar does, with what the session's worktree is about, in tabs:
// Changes, the files the session's agent or the user changed, in their
// folders; Files, the worktree's tree (files.go). A file chosen in either
// opens in the viewer, a card beside the panes: Changes' as the diffs of
// them all, one after the other, scrolled to it; Files' as its text.

// The sidebar's tabs, as prefs.Side holds them, and the viewer's kinds.
const (
	sideChanges = "changes"
	sideFiles   = "files"
)

const (
	defaultSide = 420 // the viewer's width, as it first opens
	minSide     = 300
	splitWidth  = 760 // past which diff-layout auto shows diffs side by side
)

// sideState is the right sidebar's and the viewer's, beside the prefs
// that show them and size them.
type sideState struct {
	drag    float32 // the viewer's width as a drag of its edge began, 0 while none
	barDrag float32 // the sidebar's, as a drag of its edge began
	barW    float32 // the sidebar's width as it shows this frame, 0 while hidden
	// crowded tells that the window is too narrow for the sidebar beside
	// the cards (minCards), which it then leaves to them, shown as it is,
	// unless the user shows it anyway, forced, until the window widens.
	crowded, forced bool
	// rowW is the width of the row of the cards, the panes and the viewer,
	// this frame.
	rowW    float32
	edge    float32 // the cards' column's right padding this frame, none beside the sidebar
	focused bool    // the viewer has the keyboard: its Escape, its find
	changes changesState
	files   filesState
	// views are what the viewer shows, by session; backup what the app
	// keeps of them and of their edits not saved as it quits (backup.go).
	views  map[string]*sideView
	backup backupState
	// cf is the Changes tab's for this frame, which the sidebar's tree
	// and the viewer's diffs share (changesFrameOf).
	cf *changesFrame
	// theme is the diffs' colors, made again only as the window's
	// appearance or the terminals' themes change.
	theme    *editor.Theme
	themeKey string
}

// sideView is what a session's viewer shows: the diffs of Changes, or a
// file of Files; kind is "" while it is closed.
type sideView struct {
	kind string
	file *fileView // the file of Files it shows last
	// full shows the viewer in the panes' room, the panes behind it.
	full bool
}

// changesState is the Changes tab's: the files changed since a base,
// each with its diff, one after the other in one scroll.
type changesState struct {
	base string // the base chosen: git.BaseTurn, BaseHead or BaseBranch
	// scope is the pane and base the list is of, and stamp what of its
	// worktree it was fetched at; busy while a fetch is under way.
	scope   string
	stamp   string
	busy    bool
	since   time.Time
	fetched time.Time
	res     *proto.GitChanges
	err     string
	// lists are the lists fetched, by scope, which a base shows again at
	// once as the user comes back to it, while it is fetched anew; shown
	// is the scope the view last showed, and switched when it came to.
	lists    map[string]*proto.GitChanges
	shown    string
	switched time.Time
	// files are the files' diffs, by scope and path, kept as the list is
	// fetched anew; collapsed the files the user folded or unfolded, by
	// the same key; focus the file whose diff has the keyboard, for Find.
	files     map[string]*sideFile
	collapsed map[string]bool
	focus     *sideFile
	// places are the diffs' places, by scope.
	places map[string]*ui.ListState
	// tree is the tree of the files of treeOf, the list it was made of
	// (changes_tree.go); treeClosed the folders the user folded in it, by
	// scope and path; treePlaces its places, by scope. picked is the file
	// last chosen in it, and current the one it showed as there, which it
	// scrolls to as that changes; reveal the one the diffs are to scroll
	// to, as they come.
	tree       []*treeNode
	treeOf     *proto.GitChanges
	treeClosed map[string]bool
	treePlaces map[string]*ui.ListState
	picked     string
	current    string
	reveal     string
}

// sideFile is a changed file's diff since a base, whose new side the
// user edits.
type sideFile struct {
	pane, base string
	root       string // the worktree's, on the host
	edits      edited
	f          proto.GitFile     // the file as listed when its text was asked for
	d          *editor.DiffState // nil until its text comes, or for a binary file
	patch      bool              // d shows a patch: a file too large to show whole
	hash       string            // the file's on disk, as d shows it
	text       proto.GitText     // what came of it last, but its text
	stamp      string            // the worktree's stamp it was fetched at
	busy       bool
	writing    bool
	err        string
}

// sideTab is the tab the sidebar shows, or showed last while it hides.
func (a *App) sideTab() string {
	tab := a.prefs.Side
	if tab == "" {
		tab = a.prefs.SideTab
	}
	if tab != sideFiles {
		tab = sideChanges
	}
	return tab
}

// showTab shows a tab of the sidebar.
func (a *App) showTab(tab string) {
	a.prefs.Side, a.prefs.SideTab = tab, tab
	a.savePrefs()
}

// toggleSide shows a tab of the sidebar, and for Changes their diffs; or,
// when the sidebar shows it, hides the sidebar, closing the viewer with
// what the tab opened in it.
func (a *App) toggleSide(tab string) {
	// A shell's folder changes with no event: read the panes again, so
	// the tab shows the worktree the pane is in now.
	if h := a.local(); h != nil && h.cl != nil {
		h.cl.Refresh()
	}
	s := a.session(a.selected)
	if a.prefs.Side == tab && a.rightShown() {
		a.prefs.Side, a.prefs.SideTab = "", tab
		a.side.forced = false
		if v := a.viewOf(s); v != nil && v.kind == tab {
			a.closeView(s)
		}
	} else {
		a.prefs.Side, a.prefs.SideTab = tab, tab
		a.side.forced = a.side.crowded
		if tab == sideChanges && s != nil {
			a.openView(s, sideChanges, nil)
			// A program asking through kero do leaves the user's keyboard be.
			if a.from == nil {
				a.focusViewer()
			}
		}
	}
	a.savePrefs()
}

// toggleRight shows the sidebar, on the tab it showed last, or hides it.
func (a *App) toggleRight() {
	if a.rightShown() {
		a.prefs.Side, a.prefs.SideTab = "", a.prefs.Side
		a.side.forced = false
	} else {
		if a.prefs.Side == "" {
			a.prefs.Side = a.sideTab()
		}
		a.side.forced = a.side.crowded
	}
	a.savePrefs()
}

// minCards is the room the right sidebar leaves the cards: in a window
// narrower, it gets out of their way (sideState.crowded). It holds a
// terminal of about 70 columns, or two panes side by side that each still
// read a prompt.
const minCards = 600

// rightShown tells that the right sidebar shows, or comes: shown, and the
// window wide enough for it, or shown anyway.
func (a *App) rightShown() bool {
	return a.prefs.Side != "" && (!a.side.crowded || a.side.forced)
}

// viewOf is what a session's viewer shows, nil while it is closed.
func (a *App) viewOf(s *proto.Session) *sideView {
	if s == nil {
		return nil
	}
	if v := a.side.views[s.ID]; v != nil && v.kind != "" {
		return v
	}
	return nil
}

// openView opens a session's viewer on the diffs of Changes, or on a file
// of Files, f, or the one it showed last when nil.
func (a *App) openView(s *proto.Session, kind string, f *fileView) {
	if a.side.views == nil {
		a.side.views = map[string]*sideView{}
	}
	v := a.side.views[s.ID]
	if v == nil {
		v = &sideView{}
		a.side.views[s.ID] = v
	}
	v.kind = kind
	if f != nil {
		if v.file != nil && v.file != f {
			v.file.look.close()
		}
		v.file = f
	}
}

// closeView closes a session's viewer, giving the keyboard back to its
// pane when the viewer had it.
func (a *App) closeView(s *proto.Session) {
	v := a.viewOf(s)
	if v == nil {
		return
	}
	// Asking first to save what the user edited in it.
	a.askToSave(a.viewTargets(v), func() {
		v.kind = ""
		v.full = false
		if v.file != nil && v.file.look != nil {
			// What was read for it goes: a picture's pixels, a PDF's
			// pages, the reads under way.
			v.file.look.close()
			v.file.look = nil
		}
		if a.side.focused {
			a.side.focused = false
			a.focusTerminal()
		}
	})
}

// focusFiles gives the Files tree the keyboard, on its cursor or the file
// the viewer shows.
func (a *App) focusFiles() {
	if s := a.session(a.selected); s != nil {
		if v := a.viewOf(s); v != nil && v.file != nil && a.side.files.cursor == "" {
			a.side.files.cursor = v.file.path
		}
	}
	a.paneRef("files-tree").Focus()
}

// focusViewer gives the viewer the keyboard, as it comes if it has yet to:
// a click that opens it leaves the keyboard with no one, where Close would
// take the pane beside it.
func (a *App) focusViewer() { a.paneRef("side-card").Focus() }

// rightWidth is the sidebar's width, shown.
func (a *App) rightWidth() float32 {
	if w := a.prefs.RightWidth; w != 0 {
		return w
	}
	return defaultSidebar
}

// rightHandle is the sidebar's edge, which a drag moves: in the window,
// over the edge between the cards and the sidebar, right wide.
func (a *App) rightHandle(c *ui.Context, right float32) {
	h := ui.Box(c.Key("resize-right")).Absolute().Right(right - 5).Top(0).Bottom(0).Width(9).Cursor(ui.CursorResizeColumn).KeepFocus()
	if dx, _, ok := h.Dragged(); ok {
		if a.side.barDrag == 0 {
			a.side.barDrag = a.rightWidth()
		}
		a.side.barDrag -= dx
		a.prefs.RightWidth = min(max(a.side.barDrag, minSidebar), maxSidebar)
	} else if a.side.barDrag != 0 && !h.Pressed() {
		a.side.barDrag = 0
		a.savePrefs()
	}
	if h.DoubleClicked() {
		a.prefs.RightWidth = 0
		a.savePrefs()
	}
}

// rightSidebar is the sidebar at the window's right: its tabs, in a
// title bar as the left sidebar's, then the tab's view.
func (a *App) rightSidebar(c *ui.Context, pal *palette, s *proto.Session) {
	tab := a.sideTab()
	// The bar's tabs and button slide in from the window's edge with the
	// sidebar, under the window controls, which stay in the window's
	// corner (mainControls): they show up to the controls, not under them.
	w := a.rightWidth() - 10
	shown := w
	if ownControls && a.win != nil {
		w -= controlsW + 8
		shown = min(max(a.side.barW-a.side.edge-10-(controlsW+8), 0), w)
	}
	ui.Row(c.Key("right-bar")).Height(topBar).Shrink(0).AlignItems(ui.Stretch).DragWindow().Children(func() {
		ui.Row(c.Key("shown")).Width(shown).Shrink(0).AlignItems(ui.Stretch).ClipX().Children(func() {
			ui.Row(c.Key("tabs")).Width(w).Shrink(0).AlignItems(ui.Center).Padding(0, 0, 0, 10).Gap(2).Children(func() {
				// The tabs scroll sideways in a narrow bar, and the button
				// that hides the sidebar stays whole beside them: it was
				// cut to a sliver at 268 (council round 1, visual 20).
				ui.ScrollHorizontal(c.Key("tab-row")).Shrink(1).MinWidth(0).Children(func() {
					ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
						a.tabButton(c, pal, sideChanges, locales.SideChanges(), tab)
						a.tabButton(c, pal, sideFiles, locales.SideFiles(), tab)
					})
				})
				ui.Spacer(c)
				ui.Row(c).Shrink(0).Children(func() {
					iconButton(c, "hide-right", sidebarRightIcon, a.tip(locales.RightSidebarHide(), "rightSidebar.toggle"), pal, a.toggleRight)
				})
			})
		})
	})
	ui.Column(c.Key("tab-" + tab)).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		if tab == sideFiles {
			a.filesSide(c, pal, s)
		} else {
			a.changesSide(c, pal, s)
		}
	})
}

// tabButton is a tab of the sidebar, its own chosen as a session's row is,
// in the same weight as the others.
func (a *App) tabButton(c *ui.Context, pal *palette, id, label, tab string) {
	b := ui.ButtonBase(c.Key("tab-"+id)).Height(26).Padding(0, 10).Radius(rRow).Center().Label(label)
	if pal.dark {
		// Room for the chosen tab's edge in every tab.
		b.Border(1, ui.Transparent)
	}
	bg, fg := ui.Transparent, pal.textMuted
	switch {
	case id == tab:
		bg, fg = pal.rowSelected, pal.text
		chosen(b, pal)
	case b.Hovered():
		bg, fg = pal.rowHover, pal.text
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: dHoverOut}))
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		a.showTab(id)
	}
}

// drawerWidth is the viewer drawer's width over a row of panes w wide:
// the width the person gave it, and at most what leaves a strip of the
// panes in view beside it.
func (a *App) drawerWidth(w float32) float32 {
	sw := a.prefs.SideWidth
	if sw == 0 {
		sw = defaultSide
	}
	if w <= 0 {
		return sw
	}
	return max(min(sw, w-drawerPeek-paneGap), min(minSide, w-paneGap))
}

// drawerPeek is the least of the panes the viewer drawer leaves in view.
const drawerPeek = 96

// sideEdge is the viewer drawer's left edge, which a drag moves.
func (a *App) sideEdge(c *ui.Context, pal *palette, row float32) {
	d := ui.Box(c.Key("side-edge")).Width(paneGap).Shrink(0).Cursor(ui.CursorResizeColumn)
	if d.Hovered() || d.Pressed() {
		d.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, pal.textFaint.Alpha(0.7), 1.5)
		})
	}
	if dx, _, ok := d.Dragged(); ok {
		if a.side.drag == 0 {
			a.side.drag = a.drawerWidth(row)
		}
		a.side.drag -= dx
		a.prefs.SideWidth = min(max(a.side.drag, minSide), max(row-drawerPeek-paneGap, minSide))
	} else if a.side.drag != 0 && !d.Pressed() {
		a.side.drag = 0
		a.savePrefs()
	}
	if d.DoubleClicked() {
		a.prefs.SideWidth = defaultSide
		a.savePrefs()
	}
}

// viewerCard is the viewer, a card beside the panes as theirs: a header
// saying what it shows, then the diffs or the file. It is the card at
// the window's right edge, whose header keeps room for the window's
// controls (controlsRoom).
func (a *App) viewerCard(c *ui.Context, pal *palette, s *proto.Session, v *sideView, width float32) {
	// Raised over the panes, as quick look and the drawer both are.
	card := floating(ui.Column(c.Key("side")).Width(width).Shrink(0).MinHeight(0).Radius(rPane).Clip().Background(pal.card), pal)
	card.Focusable().Bind(a.paneRef("side-card"))
	a.side.focused = card.FocusWithin()
	card.Children(func() {
		if v.kind == sideFiles && v.file != nil {
			a.fileViewer(c, pal, s, v.file, width)
			if v.full {
				a.lookHints(c, pal, v.file.look)
			}
		} else {
			a.changesViewer(c, pal, s, width)
		}
	})
	if v.kind == sideFiles && v.file != nil {
		a.lookKeys(card, v, v.file.look)
	}
	if card.Shortcut(0, ui.KeyEscape) {
		switch {
		case v.full:
			// Quick look closes, and the keys go back to the list.
			a.closeView(s)
			if a.rightShown() && a.sideTab() == sideFiles {
				a.focusFiles()
			}
		case v.kind == sideFiles && a.rightShown() && a.sideTab() == sideFiles:
			// Back to the list the file came from, as the TUI's quick
			// look goes back from inside the file.
			a.focusFiles()
		default:
			a.focusTerminal()
		}
	}
	if card.Shortcut(ui.Cmd, ui.KeyS) {
		a.saveFocused(v)
	}
}

// viewerHeader is the viewer's header: an icon and a title, as a pane's
// header shows its program, what follows it, then the viewer's buttons.
func (a *App) viewerHeader(c *ui.Context, pal *palette, s *proto.Session, svg *ui.SVG, title string, dirty bool, after, buttons func()) {
	ui.Row(c.Key("hdr")).Height(headerH).Shrink(0).AlignItems(ui.Center).Padding(0, 10, 0, 16).Gap(8).Children(func() {
		// A viewer too narrow for its extras keeps its title and its
		// close button: the extras go whole, as a pane header's chips do.
		roomy := true
		if w := a.paneRef("side-card").Bounds(c).W; w > 0 && w < 280 {
			roomy = false
		}
		ui.Icon(c, svg).Size(14, 14).TextColor(pal.textFaint).Shrink(0)
		oneLine(c, title, fsBody, wSemibold).TextColor(pal.text)
		if after != nil && roomy {
			after()
		}
		ui.Box(c.Key("drag")).Grow(1).AlignSelf(ui.Stretch).DragWindow()
		if buttons != nil && roomy {
			buttons()
		}
		sid := s.ID
		closeButton(c, pal, locales.SideClose(), dirty, func() { a.defer_(func() { a.closeView(a.session(sid)) }) })
		a.showRightButton(c, pal)
		a.controlsRoom(c, 8)
	})
}

// showRightButton shows the right sidebar, at the right of the header at
// the window's top right while the sidebar hides, as the left sidebar's
// shows it.
func (a *App) showRightButton(c *ui.Context, pal *palette) {
	if !a.rightShown() {
		iconButton(c, "show-right", sidebarRightIcon, a.tip(locales.RightSidebarShow(), "rightSidebar.toggle"), pal, a.toggleRight)
	}
}

// --- the Changes tab ---

// changesPanes returns the panes the Changes tab is of: the one the user
// is in, whose worktree it shows, and the one of the session whose agent
// last started a turn, the user's first, nil for none.
func (a *App) changesPanes(s *proto.Session) (here, turn *proto.Pane) {
	here = withGit(s, s.PaneByID(a.focusOf(s)))
	if here == nil {
		return nil, nil
	}
	if here.Turn != nil {
		return here, here
	}
	for i := range s.Panes {
		p := &s.Panes[i]
		if p.Turn != nil && (turn == nil || p.Turn.At > turn.Turn.At) {
			turn = p
		}
	}
	return here, turn
}

// withGit is a pane with its worktree, as kerod last told it; for one it
// has yet to look at, as a pane just split from another, the worktree of
// another pane of the session that holds its directory, so that the
// sidebar keeps showing it rather than no repository for that moment.
func withGit(s *proto.Session, p *proto.Pane) *proto.Pane {
	if p == nil || p.Git != nil {
		return p
	}
	dir := p.WorkDir()
	if dir == "" {
		return p
	}
	for i := range s.Panes {
		if g := s.Panes[i].Git; g != nil && withinDir(dir, g.Root) {
			cp := *p
			cp.Git = g
			return &cp
		}
	}
	return p
}

// withinDir tells that dir is root or a directory in it, with the paths
// of any host.
func withinDir(dir, root string) bool {
	root = strings.TrimRight(root, `/\`)
	if dir == root {
		return true
	}
	return strings.HasPrefix(dir, root+"/") || strings.HasPrefix(dir, root+`\`)
}

// changesDir is the worktree the Changes tab lists: the agent's where it
// shows its turn, else the one of the pane the user is in; "" for none.
func (a *App) changesDir(s *proto.Session) string {
	here, turn := a.changesPanes(s)
	switch {
	case turn != nil && (a.side.changes.base == git.BaseTurn || here.Git == nil):
		return turn.Turn.Root
	case here != nil && here.Git != nil:
		return here.Git.Root
	}
	return ""
}

// changesScope names what the Changes tab lists for a pane since a base:
// its worktree's changes, or its turn's, which the list, its files'
// diffs, folds and scroll position are kept by.
func changesScope(p *proto.Pane, base string) string {
	if base == git.BaseTurn && p.Turn != nil {
		return p.Turn.Root + "|" + p.Turn.Tree + "|" + base
	}
	if p.Git != nil {
		return p.Git.Root + "|" + base
	}
	return p.ID + "|" + base
}

// gitStamp is what of a worktree's status changes its changes.
func gitStamp(g *proto.GitInfo) string {
	if g == nil {
		return ""
	}
	return fmt.Sprint(g.Root, g.Head, g.Branch, g.Files, g.Added, g.Removed)
}

// sessionWorking tells that an agent of the session works.
func sessionWorking(s *proto.Session) bool {
	for _, p := range s.Panes {
		if p.Status == proto.Working {
			return true
		}
	}
	return false
}

// changesFrame is the Changes tab for a frame: what it lists, made once
// for the sidebar's tree and the viewer's diffs both.
type changesFrame struct {
	title, detail       string // why it lists nothing: git is off, no repository
	bases, labels, tips []string
	pane                *proto.Pane // whose worktree, or turn, it lists
	scope               string
	list                *proto.GitChanges // nil until one comes
	held                bool              // list is another base's, faded for a moment
	// rows are the viewer's rows this frame, heads the row of each file's
	// header, and ls their list: nil while the viewer shows no diffs.
	rows  []changeRow
	heads []int
	ls    *ui.ListState
}

// changesFrameOf makes the Changes tab's frame, once a frame: the bases
// the worktree offers and the list of the one chosen, fetched as the
// worktree changes.
func (a *App) changesFrameOf(c *ui.Context, s *proto.Session) *changesFrame {
	if a.side.cf != nil {
		return a.side.cf
	}
	cf := &changesFrame{}
	a.side.cf = cf
	ch := &a.side.changes
	here, turn := a.changesPanes(s)
	if !a.set.GitStatus {
		cf.title, cf.detail = locales.ChangesGitOff(), locales.ChangesGitOffDetail()
		return cf
	}
	if here == nil || here.Git == nil && turn == nil {
		cf.title, cf.detail = locales.ChangesNoRepo(), locales.ChangesNoRepoDetail()
		return cf
	}

	// The bases this worktree offers, and the one shown: the agent's turn
	// first, as the tab opens.
	if turn != nil {
		cf.bases, cf.labels, cf.tips = append(cf.bases, git.BaseTurn), append(cf.labels, locales.ChangesTurn()), append(cf.tips, locales.ChangesTurnTip())
	}
	if here.Git != nil {
		cf.bases, cf.labels, cf.tips = append(cf.bases, git.BaseHead), append(cf.labels, locales.ChangesUncommitted()), append(cf.tips, locales.ChangesUncommittedTip())
		if d := here.Git.Default; d != "" {
			cf.bases, cf.labels, cf.tips = append(cf.bases, git.BaseBranch), append(cf.labels, locales.ChangesBranch()), append(cf.tips, locales.ChangesBranchTip(d))
		}
	}
	if !slices.Contains(cf.bases, ch.base) {
		ch.base = cf.bases[0]
	}
	pane := here
	if ch.base == git.BaseTurn {
		pane = turn
	}
	// The list is of a worktree, not of a pane: the panes the user moves
	// between in one show it as it was, scrolled where it was. A turn's is
	// of its snapshot.
	scope := changesScope(pane, ch.base)
	stamp := gitStamp(pane.Git)
	if pane.Turn != nil && ch.base == git.BaseTurn {
		stamp += "|" + pane.Turn.Tree
	}
	working := sessionWorking(s)
	// The list follows the worktree: as its status changes, and every few
	// seconds while an agent works in the session, as a file can change
	// without its count of lines.
	stale := scope != ch.scope || stamp != ch.stamp || working && time.Since(ch.fetched) > 5*time.Second
	if stale && !ch.busy && a.hostOf(pane.ID).cl != nil {
		a.fetchChanges(scope, stamp, pane.ID, ch.base)
	}
	if working {
		c.After(5 * time.Second)
	}

	// The list of the base shown, else the one fetched last of it; one of
	// another base stays, faded, for the moment this one takes to come,
	// rather than blink away.
	if scope != ch.shown {
		ch.shown, ch.switched = scope, c.Now()
	}
	list, current := ch.res, ch.res != nil && ch.scope == scope
	if !current && ch.lists[scope] != nil {
		list, current = ch.lists[scope], true
	}
	held := !current && list != nil && c.Now().Sub(ch.switched) < 400*time.Millisecond
	if !current && !held {
		list = nil
	}
	if held {
		c.After(400 * time.Millisecond)
	}
	cf.pane, cf.scope, cf.list, cf.held = pane, scope, list, held
	return cf
}

// changesNone shows why the Changes tab lists no files, a spinner while
// they come, and tells whether it showed one.
func (a *App) changesNone(c *ui.Context, pal *palette, cf *changesFrame) bool {
	ch := &a.side.changes
	res := cf.list
	switch {
	case cf.title != "":
		a.sideEmpty(c, pal, branchIcon, cf.title, cf.detail)
	case res == nil:
		if ch.err != "" && !ch.busy && ch.scope == cf.scope {
			a.sideMessage(c, pal, locales.ChangesCantRead(), ch.err)
		} else if c.Now().Sub(ch.switched) > 250*time.Millisecond {
			ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c) })
		} else {
			c.After(260 * time.Millisecond)
		}
	case len(res.Files) == 0 && cf.held:
		// Another base's "no changes" would say the wrong thing.
	case len(res.Files) == 0:
		detail := map[string]string{git.BaseTurn: locales.ChangesNoneTurn(), git.BaseHead: locales.ChangesNoneUncommitted()}[a.side.changes.base]
		if a.side.changes.base == git.BaseBranch {
			detail = locales.ChangesNoneBranch(res.BaseName)
		}
		a.sideEmpty(c, pal, branchIcon, locales.ChangesNone(), detail)
	default:
		return false
	}
	return true
}

// changesSide is the Changes tab: the bases to choose from, what changed
// since the one chosen, and the tree of the files, a click on one of
// which opens the diffs in the viewer, scrolled to it.
func (a *App) changesSide(c *ui.Context, pal *palette, s *proto.Session) {
	cf := a.changesFrameOf(c, s)
	ch := &a.side.changes
	if cf.title != "" {
		a.sideMessage(c, pal, cf.title, cf.detail)
		return
	}
	list := cf.list
	ui.Column(c.Key("bases")).Shrink(0).Padding(0, 12, 6, 12).Gap(6).AlignItems(ui.Stretch).Children(func() {
		if len(cf.bases) > 1 {
			segmentedIn(c, "base", &ch.base, cf.bases, cf.labels, true)
		}
		i := slices.Index(cf.bases, ch.base)
		ui.Row(c).Height(28).Gap(8).AlignItems(ui.Center).Children(func() {
			if list == nil || len(list.Files) == 0 {
				ui.Text(c, cf.tips[i]).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0).Tooltip(cf.tips[i])
				return
			}
			added, removed := 0, 0
			for _, f := range list.Files {
				added, removed = added+f.Added, removed+f.Removed
			}
			ui.Text(c, locales.ChangesFiles(len(list.Files))).FontSize(fsSmall).TextColor(pal.textMuted).SingleLine().Shrink(0)
			a.lineCounts(c, pal, added, removed, 12.5)
			if ch.base == git.BaseBranch && list.BaseName != "" {
				ui.Text(c, locales.ChangesSince(list.BaseName)).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").MinWidth(0)
			}
			ui.Spacer(c)
			if v := a.viewOf(s); v == nil || v.kind != sideChanges {
				iconButton(c, "show-diffs", diffIcon, locales.ChangesShowDiffs(), pal, func() {
					a.openView(s, sideChanges, nil)
					a.focusViewer()
				})
			}
		})
	})
	if a.changesNone(c, pal, cf) {
		return
	}
	box := ui.Column(c.Key("tree-" + cf.scope)).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	box.Opacity(box.Animate("held", 1-0.45*b2f(cf.held), dFade))
	box.Children(func() { a.changesTree(c, pal, s, cf) })
}

// changesViewer is the viewer of Changes: the diffs of the files the tab
// lists, one after the other, scrolled to the one chosen in the tree.
func (a *App) changesViewer(c *ui.Context, pal *palette, s *proto.Session, width float32) {
	cf := a.changesFrameOf(c, s)
	ch := &a.side.changes
	files := cf.list != nil && len(cf.list.Files) > 0
	dirty := false
	for _, sf := range ch.files {
		dirty = dirty || a.diffTarget(sf).dirty()
	}
	a.viewerHeader(c, pal, s, diffIcon, locales.SideChanges(), dirty, func() {
		if dir := a.changesDir(s); dir != "" {
			ui.Text(c, a.hostOf(s.ID).shortDir(dir)).FontSize(fsBody).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Shrink(2).MinWidth(0).Tooltip(dir)
		}
	}, func() {
		if files {
			a.foldAllButton(c, pal, cf.scope, cf.list.Files)
		}
		split := a.diffSplit(width)
		layoutIcon, layoutTip := splitRightIcon, locales.ChangesSplit()
		if split {
			layoutIcon, layoutTip = splitDownIcon, locales.ChangesUnified()
		}
		iconButton(c, "layout", layoutIcon, layoutTip, pal, func() {
			set := a.set
			set.DiffLayout = "split"
			if split {
				set.DiffLayout = "unified"
			}
			a.applySettings(set)
		})
	})
	if a.changesNone(c, pal, cf) {
		return
	}
	res, scope := cf.list, cf.scope
	if !cf.held {
		a.syncFiles(c, scope, cf.pane, res.Root, res.Files)
	}
	if ch.places == nil {
		ch.places = map[string]*ui.ListState{}
	}
	ls := ch.places[scope]
	if ls == nil {
		ls = &ui.ListState{}
		ch.places[scope] = ls
	}
	// A file is two rows: its header, then its diff while unfolded,
	// which scrolls under the header as the list pins it at the top,
	// until the next file's header pushes it out, as the files of a
	// pull request do.
	var rows []changeRow
	heads := make([]int, len(res.Files)) // the row of each file's header
	for i, f := range res.Files {
		heads[i] = len(rows)
		rows = append(rows, changeRow{f: f})
		if ch.files[scope+"|"+f.Path] != nil && !a.fileCollapsed(scope, f) {
			rows = append(rows, changeRow{f: f, diff: true})
		}
	}
	cf.rows, cf.heads, cf.ls = rows, heads, ls
	// The file chosen in the tree, as the viewer opens on it.
	if ch.reveal != "" {
		if i := slices.IndexFunc(res.Files, func(f proto.GitFile) bool { return f.Path == ch.reveal }); i >= 0 {
			ls.ScrollTo(heads[i], ui.Start)
		}
		ch.reveal = ""
	}
	ls.Header = func(i int) bool { return !rows[i].diff }
	ls.Key = func(i int) any { return changeKey{rows[i].f.Path, rows[i].diff} }
	box := ui.Row(c.Key("files-" + scope)).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
	box.Opacity(box.Animate("held", 1-0.45*b2f(cf.held), dFade))
	box.Children(func() {
		// The scroll bar runs below the header pinned at the top, not
		// under it.
		ui.List(c.Key("rows"), ls, len(rows), func(i int) {
			r := rows[i]
			if r.diff {
				a.fileDiff(c, pal, s, scope, r.f, width)
				return
			}
			// Opaque, over the diff scrolling under it; a folded file's
			// with the room below its section.
			gap := float32(sectionGap)
			if i+1 < len(rows) && rows[i+1].diff {
				gap = 0
			}
			ui.Column(c).FillWidth().AlignItems(ui.Stretch).Padding(0, 0, gap, 0).Background(pal.card).Children(func() {
				a.fileHeader(c, pal, scope, res.Root, r.f, func() {
					// Folding it as the list pins it brings it back where
					// it stays: at the top.
					ls.ScrollIntoView(i)
				})
			})
		}).Grow(1).MinWidth(0).MinHeight(0).Padding(0, 0, 16, 0).ScrollbarInsets(stickyH, 0, 0, 0)
	})
}

// changeRow is a row of the list of changes: a file's header, or its
// diff; changeKey what it is, as the list keeps a row's state by.
type changeRow struct {
	f    proto.GitFile
	diff bool
}

type changeKey struct {
	path string
	diff bool
}

// stickyH is the height of a file's header, which the list pins at its
// top; sectionGap the room below each file's section.
const (
	stickyH    = 32
	sectionGap = 6
)

// syncFiles keeps the diffs of a list's files: it asks for the text of
// each file shown that has none yet, a few at a time, and again of one
// whose change the list counts anew (Known, so that kerod sends only what
// changed), or of every one as the worktree's HEAD moves; the new side
// not of one the user edited (showText).
func (a *App) syncFiles(c *ui.Context, scope string, pane *proto.Pane, root string, files []proto.GitFile) {
	ch := &a.side.changes
	if ch.files == nil || len(ch.files) > 400 {
		// Keeping those with edits to save.
		kept := map[string]*sideFile{}
		for k, sf := range ch.files {
			if a.diffTarget(sf).dirty() {
				kept[k] = sf
			}
		}
		ch.files = kept
	}
	stamp := gitStamp(pane.Git)
	asking := 0
	for _, sf := range ch.files {
		if sf.busy {
			asking++
		}
	}
	for _, f := range files {
		key := scope + "|" + f.Path
		sf := ch.files[key]
		if sf == nil {
			sf = &sideFile{pane: pane.ID, base: ch.base, f: f}
			ch.files[key] = sf
		}
		sf.root = root
		if a.fileCollapsed(scope, f) || sf.busy || sf.writing || a.hostOf(pane.ID).cl == nil {
			continue
		}
		known := sf.hash
		switch {
		case sf.d == nil && !sf.text.Binary && !sf.text.Large && sf.err == "":
			known = "" // never shown
		case sf.stamp != stamp && sf.base != git.BaseTurn:
			known = "" // HEAD moved: the base's text too
		case sf.f != f:
		default:
			continue
		}
		if asking >= 4 {
			c.After(50 * time.Millisecond)
			return
		}
		asking++
		sf.f, sf.stamp = f, stamp
		a.fetchFile(sf, known)
	}
}

// fileCollapsed tells whether a file's diff is folded: as the user left
// it, else folded for a binary file or one of very many lines changed.
func (a *App) fileCollapsed(scope string, f proto.GitFile) bool {
	if v, ok := a.side.changes.collapsed[scope+"|"+f.Path]; ok {
		return v
	}
	return f.Binary || f.Added+f.Removed > 600
}

// foldAllButton folds every file's diff while any shows, else unfolds
// them all, keeping the file at the top of the list there.
func (a *App) foldAllButton(c *ui.Context, pal *palette, scope string, files []proto.GitFile) {
	ch := &a.side.changes
	fold := slices.ContainsFunc(files, func(f proto.GitFile) bool { return !a.fileCollapsed(scope, f) })
	svg, label := collapseIcon, locales.ChangesCollapseFiles()
	if !fold {
		svg, label = expandIcon, locales.ChangesExpandFiles()
	}
	iconButton(c, "fold-all", svg, label, pal, func() {
		// The file at the top, whose header stays there: folded, each
		// file is a row, its header.
		top := -1
		if ls := ch.places[scope]; ls != nil && fold {
			if first, _ := ls.Visible(); first >= 0 {
				for i, n := 0, 0; i < len(files) && n <= first; i++ {
					top = i
					n++
					if ch.files[scope+"|"+files[i].Path] != nil && !a.fileCollapsed(scope, files[i]) {
						n++
					}
				}
			}
		}
		if ch.collapsed == nil {
			ch.collapsed = map[string]bool{}
		}
		for _, f := range files {
			ch.collapsed[scope+"|"+f.Path] = fold
		}
		if top >= 0 {
			ch.places[scope].ScrollTo(top, ui.Start)
		}
	})
}

// fileDiff is a changed file's diff, under its header, or why it shows
// none.
func (a *App) fileDiff(c *ui.Context, pal *palette, s *proto.Session, scope string, f proto.GitFile, width float32) {
	ch := &a.side.changes
	sf := ch.files[scope+"|"+f.Path]
	ui.Column(c).FillWidth().AlignItems(ui.Stretch).Padding(4, 0, sectionGap, 0).Children(func() {
		switch {
		case sf.err != "" && sf.d == nil:
			a.sectionNote(c, pal, locales.ChangesCantRead()+": "+sf.err)
		case sf.text.Binary:
			a.sectionNote(c, pal, locales.ChangesBinary())
		case sf.text.Large && sf.d == nil:
			a.sectionNote(c, pal, locales.ChangesTooLarge())
		case sf.d == nil:
			ui.Column(c.Key("loading")).Height(48).Center().Children(func() { ui.Spinner(c) })
		default:
			a.diffOptions(c, pal, s, sf, width)
			diff := editor.DiffView(c.Key("diff"), sf.d)
			if diff.FocusWithin() {
				ch.focus = sf
			}
		}
	})
}

// fileHeader is a changed file's header: its change, its name and folder
// and the lines it adds and removes, which a click folds or unfolds,
// running folding first when set.
func (a *App) fileHeader(c *ui.Context, pal *palette, scope, root string, f proto.GitFile, folding func()) {
	ch := &a.side.changes
	key := scope + "|" + f.Path
	collapsed := a.fileCollapsed(scope, f)
	// Across the viewer, edge to edge.
	hdr := ui.Row(c.Key("hdr")).Height(stickyH).Padding(0, 10, 0, 12).Gap(8).AlignItems(ui.Center).Background(pal.chip)
	if hdr.Hovered() {
		hdr.Background(pal.rowHover.Mix(pal.chip, 0.5))
	}
	tip := f.Path
	if f.OldPath != "" {
		tip = f.OldPath + " → " + f.Path
	}
	hdr.Tooltip(tip)
	if hdr.Clicked() {
		if folding != nil {
			folding()
		}
		if ch.collapsed == nil {
			ch.collapsed = map[string]bool{}
		}
		ch.collapsed[key] = !collapsed
	}
	hdr.Children(func() {
		chev := ui.Icon(c, chevronIcon).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
		chev.Rotate(chev.AnimateTo("turn", 90*(1-b2f(collapsed)), ui.Snappy))
		a.statusBadge(c, pal, f.Status)
		dir, name := path.Split(f.Path)
		fg := pal.text
		if f.Status == "deleted" {
			fg = pal.textMuted
		}
		ui.Text(c, name).FontSize(fsBody).FontWeight(wSemibold).TextColor(fg).SingleLine().Ellipsis("…").Shrink(0).MaxWidth(260)
		if sf := ch.files[key]; sf != nil && a.diffTarget(sf).dirty() {
			ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).Background(pal.textMuted).Description(locales.ViewerUnsaved())
		}
		ui.Text(c, strings.TrimSuffix(dir, "/")).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
		if f.Binary {
			ui.Text(c, locales.ChangesBinary()).FontSize(fsSmall).TextColor(pal.textFaint).SingleLine()
		} else {
			a.lineCounts(c, pal, f.Added, f.Removed, 12)
		}
		if f.Status != "deleted" {
			full := filepath.Join(root, filepath.FromSlash(f.Path))
			iconButton(c, "open", openFileIcon, locales.ChangesOpenFile(), pal, func() { openPath(full) })
		}
	})
}

// sectionNote says why a file shows no diff.
func (a *App) sectionNote(c *ui.Context, pal *palette, text string) {
	ui.Text(c, text).FontSize(fsSmall).TextColor(pal.textMuted).Padding(10, 12).LineHeight(1.4)
}

// fetchChanges asks kerod for the list of a pane's changes since a base.
func (a *App) fetchChanges(scope, stamp, pane, base string) {
	ch := &a.side.changes
	ch.busy, ch.since = true, time.Now()
	if ch.scope != scope {
		ch.err = ""
	}
	cl := a.hostOf(pane).cl
	if cl == nil {
		ch.busy = false
		return
	}
	go func() {
		var res proto.GitChanges
		err := cl.Call("git.changes", proto.GitParams{Pane: pane, Base: base}, &res)
		a.update(func() {
			ch.busy, ch.fetched = false, time.Now()
			ch.scope, ch.stamp = scope, stamp
			if err != nil {
				ch.res, ch.err = nil, err.Error()
				delete(ch.lists, scope)
				return
			}
			ch.res, ch.err = &res, ""
			if ch.lists == nil || len(ch.lists) > 12 {
				ch.lists = map[string]*proto.GitChanges{}
			}
			ch.lists[scope] = &res
		})
	}()
}

// statusBadge is a file's change as a letter in its color: A added, M
// modified, D deleted, R renamed, T a change of type.
func (a *App) statusBadge(c *ui.Context, pal *palette, status string) {
	letter, col := a.statusLetter(pal, status)
	ui.Box(c).Size(18, 18).Radius(rChip).Shrink(0).Center().Background(col.Alpha(0.14)).Children(func() {
		ui.Text(c, letter).FontSize(fsCaption).FontWeight(wSemibold).TextColor(col).SingleLine()
	})
}

// statusLetter is a file's change as a letter, and its color.
func (a *App) statusLetter(pal *palette, status string) (string, ui.Color) {
	added, removed, modified := a.diffColors(pal)
	switch status {
	case "added":
		return "A", added
	case "deleted":
		return "D", removed
	case "renamed":
		return "R", modified
	case "type":
		return "T", modified
	}
	return "M", modified
}

// lineCounts shows lines added and removed, as +12 −3, in the diffs'
// colors; nothing for none.
func (a *App) lineCounts(c *ui.Context, pal *palette, added, removed int, size float32) {
	if added == 0 && removed == 0 {
		return
	}
	plus, minus, _ := a.diffColors(pal)
	ui.Row(c).Gap(5).Shrink(0).Children(func() {
		if added > 0 {
			ui.Text(c, "+"+itoa(added)).FontSize(size).FontFeatures("tnum").TextColor(plus).SingleLine()
		}
		if removed > 0 {
			ui.Text(c, "−"+itoa(removed)).FontSize(size).FontFeatures("tnum").TextColor(minus).SingleLine()
		}
	})
}

// diffColors are the colors of lines added, removed and modified: the
// terminals' green, red and yellow, so that a diff reads as one printed in
// the pane beside it.
func (a *App) diffColors(pal *palette) (added, removed, modified ui.Color) {
	light, dark := a.termThemes()
	t := light
	if pal.dark {
		t = dark
	}
	return t.Palette[2], t.Palette[1], t.Palette[3]
}

// codeTheme is the diffs' colors: the card's, with the diff colors.
func (a *App) codeTheme(c *ui.Context, pal *palette) *editor.Theme {
	added, removed, modified := a.diffColors(pal)
	key := fmt.Sprint(pal.dark, added, removed, modified)
	if a.side.theme == nil || a.side.themeKey != key {
		t := editor.DefaultTheme(c.Theme())
		t.Background, t.Text = pal.card, pal.text
		t.Added, t.Deleted, t.Modified = added, removed, modified
		a.side.theme, a.side.themeKey = t, key
	}
	return a.side.theme
}

// sideMessage says why the view shows nothing.
func (a *App) sideMessage(c *ui.Context, pal *palette, title, detail string) {
	emptyBlock(c, pal, nil, title, detail, false, nil)
}

// sideEmpty is a sidebar tab with nothing to show: the main area's empty
// state, with its icon tile and detail (council round 1, visual 29).
func (a *App) sideEmpty(c *ui.Context, pal *palette, icon *ui.SVG, title, detail string) {
	emptyBlock(c, pal, icon, title, detail, false, nil)
}

// --- a file's diff ---

// fetchFile asks kerod for a file's text in the base and now; known is
// the hash of the text shown, which kerod then sends again only when it
// changed.
func (a *App) fetchFile(sf *sideFile, known string) {
	cl := a.hostOf(sf.pane).cl
	if cl == nil {
		return
	}
	sf.busy = true
	go func() {
		var t proto.GitText
		err := cl.Call("git.file", proto.GitParams{Pane: sf.pane, Base: sf.base, Path: sf.f.Path, OldPath: sf.f.OldPath, Known: known}, &t)
		a.update(func() {
			sf.busy = false
			if err != nil {
				sf.err = err.Error()
				return
			}
			sf.err = ""
			if !t.Same {
				a.showText(sf, t)
			}
		})
	}()
}

// showText puts a file's text in its diff: anew for its first, else as
// edits, so that the diff keeps where it was scrolled; the new side not
// while the user has edits of it to write.
func (a *App) showText(sf *sideFile, t proto.GitText) {
	sf.text = proto.GitText{Binary: t.Binary, Large: t.Large}
	if sf.d != nil && !sf.patch && !t.Binary && !t.Large && (sf.d.New.Modified() || sf.edits.saving) {
		replaceText(sf.d.Old, t.Old)
		return
	}
	sf.hash = t.Hash
	switch {
	case t.Binary || t.Large && t.Patch == "":
		sf.d, sf.patch = nil, false
	case t.Large:
		sf.d, sf.patch = &editor.DiffState{}, true
		if files, err := editor.ParsePatch(t.Patch); err == nil && len(files) > 0 {
			sf.d.SetPatch(files[0])
		}
	case sf.d == nil || sf.patch:
		lang := editor.LanguageFor(path.Base(sf.f.Path))
		old, now := editor.NewBuffer(t.Old), editor.NewBuffer(t.New)
		old.SetLanguage(lang)
		now.SetLanguage(lang)
		sf.d, sf.patch = &editor.DiffState{Old: old, New: now}, false
		h := a.hostOf(sf.pane)
		a.restoreEdit(sideChanges, h, h.joinPath(sf.root, sf.f.Path), now, &sf.hash)
	default:
		replaceText(sf.d.Old, t.Old)
		replaceText(sf.d.New, t.New)
		sf.d.New.MarkSaved()
	}
}

// replaceText makes a buffer's text text by one edit of what differs
// between them, on whole characters.
func replaceText(b *editor.Buffer, text string) {
	// A buffer keeps its lines' \r\n as \n.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	cur := b.String()
	if cur == text {
		return
	}
	p := 0
	for p < len(cur) && p < len(text) && cur[p] == text[p] {
		p++
	}
	for p > 0 && p < len(cur) && !utf8.RuneStart(cur[p]) {
		p--
	}
	q := 0
	for q < len(cur)-p && q < len(text)-p && cur[len(cur)-1-q] == text[len(text)-1-q] {
		q++
	}
	for q > 0 && !utf8.RuneStart(cur[len(cur)-q]) {
		q--
	}
	b.Edit(editor.Edit{Start: p, End: len(cur) - q, Text: text[p : len(text)-q]})
}

// diffSplit tells whether diffs show side by side in a viewer this wide.
func (a *App) diffSplit(width float32) bool {
	switch a.set.DiffLayout {
	case "split":
		return true
	case "unified":
		return false
	}
	return width >= splitWidth
}

// diffOptions sets a file's diff's looks and actions for this frame: its
// new side edits (edits.go), and in a turn, while no agent of the session
// works, Revert and Keep show on each change, as the user reviews what
// the agent did.
func (a *App) diffOptions(c *ui.Context, pal *palette, s *proto.Session, sf *sideFile, width float32) {
	d := sf.d
	d.FitContent = true
	d.Font = ui.Font{Family: a.termFamily(), Size: max(a.set.FontSize-1, 10)}
	d.Theme = a.codeTheme(c, pal)
	d.Layout = editor.DiffUnified
	if a.diffSplit(width) {
		d.Layout = editor.DiffSplit
	}
	working := sessionWorking(s)
	// The new side, the file now, edits.
	d.Editable = !sf.patch && !sf.writing && sf.f.Status != "deleted"
	d.RenderSeparator = func(c *ui.Context, sep ui.Element, s *editor.DiffSeparator) { a.separator(c, pal, sep, s) }
	d.HunkActions = sf.base == git.BaseTurn && !sf.patch && !working && !sf.writing
	d.RenderHunkActions = func(c *ui.Context, bar ui.Element, h *editor.DiffHunkActions) {
		floating(bar.Gap(2).Padding(3).Radius(rControl).Background(pal.popover).AlignItems(ui.Center), pal)
		bar.Children(func() {
			if hunkButton(c, "revert", revertIcon, locales.ChangesRevert(), locales.ChangesRevertTip(), pal) {
				h.Revert()
				a.writeBack(sf)
			}
			if hunkButton(c, "keep", checkIcon, locales.ChangesKeep(), locales.ChangesKeepTip(), pal) {
				h.Keep()
			}
		})
	}
}

// separator is a diff's unchanged lines collapsed between its changes:
// chevrons that show more of them, up or down, and "N unmodified lines",
// which shows them all, on the diff's own background, with no bar; or a
// patch's hunk's header. Shift shows them all from any of them.
func (a *App) separator(c *ui.Context, pal *palette, sep ui.Element, s *editor.DiffSeparator) {
	sep.Children(func() {
		// Its chevron under the file's header's: the header's starts 12 in,
		// 10 wide, and the button's icon, 11 wide, sits in 24.
		ui.Row(c).Grow(1).AlignSelf(ui.Stretch).AlignItems(ui.Center).Gap(2).Padding(0, 12, 0, 5).Children(func() {
			if s.Lines == 0 {
				ui.Text(c, s.Header).Font(s.Font().Family).FontSize(s.Font().Size-1).TextColor(pal.textFaint).SingleLine().Shrink(1).Padding(0, 7)
				return
			}
			expand := func(fn func()) func() {
				return func() {
					if c.Modifiers()&ui.Shift != 0 {
						fn = s.ExpandAll
					}
					fn()
				}
			}
			chevron := func(key string, svg *ui.SVG, label string, fn func()) {
				b := ui.ButtonBase(c.Key(key)).Size(24, 24).Radius(rRow).Center().TextColor(pal.textMuted).Label(label).Tooltip(label)
				if b.Hovered() || b.Pressed() {
					b.Background(pal.rowHover).TextColor(pal.text)
				}
				b.Children(func() { ui.Icon(c, svg).Size(12, 12) })
				if b.Clicked() {
					expand(fn)()
				}
			}
			switch {
			case s.First:
				chevron("up", chevronUpIcon, locales.ChangesExpandUp(), s.ExpandUp)
			case s.Last:
				chevron("down", chevronDownIcon, locales.ChangesExpandDown(), s.ExpandDown)
			case s.Lines <= s.Step:
				chevron("all", chevronIcon, locales.ChangesExpandAll(), s.ExpandAll)
			default:
				chevron("down", chevronDownIcon, locales.ChangesExpandDown(), s.ExpandDown)
				chevron("up", chevronUpIcon, locales.ChangesExpandUp(), s.ExpandUp)
			}
			label := locales.ChangesUnmodified(s.Lines)
			b := ui.ButtonBase(c.Key("lines")).Height(24).Padding(0, 4).Radius(rRow).AlignItems(ui.Center).Label(label).Tooltip(locales.ChangesExpandAll())
			b.Children(func() {
				fg := pal.textFaint
				if b.Hovered() {
					fg = pal.textMuted
				}
				ui.Text(c, label).FontSize(fsSmall).TextColor(fg).SingleLine()
			})
			if b.Clicked() {
				s.ExpandAll()
			}
		})
	})
}

// hunkButton is a small button of a change's actions, an icon and a
// label.
func hunkButton(c *ui.Context, key string, svg *ui.SVG, label, tip string, pal *palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 8).Gap(5).Radius(rRow).AlignItems(ui.Center).
		TextColor(pal.textMuted).Label(label).Tooltip(tip)
	if b.Hovered() || b.Pressed() {
		b.Background(pal.rowHover).TextColor(pal.text)
	}
	b.Children(func() {
		ui.Icon(c, svg).Size(14, 14)
		ui.Text(c, label).FontSize(fsSmall).FontWeight(wMedium).SingleLine()
	})
	return b.Clicked()
}

// writeBack writes a file's diff's new text, which a change reverted
// changed, to the file, through kerod, which writes it only as the file
// was shown: one changed meanwhile is shown anew instead.
func (a *App) writeBack(sf *sideFile) {
	cl := a.hostOf(sf.pane).cl
	if cl == nil {
		return
	}
	text, v := bufferText(sf.d.New), sf.d.New.Version()
	sf.writing, sf.edits.saving = true, true
	go func() {
		var hash string
		err := cl.Call("git.revert", proto.GitWriteParams{Pane: sf.pane, Base: sf.base, Path: sf.f.Path, Hash: sf.hash, Text: text}, &hash)
		a.update(func() {
			sf.writing, sf.edits.saving = false, false
			if err != nil {
				a.toast(locales.ChangesRevertFailed(err.Error()))
				sf.d.New.MarkSaved()
				a.fetchFile(sf, "")
				return
			}
			sf.hash = hash
			if sf.d.New.Version() == v {
				sf.d.New.MarkSaved()
			}
		})
	}()
}
