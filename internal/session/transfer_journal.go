package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

const transferPartMaxAge = 7 * 24 * time.Hour

const transferPartDirsMax = 1000

const transferProgressEvery = 250 * time.Millisecond

const transferSaveFinishedEvery = 2 * time.Second

func transferJournalDir() string {
	return filepath.Join(getResurrectionDir(), "transfers")
}

type transferRecord struct {
	Version     int           `json:"version"`
	ID          string        `json:"id"`
	Socket      string        `json:"socket"`
	Src         Endpoint      `json:"src"`
	Dst         Endpoint      `json:"dst"`
	Move        bool          `json:"move,omitempty"`
	Conflict    string        `json:"conflict,omitempty"`
	Each        string        `json:"each,omitempty"`
	Place       string        `json:"place,omitempty"`
	Label       string        `json:"label,omitempty"`
	Private     bool          `json:"private,omitempty"`
	NoPerms     bool          `json:"no_perms,omitempty"`
	NoTimes     bool          `json:"no_times,omitempty"`
	NoCompress  bool          `json:"no_compress,omitempty"`
	RateLimit   int64         `json:"rate_limit,omitempty"`
	Pane        string        `json:"pane,omitempty"`
	PaneSession string        `json:"pane_session,omitempty"`
	PaneRoot    string        `json:"pane_root,omitempty"`
	Link        *transferLink `json:"link,omitempty"`
	Created     int64         `json:"created"`
	State       string        `json:"state"`
	Target      string        `json:"target,omitempty"`
	Final       string        `json:"final,omitempty"`
	Finished    []string      `json:"finished,omitempty"`
	Same        int           `json:"same,omitempty"`
	Skips       int           `json:"conflicts_skipped,omitempty"`
	Error       string        `json:"error,omitempty"`
	Code        string        `json:"code,omitempty"`
	Ended       int64         `json:"ended,omitempty"`
}

const transferJournalFinishedMax = 50000

func (j *transferJob) recordLocked(socket string) transferRecord {
	o := j.opts
	r := transferRecord{
		Version: 1, ID: j.id, Socket: socket, Src: j.src, Dst: j.dst, Move: o.Move,
		Conflict: o.Conflict, Each: o.Each, Place: o.Place, Label: o.Label, Private: o.Private,
		NoPerms: o.NoPerms, NoTimes: o.NoTimes, NoCompress: o.NoCompress, RateLimit: o.RateLimit,
		Pane: o.Pane, PaneSession: o.PaneSession, PaneRoot: o.PaneRoot, Link: o.Link, Created: j.created.UnixMilli(),
		State: j.state, Target: j.target, Final: j.final, Same: j.same, Skips: j.conflictsSkipped,
		Error: j.errText, Code: j.errCode,
	}
	if !j.ended.IsZero() {
		r.Ended = j.ended.UnixMilli()
	}
	if len(j.finished) <= transferJournalFinishedMax {
		for rel := range j.finished {
			r.Finished = append(r.Finished, rel)
		}
		slices.Sort(r.Finished)
	}
	return r
}

func (m *transferManager) socket() string {
	if m.d == nil || m.d.manager == nil {
		return ""
	}
	return m.d.manager.SocketPath()
}

func (m *transferManager) save(j *transferJob) {
	j.mu.Lock()
	rec := j.recordLocked(m.socket())
	j.saved = time.Now()
	j.mu.Unlock()
	path := filepath.Join(transferJournalDir(), rec.ID+".json")
	if rec.State == transferDone || rec.State == transferCancelled {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			LogError("Transfer %s: the journal entry could not be removed: %v", rec.ID, err)
		}
		return
	}
	data, err := jsonx.Marshal(rec)
	if err != nil {
		return
	}
	m.journalMu.Lock()
	err = atomicfile.WritePrivate(path, data, true)
	m.journalMu.Unlock()
	if err != nil {
		LogError("Transfer %s: the journal entry could not be saved, so a restart loses this copy: %v", rec.ID, err)
	}
}

