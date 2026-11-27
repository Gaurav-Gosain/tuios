package session

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// The mail archive: direct mail the ring dropped, kept on disk.
//
// A session's ring holds the newest mail (agentMailboxMaxMessages). A lead
// that looked back past it had only its own transcript, and a reply to an
// evicted message lost its thread. When [agents.mail] archive is on (the
// default), each direct message the ring evicts is written as one JSON line
// to the state folder, mail/SESSION.jsonl, mode 0600. The file is cut at
// mailArchiveMaxBytes and its older half kept as SESSION.jsonl.1, so one
// session holds at most twice that on disk. Both go when the session ends.
// read-agent-messages --archive reads them, and marks nothing.
//
// Mail text is kept on disk while the session lives, where the ring keeps it
// only in memory. That is the trade the config key exists for.
//
// Writes happen off the send path: an eviction queues the line, and a writer
// goroutine runs only while something is queued. With nothing evicted,
// nothing runs.

// mailArchiveMaxBytes is where a session's archive file is cut.
const mailArchiveMaxBytes = 4 << 20

// mailArchiveThreads bounds the per-session index of evicted ids to their
// thread, which lets a reply to an evicted message join its thread.
const mailArchiveThreads = 1024

type mailArchive struct {
	mu      sync.Mutex
	on      bool
	pending []archiveLine
	writing bool
	// wmu orders the writes of the writer goroutine against a session's
	// files going away or moving.
	wmu sync.Mutex
	// dir overrides the folder, for tests.
	dir string
}

type archiveLine struct {
	session string
	line    []byte
}

func (a *mailArchive) folder() string {
	if a.dir != "" {
		return a.dir
	}
	return filepath.Join(getResurrectionDir(), "mail")
}

func (a *mailArchive) path(session string) (string, error) {
	return stateJoin(a.folder(), session, ".jsonl")
}

func (a *mailArchive) setOn(on bool) {
	a.mu.Lock()
	a.on = on
	if !on {
		a.pending = nil
	}
	a.mu.Unlock()
}

func (a *mailArchive) isOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.on
}

// add queues the direct mail of gone for session's archive.
func (a *mailArchive) add(session string, gone []*AgentMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.on {
		return
	}
	for _, m := range gone {
		if m.Kind != agentMsgDirect {
			continue
		}
		line, err := jsonx.Marshal(m)
		if err != nil {
			continue
		}
		a.pending = append(a.pending, archiveLine{session: session, line: line})
	}
	if len(a.pending) > 0 && !a.writing {
		a.writing = true
		go a.drain()
	}
}

// drain writes what is queued and ends when nothing is.
func (a *mailArchive) drain() {
	for {
		a.mu.Lock()
		batch := a.pending
		a.pending = nil
		if len(batch) == 0 {
			a.writing = false
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		a.wmu.Lock()
		for len(batch) > 0 {
			session := batch[0].session
			var lines [][]byte
			rest := batch[:0:0]
			for _, l := range batch {
				if l.session == session {
					lines = append(lines, l.line)
				} else {
					rest = append(rest, l)
				}
			}
			if err := a.write(session, lines); err != nil {
				log.Printf("Mail archive: session %s: %v", session, err)
			}
			batch = rest
		}
		a.wmu.Unlock()
	}
}

// write appends lines to the session's file. Before a line that would pass
// the cap, the file moves to .1 and a new file starts, so one large batch
// cannot grow a file past the cap. Callers hold wmu.
func (a *mailArchive) write(session string, lines [][]byte) error {
	path, err := a.path(session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	var buf bytes.Buffer
	for len(lines) > 0 {
		buf.Reset()
		for len(lines) > 0 {
			next := size + int64(buf.Len()+len(lines[0])+1)
			if next > mailArchiveMaxBytes && size+int64(buf.Len()) > 0 {
				break
			}
			buf.Write(lines[0])
			buf.WriteByte('\n')
			lines = lines[1:]
		}
		if buf.Len() > 0 {
			if err := appendFile(path, buf.Bytes()); err != nil {
				return err
			}
		}
		if len(lines) > 0 {
			if err := os.Rename(path, path+".1"); err != nil {
				return err
			}
			size = 0
		}
	}
	return nil
}

func appendFile(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// forget removes the session's files and what is queued for it.
func (a *mailArchive) forget(session string) {
	a.mu.Lock()
	a.pending = slices.DeleteFunc(a.pending, func(l archiveLine) bool { return l.session == session })
	a.mu.Unlock()
	a.wmu.Lock()
	defer a.wmu.Unlock()
	path, err := a.path(session)
	if err != nil {
		return
	}
	for _, p := range []string{path, path + ".1"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("Mail archive: remove %s: %v", p, err)
		}
	}
}

// rename moves the session's files to its new name. What is queued under the
// old name is written under the new one.
func (a *mailArchive) rename(old, name string) {
	a.mu.Lock()
	for i := range a.pending {
		if a.pending[i].session == old {
			a.pending[i].session = name
		}
	}
	a.mu.Unlock()
	a.wmu.Lock()
	defer a.wmu.Unlock()
	from, err1 := a.path(old)
	to, err2 := a.path(name)
	if err1 != nil || err2 != nil {
		return
	}
	for _, sfx := range []string{"", ".1"} {
		if err := os.Rename(from+sfx, to+sfx); err != nil && !errors.Is(err, fs.ErrNotExist) {
			log.Printf("Mail archive: rename %s: %v", from+sfx, err)
		}
	}
}

// read is the archived mail of session that keep accepts, oldest first, at
// most limit of the newest. It reads the files on disk, so it is not called
// with the bus lock held.
func (a *mailArchive) read(session string, limit int, keep func(*AgentMessage) bool) ([]AgentMessage, int, error) {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	path, err := a.path(session)
	if err != nil {
		return nil, 0, err
	}
	var out []AgentMessage
	for _, p := range []string{path + ".1", path} {
		f, err := os.Open(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), agentMsgMaxText*8)
		for sc.Scan() {
			var m AgentMessage
			if jsonx.Unmarshal(sc.Bytes(), &m) != nil {
				continue
			}
			if keep(&m) {
				out = append(out, m)
			}
		}
		err = sc.Err()
		_ = f.Close()
		if err != nil {
			return nil, 0, err
		}
	}
	total := len(out)
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, total, nil
}

// noteEvictedThreads keeps the thread of each evicted message, so a reply to
// it joins its thread (threadFor). The oldest go past mailArchiveThreads.
func (mb *agentMailbox) noteEvictedThreads(gone []*AgentMessage) {
	for _, m := range gone {
		if mb.evictedThread == nil {
			mb.evictedThread = map[uint64]uint64{}
		}
		mb.evictedThread[m.ID] = m.ThreadID
		mb.evictedOrder = append(mb.evictedOrder, m.ID)
	}
	for len(mb.evictedOrder) > mailArchiveThreads {
		delete(mb.evictedThread, mb.evictedOrder[0])
		mb.evictedOrder = mb.evictedOrder[1:]
	}
}

// SetMailArchive applies [agents.mail] archive.
func (d *Daemon) SetMailArchive(on bool) {
	d.agents.archive.setOn(on)
}
