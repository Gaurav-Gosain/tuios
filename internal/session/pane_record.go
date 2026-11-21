package session

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/guestenv"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const (
	recordDefaultMaxBytes = 64 << 20
	recordMaxBytes        = 1 << 30
	maxRecordings         = 16
	recordScrollback      = paneStreamScrollback
	recordFlushEvery      = time.Second
	maxRecordLabel        = 200
)

// ErrVerbNoRecording is the code of stop-recording or mark-recording on a
// pane that is not being recorded.
const ErrVerbNoRecording = "no_recording"

type recordings struct {
	mu sync.Mutex
	m  map[string]*paneRecorder
}

type paneRecorder struct {
	d      *Daemon
	pty    *PTY
	window string
	path   string
	subID  string
	sub    *ptySubscriber
	pos    int64
	cols   int
	rows   int
	snap   *TerminalState

	f    *os.File
	w    *bufio.Writer
	t0   time.Time
	last float64
	held []byte

	maxBytes int64
	bytes    int64
	events   int
	started  time.Time

	marks  chan string
	stop   chan string
	done   chan struct{}
	reason string
	err    error
}

func (r *paneRecorder) summary() map[string]any {
	out := map[string]any{
		"path":       r.path,
		"window":     r.window,
		"started_at": r.started.UTC().Format(time.RFC3339),
	}
	select {
	case <-r.done:
		out["events"] = r.events
		out["bytes"] = r.bytes
		out["seconds"] = r.last
		out["reason"] = r.reason
	default:
	}
	return out
}

