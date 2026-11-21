package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"io"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const handoffMagic = "TUIOSHO\n"

const (
	handoffRecHeader  = 'H'
	handoffRecSession = 'S'
	handoffRecPane    = 'P'
	handoffRecDaemon  = 'D'
	handoffRecEnd     = 'E'
)

const handoffMaxRecord = 256 << 20

type handoffHeader struct {
	Version  int    `json:"version"`
	Writer   string `json:"writer"`
	Backend  string `json:"backend"`
	Sessions int    `json:"sessions"`
	Panes    int    `json:"panes"`
}

type handoffSession struct {
	State           *SessionState     `json:"state"`
	ID              string            `json:"id"`
	StartDir        string            `json:"start_dir,omitempty"`
	WindowStartDirs map[string]string `json:"window_start_dirs,omitempty"`
	LastUsed        int64             `json:"last_used,omitempty"`
}

type handoffPane struct {
	ID         string
	Session    string
	Window     string
	FD         int
	PID        int
	TTY        string
	Width      int
	Height     int
	SpawnW     int
	SpawnH     int
	Exited     bool
	ExitCode   int
	ExitSignal int
	KeepOpen   bool
	State      *TerminalState
	Live       handoffLive
}

type handoffDaemon struct {
	SocketPath   string              `json:"socket_path"`
	Listeners    map[string]int      `json:"listeners"`
	LockFD       int                 `json:"lock_fd"`
	PaneTokenKey []byte              `json:"pane_token_key,omitempty"`
	Events       handoffEvents       `json:"events"`
	Mail         handoffMail         `json:"mail"`
	Fallback     string              `json:"fallback"`
	FallbackFD   int                 `json:"fallback_fd"`
	Keeper       handoffKeeperRecord `json:"keeper"`
	Stats        HandoffStats        `json:"stats"`
	FarHolds     []handoffFarHold    `json:"far_holds,omitempty"`
}

type handoffFarHold struct {
	Window  string `json:"window"`
	Host    string `json:"host"`
	Session string `json:"session"`
	ID      string `json:"id"`
}

type handoffManifest struct {
	Header   handoffHeader
	Sessions []handoffSession
	Panes    []handoffPane
	Daemon   handoffDaemon
	Size     int64
}

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
		return errfmt.Errorf("a handoff record of %d bytes is over the limit", len(payload))
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
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	return mw.record(kind, data)
}

func encodeGob(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (mw *manifestWriter) finish() (int64, error) {
	if err := mw.record(handoffRecEnd, nil); err != nil {
		return 0, err
	}
	return mw.n, mw.w.Flush()
}

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
			return nil, errfmt.Errorf("the handoff manifest ends early: %w", err)
		}
		size := binary.BigEndian.Uint32(hdr[1:])
		if size > handoffMaxRecord {
			return nil, errfmt.Errorf("a handoff record claims %d bytes", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return nil, errfmt.Errorf("the handoff manifest ends inside a record: %w", err)
		}
		kind := hdr[0]
		if first != (kind == handoffRecHeader) {
			return nil, errors.New("the handoff manifest does not start with its header")
		}
		first = false
		var err error
		switch kind {
		case handoffRecHeader:
			err = jsonx.Unmarshal(payload, &m.Header)
			if err == nil {
				err = checkHandoffVersion(m.Header.Version)
			}
		case handoffRecSession:
			var s handoffSession
			if err = jsonx.Unmarshal(payload, &s); err == nil {
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
			m.Daemon.FallbackFD = -1
			err = jsonx.Unmarshal(payload, &m.Daemon)
		case handoffRecEnd:
			m.Size = read.Load() - int64(br.Buffered())
			return m, nil
		default:
			err = errfmt.Errorf("unknown handoff record %q", kind)
		}
		if err != nil {
			return nil, errfmt.Errorf("handoff record %q: %w", kind, err)
		}
	}
}

func checkHandoffVersion(v int) error {
	if v < MinHandoffVersion || v > HandoffVersion {
		return errfmt.Errorf("the previous daemon wrote handoff v%d, and this tuios reads v%d to v%d, so it cannot take over the panes. %s", v, MinHandoffVersion, HandoffVersion, restartAdvice())
	}
	return nil
}
