//go:build linux || darwin

// tuios-rd shows a screen-relay desktop in a terminal pane with kitty
// graphics, and drives it with the mouse and the keyboard.
//
// Video: /stream (H.264) -> openh264 (dlopen, no cgo) -> RGB at the pane's
// pixel size -> kitty graphics, one image id. Local terminals get whole
// frames through a reused /dev/shm pool (t=s). A pty that crosses a link
// gets zlib frame edits of the changed tiles (a=f). A terminal that refuses
// a=f (Ghostty) gets whole zlib frames.
//
// Input: "share" by default: the viewer watches with /stream?view=1 and
// sends input with POST /input, so agents keep working. The control key
// takes control with /stream?take=1 (only the person's input reaches the
// desktop) and gives it back.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

var version = "dev"

type config struct {
	base          string
	token         string
	toggle        keySpec
	quit          keySpec
	take          bool
	mode          string
	stats         string
	duration      time.Duration
	jobs          int
	inflight      int
	input         string
	refineAfter   time.Duration
	pace          time.Duration
	why           string
	codec         string
	tilesInflight int
	noSize        bool
	output        string

	// for the tuios frame-clock harness
	guestLog string
	seqBase  int
	noReply  bool
	report   string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "connect" {
		os.Exit(connectMain(os.Args[2:]))
	}
	os.Exit(viewMain(os.Args[1:]))
}

// atExit runs before tuios-rd exits through fail, such as the tunnel that
// connect started.
var atExit []func()

// viewMain shows the desktop at a URL: tuios-rd [flags] [URL]. It returns
// the exit status.
func viewMain(args []string) int {
	var cfg config
	var toggle, quit string
	var showVersion bool
	var superKey string
	flag.StringVar(&cfg.token, "token", os.Getenv("SCREEN_RELAY_TOKEN"), "session token (default: $SCREEN_RELAY_TOKEN)")
	flag.StringVar(&toggle, "key", "ctrl+]", "key that takes control and gives it back")
	flag.StringVar(&quit, "quit-key", "ctrl+\\", "key that quits tuios-rd")
	flag.BoolVar(&cfg.take, "take", false, "start with control of the desktop")
	flag.StringVar(&cfg.mode, "mode", "auto", "auto, shm, edits or frames")
	flag.StringVar(&cfg.stats, "stats", "", "append one JSON line of measurements a second to this file")
	flag.DurationVar(&cfg.duration, "duration", 0, "quit after this time and print a summary")
	flag.IntVar(&cfg.jobs, "jobs", min(8, runtime.NumCPU()), "goroutines for the colour conversion")
	flag.IntVar(&cfg.inflight, "inflight", 2, "shared memory frames in flight to the terminal")
	flag.BoolVar(&showVersion, "version", false, "print the version")
	// 12 ms: a burst of frames is spread out, but a 60 fps frame that comes
	// a little early is not held back to the next one.
	flag.BoolVar(&cfg.noSize, "no-size", false, "do not ask the engine to scale tiles to the pane")
	flag.IntVar(&cfg.tilesInflight, "tiles-inflight", 1, "tiles frames the engine may send before an ACK (1 to 4)")
	flag.StringVar(&cfg.codec, "codec", "auto", "auto (tiles when the engine offers it), h264 or tiles")
	flag.StringVar(&superKey, "super-key", "none", "while you have control, send this modifier as Super: alt, ctrl or none")
	flag.StringVar(&cfg.output, "output", "", "show this output of the engine, for example DP-2 (default: the engine's default output)")
	flag.StringVar(&cfg.why, "why", "", "a shell command that prints, in one line, why the engine does not answer; tuios-rd shows that line")
	flag.DurationVar(&cfg.pace, "pace", 12*time.Millisecond, "at most one frame in this time (0: no pacing)")
	flag.DurationVar(&cfg.refineAfter, "refine", 150*time.Millisecond, "draw the engine's lossless screenshot when no frame came for this long (0: never)")
	flag.StringVar(&cfg.input, "input", "socket", "socket: input on a WebSocket that tuios-rd holds while you type; post: POST /input for each batch")
	// Measurement flags, named as the tuios frame-clock harness passes them.
	flag.StringVar(&cfg.guestLog, "guestlog", "", "measure: append 'seq capture-wall-ns' per shared memory frame and put seq in its first 8 bytes")
	flag.IntVar(&cfg.seqBase, "seqbase", 0, "measure: added to the sequence in -guestlog")
	flag.BoolVar(&cfg.noReply, "noreply", false, "measure: do not wait for the terminal's answers")
	flag.StringVar(&cfg.report, "report", "", "measure: append the summary to this file")
	flag.DurationVar(&cfg.duration, "t", 0, "the same as --duration")
	flag.Duration("warm", 0, "ignored, for the harness")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tuios-rd [flags] [URL]\n       tuios-rd connect HOST [flags]\n\nShows a screen-relay desktop in this terminal. URL defaults to http://127.0.0.1:8080.\nconnect sets up and shows the desktop of a host of tuios: tuios-rd connect --help.\n\n")
		flag.PrintDefaults()
	}
	_ = flag.CommandLine.Parse(args)
	if m, err := parseSuperKey(superKey); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	} else {
		superFrom = m
	}
	if showVersion {
		fmt.Println("tuios-rd", version)
		return 0
	}
	cfg.base = "http://127.0.0.1:8080"
	if flag.NArg() > 0 {
		cfg.base = flag.Arg(0)
	}
	if !strings.Contains(cfg.base, "://") {
		cfg.base = "http://" + cfg.base
	}
	cfg.base = strings.TrimRight(cfg.base, "/")
	var err error
	if cfg.toggle, err = parseKeySpec(toggle); err != nil {
		fail("tuios-rd: --key: %v", err)
	}
	if cfg.quit, err = parseKeySpec(quit); err != nil {
		fail("tuios-rd: --quit-key: %v", err)
	}
	switch cfg.codec {
	case "auto", "tiles":
	case "h264":
		if !haveH264 {
			fail("tuios-rd: this build shows tiles only. Use --codec tiles, or build with -tags h264.")
		}
	default:
		fail("tuios-rd: --codec must be auto, h264 or tiles.")
	}
	switch cfg.mode {
	case "auto", "shm", "edits", "frames":
	default:
		fail("tuios-rd: --mode must be auto, shm, edits or frames.")
	}
	lib, err := loadOpenH264()
	if err != nil {
		fail("%v", err)
	}
	return run(cfg, lib)
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	for _, fn := range atExit {
		fn()
	}
	os.Exit(1)
}