func (d *Daemon) verbRecordPane(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session    string `json:"session"`
		Window     string `json:"window"`
		Out        string `json:"out"`
		Overwrite  bool   `json:"overwrite"`
		DurationMS int64  `json:"duration_ms"`
		MaxBytes   int64  `json:"max_bytes"`
		Title      string `json:"title"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.Out == "" || p.Out == "~" || hasControl(p.Out) {
		return nil, invalidParam("out", "record-pane needs out: the absolute path of the .cast file to write, or one that starts with ~.")
	}
	out, verr := expandPath(p.Out)
	if verr != nil {
		return nil, invalidParam("out", verr.Message)
	}
	p.Out = out
	if p.DurationMS < 0 {
		return nil, invalidParam("duration_ms", "duration_ms must be 0 (until stopped) or more.")
	}
	maxBytes := p.MaxBytes
	if maxBytes == 0 {
		maxBytes = recordDefaultMaxBytes
	}
	if maxBytes < 4096 || maxBytes > recordMaxBytes {
		return nil, invalidParam("max_bytes", "max_bytes must be between 4096 and "+strconv.Itoa(recordMaxBytes)+".")
	}
	if len(p.Title) > maxRecordLabel || hasControl(p.Title) {
		return nil, invalidParam("title", "title must be one line of at most "+strconv.Itoa(maxRecordLabel)+" bytes.")
	}
	sess, verr := d.resolveVerbSession(cs, p.Session)
	if verr != nil {
		return nil, verr
	}
	win, pty, verr := d.recordTarget(sess, p.Window)
	if verr != nil {
		return nil, verr
	}

	fsys, verr := d.linkWriteFS(cs, p.Out)
	if verr != nil {
		return nil, verr
	}
	defer fsys.Close()

	d.recordings.mu.Lock()
	defer d.recordings.mu.Unlock()
	if d.recordings.m == nil {
		d.recordings.m = map[string]*paneRecorder{}
	}
	if cur, ok := d.recordings.m[pty.ID]; ok {
		return nil, hintedVerbError(ErrVerbBusy, "record-pane: the pane is already being recorded to "+echoName(cur.path), &VerbHint{
			Detail: "Nothing changed. Stop that recording with stop-recording first.",
		})
	}
	if len(d.recordings.m) >= maxRecordings {
		return nil, newVerbError(ErrVerbBusy, "record-pane: "+strconv.Itoa(maxRecordings)+" recordings run already. Stop one first")
	}
	if !p.Overwrite {
		if _, err := fsys.Lstat(p.Out); err == nil {
			return nil, hintedVerbError(ErrVerbFileExists, "record-pane: "+echoName(p.Out)+" already exists", &VerbHint{
				Param: "overwrite", Detail: "Nothing was written. Name another file, or set overwrite to replace it.",
			})
		}
	}
	f, err := openPart(fsys, p.Out)
	if err != nil {
		return nil, fileError("record-pane", p.Out, err)
	}
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return nil, fileError("record-pane", p.Out, err)
	}

	r := &paneRecorder{
		d: d, pty: pty, window: win.ID, path: p.Out, f: f, w: bufio.NewWriterSize(f, 64<<10),
		subID:    "record:" + pty.ID,
		maxBytes: maxBytes,
		marks:    make(chan string, 64),
		stop:     make(chan string, 1),
		done:     make(chan struct{}),
	}
	if verr := r.subscribe(); verr != nil {
		_ = f.Close()
		_ = os.Remove(p.Out)
		return nil, verr
	}
	r.t0, r.started = time.Now(), time.Now()
	term := guestenv.FallbackTerm
	if sess.config != nil {
		term = guestenv.PaneTerm(sess.config.Term)
	}
	header := map[string]any{
		"version":   2,
		"width":     r.cols,
		"height":    r.rows,
		"timestamp": r.started.Unix(),
		"env":       map[string]string{"TERM": term, "SHELL": sess.getShell()},
	}
	if p.Title != "" {
		header["title"] = p.Title
	}
	err = r.writeLine(header)
	if err == nil {
		err = r.writeSnap(false)
	}
	if err == nil {
		err = r.w.Flush()
	}
	if err != nil {
		r.pty.unsubscribeSub(r.subID, r.sub)
		_ = f.Close()
		return nil, fileError("record-pane", p.Out, err)
	}
	d.recordings.m[pty.ID] = r
	pty.recording.Store(true)
	go r.run(time.Duration(p.DurationMS) * time.Millisecond)
	LogBasic("Recording pane %s to %s", shortWindowID(win.ID), p.Out)
	return map[string]any{
		"type":       "recording",
		"path":       p.Out,
		"window":     win.ID,
		"width":      r.cols,
		"height":     r.rows,
		"started_at": r.started.UTC().Format(time.RFC3339),
	}, nil
}

func (d *Daemon) recordTarget(sess *Session, target string) (WindowState, *PTY, *verbError) {
	state := sess.GetState()
	if target == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return WindowState{}, nil, mapResolveErr(err, sess)
		}
		target = id
	}
	idx, err := findWindowStateIndex(state.Windows, target)
	if err != nil {
		return WindowState{}, nil, mapResolveErr(err, sess)
	}
	pty, err := d.resolvePTYForTarget(sess, state.Windows[idx].ID)
	if err != nil {
		return WindowState{}, nil, mapResolveErr(err, sess)
	}
	return state.Windows[idx], pty, nil
}

func (d *Daemon) recorderFor(cs *connState, session, target, verb string) (*paneRecorder, *verbError) {
	sess, verr := d.resolveVerbSession(cs, session)
	if verr != nil {
		return nil, verr
	}
	_, pty, verr := d.recordTarget(sess, target)
	if verr != nil {
		return nil, verr
	}
	d.recordings.mu.Lock()
	r := d.recordings.m[pty.ID]
	d.recordings.mu.Unlock()
	if r == nil {
		return nil, hintedVerbError(ErrVerbNoRecording, verb+": the pane is not being recorded", &VerbHint{
			Detail: "Nothing changed. list-recordings shows the recordings that run.",
		})
	}
	return r, nil
}

func (d *Daemon) verbStopRecording(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	r, verr := d.recorderFor(cs, p.Session, p.Window, "stop-recording")
	if verr != nil {
		return nil, verr
	}
	r.end("stopped")
	<-r.done
	out := r.summary()
	out["type"] = "recording_stopped"
	return out, nil
}

func (d *Daemon) verbMarkRecording(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		Label   string `json:"label"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if len(p.Label) > maxRecordLabel || hasControl(p.Label) {
		return nil, invalidParam("label", "label must be one line of at most "+strconv.Itoa(maxRecordLabel)+" bytes.")
	}
	r, verr := d.recorderFor(cs, p.Session, p.Window, "mark-recording")
	if verr != nil {
		return nil, verr
	}
	select {
	case r.marks <- p.Label:
	case <-r.done:
		return nil, newVerbError(ErrVerbNoRecording, "mark-recording: the recording ended before the mark")
	default:
		return nil, newVerbError(ErrVerbBusy, "mark-recording: too many marks wait to be written. Try again")
	}
	return map[string]any{"type": "recording_marked", "path": r.path, "window": r.window, "label": p.Label}, nil
}

