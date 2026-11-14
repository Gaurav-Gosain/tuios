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
	// Created and LastActive are the session's own, in Unix nanoseconds.
	Created    int64 `json:"created,omitempty"`
	LastActive int64 `json:"last_active,omitempty"`
	// ReturnTo and WorkspaceTrail are where the session goes when the
	// workspace on screen loses its last pane. See empty_workspace.go.
	ReturnTo       map[int]int `json:"return_to,omitempty"`
	WorkspaceTrail []int       `json:"workspace_trail,omitempty"`
	// ReportColors are the colours the last client pushed for the panes'
	// emulators to answer OSC 10, 11 and 4 with. See report_colors.go.
	ReportColors handoffReportColors `json:"report_colors,omitzero"`
}

// handoffReportColors is a session's report colours, as the client pushed
// them.
type handoffReportColors struct {
	Bg      string `json:"bg,omitempty"`
	Fg      string `json:"fg,omitempty"`
	Palette string `json:"palette,omitempty"`
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
	// CloseOnExit is a pane whose window closes when its process exits. See
	// PTY.closeOnExit.
	CloseOnExit bool
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
	Fallback string `json:"fallback"`
	// FallbackFD is the descriptor Fallback names, carried across the exec,
	// or -1 when Fallback is a file by name.
	FallbackFD int `json:"fallback_fd"`
	// Keeper is the process holding a copy of the descriptors until the new
	// image is up. See handoff_keeper.go.
	Keeper handoffKeeperRecord `json:"keeper"`
	Stats  HandoffStats        `json:"stats"`
	// FarHolds are the cards this daemon's panes hold on other machines.
	// Only this daemon knows them, and a pane that closes after the
	// upgrade must still let its card go there (task_project.go).
	FarHolds []handoffFarHold `json:"far_holds,omitempty"`

	// What the daemon keeps about windows outside the sessions, each by
	// window id. See handoff_window_records.go.
	PluginPanes   map[string]handoffPluginPane `json:"plugin_panes,omitempty"`
	ProtocolPanes map[string]string            `json:"protocol_panes,omitempty"`
	PaneCreators  map[string]string            `json:"pane_creators,omitempty"`
	MovedWindows  map[string]handoffMovedFrom  `json:"moved_windows,omitempty"`
}

// handoffFarHold is one card a local window holds on another machine.
type handoffFarHold struct {
	Window  string `json:"window"`
	Host    string `json:"host"`
	Session string `json:"session"`
	ID      string `json:"id"`
}

// handoffPluginPane is a pluginPane.
type handoffPluginPane struct {
	Plugin     string `json:"plugin"`
	Entrypoint string `json:"entrypoint"`
}

// handoffMovedFrom is a movedFrom.
type handoffMovedFrom struct {
	IDs   []string `json:"ids"`
	Names []string `json:"names"`
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
			// A record from an image without fallback_fd names its binary
			// by path; left at zero, the field would name stdin.
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

// checkHandoffVersion refuses a manifest this build cannot read.
func checkHandoffVersion(v int) error {
	if v < MinHandoffVersion || v > HandoffVersion {
		return errfmt.Errorf("the previous daemon wrote handoff v%d, and this tuios reads v%d to v%d, so it cannot take over the panes. %s", v, MinHandoffVersion, HandoffVersion, restartAdvice())
	}
	return nil
}
