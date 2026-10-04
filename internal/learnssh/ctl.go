// Package learnssh is `ssh learn.tuios.dev`: a public SSH server that
// teaches tuios with the same lessons as tuios.dev/learn.
//
// The server (Serve) never runs a shell. Each SSH session gets its own child
// process (RunSession), which runs the real tuios in Learn mode with the fake
// shell from internal/webshell in every pane, and a tutor around it that
// shows the lessons. A process per session is deliberate: tuios keeps its
// theme, colour depth, key registry and the fake shell's filesystem in
// process-wide state, so two sessions in one process would see each other's
// changes. A process also gives each session its own memory limit, and a
// crash or a runaway session ends only that session.
//
// The server and a child talk over two pipes, one JSON object per line. This
// file is that protocol.
package learnssh

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// Message types.
const (
	// Server to child.
	MsgInit   = "init"   // the first message: who, what terminal, bests, board
	MsgResize = "resize" // W, H
	MsgNotice = "notice" // Text: a note to show, such as time running out
	MsgBye    = "bye"    // Text: why the session ends; the child says so and exits
	MsgResult = "result" // ID, Ms, Best, Rank, Board: a challenge result was recorded
	MsgBoard  = "board"  // Board: the leaderboard changed

	// Child to server.
	MsgTrackDone = "track"     // ID: a chapter was finished
	MsgChallenge = "challenge" // ID, Ms: a challenge was finished in Ms
	MsgPublish   = "publish"   // ID: show my name on the board for my last result
	MsgQuit      = "quit"      // the reader chose to leave
)

// Msg is one control message. Only the fields its type uses are set.
type Msg struct {
	T string `json:"t"`

	// init
	User    string           `json:"user,omitempty"`
	Name    string           `json:"name,omitempty"` // the name the board would show
	Term    string           `json:"term,omitempty"`
	Color   string           `json:"color,omitempty"` // "truecolor", "256" or "16"
	W       int              `json:"w,omitempty"`
	H       int              `json:"h,omitempty"`
	Bests   map[string]int64 `json:"bests,omitempty"`
	Keyed   bool             `json:"keyed,omitempty"` // bests persist across visits
	MaxMins int              `json:"maxMins,omitempty"`

	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Ms    int64  `json:"ms,omitempty"`
	Best  int64  `json:"best,omitempty"`
	Rank  int    `json:"rank,omitempty"`
	Board Board  `json:"board,omitempty"`
}

// Board is the top times per challenge.
type Board map[string][]Entry

// Entry is one row of the board. Name is "anonymous" unless the person chose
// to show theirs.
type Entry struct {
	Name string `json:"name"`
	Ms   int64  `json:"ms"`
}

// maxLine bounds one control line, so neither side can make the other buffer
// without end.
const maxLine = 256 << 10

// ctlWriter writes messages, safe for concurrent use.
type ctlWriter struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newCtlWriter(w io.Writer) *ctlWriter { return &ctlWriter{enc: json.NewEncoder(w)} }

func (c *ctlWriter) Send(m Msg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(m)
}

// readCtl calls fn for each message until r ends or a line is too long or
// not JSON.
func readCtl(r io.Reader, fn func(Msg)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), maxLine)
	for sc.Scan() {
		var m Msg
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			return err
		}
		fn(m)
	}
	return sc.Err()
}
