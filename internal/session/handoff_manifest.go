package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
)

// The handoff manifest: what one daemon image writes down for the image it
// execs, on a descriptor the exec keeps. See docs/design/live-handoff.md,
// section 2.5.
//
// It is a magic string and a run of framed records, each a kind byte, a
// big-endian length and the payload, so it is written and read one record at a
// time and no whole-manifest buffer exists on either side. The header record
// comes first and the end record last; a manifest without its end was cut
// short and is refused.

// handoffMagic opens every manifest.
const handoffMagic = "TUIOSHO\n"

// The kinds of record.
const (
	handoffRecHeader  = 'H'
	handoffRecSession = 'S'
	handoffRecPane    = 'P'
	handoffRecDaemon  = 'D'
	handoffRecEnd     = 'E'
)

// handoffMaxRecord bounds one record. The largest is a pane with its history,
// a few megabytes at the depths a handoff carries.
const handoffMaxRecord = 256 << 20

// handoffHeader says who wrote the manifest and what it holds.
type handoffHeader struct {
	Version  int    `json:"version"`
	Writer   string `json:"writer"`
	Backend  string `json:"backend"`
	Sessions int    `json:"sessions"`
	Panes    int    `json:"panes"`
}

// handoffSession is one session as the next image rebuilds it: its state as
// it stands, and what the state does not hold.
type handoffSession struct {
	State    *SessionState `json:"state"`
	ID       string        `json:"id"`
	StartDir string        `json:"start_dir,omitempty"`
	// WindowStartDirs is each window's start folder, by window id.
	WindowStartDirs map[string]string `json:"window_start_dirs,omitempty"`
	LastUsed        int64             `json:"last_used,omitempty"`
}

// handoffPane is one pane: the descriptor of its master, which the exec keeps,
// its process, and what the next image needs to read and size it.
type handoffPane struct {
	ID      string
	Session string
	Window  string
	FD      int
	PID     int
	TTY     string
	Width   int
	Height  int
	SpawnW  int
	SpawnH  int
	// Exited is a pane whose process this image reaped before the reap gates
	// shut. Its status is here, and there is no process to wait on.
	Exited     bool
	ExitCode   int
	ExitSignal int
	KeepOpen   bool
	// State is the pane's emulator, packed, with handoffHistoryRows of its
	// history and the character cluster it left open.
	State *TerminalState
	// Live is what the snapshot does not hold.
	Live handoffLive
}

// handoffDaemon is the daemon's own record.
type handoffDaemon struct {
	SocketPath string `json:"socket_path"`
	// Listeners is the descriptor of each listening socket, by name.
	Listeners map[string]int `json:"listeners"`
	LockFD    int            `json:"lock_fd"`
	// PaneTokenKey signs every pane's TUIOS_PANE_TOKEN. Without it a token
	// stops verifying at the handoff, and a process outside its pane's tree
	// can no longer prove which pane it is.
	PaneTokenKey []byte        `json:"pane_token_key,omitempty"`
	Events       handoffEvents `json:"events"`
	Mail         handoffMail   `json:"mail"`
	// Fallback is the binary the writing image runs, kept for the new image
	// to re-exec when it cannot adopt the panes. See handoff_prev.go.
	Fallback string       `json:"fallback"`
	Stats    HandoffStats `json:"stats"`
}

// handoffManifest is a manifest as read.
type handoffManifest struct {
	Header   handoffHeader
	Sessions []handoffSession
	Panes    []handoffPane
	Daemon   handoffDaemon
	// Size is how many bytes the manifest took.
	Size int64
}

// manifestWriter writes records to w.
type manifestWriter struct {
	w *bufio.Writer
	n int64
}

func newManifestWriter(w io.Writer) (*manifestWriter, error) {
	mw := &manifestWriter{w: bufio.NewWriterSize(w, 256<<10)}
	if _, err := mw.w.WriteString(handoffMagic); err != nil {
		return nil, err
	}
	mw.n = int64(len(handoffMagic))
	return mw, nil
}

func (mw *manifestWriter) record(kind byte, payload []byte) error {
	if len(payload) > handoffMaxRecord {
		return fmt.Errorf("a handoff record of %d bytes is over the limit", len(payload))
	}
	var hdr [5]byte
	hdr[0] = kind
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload))) //nolint:gosec // bounded above
	if _, err := mw.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := mw.w.Write(payload); err != nil {
		return err
	}
	mw.n += int64(len(hdr) + len(payload))
	return nil
}

func (mw *manifestWriter) json(kind byte, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return mw.record(kind, data)
}

// encodeGob is v as one gob record.
func encodeGob(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// finish writes the end record and flushes. It returns the manifest's size.
func (mw *manifestWriter) finish() (int64, error) {
	if err := mw.record(handoffRecEnd, nil); err != nil {
		return 0, err
	}
	return mw.n, mw.w.Flush()
}

// readHandoffManifest reads a whole manifest from r.
func readHandoffManifest(r io.Reader) (*handoffManifest, error) {
	var read atomic.Int64
	br := bufio.NewReaderSize(&countingReader{r: r, n: &read}, 256<<10)
	magic := make([]byte, len(handoffMagic))
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != handoffMagic {
		return nil, errors.New("not a handoff manifest")
	}
	m := &handoffManifest{}
	first := true
	for {
		var hdr [5]byte
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return nil, fmt.Errorf("the handoff manifest ends early: %w", err)
		}
		size := binary.BigEndian.Uint32(hdr[1:])
		if size > handoffMaxRecord {
			return nil, fmt.Errorf("a handoff record claims %d bytes", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, fmt.Errorf("the handoff manifest ends inside a record: %w", err)
		}
		kind := hdr[0]
		if first != (kind == handoffRecHeader) {
			return nil, errors.New("the handoff manifest does not start with its header")
		}
		first = false
		var err error
		switch kind {
		case handoffRecHeader:
			err = json.Unmarshal(payload, &m.Header)
			if err == nil {
				err = checkHandoffVersion(m.Header.Version)
			}
		case handoffRecSession:
			var s handoffSession
			if err = json.Unmarshal(payload, &s); err == nil {
				if s.State == nil {
					err = errors.New("a session record has no state")
				}
				m.Sessions = append(m.Sessions, s)
			}
		case handoffRecPane:
			var p handoffPane
			if err = gob.NewDecoder(bytes.NewReader(payload)).Decode(&p); err == nil {
				m.Panes = append(m.Panes, p)
			}
		case handoffRecDaemon:
			err = json.Unmarshal(payload, &m.Daemon)
		case handoffRecEnd:
			m.Size = read.Load() - int64(br.Buffered())
			return m, nil
		default:
			err = fmt.Errorf("unknown handoff record %q", kind)
		}
		if err != nil {
			return nil, fmt.Errorf("handoff record %q: %w", kind, err)
		}
	}
}

// checkHandoffVersion refuses a manifest this build cannot read.
func checkHandoffVersion(v int) error {
	if v < MinHandoffVersion || v > HandoffVersion {
		return fmt.Errorf("handoff manifest version %d, this build reads %d to %d", v, MinHandoffVersion, HandoffVersion)
	}
	return nil
}