// ---- the app ----

type frame struct {
	tiles    bool // a type 8 payload in au
	gen, seq uint32
	capUS    int64
	recvUS   int64
	au       []byte
}

type app struct {
	cfg  config
	lib  *openh264
	ctx  context.Context
	stop context.CancelFunc
	out  *os.File

	mode    outMode
	pool    shmPool
	replies bool // the terminal answers q=0 frames

	cellPx     atomic.Int64 // w<<16 | h from CSI 16 t
	pixelMouse atomic.Bool
	dispW      atomic.Int64 // displayed image size in pixels, for the mouse
	offX, offY atomic.Int64 // where the picture starts in the pane, in pixels
	dispH      atomic.Int64
	deskW      atomic.Int64
	deskH      atomic.Int64

	// stream session
	connMu  sync.Mutex
	conn    *websocket.Conn
	gen     uint32
	control atomic.Bool // the person wants control
	haveCtl atomic.Bool // the current stream owns input
	redial  chan struct{}
	rttUS   atomic.Int64
	inRTTUS atomic.Int64

	// notices for the status line
	noticeMu sync.Mutex
	notice   string
	noticeAt time.Time
	connMsg  atomic.Value // string: "" when connected

	// decoded frames
	decMu     sync.Mutex
	latest    *yuv
	latestF   *frame
	free      chan *yuv
	wake      chan struct{}
	minRecv   atomic.Int64 // min(recv - capture), for the clock offset
	recvN     atomic.Int64
	wireBytes atomic.Int64
	decErrs   atomic.Int64

	frames        chan frame
	cellChanged   chan struct{}
	tilesWake     chan struct{}
	tb            tilesBuf
	useTiles      atomic.Bool
	tilesScale    atomic.Bool  // the engine scales tiles to a size=WxH we ask for
	sentSize      atomic.Value // string: the size in the current connection
	tilesApplyMax atomic.Int64
	tilesScaleMax atomic.Int64
	tilesGapMax   atomic.Int64 // longest time between two tiles frames
	lastTilesRecv int64

	// input
	inputQ       chan []record
	ctl          controlSock
	buttonsHeld  atomic.Bool
	controlDials atomic.Int64
	lastWhy      time.Time
	whyMsg       atomic.Value  // string
	backend      atomic.Value  // string
	outputs      atomic.Value  // []outputInfo, capabilities.outputs
	pointerX     atomic.Uint32 // last pointer position sent, 0..65535 plus 1; 0 before any
	pointerY     atomic.Uint32
	clipOK       atomic.Bool // capabilities.clipboard
	clipNotice   chan struct{}
	clipCopies   atomic.Int64
	clipPastes   atomic.Int64
	refines      atomic.Int64
	stillMS      atomic.Int64
	inputSent    atomic.Int64 // when the last input went out, for key-to-pixel timing
	http         *http.Client
}

