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
	"github.com/Gaurav-Gosain/tuios/internal/procinfo"
	"github.com/Gaurav-Gosain/tuios/internal/transcript"
	"github.com/Gaurav-Gosain/tuios/internal/transcriptview"
)

// transcriptDebounce coalesces the several appends one turn produces into one
// read. It is a one-shot armed on the first event and gone when it fires, not a
// tick: a pane whose agent is silent arms nothing and costs nothing.
const transcriptDebounce = 150 * time.Millisecond

// transcriptJoinRetry bounds how often a pane with no join searches for one. The
// search is a directory glob plus a tail read per candidate, which is cheap but
// not free, and the answer only changes when a session file appears.
const transcriptJoinRetry = 10 * time.Second

// transcriptWatch is the filesystem notification the joins are driven by,
// narrowed to what this file needs so the session can be tested without one.
//
// Watch takes a file and reports an error when notification is unavailable for
// it. That error is not a failure: it means this join falls back to reading on
// the pane's own output, which is the same trigger the screen tier uses and
// costs exactly nothing on a silent pane.
type transcriptWatch interface {
	Watch(path, key string, onChange func()) error
	Unwatch(path, key string)
}

// transcriptJoin binds one window to one transcript file.
//
// The path is held here, in daemon memory, and deliberately nowhere else. It
// names a project directory and a session, so it is not state to be synced: it
// is absent from WindowState, so it never reaches session.json, and absent from
// the wire protocol, so it never reaches a client. The only thing derived from
// it that leaves the daemon is an AgentState.
type transcriptJoin struct {
	windowID string
	harness  string
	// session is the session the join reads in a store that holds many
	// (harness.Transcript.BySession), "" for a file of one session.
	session string
	// format is the reader the harness's manifest names. Only Claude Code's
	// (harness.ReaderJSONL) gives a turn state. Every other format is joined
	// for the person to read, and its growth raises the transcript event.
	format string
	// doc says the harness rewrites the file whole, so its growth is told by
	// its size and time (docSize, docMod) rather than by an offset.
	doc     bool
	docSize int64
	docMod  time.Time
	// docEventAt is when the last event of a document was raised, and
	// docTimer the event waiting for docEventGap to pass.
	docEventAt time.Time
	docTimer   *time.Timer
	reader     *transcript.Reader
	// exact records that this join came from the harness telling us its own
	// transcript path, rather than from searching. A searched join is given up
	// the moment an exact one arrives for the same window.
	exact bool
	// watched is false when filesystem notification was unavailable for this
	// file, which puts it on the output-driven fallback instead.
	watched bool
	// debounce is the one-shot that coalesces a turn's appends.
	debounce *time.Timer
	// missing counts consecutive reads that found no file. A transcript that has
	// gone is an agent that is not coming back, and the claim is given up rather
	// than left pinning the pane.
	missing int
	// announced is the offset the last transcript event was raised at, so
	// a read that found nothing new raises none.
	announced int64
}

// transcriptMissingLimit is how many consecutive gone-file reads end a join. It
// is more than one because a harness may rotate its file, and the read after a
// rotation finds nothing for an instant.
const transcriptMissingLimit = 3

