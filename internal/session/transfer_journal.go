package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The transfer journal, the part folders, and the transfer events.
//
// A job lives in the daemon's memory, and a daemon restarts: tuios update,
// tuios kill-server, a crash. Without a record the job is gone and its part
// files stay behind as hidden files that nothing removes. So each job that has
// not ended is a JSON file in the journal folder, written on every change of
// state, and the next daemon start reads the folder and goes on with each job
// from its parts. A job that ended is removed from the journal, except a
// failed one, which stays until it leaves the list (transferKeepDone), so a
// restart still shows it and a person can try it again.
//
// Every part file this daemon writes, for its own copies and for copies a
// linked machine runs, is in a folder the daemon notes in the part folders
// file. At start the daemon removes the parts in those folders that are older
// than transferPartMaxAge and belong to no job it holds. A part of a copy a
// linked machine runs is that machine's to resume, so it waits out the age.
// The daemon never looks for parts in a folder it did not note.

// transferPartMaxAge is how long a part with no job of this daemon is kept.
const transferPartMaxAge = 7 * 24 * time.Hour

// transferPartDirsMax bounds the part folders the daemon notes. The oldest
// note goes first.
const transferPartDirsMax = 1000

// transferProgressEvery is the shortest time between two progress events of
// one job.
const transferProgressEvery = 250 * time.Millisecond

// transferSaveFinishedEvery is the shortest time between two journal writes
// of a folder copy's finished files. A file finished after the last write is
// copied again after a restart, which costs its bytes and nothing else.
const transferSaveFinishedEvery = 2 * time.Second

// transferJournalDir is the folder that holds the journal.
func transferJournalDir() string {
	return filepath.Join(getResurrectionDir(), "transfers")
}

// transferRecord is one job in the journal.
type transferRecord struct {
	Version  int      `json:"version"`
	ID       string   `json:"id"`
	Socket   string   `json:"socket"`
	Src      Endpoint `json:"src"`
	Dst      Endpoint `json:"dst"`
	Move     bool     `json:"move,omitempty"`
	Conflict string   `json:"conflict,omitempty"`
	Private  bool     `json:"private,omitempty"`
	Created  int64    `json:"created"`
	State    string   `json:"state"`
	Final    string   `json:"final,omitempty"`
	Finished []string `json:"finished,omitempty"`
	Error    string   `json:"error,omitempty"`
	Code     string   `json:"code,omitempty"`
	Ended    int64    `json:"ended,omitempty"`
}

// record is the job as the journal keeps it. The caller holds j.mu.
func (j *transferJob) recordLocked(socket string) transferRecord {
	r := transferRecord{
		Version: 1, ID: j.id, Socket: socket, Src: j.src, Dst: j.dst, Move: j.move,
		Conflict: j.conflict, Private: j.private, Created: j.created.UnixMilli(),
		State: j.state, Final: j.final, Error: j.errText, Code: j.errCode,
	}
	if !j.ended.IsZero() {
		r.Ended = j.ended.UnixMilli()
	}
	for rel := range j.finished {
		r.Finished = append(r.Finished, rel)
	}
	slices.Sort(r.Finished)
	return r
}

func (m *transferManager) socket() string {
	if m.d == nil || m.d.manager == nil {
		return ""
	}
	return m.d.manager.SocketPath()
}

// save writes the job's journal entry, or removes it when the job is done or
// cancelled.
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
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := writeFileAtomic(path, data); err != nil {
		LogError("Transfer %s: the journal entry could not be saved, so a restart loses this copy: %v", rec.ID, err)
	}
}

// forget removes the job's journal entry.
func (m *transferManager) forget(j *transferJob) {
	err := os.Remove(filepath.Join(transferJournalDir(), j.id+".json"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		LogError("Transfer %s: the journal entry could not be removed: %v", j.id, err)
	}
}

// saveFinished writes the journal after a file of a folder copy finished, at
// most every transferSaveFinishedEvery.
func (m *transferManager) saveFinished(j *transferJob) {
	j.mu.Lock()
	due := time.Since(j.saved) >= transferSaveFinishedEvery
	j.mu.Unlock()
	if due {
		m.save(j)
	}
}

// writeFileAtomic writes data to path through a temporary file and a rename,
// owner only, so a crash leaves the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// note says a change of the job: it writes the journal and publishes a
// transfer event when the state is not the one the last event said. A folder
// copy goes between running and verifying for each of its files, which is
// one state to a reader, so that change alone is not said. created marks the
// first note of a new job.
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
}