func (m *transferManager) forget(j *transferJob) {
	err := os.Remove(filepath.Join(transferJournalDir(), j.id+".json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		LogError("Transfer %s: the journal entry could not be removed: %v", j.id, err)
	}
}

func (m *transferManager) saveFinished(j *transferJob) {
	j.mu.Lock()
	due := time.Since(j.saved) >= transferSaveFinishedEvery
	j.mu.Unlock()
	if due {
		m.save(j)
	}
}

func (m *transferManager) note(j *transferJob, created bool) {
	j.mu.Lock()
	state, was := j.state, j.emitted
	quiet := !created && j.isDir && isMoving(state) && isMoving(was)
	if state == was && !created || quiet {
		j.mu.Unlock()
		return
	}
	j.emitted = state
	j.mu.Unlock()
	m.save(j)
	action := "state"
	switch {
	case created:
		action = "created"
	case transferEnded(state):
		action = "ended"
	}
	m.publish(EventTransfer, action, j)
	if action == "ended" {
		m.expireLater(j)
	}
}

func isMoving(state string) bool {
	return state == transferRunning || state == transferVerifying
}

func (m *transferManager) progress(j *transferJob) {
	now := time.Now()
	j.mu.Lock()
	if now.Sub(j.lastProgress) < transferProgressEvery {
		j.mu.Unlock()
		return
	}
	j.lastProgress = now
	j.mu.Unlock()
	m.publish(EventTransferProgress, "progress", j)
}

func (m *transferManager) publish(kind, action string, j *transferJob) {
	if m.d == nil || m.d.events == nil {
		return
	}
	row := j.row()
	m.d.events.publish(streamEvent{Type: kind, Action: action, Transfer: &row})
}

// TransferFile is one file of a copy as a transfer-file event says it.
type TransferFile struct {
	// ID is the copy's id, and Pane the pane that started it.
	ID   string `json:"id"`
	Pane string `json:"pane,omitempty"`
	// Rel is the file under the folder, or the file's name for a copy of one
	// file.
	Rel string `json:"rel"`
	// State is done (copied and checked), same (there already with the same
	// bytes), skipped (there and different, left as it was), conflict (made
	// there during the copy, left as it was) or failed.
	State  string `json:"state"`
	SHA256 string `json:"sha256,omitempty"`
	Code   string `json:"code,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (m *transferManager) fileEvent(j *transferJob, rel, state, sum, code, msg string) {
	if m.d == nil || m.d.events == nil {
		return
	}
	m.d.events.publish(streamEvent{Type: EventTransferFile, Action: state, File: &TransferFile{
		ID: j.id, Pane: j.opts.Pane, Rel: rel, State: state, SHA256: sum, Code: code, Error: msg,
	}})
}

func (m *transferManager) load() {
	dir := transferJournalDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		LogError("The transfer journal could not be read: %v", err)
	}
	socket := m.socket()
	var resumed []*transferJob
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == transferPartDirsFile {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec transferRecord
		if err := jsonx.Unmarshal(data, &rec); err != nil || rec.Version != 1 || rec.ID == "" || checkPartID(rec.ID) != nil || rec.ID+".json" != name {
			LogError("Transfer journal entry %s could not be read, so it was removed: %v", name, err)
			_ = os.Remove(path)
			continue
		}
		if rec.Socket != socket {
			continue
		}
		if rec.Label != "" && checkKeepLabel(rec.Label) != nil {
			rec.Label = ""
		}
		j := newJob(rec.ID, rec.Src, rec.Dst, transferOptions{
			Move: rec.Move, Conflict: rec.Conflict, Each: rec.Each, Place: rec.Place, Label: rec.Label,
			Private: rec.Private, NoPerms: rec.NoPerms, NoTimes: rec.NoTimes, NoCompress: rec.NoCompress,
			RateLimit: max(rec.RateLimit, 0), Pane: rec.Pane, PaneSession: rec.PaneSession, PaneRoot: rec.PaneRoot, Link: rec.Link,
		})
		j.created = time.UnixMilli(rec.Created)
		j.target, j.final = rec.Target, rec.Final
		j.same, j.conflictsSkipped = rec.Same, rec.Skips
		for _, rel := range rec.Finished {
			if safeRel(rel) {
				j.finished[rel] = true
			}
		}
		j.filesDone = len(j.finished)
		switch rec.State {
		case transferPaused:
			j.to(transferPaused)
		case transferFailed:
			j.to(transferFailed)
			j.errText, j.errCode = rec.Error, rec.Code
			j.ended = time.UnixMilli(rec.Ended)
		default:
			j.to(transferWaiting)
			j.errText = "tuios restarted. The copy goes on from where it stopped."
			resumed = append(resumed, j)
		}
		j.emitted = j.state
		m.mu.Lock()
		m.jobs[j.id] = j
		m.order = append(m.order, j.id)
		m.mu.Unlock()
		LogBasic("Transfer %s is back from the journal as %s: %s to %s", j.id, j.state, j.src, j.dst)
	}
	m.mu.Lock()
	slices.SortStableFunc(m.order, func(a, b string) int {
		return m.jobs[a].created.Compare(m.jobs[b].created)
	})
	m.pruneLocked()
	paused := []*transferJob{}
	var ended []*transferJob
	for _, id := range m.order {
		switch j := m.jobs[id]; {
		case j.getState() == transferPaused:
			paused = append(paused, j)
		case transferEnded(j.getState()):
			ended = append(ended, j)
		}
	}
	m.mu.Unlock()
	for _, j := range ended {
		m.expireLater(j)
	}
	m.parts.sweep(m.partIDs(), time.Now())
	for _, j := range resumed {
		m.publish(EventTransfer, "state", j)
		go m.run(j)
	}
	for _, j := range paused {
		go m.run(j)
	}
}

func (m *transferManager) partIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.jobs))
	for id := range m.jobs {
		out[id] = true
	}
	return out
}

const transferPartDirsFile = "part-dirs.json"

type partDirs struct {
	mu   sync.Mutex
	dirs map[string]int64
	path string
}

func newPartDirs() *partDirs {
	return &partDirs{dirs: map[string]int64{}}
}

func (p *partDirs) file() string {
	if p.path != "" {
		return p.path
	}
	return filepath.Join(transferJournalDir(), transferPartDirsFile)
}

func (p *partDirs) readLocked() {
	if p.dirs == nil {
		p.dirs = map[string]int64{}
	}
	if len(p.dirs) > 0 {
		return
	}
	data, err := os.ReadFile(p.file())
	if err != nil {
		return
	}
	var saved map[string]int64
	if jsonx.Unmarshal(data, &saved) == nil {
		for dir, at := range saved {
			if filepath.IsAbs(dir) {
				p.dirs[dir] = at
			}
		}
	}
}

func (p *partDirs) saveLocked() {
	if len(p.dirs) > transferPartDirsMax {
		type at struct {
			dir string
			ms  int64
		}
		all := make([]at, 0, len(p.dirs))
		for d, ms := range p.dirs {
			all = append(all, at{d, ms})
		}
		sortx.SortFunc(all, func(a, b at) int { return int(a.ms - b.ms) })
		for _, a := range all[:len(all)-transferPartDirsMax] {
			delete(p.dirs, a.dir)
		}
	}
	data, err := jsonx.Marshal(p.dirs)
	if err != nil {
		return
	}
	if err := atomicfile.WritePrivate(p.file(), data, true); err != nil {
		LogError("The part folders could not be saved: %v", err)
	}
}

func (p *partDirs) note(dir string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readLocked()
	now := time.Now().UnixMilli()
	if at, ok := p.dirs[dir]; ok && now-at < int64(24*time.Hour/time.Millisecond) {
		return
	}
	p.dirs[dir] = now
	p.saveLocked()
}

func (p *partDirs) sweep(keep map[string]bool, now time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readLocked()
	changed := false
	for dir := range p.dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			delete(p.dirs, dir)
			changed = true
			continue
		}
		left := 0
		for _, e := range entries {
			if !isPartName(e.Name()) || !e.Type().IsRegular() {
				continue
			}
			if id := partIDOf(e.Name()); id != "" && keep[id] {
				left++
				continue
			}
			fi, err := e.Info()
			if err != nil || now.Sub(fi.ModTime()) < transferPartMaxAge {
				left++
				continue
			}
			if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
				LogBasic("Removed the stale part %s, which no copy will finish", filepath.Join(dir, e.Name()))
			} else {
				left++
			}
		}
		if left == 0 {
			delete(p.dirs, dir)
			changed = true
		}
	}
	if changed {
		p.saveLocked()
	}
}

func partIDOf(name string) string {
	_, id, ok := strings.Cut(name, partSuffix+"-")
	if !ok || checkPartID(id) != nil {
		return ""
	}
	return id
}