// agentTranscriptState is the session's join table. It is a plain map behind a
// mutex rather than part of SessionState because none of it is state: it is not
// serialised, not versioned, and not pushed to clients.
type agentTranscriptState struct {
	mu    sync.Mutex
	joins map[string]*transcriptJoin
	// nextTry throttles the hookless search per window.
	nextTry map[string]time.Time
	// viewTry is the backoff of the search for the view-only formats
	// (transcript_search.go).
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
		if prev.reader.Path() == abs {
			// Already joined to this file. Upgrading a searched join to an exact
			// one is worth recording; rejoining is not, because it would throw
			// away the read offset and re-read the tail window.
			prev.exact = prev.exact || exact
			s.transcripts.mu.Unlock()
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
		doc: transcriptview.IsDocument(format, abs),
	}
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

// releaseJoinLocked stops a join's timer and unwatches its file. Called with the
// transcript mutex held.
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

// transcriptWatchKey names one join to the watcher: the session and the
// window, since windows of two sessions may share one file.
func (s *Session) transcriptWatchKey(windowID string) string {
	return s.ID + "/" + windowID
}

// onTranscriptChanged is what the watcher calls. It arms the debounce rather
// than reading, because one turn appends several records and reading each of
// them would parse the same answer several times.
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

// readAgentTranscript reads whatever has been appended and publishes what it
// says, reporting whether it published anything.
func (s *Session) readAgentTranscript(windowID string) bool {
	s.transcripts.mu.Lock()
	j, ok := s.transcripts.joins[windowID]
	s.transcripts.mu.Unlock()
	if !ok {
		return false
	}

	if j.session != "" {
		// A store's session: the harness's reports raise its event.
		return false
	}
	if j.format != harness.ReaderJSONL {
		return s.readViewTranscript(windowID, j)
	}
	obs, fresh, err := j.reader.Read()
	if err != nil {
		// A path that is not a regular file now (a FIFO put in its place)
		// is as good as gone: it is not read, and the join ends.
		if errors.Is(err, transcript.ErrNoFile) || errors.Is(err, transcript.ErrNotRegular) {
			s.transcripts.mu.Lock()
			j.missing++
			gone := j.missing >= transcriptMissingLimit
			s.transcripts.mu.Unlock()
			if gone {
				// The file the claim rests on is gone, so the claim goes with it.
				// Without this the pane would hold whatever it last said against
				// every weaker tier for the rest of its life.
				s.DropAgentTranscript(windowID)
			}
		}
		// Every other error is transient by assumption and silent by policy: a
		// message about it would have to name the file, and the file's name is
		// the user's project and session.
		return false
	}
	s.transcripts.mu.Lock()
	j.missing = 0
	harnessID := j.harness
	s.transcripts.mu.Unlock()
	// The file grew: a reader of the conversation (agent-transcript) can
	// read again. Raised for bookkeeping appends too, since only the
	// reader can tell what they hold.
	s.noteTranscriptGrowth(windowID, j)
	if !fresh {
		// An append that only wrote bookkeeping. A real event with no answer in
		// it, so the pane keeps believing what it already believed.
		return false
	}
	return s.applyTranscriptTurn(windowID, harnessID, obs.Turn)
}

// readViewTranscript reads a join whose format gives no turn state: it
// notes growth for the transcript event, and nothing else. It decodes
// nothing. A JSONL format grows by appends, and the end of its last whole
// line (viewTailOffset) is the cursor of the event, as for Claude Code. A
// document is rewritten whole, so its size and time say it changed, its
// event carries no cursor, and it raises at most one event a second
// (docEventGap).
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

// docEventGap is the least time between two transcript events of a
// document. A harness that rewrites a thread as it streams writes it many
// times a second, and each event sends the reader to read it.
const docEventGap = time.Second

// docEvent raises a document's transcript event now, or once the gap since
// the last one has passed.
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

// viewTailMax bounds how far back viewTailOffset looks for a newline. A last
// line longer than this is not finished as far as the event is concerned.
const viewTailMax = 16 << 20

// viewTailOffset is the offset after the last newline of the file at path:
// the end of its last whole line. It reads back from the end, a chunk at a
// time, and reads none of the lines before the last.
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

// applyTranscriptTurn publishes a turn as an agent state.
//
// The mapping is the narrow part of this whole source, and it is narrow on
// purpose. The records support exactly two honest claims:
//
//   - end_turn means the agent finished and is waiting for the human, which is
//     the same fact the Stop hook reports, read from the file instead.
//   - anything else in the alternation means a turn is in progress.
//
// It does not assert needs_input. The signature for that ("newest record is a
// tool call, no result has landed, and N seconds have passed") is an inference
// rather than a record, it was never validated against a live permission prompt,
// and it is the one state that raises an alert, so a wrong one is worse than a
// missing one. The screen tier already reads that prompt off the pane, and the
// visible-blocker exception lets it say so over this tier's claim.
//
// It does not assert idle either. Silence is not something the file can see.
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

// transcriptFallbackDue reports whether a window's join is on the output-driven
// fallback, so the pane's own output should drive a read.
func (s *Session) transcriptFallbackDue(windowID string) bool {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	j, ok := s.transcripts.joins[windowID]
	return ok && !j.watched
}

// readTranscriptOnOutput is the fallback for a join with no filesystem
// notification behind it. It reads on the pane's own output, throttled and
// settled by the same gates the screen tier uses.
//
// It costs nothing on an idle pane, because a silent pane emits no output. And
// the aim is good: a turn ends immediately after the agent paints its last
// chunk, so the settle scan that catches a blocking prompt catches a finished
// turn in the same pass.
//
// A join the watcher accepted does nothing here, so a machine with working
// notification never reads a file twice.
func (s *Session) readTranscriptOnOutput(ptyID string) {
	winID, _ := s.agentHarnessOf(ptyID)
	if winID == "" || !s.transcriptFallbackDue(winID) {
		return
	}
	s.readAgentTranscript(winID)
}

// hasTranscriptJoin reports whether a window is joined.
func (s *Session) hasTranscriptJoin(windowID string) bool {
	s.transcripts.mu.Lock()
	defer s.transcripts.mu.Unlock()
	_, ok := s.transcripts.joins[windowID]
	return ok
}

// maintainAgentTranscripts keeps the join table matching what the panes are
// running. It rides the agent-detection tick that already exists rather than
// adding one of its own.
//
// It does two things: it drops a join whose window stopped running the harness
// it was joined for, and it looks for a join for a window that has a harness
// with a transcript and no join yet. The search is throttled per window and only
// ever runs for a window that is missing one, so a fully joined session does no
// work here at all.
func (s *Session) maintainAgentTranscripts(reg *harness.Registry, cwdOf func(ptyID string) agentIdentity) {
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

	// A window that is gone, or that stopped running an agent, keeps no join.
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
			// The view-only formats are searched across sessions, by
			// Daemon.searchViewTranscripts.
			continue
		}
		if !s.joinSearchDue(p.windowID) {
			continue
		}
		id := cwdOf(p.ptyID)
		if path, ok := searchTranscript(tr, id); ok {
			_ = s.JoinAgentTranscriptAs(p.windowID, p.harnessID, tr.Reader, path, false)
		}
	}
}

