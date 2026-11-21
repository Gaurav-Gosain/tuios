package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
	"github.com/Gaurav-Gosain/tuios/internal/transcript"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

const transcriptDebounce = 150 * time.Millisecond

const transcriptJoinRetry = 10 * time.Second

type transcriptWatch interface {
	Watch(path, key string, onChange func()) error
	Unwatch(path, key string)
}

type transcriptJoin struct {
	windowID   string
	harness    string
	session    string
	format     string
	leaf       *string
	doc        bool
	docSize    int64
	docMod     time.Time
	docEventAt time.Time
	docTimer   *time.Timer
	reader     *transcript.Reader
	exact      bool
	watched    bool
	debounce   *time.Timer
	missing    int
	announced  int64
}

const transcriptMissingLimit = 3

type agentTranscriptState struct {
	mu      sync.Mutex
	joins   map[string]*transcriptJoin
	nextTry map[string]time.Time
	viewTry map[string]*viewSearch
	watcher transcriptWatch
}

// SetTranscriptWatcher installs the filesystem notification the transcript
// source uses. A session with none never joins anything.
func (s *Session) SetTranscriptWatcher(w transcriptWatch) {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	s.transcripts.watcher = w
}

// JoinAgentTranscript binds a window to a transcript file and reads it once.
//
// exact says the path came from the harness itself, through the session-identity
// hook. An exact join replaces a searched one; a searched one never replaces an
// exact one, because the harness naming its own file is not a thing a directory
// search can improve on.
func (s *Session) JoinAgentTranscript(windowID, harnessID, path string, exact bool) error {
	return s.JoinAgentTranscriptAs(windowID, harnessID, harness.ReaderJSONL, path, exact)
}

// JoinAgentTranscriptAs is JoinAgentTranscript for a transcript in the named
// format, the reader of the harness's manifest.
func (s *Session) JoinAgentTranscriptAs(windowID, harnessID, format, path string, exact bool) error {
	return s.joinAgentTranscriptLeaf(windowID, harnessID, format, path, exact, nil)
}