func (d *Daemon) verbListRecordings(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct{}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	d.recordings.mu.Lock()
	list := make([]map[string]any, 0, len(d.recordings.m))
	for _, r := range d.recordings.m {
		list = append(list, r.summary())
	}
	d.recordings.mu.Unlock()
	slices.SortFunc(list, func(a, b map[string]any) int {
		return cmp.Compare(a["path"].(string), b["path"].(string))
	})
	return map[string]any{"type": "recordings", "recordings": list}, nil
}

func (d *Daemon) stopRecordings(reason string) {
	d.recordings.mu.Lock()
	all := make([]*paneRecorder, 0, len(d.recordings.m))
	for _, r := range d.recordings.m {
		all = append(all, r)
	}
	d.recordings.mu.Unlock()
	for _, r := range all {
		r.end(reason)
	}
	for _, r := range all {
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			LogBasic("Recording %s did not close in time", r.path)
		}
	}
}

func (r *paneRecorder) end(reason string) {
	select {
	case r.stop <- reason:
	default:
	}
}

func (r *paneRecorder) subscribe() *verbError {
	for attempt := 0; ; attempt++ {
		st := r.pty.GetTerminalState(recordScrollback, 0)
		if st == nil {
			return newVerbError(ErrVerbInternal, "record-pane: the pane has no terminal to record")
		}
		sub, ok := r.pty.subscribeAt(r.subID, st.Seq, true)
		if !ok && attempt >= 20 {
			sub, ok = r.pty.subscribeSub(r.subID, st.Seq, true), true
		}
		if ok && sub != nil {
			sub.noPace.Store(true)
			r.sub, r.snap = sub, st
			r.pos = st.Seq
			r.cols, r.rows = st.Width, st.Height
			return nil
		}
		if r.pty.ctx.Err() != nil {
			return newVerbError(ErrVerbUnknownPane, "record-pane: the pane closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *paneRecorder) writeSnap(resync bool) error {
	st := r.snap
	r.snap = nil
	body := SnapshotVT(st)
	if resync {
		if err := r.event("m", "resync"); err != nil {
			return err
		}
		if err := r.event("r", strconv.Itoa(st.Width)+"x"+strconv.Itoa(st.Height)); err != nil {
			return err
		}
		body = append([]byte("\x1bc"), body...)
	}
	r.held = nil
	return r.output(body)
}

func (r *paneRecorder) run(duration time.Duration) {
	r.publish()
	reason := r.loop(duration)
	r.finish(reason)
	r.publish()
}

func (r *paneRecorder) publish() {
	if s := r.pty.owner(); s != nil {
		s.PublishLiveFacts()
	}
}

func (s *Session) recordedPanes() map[string]bool {
	s.ptysMu.RLock()
	defer s.ptysMu.RUnlock()
	var out map[string]bool
	for id, p := range s.ptys {
		if p.recording.Load() {
			if out == nil {
				out = make(map[string]bool)
			}
			out[id] = true
		}
	}
	return out
}

func (r *paneRecorder) loop(duration time.Duration) string {
	tick := time.NewTicker(recordFlushEvery)
	defer tick.Stop()
	var limit <-chan time.Time
	if duration > 0 {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		limit = timer.C
	}
	quiet := 0
	for {
		select {
		case reason := <-r.stop:
			r.drain()
			if reason != "stopped" {
				_ = r.event("m", reason)
			}
			return reason
		case <-limit:
			r.drain()
			return "duration"
		case label := <-r.marks:
			if err := r.event("m", label); err != nil {
				return r.failed(err)
			}
		case <-tick.C:
			if err := r.w.Flush(); err != nil {
				return r.failed(err)
			}
			r.sub.out.trim()
			if r.pty.IsExited() && r.sub.out.len() == 0 {
				if quiet++; quiet >= 2 {
					return r.exitReason()
				}
			}
		case <-r.sub.out.wake:
			quiet = 0
			closed, err := r.takeAll()
			if err != nil {
				return r.failed(err)
			}
			if closed {
				return r.exitReason()
			}
		}
		if r.bytes >= r.maxBytes {
			_ = r.event("m", "size limit")
			return "size limit"
		}
		if r.sub.gapped.Load() && r.sub.out.len() == 0 {
			if err := r.recoverGap(); err != nil {
				return r.failed(err)
			}
		}
	}
}

func (r *paneRecorder) drain() {
	_, _ = r.takeAll()
}

func (r *paneRecorder) takeAll() (closed bool, err error) {
	for {
		c, ok, isClosed := r.sub.out.pop()
		if isClosed {
			return true, nil
		}
		if !ok {
			return false, nil
		}
		if err := r.take(c); err != nil {
			return false, err
		}
	}
}

func (r *paneRecorder) take(c ptyChunk) error {
	if c.isResize() {
		if c.width == r.cols && c.height == r.rows {
			return nil
		}
		r.cols, r.rows = c.width, c.height
		return r.event("r", strconv.Itoa(c.width)+"x"+strconv.Itoa(c.height))
	}
	data := takeChunk(nil, c, r.sub)
	r.pty.wakePacer()
	end := c.end
	if c.frame != nil {
		end = c.frame.end
	}
	if end == 0 {
		end = r.pos + int64(len(data))
	}
	if end <= r.pos {
		return nil
	}
	if start := end - int64(len(data)); start < r.pos {
		data = data[r.pos-start:]
	}
	r.pos = end
	return r.output(data)
}

func (r *paneRecorder) output(data []byte) error {
	if len(r.held) > 0 {
		data = append(r.held, data...)
		r.held = nil
	}
	cut := incompleteTail(data)
	if cut > 0 {
		r.held = append([]byte(nil), data[len(data)-cut:]...)
		data = data[:len(data)-cut]
	}
	if len(data) == 0 {
		return nil
	}
	return r.event("o", string(data))
}

func incompleteTail(b []byte) int {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return 0
		}
		if c >= 0xC0 {
			if utf8.FullRune(b[len(b)-i:]) {
				return 0
			}
			return i
		}
	}
	return 0
}

func (r *paneRecorder) event(code, data string) error {
	t := time.Since(r.t0).Seconds()
	if r.events == 0 {
		t = 0
	}
	if t < r.last {
		t = r.last
	}
	r.last = t
	if err := r.writeLine([]any{json.Number(strconv.FormatFloat(t, 'f', 6, 64)), code, data}); err != nil {
		return err
	}
	r.events++
	return nil
}

func (r *paneRecorder) writeLine(v any) error {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := r.w.Write(b)
	r.bytes += int64(n)
	return err
}

func (r *paneRecorder) recoverGap() error {
	r.pty.unsubscribeSub(r.subID, r.sub)
	if sub, ok := r.pty.subscribeAt(r.subID, r.pos, false); ok {
		sub.noPace.Store(true)
		r.sub = sub
		return nil
	}
	if verr := r.subscribe(); verr != nil {
		return errors.New(verr.Message)
	}
	return r.writeSnap(true)
}

func (r *paneRecorder) failed(err error) string {
	r.err = err
	return "write failed"
}

func (r *paneRecorder) exitReason() string {
	if r.pty.ctx.Err() != nil {
		return "closed"
	}
	if code, exited := r.pty.ExitStatus(); exited {
		if sig := r.pty.ExitSignal(); sig > 0 {
			return "killed " + strconv.Itoa(sig)
		}
		return "exited " + strconv.Itoa(code)
	}
	return "closed"
}

func (r *paneRecorder) finish(reason string) {
	if len(r.held) > 0 {
		held := r.held
		r.held = nil
		_ = r.event("o", string(held))
	}
	err := r.w.Flush()
	if cerr := r.f.Close(); err == nil {
		err = cerr
	}
	if err != nil && r.err == nil {
		r.err = err
		reason = "write failed"
	}
	r.pty.unsubscribeSub(r.subID, r.sub)
	r.d.recordings.mu.Lock()
	if r.d.recordings.m[r.pty.ID] == r {
		delete(r.d.recordings.m, r.pty.ID)
		r.pty.recording.Store(false)
	}
	r.d.recordings.mu.Unlock()
	r.reason = reason
	if r.err != nil && !errors.Is(r.err, fs.ErrClosed) {
		LogBasic("Recording of pane %s to %s ended: %s: %v", shortWindowID(r.window), r.path, reason, r.err)
	} else {
		LogBasic("Recording of pane %s to %s ended: %s", shortWindowID(r.window), r.path, reason)
	}
	close(r.done)
}

func recordVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"record-pane": {
			scope:       scopeDeny,
			link:        needFiles,
			description: "Record a pane to an asciicast v2 file (.cast) that asciinema, agg and reel play. The first event paints the screen the pane shows now, its history included; then every output chunk and resize is an event at its real time. Input is not recorded. The recording runs until stop-recording, duration_ms, max_bytes or the pane's end. The file holds everything the pane prints while it runs.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "out", Type: "string", Required: true, Description: "The path of the file to write, absolute or starting with ~, made with mode 0600. Over a link, held to the file verbs' write rules."},
				{Name: "overwrite", Type: "bool", Description: "Replace out when it exists. Without it, an existing file is refused with file_exists.", Default: "false"},
				{Name: "duration_ms", Type: "int", Description: "Stop after this long. 0 records until stopped.", Default: "0"},
				{Name: "max_bytes", Type: "int", Description: "Stop when the file reaches this size, with a size limit marker. Between 4096 and 1073741824.", Default: "67108864"},
				{Name: "title", Type: "string", Description: "The title the header carries, which players show."},
			},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "The file being written."},
				{Name: "window", Type: "string", Description: "The id of the pane recorded."},
				{Name: "width", Type: "int", Description: "The pane's width when the recording started, the header's width."},
				{Name: "height", Type: "int", Description: "The pane's height when the recording started, the header's height."},
				{Name: "started_at", Type: "string", Description: "When the recording started, RFC 3339 in UTC."},
			},
			examples: []string{`{"id":1,"verb":"record-pane","params":{"session":"work","window":"build","out":"~/build.cast"}}`},
			handler:  (*Daemon).verbRecordPane,
		},
		"stop-recording": {
			scope:       scopeDeny,
			link:        needFiles,
			description: "Stop a pane's recording. Answers once the file is closed.",
			params:      []verbParam{sessionParam, windowParam},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "The file written."},
				{Name: "window", Type: "string", Description: "The id of the pane recorded."},
				{Name: "events", Type: "int", Description: "How many events the file holds, the snapshot included."},
				{Name: "bytes", Type: "int", Description: "The size of the file."},
				{Name: "seconds", Type: "float", Description: "The time of the last event."},
				{Name: "reason", Type: "string", Description: "Why the recording ended: stopped here, or what ended it first."},
				{Name: "started_at", Type: "string", Description: "When the recording started, RFC 3339 in UTC."},
			},
			examples: []string{`{"id":1,"verb":"stop-recording","params":{"session":"work","window":"build"}}`},
			handler:  (*Daemon).verbStopRecording,
		},
		"mark-recording": {
			scope:       scopeSelf,
			link:        needFiles,
			description: "Write a marker (an m event) into a pane's recording at the current time, so a player can jump to the moment.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "label", Type: "string", Description: "The marker's label, one line of at most 200 bytes."},
			},
			returns: []verbParam{
				{Name: "path", Type: "string", Description: "The file being written."},
				{Name: "window", Type: "string", Description: "The id of the pane recorded."},
				{Name: "label", Type: "string", Description: "The label written."},
			},
			examples: []string{`{"id":1,"verb":"mark-recording","params":{"session":"work","window":"build","label":"tests start"}}`},
			handler:  (*Daemon).verbMarkRecording,
		},
		"list-recordings": {
			scope:       scopeGlobal,
			link:        needList,
			description: "List the recordings that run now, across every session.",
			params:      []verbParam{},
			returns: []verbParam{
				{Name: "recordings", Type: "[]object", Description: "One entry per recording: path, window, started_at."},
			},
			examples: []string{`{"id":1,"verb":"list-recordings","params":{}}`},
			handler:  (*Daemon).verbListRecordings,
		},
	}
}