// joinSearchDue reports whether a window may search again, and claims the slot.
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

// agentIdentity is what a searched transcript is checked against: the
// agent's working directory, its build, and when its process started.
type agentIdentity struct {
	cwd, version string
	// started is zero when the start time cannot be read.
	started time.Time
}

// searchTranscript is the hookless join: find the file in the harness's
// directory that belongs to this pane, and refuse when it cannot be sure.
//
// Refusing is the important half. A wrong join attributes one agent's state to
// another pane, which is a confident lie, where no join is merely no state and
// the pane behaves as if this source did not exist. So two Claude Code files
// that both verify means neither is used, and no amount of tie-breaking on
// modification time is applied: whichever was written most recently is not
// evidence of which pane is looking at it.
//
// This fails for two agents in one directory on one version. The hook is the
// answer to that, and this is what runs until someone installs it.
//
// The other formats are searched for by Daemon.searchViewTranscripts.
func searchTranscript(tr *harness.Transcript, id agentIdentity) (string, bool) {
	return searchTranscriptIn(tr.ExpandDir(id.cwd), tr, id.cwd, id.version)
}

// searchTranscriptIn is searchTranscript with the directory already resolved, so
// the verification rules can be tested without a manifest pointing at a real
// home directory.
func searchTranscriptIn(dir string, tr *harness.Transcript, cwd, version string) (string, bool) {
	if dir == "" {
		return "", false
	}
	files, err := filepath.Glob(filepath.Join(dir, tr.Glob))
	if err != nil || len(files) == 0 {
		return "", false
	}
	var found string
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
		if found != "" {
			// Two candidates survived verification. Refuse.
			return "", false
		}
		found = f
	}
	return found, found != ""
}

// paneAgentIdentity reports the working directory and the version string of the
// agent running in a pane, which are what a searched candidate is verified
// against.
//
// The version comes from the resolved executable's own name because that is
// where installers that keep one binary per release put it
// (".../share/claude/versions/2.1.222"), and the same string is a field on every
// record in the file. It is a discriminator that costs a readlink and rules out
// every transcript written by a different build.
func paneAgentIdentity(info foregroundInfo) agentIdentity {
	var id agentIdentity
	if info.pid <= 0 {
		return id
	}
	if target, err := os.Readlink("/proc/" + strconv.Itoa(info.pid) + "/cwd"); err == nil {
		id.cwd = target
	}
	if info.exe != "" {
		id.version = filepath.Base(info.exe)
	}
	id.started, _ = procinfo.StartedAt(info.pid)
	return id
}
