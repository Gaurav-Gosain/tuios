package app

import (
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const fileWatchSettle = 100 * time.Millisecond

const fileWatchGap = 500 * time.Millisecond

type fileDirChangedMsg struct {
	at time.Time
}

type fileWatcher struct {
	want fileWatchSpec

	ch chan struct{}

	mu      sync.Mutex
	dir     string
	w       *dirwatch.Watcher
	remote  *session.TUIClient
	latest  fileWatchSpec
	stopped bool

	treeWant string
	treeSeq  uint64
	set      *dirwatch.Set
	setDirs  map[string]bool
	treeMu   sync.Mutex

	dmu       sync.Mutex
	dirtyRoot bool
	dirty     map[string]struct{}
}

func (fw *fileWatcher) markRootDirty() {
	fw.dmu.Lock()
	fw.dirtyRoot = true
	fw.dmu.Unlock()
}

func (fw *fileWatcher) markDirty(dir string) {
	fw.dmu.Lock()
	if fw.dirty == nil {
		fw.dirty = map[string]struct{}{}
	}
	fw.dirty[dir] = struct{}{}
	fw.dmu.Unlock()
}

func (fw *fileWatcher) takeDirty() (root bool, dirs []string) {
	fw.dmu.Lock()
	defer fw.dmu.Unlock()
	root = fw.dirtyRoot
	fw.dirtyRoot = false
	for d := range fw.dirty {
		dirs = append(dirs, d)
	}
	clear(fw.dirty)
	return root, dirs
}

func (m *OS) fileWatchChan() chan struct{} {
	if m.fileWatch.ch == nil {
		m.fileWatch.ch = make(chan struct{}, 1)
	}
	return m.fileWatch.ch
}

func listenForFileChange(stop <-chan struct{}, ch chan struct{}, last time.Time) tea.Cmd {
	return func() tea.Msg {
		select {
		case _, ok := <-ch:
			if !ok {
				return nil
			}
		case <-stop:
			return nil
		}
		time.Sleep(max(fileWatchSettle, time.Until(last.Add(fileWatchGap))))
		select {
		case <-ch:
		default:
		}
		return fileDirChangedMsg{at: time.Now()}
	}
}

type fileWatchSpec struct {
	dir    string
	remote *session.TUIClient
	origin string
}

func (m *OS) fileWatchTarget() fileWatchSpec {
	v := m.filesView
	if v.Dir == "" || v.Err != "" || m.Client == ClientUnknown {
		return fileWatchSpec{}
	}
	if v.Host == "" && m.AttachedHost == "" {
		return fileWatchSpec{dir: v.Dir}
	}
	if m.DaemonClient == nil {
		return fileWatchSpec{}
	}
	return fileWatchSpec{dir: v.Dir, remote: m.DaemonClient, origin: v.Origin}
}

func (m *OS) syncFileWatch() {
	target := m.fileWatchTarget()
	fw := &m.fileWatch
	if target == fw.want {
		return
	}
	fw.want = target
	ch := m.fileWatchChan()
	fw.mu.Lock()
	fw.latest = target
	fw.mu.Unlock()
	go fw.retarget(target, ch)
}

func (fw *fileWatcher) retarget(target fileWatchSpec, ch chan struct{}) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped || target != fw.latest {
		return
	}
	if fw.w != nil {
		fw.w.Close()
		fw.w = nil
	}
	if fw.remote != nil && fw.remote != target.remote {
		_ = fw.remote.WatchDir("", "")
	}
	fw.dir, fw.remote = "", nil
	dir := target.dir
	if dir == "" {
		return
	}
	if target.remote != nil {
		if err := target.remote.WatchDir(target.origin, dir); err == nil {
			fw.dir, fw.remote = dir, target.remote
		}
		return
	}
	w, err := dirwatch.Watch(dir, func() {
		fw.markRootDirty()
		select {
		case ch <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return
	}
	fw.w, fw.dir = w, dir
}

func (m *OS) stopFileWatch() {
	fw := &m.fileWatch
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped {
		return
	}
	fw.stopped = true
	if fw.w != nil {
		fw.w.Close()
		fw.w, fw.dir = nil, ""
	}
	if fw.remote != nil {
		_ = fw.remote.WatchDir("", "")
		fw.remote, fw.dir = nil, ""
	}
	if fw.set != nil {
		fw.set.Close()
		fw.set, fw.setDirs = nil, nil
	}
	if fw.ch != nil {
		close(fw.ch)
	}
}

func (fw *fileWatcher) remoteDirChanged(dir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped || fw.remote == nil || fw.dir != dir || fw.ch == nil {
		return
	}
	fw.markRootDirty()
	select {
	case fw.ch <- struct{}{}:
	default:
	}
}

func (m *OS) refreshChangedFolder() tea.Cmd {
	v := m.filesView
	if v.Dir == "" || v.Want != v.Dir || m.fileWatchTarget().dir == "" {
		return nil
	}
	if v.Loading {
		return m.readFileList(v.Dir, v.Origin, v.Pinned)
	}
	m.filesView.QuietReq++
	return m.fileListCmd(v.Dir, v.Origin, v.Pinned, m.filesView.QuietReq)
}

func (m *OS) syncTreeWatch(dirs []string) {
	if t := m.fileWatchTarget(); t.dir == "" || t.remote != nil {
		dirs = nil
	}
	key := strings.Join(dirs, "\x00")
	fw := &m.fileWatch
	if key == fw.treeWant {
		return
	}
	fw.treeWant = key
	ch := m.fileWatchChan()
	fw.mu.Lock()
	fw.treeSeq++
	seq := fw.treeSeq
	fw.mu.Unlock()
	go fw.retargetTree(slices.Clone(dirs), seq, ch)
}

func (fw *fileWatcher) retargetTree(dirs []string, seq uint64, ch chan struct{}) {
	fw.treeMu.Lock()
	defer fw.treeMu.Unlock()
	fw.mu.Lock()
	if fw.stopped || seq != fw.treeSeq {
		fw.mu.Unlock()
		return
	}
	if len(dirs) == 0 {
		set := fw.set
		fw.set, fw.setDirs = nil, nil
		fw.mu.Unlock()
		if set != nil {
			set.Close()
		}
		return
	}
	if fw.set == nil {
		set, err := dirwatch.NewSet(func(dir string) {
			fw.markDirty(dir)
			dirwatch.Nudge(ch)
		})
		if err != nil {
			fw.mu.Unlock()
			return
		}
		fw.set, fw.setDirs = set, map[string]bool{}
	}
	set := fw.set
	want := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		want[d] = true
	}
	var drop []string
	for d := range fw.setDirs {
		if !want[d] {
			drop = append(drop, d)
			delete(fw.setDirs, d)
		}
	}
	for _, d := range dirs {
		fw.setDirs[d] = true
	}
	fw.mu.Unlock()
	for _, d := range drop {
		set.Remove(d)
	}
	for _, d := range dirs {
		_ = set.Add(d)
	}
}

func (m *OS) rewatchTree(dir string) {
	fw := &m.fileWatch
	go func() {
		fw.treeMu.Lock()
		defer fw.treeMu.Unlock()
		fw.mu.Lock()
		set, ok := fw.set, fw.setDirs[dir]
		fw.mu.Unlock()
		if set != nil && ok {
			_ = set.Refresh(dir)
		}
	}()
}