func (a *app) setNotice(s string) {
	a.noticeMu.Lock()
	a.notice, a.noticeAt = s, time.Now()
	a.noticeMu.Unlock()
}

func run(cfg config, lib *openh264) int {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	a := &app{
		cfg: cfg, lib: lib, ctx: ctx, stop: stop, out: os.Stdout,
		redial:      make(chan struct{}, 1),
		clipNotice:  make(chan struct{}, 1),
		free:        make(chan *yuv, 4),
		cellChanged: make(chan struct{}, 1),
		tilesWake:   make(chan struct{}, 1),
		wake:        make(chan struct{}, 1),
		inputQ:      make(chan []record, 256),
		frames:      make(chan frame, 256),
		http:        &http.Client{Timeout: 5 * time.Second},
	}
	a.minRecv.Store(1 << 62)
	a.connMsg.Store("Connecting to " + cfg.base)
	a.control.Store(cfg.take)
	a.pixelMouse.Store(true) // until the terminal says it has no pixel mode
	for range 4 {
		a.free <- &yuv{}
	}
	restore, err := makeRaw()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tuios-rd: stdin is not a terminal. Run tuios-rd in a terminal pane.")
		return 1
	}
	termEvs := make(chan termEv, 256)
	go readTerminal(termEvs)

	probe, perr := a.probe(termEvs)
	if perr != nil {
		restore()
		fmt.Fprintln(os.Stderr, perr)
		return 1
	}
	a.mode = probe.mode
	a.pool = shmPool{slots: make([]*shmSlot, cfg.inflight+1), pid: os.Getpid()}
	if a.mode == modeShm {
		cleanStale()
	}

	// Screen set-up: alternate screen, no cursor, mouse with pixels, paste,
	// kitty keyboard "disambiguate" so Escape, Alt and Super keys arrive whole.
	_, _ = a.out.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[?1003h\x1b[?1006h\x1b[?1016h\x1b[?2004h\x1b[>1u\x1b[?1016$p")
	cleanup := func() {
		_, _ = fmt.Fprintf(a.out, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\\x1b[<u\x1b[?2004l\x1b[?1016l\x1b[?1006l\x1b[?1003l\x1b[?25h\x1b[?1049l", imgID)
		restore()
		a.pool.close()
	}
	defer cleanup()

	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGWINCH)
	defer signal.Stop(sig)

	go a.session()
	go a.decodeLoop()
	go a.inputLoop()
	go a.clipLoop()
	okCh := make(chan *gfxReply, 64)
	go a.handleInput(termEvs, okCh)

	return a.display(okCh, sig)
}

// ---- terminal probe ----

type probeResult struct{ mode outMode }

func readTerminal(ch chan<- termEv) {
	data := make(chan []byte, 64)
	go func() {
		for {
			b := make([]byte, 8192)
			n, err := os.Stdin.Read(b)
			if n > 0 {
				data <- b[:n]
			}
			if err != nil {
				close(data)
				return
			}
		}
	}()
	var ps parser
	emit := func(e termEv) { ch <- e }
	var timer <-chan time.Time
	for {
		select {
		case b, ok := <-data:
			if !ok {
				close(ch)
				return
			}
			ps.feed(b, false, emit)
			timer = nil
			if len(ps.buf) > 0 {
				timer = time.After(25 * time.Millisecond)
			}
		case <-timer:
			timer = nil
			ps.feed(nil, true, emit)
			if len(ps.buf) > 0 { // a broken sequence: drop it
				ps.buf = ps.buf[:0]
			}
		}
	}
}

func (a *app) probe(evs chan termEv) (probeResult, error) {
	const qID, shmID, editID = 7301, 7302, 7303
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b_Gi=%d,a=q,s=1,v=1,f=24,t=d;AAAA\x1b\\", qID)
	shmName := ""
	tryShm := runtime.GOOS == "linux" && os.Getenv("SSH_CONNECTION") == "" && (a.cfg.mode == "auto" || a.cfg.mode == "shm")
	if tryShm {
		if n, err := probeShmFile(); err == nil {
			shmName = n
			fmt.Fprintf(&b, "\x1b_Gi=%d,a=q,s=1,v=1,f=24,t=f,S=3;%s\x1b\\", shmID, b64(n))
		}
	}
	fmt.Fprintf(&b, "\x1b_Gi=%d,a=t,s=2,v=1,f=24,t=d,q=0;AAAAAAAA\x1b\\", editID)
	fmt.Fprintf(&b, "\x1b_Ga=f,i=%d,r=1,x=0,y=0,s=1,v=1,f=24,X=1,q=0;AAAA\x1b\\", editID)
	fmt.Fprintf(&b, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", editID)
	b.WriteString("\x1b[16t\x1b[c")
	_, _ = a.out.WriteString(b.String())
	defer func() {
		if shmName != "" {
			os.Remove(shmName)
		}
	}()

	var gfxOK, shmOK bool
	shmSeen := false
	var editReplies []string
	deadline := time.After(3 * time.Second)
	var grace <-chan time.Time
	da1 := false
wait:
	for {
		select {
		case e, ok := <-evs:
			if !ok {
				break wait
			}
			switch {
			case e.reply != nil:
				switch e.reply.id {
				case qID:
					gfxOK = e.reply.msg == "OK"
				case shmID:
					shmSeen = true
					shmOK = e.reply.msg == "OK"
				case editID:
					editReplies = append(editReplies, e.reply.msg)
				}
			case e.cellPx[0] > 0:
				a.cellPx.Store(int64(e.cellPx[1])<<16 | int64(e.cellPx[0]))
			case e.da1:
				da1 = true
				grace = time.After(300 * time.Millisecond)
			}
			if da1 && gfxOK && (shmName == "" || shmSeen) && len(editReplies) >= 2 {
				break wait
			}
		case <-grace:
			break wait
		case <-deadline:
			break wait
		}
	}
	if !gfxOK {
		return probeResult{}, errors.New("tuios-rd: this terminal does not show kitty graphics. Run tuios-rd in kitty, Ghostty, WezTerm or a tuios pane")
	}
	editOK := len(editReplies) >= 2 && editReplies[1] == "OK"
	var m outMode
	switch a.cfg.mode {
	case "shm":
		if !shmOK {
			return probeResult{}, errors.New("tuios-rd: the terminal cannot read files in /dev/shm (t=f). Use --mode edits or --mode frames")
		}
		m = modeShm
	case "edits":
		m = modeEdits
	case "frames":
		m = modeFull
	default:
		switch {
		case shmOK:
			m = modeShm
		case editOK:
			m = modeEdits
		default:
			m = modeFull
		}
	}
	return probeResult{mode: m}, nil
}

// ---- stream session ----

func (a *app) wsURL(take bool) string {
	u := strings.Replace(a.cfg.base, "http", "ws", 1) + "/stream"
	if take {
		u += "?take=1"
		if a.clipOK.Load() {
			u += "&clipboard=1" // clipboard notices; the engine sends them only to the input owner
		}
	} else {
		u += "?view=1"
	}
	u += a.outputQuery("&")
	if a.useTiles.Load() {
		u += "&codec=tiles" // the engine wants it right after the others
		if k := a.cfg.tilesInflight; k > 1 {
			u += fmt.Sprintf("&tiles_inflight=%d", k)
		}
		if s := a.paneSize(); a.tilesScale.Load() && s != "" {
			u += "&size=" + s // the engine scales by a whole factor to fit
			a.sentSize.Store(s)
		}
	}
	return u
}

func (a *app) session() {
	in := a.frames
	backoff := 250 * time.Millisecond
	for a.ctx.Err() == nil {
		take := a.control.Load()
		a.chooseCodec()
		protos := []string{"nsl1"}
		if a.cfg.token != "" {
			protos = append(protos, "bearer."+a.cfg.token)
		}
		dctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		c, _, err := websocket.Dial(dctx, a.wsURL(take), &websocket.DialOptions{Subprotocols: protos})
		cancel()
		if err != nil {
			if take {
				// No slot or not allowed: go back to sharing.
				a.control.Store(false)
				a.setNotice("Could not take control: " + shortErr(err))
				continue
			}
			a.connMsg.Store(fmt.Sprintf("Cannot connect to %s: %s. Trying again.", a.cfg.base, shortErr(err)))
			a.explain()
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Second)
			continue
		}
		backoff = 250 * time.Millisecond
		c.SetReadLimit(9 << 20)
		a.connMu.Lock()
		a.conn, a.gen = c, 0
		a.connMu.Unlock()
		a.haveCtl.Store(take)
		a.connMsg.Store("")
		a.whyMsg.Store("")
		go a.readBackend()
		if take {
			a.setNotice("You have control. Agents cannot type until you give it back.")
		}
		err = a.readStream(c, in)
		a.connMu.Lock()
		a.conn = nil
		a.connMu.Unlock()
		a.haveCtl.Store(false)
		_ = c.CloseNow()
		if a.ctx.Err() != nil {
			return
		}
		switch {
		case websocket.CloseStatus(err) == 4001:
			a.control.Store(false)
			a.setNotice("Another viewer took control. You share the desktop again.")
		case errors.Is(err, errRedial):
		default:
			a.connMsg.Store("The stream closed: " + shortErr(err) + ". Connecting again.")
			a.explain()
			time.Sleep(backoff)
		}
	}
}

var errRedial = errors.New("redial")

// explain runs --why, at most once in 5 s, and shows its first line in
// place of the connection error. The engine cannot say why it stopped, but
// its host can, for example from the engine's log.
func (a *app) explain() {
	if a.cfg.why == "" {
		return
	}
	if time.Since(a.lastWhy) < 5*time.Second {
		if w, _ := a.whyMsg.Load().(string); w != "" {
			a.connMsg.Store(w) // keep the reason over the next connection error
		}
		return
	}
	a.lastWhy = time.Now()
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", a.cfg.why)
	cmd.Stdin = nil
	out, _ := cmd.Output()
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	line = strings.TrimSpace(line)
	a.whyMsg.Store(line)
	if line != "" {
		a.connMsg.Store(line)
	}
}

// paneSize is the pixel area for the picture: the pane above the status
// line, as WxH. The engine scales the capture by a whole factor to fit it.
func (a *app) paneSize() string {
	if a.cfg.noSize {
		return ""
	}
	g := a.geom()
	w, h := int(float64(g.cols)*g.cw), int(float64(max(g.rows-1, 1))*g.ch)
	if w < 16 || h < 16 {
		return ""
	}
	return fmt.Sprintf("%dx%d", w, h)
}

// chooseCodec asks /capabilities whether the engine offers tiles.
func (a *app) chooseCodec() {
	switch a.cfg.codec {
	case "h264":
		a.useTiles.Store(false)
	case "tiles":
		a.useTiles.Store(true)
	}
	req, _ := http.NewRequestWithContext(a.ctx, "GET", a.cfg.base+"/capabilities", nil)
	if a.cfg.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var c struct {
		Backend    string       `json:"backend"`
		Codecs     []string     `json:"codecs"`
		TilesScale string       `json:"tiles_scale"`
		Clipboard  bool         `json:"clipboard"`
		Outputs    []outputInfo `json:"outputs"`
	}
	if json.NewDecoder(resp.Body).Decode(&c) == nil {
		a.backend.Store(c.Backend)
		a.outputs.Store(c.Outputs)
		a.clipOK.Store(c.Clipboard)
		if a.cfg.codec == "auto" {
			a.useTiles.Store(slices.Contains(c.Codecs, "tiles"))
		}
		if !haveH264 {
			// This build shows tiles only.
			a.useTiles.Store(true)
			if len(c.Codecs) > 0 && !slices.Contains(c.Codecs, "tiles") {
				a.whyMsg.Store("The engine on the host sends no tiles. Install a newer engine there with tuios remote-desktop.")
			}
		}
		a.tilesScale.Store(c.TilesScale != "")
	}
}

// readBackend reads the engine's backend name (x11, wayland, macos) from
// /capabilities, for the messages.
func (a *app) readBackend() {
	req, _ := http.NewRequestWithContext(a.ctx, "GET", a.cfg.base+"/capabilities", nil)
	if a.cfg.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var c struct {
		Backend string `json:"backend"`
	}
	if json.NewDecoder(resp.Body).Decode(&c) == nil {
		a.backend.Store(c.Backend)
	}
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 80 {
		s = s[i+2:]
	}
	return s
}

func (a *app) readStream(c *websocket.Conn, in chan<- frame) error {
	errc := make(chan error, 1)
	go func() {
		for {
			_, m, err := c.Read(a.ctx)
			if err != nil {
				errc <- err
				return
			}
			now := monoUS()
			if len(m) < 20 || string(m[:4]) != "NSL1" || m[4] != 1 {
				continue
			}
			var f frame
			switch {
			case m[5] == 1 && len(m) >= 32:
				f = frame{
					gen:    binary.BigEndian.Uint32(m[8:]),
					seq:    binary.BigEndian.Uint32(m[12:]),
					capUS:  int64(binary.BigEndian.Uint64(m[24:])),
					recvUS: now,
					au:     m[32:],
				}
			case m[5] == 8 && len(m) >= 40:
				if a.lastTilesRecv != 0 && now-a.lastTilesRecv > a.tilesGapMax.Load() {
					a.tilesGapMax.Store(now - a.lastTilesRecv)
				}
				a.lastTilesRecv = now
				f = frame{
					tiles:  true,
					gen:    binary.BigEndian.Uint32(m[8:]),
					seq:    binary.BigEndian.Uint32(m[12:]),
					capUS:  int64(binary.BigEndian.Uint64(m[26:])),
					recvUS: now,
					au:     m[20:],
				}
			case m[5] == 7:
				select {
				case a.clipNotice <- struct{}{}:
				default:
				}
				continue
			default:
				continue // cursor notices (6) are not used
			}
			a.connMu.Lock()
			a.gen = f.gen
			a.connMu.Unlock()
			a.recvN.Add(1)
			a.wireBytes.Add(int64(len(m)))
			if d := now - f.capUS; d < a.minRecv.Load() {
				a.minRecv.Store(d)
			}
			if f.tiles {
				in <- f // deltas: every frame must be applied
				continue
			}
			select {
			case in <- f:
			default:
				a.ack(f, now) // the decoder is far behind: drop
			}
		}
	}()
	ping := time.NewTicker(2 * time.Second)
	defer ping.Stop()
	for {
		select {
		case err := <-errc:
			return err
		case <-a.redial:
			c.Close(websocket.StatusNormalClosure, "")
			<-errc
			return errRedial
		case <-ping.C:
			go func() {
				t0 := monoUS()
				pctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
				defer cancel()
				if c.Ping(pctx) == nil {
					a.rttUS.Store(monoUS() - t0)
				}
			}()
		}
	}
}

// ack tells the engine the frame was drawn or dropped.
func (a *app) ack(f frame, recvUS int64) {
	a.connMu.Lock()
	c, gen := a.conn, a.gen
	a.connMu.Unlock()
	if c == nil || gen != f.gen {
		return
	}
	h := make([]byte, 24)
	copy(h, "NSL1")
	h[4], h[5] = 1, 2
	binary.BigEndian.PutUint32(h[8:], f.gen)
	binary.BigEndian.PutUint32(h[12:], f.seq)
	binary.BigEndian.PutUint32(h[16:], 4)
	binary.BigEndian.PutUint32(h[20:], uint32(max(monoUS()-recvUS, 0)))
	_ = c.Write(a.ctx, websocket.MessageBinary, h)
}

// ---- decode ----

func (a *app) decodeLoop() {
	var dec *decoder
	var gen uint32
	bad := 0
	td := newTilesDecoder()
	for f := range a.frames {
		if f.tiles {
			t0 := monoUS()
			err := td.apply(&a.tb, f.au)
			if d := monoUS() - t0; d > a.tilesApplyMax.Load() {
				a.tilesApplyMax.Store(d)
			}
			if err != nil {
				a.decErrs.Add(1)
				a.keyframeRequest(f.gen)
			} else {
				a.tb.mu.Lock()
				a.tb.f = f
				a.tb.f.au = nil
				a.tb.mu.Unlock()
			}
			a.ack(f, f.recvUS)
			select {
			case a.tilesWake <- struct{}{}:
			default:
			}
			continue
		}
		if dec == nil || f.gen != gen {
			if dec != nil {
				dec.close()
			}
			var err error
			if dec, err = a.lib.newDecoder(); err != nil {
				a.connMsg.Store(err.Error())
				return
			}
			gen = f.gen
			bad = 0
		}
		p, got, err := dec.decode(f.au)
		if err != nil {
			a.decErrs.Add(1)
			bad++
			if bad == 60 { // lost sync: a new connection starts with a keyframe
				a.requestRedial()
			}
		} else {
			bad = 0
		}
		if !got {
			a.ack(f, f.recvUS)
			continue
		}
		b := <-a.free
		b.from(p)
		a.decMu.Lock()
		if a.latest != nil {
			a.free <- a.latest
		}
		ff := f
		ff.au = nil
		a.latest, a.latestF = b, &ff
		a.decMu.Unlock()
		a.ack(f, f.recvUS)
		select {
		case a.wake <- struct{}{}:
		default:
		}
	}
}

// keyframeRequest sends type 9: the next tiles frame is a keyframe.
func (a *app) keyframeRequest(gen uint32) {
	a.connMu.Lock()
	c, cur := a.conn, a.gen
	a.connMu.Unlock()
	if c == nil || cur != gen {
		return
	}
	h := make([]byte, 20)
	copy(h, "NSL1")
	h[4], h[5] = 1, 9
	binary.BigEndian.PutUint32(h[8:], gen)
	_ = c.Write(a.ctx, websocket.MessageBinary, h)
}

func (a *app) requestRedial() {
	select {
	case a.redial <- struct{}{}:
	default:
	}
}

// outputQuery is "output=NAME" after sep when --output is set, else "".
// The engine sends pointer input from that socket or request to the same output.
func (a *app) outputQuery(sep string) string {
	if a.cfg.output == "" {
		return ""
	}
	return sep + "output=" + url.QueryEscape(a.cfg.output)
}

// outputInfo is one display from "outputs" in GET /capabilities.
type outputInfo struct {
	Name    string  `json:"name"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Scale   float64 `json:"scale"`
	Primary bool    `json:"primary"`
}

// displayLabel names the displays for the status line. With --output all
// and more than one display it lists them all and puts the one under the
// pointer in brackets. With --output NAME it gives that name.
func (a *app) displayLabel() string {
	outs, _ := a.outputs.Load().([]outputInfo)
	if a.cfg.output != "all" {
		if a.cfg.output != "" && len(outs) > 1 {
			return a.cfg.output
		}
		return ""
	}
	if len(outs) < 2 {
		return ""
	}
	logical := func(o outputInfo) (int, int) {
		s := o.Scale
		if s <= 0 {
			s = 1
		}
		return int(float64(o.Width)/s + 0.5), int(float64(o.Height)/s + 0.5)
	}
	x0, y0, x1, y1 := outs[0].X, outs[0].Y, outs[0].X, outs[0].Y
	for _, o := range outs {
		w, h := logical(o)
		x0, y0, x1, y1 = min(x0, o.X), min(y0, o.Y), max(x1, o.X+w), max(y1, o.Y+h)
	}
	active := ""
	if px, py := a.pointerX.Load(), a.pointerY.Load(); px > 0 && py > 0 {
		x := float64(x0) + float64(px-1)/65535*float64(x1-x0)
		y := float64(y0) + float64(py-1)/65535*float64(y1-y0)
		for _, o := range outs {
			w, h := logical(o)
			if x >= float64(o.X) && x < float64(o.X+w) && y >= float64(o.Y) && y < float64(o.Y+h) {
				active = o.Name
				break
			}
		}
	}
	names := make([]string, 0, len(outs))
	for _, o := range outs {
		if o.Name == active {
			names = append(names, "["+o.Name+"]")
		} else {
			names = append(names, o.Name)
		}
	}
	return strings.Join(names, " ")
}
