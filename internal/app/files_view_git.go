package app

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// # Git marks in the Files view
//
// A side that shows a folder of this machine inside a repository marks each
// changed file with its git status (M, A, D, R, U) and a dot in the colour of
// the agent that made most of its lines, the same author the review names
// (review_authors.go). A folder that holds a change gets a quiet dot. Quick
// look on a changed file has a Changes view, the file's hunks drawn as the
// review draws them, and it opens on it. v on a changed file opens the review
// on that file, and O in the review shows the file here.
//
// The marks come from the daemon's review-diff with dir: the same diff, the
// same tree and the same authors as the review, of the uncommitted changes,
// so the two never disagree. A side of another machine, or of a client
// attached to another machine, has no marks.
//
// What it costs:
//
//   - A folder in no repository costs nothing: finding the repository reads
//     a few file names (gitstate.Find), off the update goroutine, and no git
//     runs.
//   - A repository is read once when a side comes to it, and again when a
//     file in it changes on disk. One dirwatch content set watches the work
//     tree's root, the git folders, the folders on screen and each changed
//     file's folder, as the live review does (review_live.go). The reads are
//     paced the same way: no sooner than reviewLiveGap after the last ended,
//     or four times as long as it took.
//   - Nothing runs while nothing changes. Closing the view ends the watch.

// filesGitMaxRepos bounds the repositories the view reads at once: one per
// side of every tab.
const filesGitMaxRepos = 8

// filesGitMark is one changed file's mark.
type filesGitMark struct {
	status string
	// origin is the file's main author, an index into the diff's authors
	// from 1, 0 for none.
	origin int
	// file is the file's diff, for quick look.
	file *review.File
}

// filesGitRepo is one repository a side shows, and its last diff.
type filesGitRepo struct {
	root string
	diff *reviewDiffResult
	// marks is by path from root, slash separated, and dirs is each folder
	// that holds a mark.
	marks map[string]filesGitMark
	dirs  map[string]bool
	// reading is set while a read runs, again when a change came in during
	// it. lastEnd and lastTook pace the next.
	reading, again   bool
	started, lastEnd time.Time
	lastTook         time.Duration
}

// filesGitState is the marks of the view: the repositories its sides show,
// and its watch.
type filesGitState struct {
	// gen tells this view's answers from a view since closed.
	gen uint64
	// roots is each side's repository by the folder it shows, "" for none.
	roots map[string]string
	repos map[string]*filesGitRepo
	// key is the watch's targets, on says the watch is up.
	key string
	on  bool
	// draw moves when a mark changes, for the frame caches.
	draw uint64
}

// filesGitRootsMsg is where the sides' folders are: the repository of each,
// "" for none.
type filesGitRootsMsg struct {
	gen   uint64
	roots map[string]string
}

// filesGitMsg is a read of one repository.
type filesGitMsg struct {
	gen  uint64
	root string
	diff *reviewDiffResult
	err  error
}

// filesGitReadyMsg says whether the watch is up.
type filesGitReadyMsg struct {
	gen uint64
	key string
	ok  bool
}

// filesGitChangedMsg says something the marks show changed on disk.
type filesGitChangedMsg struct{ gen uint64 }

// filesGitGoMsg starts a read of root the pace held back.
type filesGitGoMsg struct {
	gen  uint64
	root string
}