func isMoving(state string) bool {
	return state == transferRunning || state == transferVerifying
}

// progress publishes a progress event, at most every transferProgressEvery
// for one job.
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

// load reads the journal and takes back this daemon's jobs: a paused job
// stays paused, a failed one is listed as failed, and any other goes on from
// its parts. Then it removes the stale parts. It runs once, at start, after
// the links are started.
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
		if strings.HasSuffix(name, ".json.tmp") {
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
		if err := json.Unmarshal(data, &rec); err != nil || rec.Version != 1 || rec.ID == "" || checkPartID(rec.ID) != nil || rec.ID+".json" != name {
			LogError("Transfer journal entry %s could not be read, so it was removed: %v", name, err)
			_ = os.Remove(path)
			continue
		}
		if rec.Socket != socket {
			// Another daemon's, one that runs on another socket.
			continue
		}
		j := &transferJob{
			id: rec.ID, src: rec.Src, dst: rec.Dst, move: rec.Move, conflict: rec.Conflict,
			private: rec.Private, created: time.UnixMilli(rec.Created), final: rec.Final,
			finished: map[string]bool{}, wake: make(chan struct{}, 1),
		}
		for _, rel := range rec.Finished {
			if safeRel(rel) {
				j.finished[rel] = true
			}
		}
		j.filesDone = len(j.finished)
		switch rec.State {
		case transferPaused:
			j.state = transferPaused
		case transferFailed:
			j.state = transferFailed
			j.errText, j.errCode = rec.Error, rec.Code
			j.ended = time.UnixMilli(rec.Ended)
		default:
			j.state = transferWaiting
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
	for _, id := range m.order {
		if j := m.jobs[id]; j.getState() == transferPaused {
			paused = append(paused, j)
		}
	}
	m.mu.Unlock()
	m.parts.sweep(m.partIDs(), time.Now())
	for _, j := range resumed {
		m.publish(EventTransfer, "state", j)
		go m.run(j)
	}
	// A paused job waits in run for a resume, as one paused before the
	// restart did.
	for _, j := range paused {
		go m.run(j)
	}
}

// partIDs are the ids of the jobs this daemon holds, whose parts stay.
func (m *transferManager) partIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.jobs))
	for id := range m.jobs {
		out[id] = true
	}
	return out
}

// transferPartDirsFile names the part folders file in the journal folder.
const transferPartDirsFile = "part-dirs.json"

// partDirs is the set of folders this daemon wrote part files in.
type partDirs struct {
	mu   sync.Mutex
	dirs map[string]int64 // folder: when it was last noted, Unix ms
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

// readLocked loads the file once. The caller holds p.mu.
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
	if json.Unmarshal(data, &saved) == nil {
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
		slices.SortFunc(all, func(a, b at) int { return int(a.ms - b.ms) })
		for _, a := range all[:len(all)-transferPartDirsMax] {
			delete(p.dirs, a.dir)
		}
	}
	data, err := json.Marshal(p.dirs)
	if err != nil {
		return
	}
	if err := writeFileAtomic(p.file(), data); err != nil {
		LogError("The part folders could not be saved: %v", err)
	}
}

// note records that a part file was written in dir. The file is written only
// for a folder it did not hold, or held for more than a day, so a copy of many
// files to one folder writes it once.
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

// sweep removes, in each noted folder, the part files older than
// transferPartMaxAge whose job is not in keep. A folder with no part left is
// no longer noted.
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

// partIDOf is the part id in a part file's name, empty for a part with none.
func partIDOf(name string) string {
	_, id, ok := strings.Cut(name, partSuffix+"-")
	if !ok || checkPartID(id) != nil {
		return ""
	}
	return id
}