func (s *Session) joinAgentTranscriptLeaf(windowID, harnessID, format, path string, exact bool, leaf *string) error {
	if format == "" {
		format = harness.ReaderJSONL
	}
	if windowID == "" || path == "" {
		return errors.New("transcript join needs a window and a path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	s.transcripts.mu.Lock()
	if prev, ok := s.transcripts.joins[windowID]; ok {
		if prev.reader.Path() == abs && prev.harness == harnessID && prev.format == format {
			prev.exact = prev.exact || exact
			changed := setTranscriptLeaf(prev, leaf)
			s.transcripts.mu.Unlock()
			if changed {
				s.docEvent(windowID, prev)
			}
			return nil
		}
		if prev.exact && !exact {
			s.transcripts.mu.Unlock()
			return nil
		}
		s.releaseJoinLocked(prev)
	}
	j := &transcriptJoin{
		windowID: windowID, harness: harnessID, format: format, reader: transcript.NewReader(abs), exact: exact,
		doc: transcriptview.IsDocument(format, abs) || transcriptview.IsTree(format),
	}
	setTranscriptLeaf(j, leaf)
	if s.transcripts.joins == nil {
		s.transcripts.joins = make(map[string]*transcriptJoin)
	}
	s.transcripts.joins[windowID] = j
	watcher := s.transcripts.watcher
	s.transcripts.mu.Unlock()

	if watcher != nil {
		if err := watcher.Watch(abs, s.transcriptWatchKey(windowID), func() { s.onTranscriptChanged(windowID) }); err == nil {
			s.transcripts.mu.Lock()
			j.watched = true
			s.transcripts.mu.Unlock()
		}
	}
	s.readAgentTranscript(windowID)
	return nil
}

func setTranscriptLeaf(j *transcriptJoin, leaf *string) bool {
	if leaf == nil || (j.leaf != nil && *j.leaf == *leaf) {
		return false
	}
	value := *leaf
	j.leaf = &value
	return true
}

// JoinAgentSession binds a window to one session of a harness's store (the
// file at path, such as opencode's service.json) and raises the transcript
// event. A report that names another session of the same store moves the
// join to it. The store is not watched: it does not grow as a file, and the
// harness's own reports, which come as the session changes, raise the event.
func (s *Session) JoinAgentSession(windowID, harnessID, format, path, session string) {
	if windowID == "" || path == "" || session == "" {
		return
	}
	s.transcripts.mu.Lock()
	j, ok := s.transcripts.joins[windowID]
	if ok && (j.reader.Path() != path || j.session == "") {
		delete(s.transcripts.joins, windowID)
		s.releaseJoinLocked(j)
		ok = false
	}
	if !ok {
		j = &transcriptJoin{windowID: windowID, harness: harnessID, format: format, reader: transcript.NewReader(path), exact: true, doc: true}
		if s.transcripts.joins == nil {
			s.transcripts.joins = make(map[string]*transcriptJoin)
		}
		s.transcripts.joins[windowID] = j
	}
	j.session = session
	s.transcripts.mu.Unlock()
	s.docEvent(windowID, j)
}

// DropAgentTranscript ends a window's join and gives back the claim it held, so
// whatever tier was handling the pane before takes over again.
func (s *Session) DropAgentTranscript(windowID string) {
	s.transcripts.mu.Lock()
	j, ok := s.transcripts.joins[windowID]
	if ok {
		delete(s.transcripts.joins, windowID)
		s.releaseJoinLocked(j)
	}
	delete(s.transcripts.nextTry, windowID)
	s.transcripts.mu.Unlock()
	if ok {
		s.yieldAgentClaim(windowID, AgentSourceTranscript)
	}
}

func (s *Session) dropAgentTranscripts() {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	for id, j := range s.transcripts.joins {
		delete(s.transcripts.joins, id)
		delete(s.transcripts.nextTry, id)
		s.releaseJoinLocked(j)
	}
}

func (s *Session) releaseJoinLocked(j *transcriptJoin) {
	if j.debounce != nil {
		j.debounce.Stop()
		j.debounce = nil
	}
	if j.docTimer != nil {
		j.docTimer.Stop()
		j.docTimer = nil
	}
	if j.watched && s.transcripts.watcher != nil {
		s.transcripts.watcher.Unwatch(j.reader.Path(), s.transcriptWatchKey(j.windowID))
	}
}

func (s *Session) transcriptWatchKey(windowID string) string {
	return s.ID + "/" + windowID
}

func (s *Session) onTranscriptChanged(windowID string) {
	s.transcripts.mu.Lock()
	j, ok := s.transcripts.joins[windowID]
	if !ok {
		s.transcripts.mu.Unlock()
		return
	}
	if j.debounce != nil {
		j.debounce.Stop()
	}
	j.debounce = time.AfterFunc(transcriptDebounce, func() {
		s.transcripts.mu.Lock()
		if cur, ok := s.transcripts.joins[windowID]; ok {
			cur.debounce = nil
		}
		s.transcripts.mu.Unlock()
		s.readAgentTranscript(windowID)
	})
	s.transcripts.mu.Unlock()
}

func (s *Session) readAgentTranscript(windowID string) bool {
	s.transcripts.mu.Lock()
	j, ok := s.transcripts.joins[windowID]
	s.transcripts.mu.Unlock()
	if !ok {
		return false
	}

	if j.session != "" {
		return false
	}
	if j.format != harness.ReaderJSONL {
		return s.readViewTranscript(windowID, j)
	}
	obs, fresh, err := j.reader.Read()
	if err != nil {
		if errors.Is(err, transcript.ErrNoFile) || errors.Is(err, transcript.ErrNotRegular) {
			s.transcripts.mu.Lock()
			j.missing++
			gone := j.missing >= transcriptMissingLimit
			s.transcripts.mu.Unlock()
			if gone {
				s.DropAgentTranscript(windowID)
			}
		}
		return false
	}
	s.transcripts.mu.Lock()
	j.missing = 0
	harnessID := j.harness
	s.transcripts.mu.Unlock()
	s.noteTranscriptGrowth(windowID, j)
	if obs.ContextTokens > 0 {
		s.noteTranscriptContext(windowID, obs.ContextTokens)
	}
	if !fresh {
		return false
	}
	return s.applyTranscriptTurn(windowID, harnessID, obs.Turn)
}

// AgentMetaSourceTranscript is the metadata source of what the daemon reads
// from a pane's transcript.
const AgentMetaSourceTranscript = "transcript"

func (s *Session) noteTranscriptContext(windowID string, tokens int64) {
	v := strconv.FormatInt(tokens, 10)
	_, _ = s.SetAgentMeta(windowID, AgentMetaUpdate{
		Keys:   []string{integration.MetaContextTokens},
		Values: []*string{&v},
		Source: AgentMetaSourceTranscript,
	})
}

func (s *Session) readViewTranscript(windowID string, j *transcriptJoin) bool {
	path := j.reader.Path()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil || os.IsNotExist(err) {
			s.transcripts.mu.Lock()
			j.missing++
			drop := j.missing >= transcriptMissingLimit
			s.transcripts.mu.Unlock()
			if drop {
				s.DropAgentTranscript(windowID)
			}
		}
		return false
	}
	s.transcripts.mu.Lock()
	j.missing = 0
	changed := info.Size() != j.docSize || !info.ModTime().Equal(j.docMod)
	j.docSize, j.docMod = info.Size(), info.ModTime()
	s.transcripts.mu.Unlock()
	if !changed {
		return false
	}
	if !j.doc {
		off, err := viewTailOffset(path)
		if err == nil {
			s.announceTranscript(windowID, j, off)
		}
		return false
	}
	s.docEvent(windowID, j)
	return false
}

const docEventGap = time.Second

func (s *Session) docEvent(windowID string, j *transcriptJoin) {
	s.transcripts.mu.Lock()
	wait := docEventGap - time.Since(j.docEventAt)
	if wait > 0 {
		if j.docTimer == nil {
			j.docTimer = time.AfterFunc(wait, func() {
				s.transcripts.mu.Lock()
				cur, ok := s.transcripts.joins[windowID]
				if ok && cur == j {
					j.docTimer = nil
					j.docEventAt = time.Now()
				}
				s.transcripts.mu.Unlock()
				if ok && cur == j {
					s.emit(SessionEvent{Type: EventTranscript, Window: windowID})
				}
			})
		}
		s.transcripts.mu.Unlock()
		return
	}
	j.docEventAt = time.Now()
	s.transcripts.mu.Unlock()
	s.emit(SessionEvent{Type: EventTranscript, Window: windowID})
}

const viewTailMax = 16 << 20

func viewTailOffset(path string) (int64, error) {
	f, err := transcript.OpenRegular(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	buf := make([]byte, 64<<10)
	defer clear(buf)
	end := info.Size()
	for stop := max(0, end-viewTailMax); end > stop; {
		start := max(stop, end-int64(len(buf)))
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && n < int(end-start) {
			return 0, err
		}
		if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
			return start + int64(i) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func (s *Session) applyTranscriptTurn(windowID, harnessID string, turn transcript.Turn) bool {
	var state AgentState
	switch turn {
	case transcript.TurnDone:
		state = AgentStateDone
	case transcript.TurnWorking:
		state = AgentStateWorking
	default:
		return false
	}
	_, applied, err := s.ApplyAgentReport(windowID, AgentReport{
		State:   state,
		Source:  AgentSourceTranscript,
		Harness: harnessID,
	})
	return err == nil && applied
}

func (s *Session) transcriptFallbackDue(windowID string) bool {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	j, ok := s.transcripts.joins[windowID]
	return ok && !j.watched
}

func (s *Session) readTranscriptOnOutput(ptyID string) {
	winID, _ := s.agentHarnessOf(ptyID)
	if winID == "" || !s.transcriptFallbackDue(winID) {
		return
	}
	s.readAgentTranscript(winID)
}

func (s *Session) hasTranscriptJoin(windowID string) bool {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	_, ok := s.transcripts.joins[windowID]
	return ok
}

func (s *Session) maintainAgentTranscripts(reg *harness.Registry, cwdOf func(ptyID string) agentIdentity, taken map[string]string) {
	if reg == nil {
		return
	}
	type pane struct{ windowID, ptyID, harnessID string }
	var panes []pane
	live := map[string]struct{}{}
	s.stateMu.RLock()
	for i := range s.state.Windows {
		w := &s.state.Windows[i]
		live[w.ID] = struct{}{}
		if w.AgentHarness != "" && w.PTYID != "" {
			panes = append(panes, pane{w.ID, w.PTYID, w.AgentHarness})
		}
	}
	s.stateMu.RUnlock()

	running := make(map[string]string, len(panes))
	for _, p := range panes {
		running[p.windowID] = p.harnessID
	}
	s.transcripts.mu.Lock()
	var stale []string
	for id, j := range s.transcripts.joins {
		if h, ok := running[id]; !ok || h != j.harness {
			stale = append(stale, id)
		}
	}
	for id := range s.transcripts.nextTry {
		if _, ok := live[id]; !ok {
			delete(s.transcripts.nextTry, id)
		}
	}
	for id := range s.transcripts.viewTry {
		if _, ok := live[id]; !ok {
			delete(s.transcripts.viewTry, id)
		}
	}
	s.transcripts.mu.Unlock()
	for _, id := range stale {
		s.DropAgentTranscript(id)
	}

	for _, p := range panes {
		if s.hasTranscriptJoin(p.windowID) {
			continue
		}
		tr := reg.TranscriptFor(p.harnessID)
		if tr == nil || tr.Reader != harness.ReaderJSONL {
			continue
		}
		if !s.joinSearchDue(p.windowID) {
			continue
		}
		id := cwdOf(p.ptyID)
		if path, ok := searchTranscript(tr, id, transcriptOwner{window: p.windowID, started: id.started, resumed: id.resumed, continued: id.continued, taken: taken}); ok {
			if s.JoinAgentTranscriptAs(p.windowID, p.harnessID, tr.Reader, path, false) == nil && taken != nil {
				if abs, err := filepath.Abs(path); err == nil {
					taken[abs] = p.windowID
				}
			}
		}
	}
}

func (s *Session) addJoinedTranscripts(taken map[string]string) {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	for id, j := range s.transcripts.joins {
		taken[j.reader.Path()] = id
	}
}

func (s *Session) joinSearchDue(windowID string) bool {
	now := time.Now()
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	if next, ok := s.transcripts.nextTry[windowID]; ok && now.Before(next) {
		return false
	}
	if s.transcripts.nextTry == nil {
		s.transcripts.nextTry = make(map[string]time.Time)
	}
	s.transcripts.nextTry[windowID] = now.Add(transcriptJoinRetry)
	return true
}

type agentIdentity struct {
	cwd, version string
	started      time.Time
	pid          int
	agent        bool
	resumed      string
	continued    bool
}

func searchTranscript(tr *harness.Transcript, id agentIdentity, own transcriptOwner) (string, bool) {
	return searchOwnTranscriptIn(tr.ExpandDir(id.cwd), tr, id.cwd, id.version, own)
}

type transcriptOwner struct {
	window    string
	started   time.Time
	resumed   string
	continued bool
	taken     map[string]string
}

const transcriptStartSlack = 2 * time.Second

func (o transcriptOwner) owns(f, session string) bool {
	if abs, err := filepath.Abs(f); err == nil {
		if w, ok := o.taken[abs]; ok && w != o.window {
			return false
		}
	}
	if o.resumed != "" {
		return session == o.resumed
	}
	if o.continued {
		return true
	}
	if !o.started.IsZero() {
		if first, ok := transcript.FirstRecordTime(f); ok && first.Before(o.started.Add(-transcriptStartSlack)) {
			return false
		}
	}
	return true
}

func searchTranscriptIn(dir string, tr *harness.Transcript, cwd, version string) (string, bool) {
	return searchOwnTranscriptIn(dir, tr, cwd, version, transcriptOwner{})
}

func searchOwnTranscriptIn(dir string, tr *harness.Transcript, cwd, version string, own transcriptOwner) (string, bool) {
	if dir == "" {
		return "", false
	}
	files, err := filepath.Glob(filepath.Join(dir, tr.Glob))
	if err != nil || len(files) == 0 {
		return "", false
	}
	var found string
	var continuedFiles []string
	for _, f := range files {
		r := transcript.NewReader(f)
		obs, ok, err := r.Read()
		if err != nil || !ok {
			continue
		}
		if tr.Verifies("cwd") && obs.CWD != cwd {
			continue
		}
		if tr.Verifies("version") && (version == "" || obs.Version != version) {
			continue
		}
		if !own.owns(f, obs.SessionID) {
			continue
		}
		if own.continued {
			continuedFiles = append(continuedFiles, f)
			continue
		}
		if found != "" {
			return "", false
		}
		found = f
	}
	if own.continued {
		return continuedPick(continuedFiles, own.started)
	}
	return found, found != ""
}

func continuedPick(files []string, started time.Time) (string, bool) {
	if started.IsZero() || len(files) == 0 {
		return "", false
	}
	var fresh []string
	var best string
	var bestAt time.Time
	tie := false
	for _, f := range files {
		if first, ok := transcript.FirstRecordTime(f); ok && !first.Before(started.Add(-transcriptStartSlack)) {
			fresh = append(fresh, f)
			continue
		}
		at, ok := transcript.LastRecordBefore(f, started)
		if !ok {
			continue
		}
		switch {
		case at.After(bestAt):
			best, bestAt, tie = f, at, false
		case at.Equal(bestAt):
			tie = true
		}
	}
	switch {
	case len(fresh) == 1:
		return fresh[0], true
	case len(fresh) > 1 || tie || best == "":
		return "", false
	}
	return best, true
}

func paneAgentIdentity(info foregroundInfo) agentIdentity {
	var id agentIdentity
	if info.pid <= 0 {
		return id
	}
	id.pid = info.pid
	if cwd, ok := ptyspawn.ProcessCwd(info.pid); ok {
		id.cwd = cwd
	}
	if info.exe != "" {
		id.version = filepath.Base(info.exe)
	}
	id.started, _ = procinfo.StartedAt(info.pid)
	return id
}