// filesGitDirs is the folders of this machine the view's sides show. A side
// of another machine, or a client attached to another machine, has none.
func (m *OS) filesGitDirs() []string {
	e := &m.explorer
	if !e.open || m.AttachedHost != "" || !m.IsDaemonSession || m.filesCaller() == nil {
		return nil
	}
	var dirs []string
	for _, t := range e.tabs {
		for _, s := range t.sides {
			if s == nil || s.host != "" || s.Dir == "" {
				continue
			}
			if dir := pathroot.ExpandHome(s.Dir); !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs
}

// filesGitSync finds the repository of each folder on screen, off the update
// goroutine: it reads file names only.
func (m *OS) filesGitSync() tea.Cmd {
	dirs := m.filesGitDirs()
	if len(dirs) == 0 {
		return nil
	}
	g := &m.filesGit
	if g.gen == 0 {
		g.gen = m.filesGitLive.gen + 1
	}
	gen := g.gen
	return func() tea.Msg {
		roots := make(map[string]string, len(dirs))
		for _, dir := range dirs {
			if r := gitstate.Find(dir); r.Root != "" {
				roots[dir] = filepath.Clean(r.Root)
			} else {
				roots[dir] = ""
			}
		}
		return filesGitRootsMsg{gen: gen, roots: roots}
	}
}

// applyFilesGitRoots starts a read of each repository newly on screen, drops
// the ones no side shows, and points the watch at what is left.
func (m *OS) applyFilesGitRoots(msg filesGitRootsMsg) tea.Cmd {
	g := &m.filesGit
	if msg.gen != g.gen || !m.explorer.open {
		return nil
	}
	g.roots = msg.roots
	want := map[string]bool{}
	for _, root := range msg.roots {
		if root != "" && len(want) < filesGitMaxRepos {
			want[root] = true
		}
	}
	if g.repos == nil {
		g.repos = map[string]*filesGitRepo{}
	}
	for root := range g.repos {
		if !want[root] {
			delete(g.repos, root)
			g.draw++
		}
	}
	var cmds []tea.Cmd
	for root := range want {
		if g.repos[root] == nil {
			g.repos[root] = &filesGitRepo{root: root}
			cmds = append(cmds, m.filesGitRead(root))
		}
	}
	return tea.Batch(append(cmds, m.filesGitWatch())...)
}

// filesGitRead reads root's diff now, or holds it: until the read out ends,
// or until the pace allows.
func (m *OS) filesGitRead(root string) tea.Cmd {
	g := &m.filesGit
	r := g.repos[root]
	if r == nil {
		return nil
	}
	if r.reading {
		r.again = true
		return nil
	}
	gen := g.gen
	wait := max(reviewLiveGap, reviewLiveCostShare*r.lastTook)
	if until := time.Until(r.lastEnd.Add(wait)); !r.lastEnd.IsZero() && until > 0 {
		r.reading = true
		return tea.Tick(until, func(time.Time) tea.Msg { return filesGitGoMsg{gen: gen, root: root} })
	}
	r.reading, r.again, r.started = true, false, time.Now()
	m.tickStats.FilesGitReads++
	call := m.reviewCaller()
	params := map[string]any{"dir": root, "uncommitted": true, "origins": true, "context": 3}
	return func() tea.Msg {
		raw, err := call("review-diff", params, reviewDiffTimeout)
		msg := filesGitMsg{gen: gen, root: root, err: err}
		if err == nil {
			var d reviewDiffResult
			if err := jsonx.Unmarshal(raw, &d); err != nil {
				msg.err = err
			} else {
				msg.diff = &d
			}
		}
		return msg
	}
}

// applyFilesGitGo starts the read the pace held back.
func (m *OS) applyFilesGitGo(msg filesGitGoMsg) tea.Cmd {
	g := &m.filesGit
	r := g.repos[msg.root]
	if msg.gen != g.gen || r == nil {
		return nil
	}
	r.reading, r.lastEnd = false, time.Time{}
	return m.filesGitRead(msg.root)
}

// applyFilesGit takes a read in: the marks, the watch, and quick look when
// it shows a file of the repository.
func (m *OS) applyFilesGit(msg filesGitMsg) tea.Cmd {
	g := &m.filesGit
	r := g.repos[msg.root]
	if msg.gen != g.gen || r == nil {
		return nil
	}
	r.reading = false
	r.lastEnd = time.Now()
	r.lastTook = r.lastEnd.Sub(r.started)
	var next tea.Cmd
	if r.again {
		r.again = false
		next = m.filesGitRead(msg.root)
	}
	if msg.err != nil {
		// A repository git cannot read shows no marks. The view says
		// nothing: the marks are a help, not the listing.
		r.diff, r.marks, r.dirs = nil, nil, nil
	} else {
		r.diff = msg.diff
		r.marks, r.dirs = filesGitMarks(msg.diff)
	}
	g.draw++
	m.explorer.gen++
	return tea.Batch(next, m.filesGitWatch())
}

// filesGitMarks is a diff's marks by path, and the folders that hold one.
func filesGitMarks(d *reviewDiffResult) (map[string]filesGitMark, map[string]bool) {
	marks := make(map[string]filesGitMark, len(d.Files))
	dirs := map[string]bool{}
	for i := range d.Files {
		f := &d.Files[i]
		marks[f.Path] = filesGitMark{status: f.Status, origin: reviewFileOrigin(f), file: f}
		for dir := path.Dir(f.Path); dir != "." && dir != "/" && !dirs[dir]; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	return marks, dirs
}

// filesGitWatchTargets is what the watch looks at: each repository's root,
// its git folders, the folders on screen and each changed file's folder, and
// each changed file where a folder watch misses writes.
func (m *OS) filesGitWatchTargets() (dirs, files []string, key string) {
	g := &m.filesGit
	seen := map[string]bool{}
	add := func(dir string) {
		if dir != "" && !seen[dir] && len(dirs) < reviewLiveMax {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	roots := make([]string, 0, len(g.repos))
	for root := range g.repos {
		roots = append(roots, root)
	}
	slices.Sort(roots)
	for _, root := range roots {
		add(root)
		if gd, ok := gitstate.WatchDirs(root); ok {
			for _, dir := range gd {
				add(dir)
			}
		}
	}
	shown := make([]string, 0, len(g.roots))
	for dir, root := range g.roots {
		if root != "" && g.repos[root] != nil {
			shown = append(shown, dir)
		}
	}
	slices.Sort(shown)
	for _, dir := range shown {
		add(dir)
	}
	// A folder watch on kqueue misses a write to a file in it, so each file
	// on screen in a repository is watched too, up to the bound.
	for _, f := range m.filesGitShownFiles(shown) {
		if len(files) < reviewLiveMax {
			files = append(files, f)
		}
	}
	for _, root := range roots {
		r := g.repos[root]
		if r.diff == nil {
			continue
		}
		for _, f := range r.diff.Files {
			p := filepath.Join(root, filepath.FromSlash(f.Path))
			add(filepath.Dir(p))
			if len(files) < reviewLiveMax {
				files = append(files, p)
			}
		}
	}
	slices.Sort(files)
	files = slices.Compact(files)
	return dirs, files, strings.Join(dirs, "\x00") + "\x01" + strings.Join(files, "\x00")
}

// filesGitShownFiles is the files the sides show in the folders dirs.
func (m *OS) filesGitShownFiles(dirs []string) []string {
	var out []string
	for _, t := range m.explorer.tabs {
		for _, s := range t.sides {
			if s == nil || s.host != "" || !slices.Contains(dirs, pathroot.ExpandHome(s.Dir)) {
				continue
			}
			dir := pathroot.ExpandHome(s.Dir)
			for _, en := range s.Entries {
				if !en.Dir && len(out) < reviewLiveMax {
					out = append(out, filepath.Join(dir, en.Name))
				}
			}
		}
	}
	return out
}

// filesGitWatch points the watch at what the marks show, off the update
// goroutine. It returns nil when the watch is up on the same targets.
func (m *OS) filesGitWatch() tea.Cmd {
	g := &m.filesGit
	if len(g.repos) == 0 && !g.on && g.key == "" {
		// No repository on screen and no watch up: nothing to set up.
		return nil
	}
	dirs, files, key := m.filesGitWatchTargets()
	if key == g.key {
		return nil
	}
	g.key = key
	gen := g.gen
	lv := &m.filesGitLive
	return func() tea.Msg {
		ok := lv.retarget(gen, key, dirs, files)
		return filesGitReadyMsg{gen: gen, key: key, ok: ok}
	}
}

// applyFilesGitReady starts the listener the first time the watch is up.
func (m *OS) applyFilesGitReady(msg filesGitReadyMsg) tea.Cmd {
	g := &m.filesGit
	if msg.gen != g.gen || msg.key != g.key {
		return nil
	}
	was := g.on
	g.on = msg.ok
	if msg.ok && !was {
		return m.filesGitListen(msg.gen)
	}
	return nil
}

// filesGitListen waits for a change, lets the burst settle, and reports it.
func (m *OS) filesGitListen(gen uint64) tea.Cmd {
	lv := &m.filesGitLive
	lv.mu.Lock()
	ch, stop := lv.ch, lv.stop
	lv.mu.Unlock()
	if ch == nil || stop == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-ch:
		case <-stop:
			return nil
		}
		if !dirwatch.AfterBurst(ch, stop) {
			return nil
		}
		return filesGitChangedMsg{gen: gen}
	}
}

// applyFilesGitChanged reads every repository again and listens for the
// next change.
func (m *OS) applyFilesGitChanged(msg filesGitChangedMsg) tea.Cmd {
	g := &m.filesGit
	if msg.gen != g.gen || !m.explorer.open {
		return nil
	}
	cmds := []tea.Cmd{m.filesGitListen(msg.gen)}
	for root := range g.repos {
		cmds = append(cmds, m.filesGitRead(root))
	}
	return tea.Batch(cmds...)
}

// filesGitStop ends the marks and their watch, when the view closes or opens
// again. The watch is ended off the update goroutine.
func (m *OS) filesGitStop() {
	lv := &m.filesGitLive
	m.filesGit = filesGitState{draw: m.filesGit.draw + 1}
	go func() {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		if lv.set != nil {
			lv.set.Close()
			lv.set = nil
		}
		lv.closeStop()
		lv.ch, lv.key = nil, ""
		lv.gen++
	}()
}

// filesGitAt is the repository and the path from its root of name in side
// s's folder, and false outside every repository the view read.
func (m *OS) filesGitAt(s *filesSide, name string) (*filesGitRepo, string, bool) {
	g := &m.filesGit
	if s == nil || s.host != "" || len(g.repos) == 0 {
		return nil, "", false
	}
	dir := pathroot.ExpandHome(s.Dir)
	root := g.roots[dir]
	r := g.repos[root]
	if root == "" || r == nil || r.marks == nil {
		return nil, "", false
	}
	rel, err := filepath.Rel(root, filepath.Join(dir, name))
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return nil, "", false
	}
	return r, filepath.ToSlash(rel), true
}

// filesGitMarkFor is the mark of entry en of side s: a changed file's, or
// a quiet one for a folder that holds a change.
func (m *OS) filesGitMarkFor(s *filesSide, en fileEntry) (*filesGitRepo, filesGitMark, bool) {
	r, rel, ok := m.filesGitAt(s, en.Name)
	if !ok {
		return nil, filesGitMark{}, false
	}
	if en.Dir {
		return r, filesGitMark{}, r.dirs[rel]
	}
	mk, ok := r.marks[rel]
	return r, mk, ok
}

// filesGitMarkW is the cells the marks take at the end of a side's name
// column: none outside a repository with marks.
func (m *OS) filesGitMarkW(s *filesSide) int {
	if r, _, ok := m.filesGitAt(s, "."); ok && len(r.marks) > 0 {
		return 4
	}
	return 0
}

// filesGitDrawMark draws en's mark at x on row y: the author dot and the
// status letter, or a quiet dot for a folder.
func (m *OS) filesGitDrawMark(c *ckCanvas, pal overlay.Palette, s *filesSide, en fileEntry, x, y int) {
	r, mk, ok := m.filesGitMarkFor(s, en)
	if !ok {
		return
	}
	dot := "●"
	if overlay.UseASCII() {
		dot = "*"
	}
	if en.Dir {
		c.text(x+1, y, x+4, ckSeg{t: dot, fg: pal.FgDim})
		return
	}
	rs := reviewState{diff: r.diff}
	c.text(x+1, y, x+4, ckSeg{t: dot, fg: rs.reviewAuthorInk(mk.origin, pal)}, ckSeg{t: " "}, ckSeg{t: mk.status, fg: reviewStatusColor(mk.status, pal), attrs: ckBold})
}

// filesGitFileOf is the diff of the file quick look shows, nil when it is
// not a changed file of a repository on screen.
func (m *OS) filesGitFileOf(t quickLookTarget) (*filesGitRepo, *review.File) {
	g := &m.filesGit
	if !m.quickLook.files || t.host != "" || t.dir || len(g.repos) == 0 {
		return nil, nil
	}
	full := pathroot.ExpandHome(t.path)
	for root, r := range g.repos {
		rel, err := filepath.Rel(root, full)
		if err != nil || strings.HasPrefix(rel, "..") || r.marks == nil {
			continue
		}
		if mk, ok := r.marks[filepath.ToSlash(rel)]; ok && mk.file != nil {
			return r, mk.file
		}
	}
	return nil, nil
}

// filesGitDrawChanges draws quick look's Changes view: the file's hunks as
// the review draws them, each header with the chip of who made it.
func (m *OS) filesGitDrawChanges(c *ckCanvas, pal overlay.Palette, top, rows, w int) {
	ql := &m.quickLook
	r, f := m.filesGitFileOf(ql.target)
	if f == nil {
		return
	}
	rs := reviewState{diff: r.diff}
	look := m.reviewLook()
	dv := look.dv
	now := time.Now()
	numW := reviewNumW(f)
	type line struct {
		hunk int
		at   int
	}
	var all []line
	for h, hk := range f.Hunks {
		all = append(all, line{h, -1})
		for i := range hk.Lines {
			all = append(all, line{h, i})
		}
	}
	if len(all) == 0 {
		msg := "No lines to show."
		if f.Binary {
			msg = "A binary file changed."
		}
		c.text(1, top, w-1, ckSeg{t: msg, fg: pal.FgDim})
		return
	}
	ql.viewScroll = max(min(ql.viewScroll, len(all)-rows), 0)
	for i := 0; i < rows && ql.viewScroll+i < len(all); i++ {
		ln := all[ql.viewScroll+i]
		hk := f.Hunks[ln.hunk]
		y := top + i
		if ln.at < 0 {
			head, _ := reviewSplitHeader(hk.Header)
			c.fill(0, y, w, 1, ckColor(look.hunkBg))
			c.text(1, y, w-1, ckSeg{t: head, fg: pal.Info, bg: look.hunkBg})
			if chip := rs.reviewHunkChip(hk, pal, look.hunkBg, now); chip != nil {
				segs := make([]ckSeg, 0, len(chip))
				for _, s := range chip {
					var attrs uint8
					if s.bold {
						attrs = ckBold
					}
					segs = append(segs, ckSeg{t: s.text, fg: s.fg, bg: look.hunkBg, attrs: attrs})
				}
				c.textR(w-1, y, segs...)
			}
			continue
		}
		l := hk.Lines[ln.at]
		kind := lineKind(l)
		n := l.New
		if l.Op == review.OpDelete {
			n = l.Old
		}
		bar := " "
		if kind != diffview.Context && l.Origin > 0 {
			bar = reviewAuthorBar()
		}
		c.text(0, y, 1, ckSeg{t: bar, fg: rs.reviewAuthorInk(l.Origin, pal)})
		row := dv.Gutter(n, numW, kind, false) + dv.Sign(kind, false, false) + dv.Code(l.Text, nil, diffview.Range{}, kind, false, 0, max(w-numW-4, 1))
		c.draw(1, y, w-1, row)
	}
}

// FilesReview opens the review on the changed file under the cursor (v). The
// review is of a pane in the file's repository: the pane the view came from
// when it is there, else an agent pane there, else any pane there.
func (m *OS) FilesReview() tea.Cmd {
	s := m.filesCur()
	en, _, ok := m.filesCursorEntry()
	if !ok {
		return nil
	}
	r, rel, ok := m.filesGitAt(s, en.Name)
	if !ok || en.Dir || r.marks[rel].status == "" {
		m.ShowNotification("v reviews a changed file. This one has no changes.", "info", m.Settings.NotificationDuration)
		return nil
	}
	w := m.filesReviewPane(r.root)
	if w == nil {
		m.ShowNotification("No pane works in this repository. Open one there, then v reviews the file.", "info", m.Settings.NotificationDuration)
		return nil
	}
	who := printableTitle(m.railTitleShown(w))
	if who == "" {
		who = shortWindowLabel(w.ID)
	}
	m.CloseFilesView()
	cmd := m.openReview(m.SessionName, w.ID, who)
	m.review.openPath = rel
	return cmd
}

// filesReviewPane is the pane FilesReview reviews root through, nil for none.
func (m *OS) filesReviewPane(root string) *terminal.Window {
	in := func(w *terminal.Window) bool {
		if w == nil || w.Host != "" || w.CwdHost != "" {
			return false
		}
		dir := m.paneDir(w)
		if dir == "" {
			return false
		}
		r := gitstate.Find(dir)
		return r.Root != "" && filepath.Clean(r.Root) == root
	}
	if w := m.windowByID(m.explorer.origin); in(w) {
		return w
	}
	var any *terminal.Window
	for _, w := range m.Windows {
		if !in(w) {
			continue
		}
		if w.AgentHarness != "" {
			return w
		}
		if any == nil {
			any = w
		}
	}
	return any
}

// ReviewShowInFiles closes the review and shows the file under its cursor in
// the Files view (O).
func (m *OS) ReviewShowInFiles() tea.Cmd {
	r := &m.review
	e := r.currentFile()
	if e == nil || r.diff == nil || r.diff.Worktree == "" || r.query.Against != "" {
		m.reviewNotify("Move to a file to show it in the Files view", "info", m.Settings.NotificationDuration)
		return nil
	}
	if m.AttachedHost != "" {
		m.reviewNotify("The Files view shows the file on this machine only. This review is of another machine.", "info", m.Settings.NotificationDuration)
		return nil
	}
	full := filepath.Join(r.diff.Worktree, filepath.FromSlash(e.path))
	origin := r.query.Window
	m.CloseReview()
	cmd := m.openFilesViewAt("", filepath.Dir(full), origin)
	m.filesCur().pick = filepath.Base(full)
	return cmd
}

// filesCursorChanged reports whether the row under the cursor is a changed
// file, which v reviews.
func (m *OS) filesCursorChanged() bool {
	en, _, ok := m.filesCursorEntry()
	if !ok || en.Dir {
		return false
	}
	_, mk, ok := m.filesGitMarkFor(m.filesCur(), en)
	return ok && mk.status != ""
}
