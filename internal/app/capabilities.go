package app

import (
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// HostCapabilities holds information about the host terminal's capabilities.
// These are used to determine which features TUIOS can use for rendering.
type HostCapabilities struct {
	KittyGraphics bool
	// KittyFileTransfer reports whether the host terminal can read a
	// server-local path out of a kitty t=f / t=t / t=s transmission.
	//
	// A terminal sharing our filesystem (kitty, ghostty, wezterm) can, and
	// forwarding the path is far cheaper than shipping the pixels. A browser
	// client such as sip's cannot: it only ever sees the bytes we send it, so
	// a forwarded path is silently dropped and the image never appears. When
	// this is false the passthrough reads the file itself and re-encodes it as
	// a direct (t=d) transmission, and tells guests that file media are
	// unsupported so they stream instead.
	KittyFileTransfer bool
	// KittyAnimation reports whether the host terminal implements the kitty
	// animation protocol, specifically an a=f frame edit of an image that is
	// already on screen.
	//
	// It is what lets a repainting guest cost a damage rectangle instead of a
	// whole bitmap, so it is worth a probe of its own. A terminal that ignores
	// a=f leaves this false and gets full retransmissions, which is slow but
	// always correct; claiming it wrongly would leave the pane frozen on its
	// first frame.
	KittyAnimation bool
	SixelGraphics  bool
	TrueColor      bool
	TerminalName   string
	PixelWidth     int
	PixelHeight    int
	CellWidth      int
	CellHeight     int
	Cols           int
	Rows           int
}

// cachedCapabilities and clientCapabilities are process-globals read from
// every render and session goroutine and written by per-connection SSH
// handlers, so they are atomic pointers: the store/load pair gives readers a
// happens-before edge with the writer's struct initialization. A plain
// pointer let the race detector flag every multi-session server, and a reader
// could observe a half-initialized HostCapabilities.
var cachedCapabilities atomicHostCaps

// clientCapabilities holds capabilities received from the daemon client.
// These override detected capabilities when running in daemon mode.
var clientCapabilities atomicHostCaps

// atomicHostCaps is a typed atomic pointer to HostCapabilities.
type atomicHostCaps = atomic.Pointer[HostCapabilities]

func GetHostCapabilities() *HostCapabilities {
	// Prefer client-provided capabilities (for daemon mode)
	if caps := clientCapabilities.Load(); caps != nil {
		return caps
	}
	if caps := cachedCapabilities.Load(); caps != nil {
		return caps
	}
	caps := DetectHostCapabilities()
	cachedCapabilities.Store(caps)
	return caps
}

// SetClientCapabilities installs capabilities detected from a remote client so
// GetHostCapabilities reports the client's terminal rather than the server's
// local one. The SSH and web servers use this: the terminal that must render
// graphics is the one the user connected from, reached over the session, not
// the (often headless) machine running the server.
//
// This is a process-global. A server handling several simultaneous clients with
// different terminals shares the last-installed value for the live consumers
// (image cell math, cell size); the per-connection graphics-enable decision is
// snapshotted at construction and is not affected. Single-client connections,
// the common case, are fully correct.
func SetClientCapabilities(caps *HostCapabilities) {
	clientCapabilities.Store(caps)
}

func DetectHostCapabilities() *HostCapabilities {
	caps := &HostCapabilities{}

	// Detect terminal name from environment
	detectTerminalName(caps)

	// Detect truecolor from environment
	detectTrueColor(caps)

	// Query terminal size (cols/rows)
	queryTerminalSize(caps)

	// Pixel geometry and graphics support, in one round trip
	probeTerminal(caps)

	// Apply environment overrides
	applyEnvironmentOverrides(caps)

	// Debug output if requested - writes to /tmp/tuios_caps.log
	if os.Getenv("TUIOS_DEBUG_CAPS") == "1" {
		if f, err := os.OpenFile("/tmp/tuios_caps.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644); err == nil {
			_, _ = fmt.Fprintf(f, "Terminal: %s\nKitty: %v\nSixel: %v\nTrueColor: %v\nCell: %dx%d\nPixel: %dx%d\n",
				caps.TerminalName, caps.KittyGraphics, caps.SixelGraphics, caps.TrueColor,
				caps.CellWidth, caps.CellHeight, caps.PixelWidth, caps.PixelHeight)
			_ = f.Close()
		}
	}

	return caps
}

func detectTerminalName(caps *HostCapabilities) {
	term := strings.ToLower(os.Getenv("TERM"))
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))

	switch {
	case strings.Contains(termProgram, "ghostty"):
		caps.TerminalName = "ghostty"
	case strings.Contains(termProgram, "kitty"):
		caps.TerminalName = "kitty"
	case strings.Contains(termProgram, "wezterm"):
		caps.TerminalName = "wezterm"
	case strings.Contains(termProgram, "konsole"):
		caps.TerminalName = "konsole"
	case strings.Contains(termProgram, "iterm"):
		caps.TerminalName = "iterm2"
	case strings.Contains(termProgram, "alacritty"):
		caps.TerminalName = "alacritty"
	case strings.Contains(termProgram, "foot"):
		caps.TerminalName = "foot"
	case strings.Contains(termProgram, "contour"):
		caps.TerminalName = "contour"
	case strings.Contains(termProgram, "mlterm"):
		caps.TerminalName = "mlterm"
	case strings.Contains(termProgram, "mintty"):
		caps.TerminalName = "mintty"
	default:
		if strings.Contains(term, "kitty") {
			caps.TerminalName = "kitty"
		} else if strings.Contains(term, "xterm") {
			caps.TerminalName = "xterm"
		} else if strings.Contains(term, "mlterm") {
			caps.TerminalName = "mlterm"
		}
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" {
		caps.TerminalName = "kitty"
	}
	if os.Getenv("WEZTERM_PANE") != "" {
		caps.TerminalName = "wezterm"
	}
}

func detectTrueColor(caps *HostCapabilities) {
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))
	term := strings.ToLower(os.Getenv("TERM"))

	if colorterm == "truecolor" || colorterm == "24bit" {
		caps.TrueColor = true
	}
	if strings.Contains(term, "256color") || strings.Contains(term, "truecolor") || strings.Contains(term, "direct") {
		caps.TrueColor = true
	}
}

// probeTimeout is the backstop for the capability probe. It is only ever spent
// in full by a host that answers nothing at all, since the probe stops on the
// DA1 reply rather than on the clock.
const probeTimeout = 300 * time.Millisecond

// da1Response matches a primary device attributes reply.
//
// The probe stops on this rather than on a count of terminator bytes. Byte
// counting cost a fixed half second on every start: the XTWINOPS replies
// contain neither of the bytes that were being counted, so both of their reads
// always ran to the timeout, and a terminal without kitty graphics never sent
// the kitty terminators the graphics read was waiting for either.
var da1Response = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// probeTerminal asks the host terminal what it can do, in one round trip.
//
// Every query goes out in a single write with DA1 last. A terminal answers in
// the order it was asked, so the DA1 reply arriving is proof that everything
// written before it has already been answered or ignored, which is what lets
// one read collect all of them and stop as soon as the host is done. DA1 is
// universally implemented, so in practice this costs a round trip rather than
// the timeout.
//
// Both sets of answers are parsed out of the one buffer: graphics support from
// the DA1 parameters and the kitty replies, and pixel geometry from the
// XTWINOPS replies.
func probeTerminal(caps *HostCapabilities) {
	if !isTerminal(os.Stdin.Fd()) {
		setDefaultCellSize(caps)
		return
	}

	// Open /dev/tty for queries to avoid messing with stdin
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		setDefaultCellSize(caps)
		return
	}
	defer func() { _ = tty.Close() }()

	oldState, err := makeRaw(tty.Fd())
	if err != nil {
		setDefaultCellSize(caps)
		return
	}
	defer restoreTerminal(tty.Fd(), oldState)

	// Probe direct transmission (i=1) and, if we can stage a file for it,
	// file transmission (i=2). The ids let the two answers be told apart; the
	// spec requires a terminal to echo the id it was asked about.
	probeFile, probeFileErr := writeGraphicsProbeFile()
	if probeFileErr == nil {
		defer func() { _ = os.Remove(probeFile) }()
	}

	var q strings.Builder
	q.WriteString("\x1b[14t")                                  // window size in pixels
	q.WriteString("\x1b[16t")                                  // cell size in pixels
	q.WriteString("\x1b_Gi=1,a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\") // kitty direct transmission
	if probeFileErr == nil {
		// t=f rather than t=t: a terminal that honours a temp-file
		// transmission deletes the file, and this one is ours to clean up.
		fmt.Fprintf(&q, "\x1b_Gi=2,a=q,t=f,f=24,s=1,v=1;%s\x1b\\",
			base64.StdEncoding.EncodeToString([]byte(probeFile)))
	}
	writeAnimationProbe(&q)
	q.WriteString("\x1b[c") // DA1 last, so its reply closes the whole batch
	_, _ = tty.WriteString(q.String())

	response := readTTYResponse(tty, probeTimeout, da1Response.MatchString)

	parsePixelGeometry(caps, response)
	parseGraphicsSupport(caps, response, probeFileErr == nil)
}

// parseGraphicsSupport reads kitty and sixel support out of a probe response.
func parseGraphicsSupport(caps *HostCapabilities, response string, probedFile bool) {
	// Parse DA1 response for sixel (look for "4" in params)
	da1Re := regexp.MustCompile(`\x1b\[\?([0-9;]+)c`)
	if matches := da1Re.FindStringSubmatch(response); len(matches) >= 2 {
		if slices.Contains(slices.Collect(strings.SplitSeq(matches[1], ";")), "4") {
			caps.SixelGraphics = true
		}
	}

	// Parse Kitty response (look for OK)
	if strings.Contains(response, "OK") {
		caps.KittyGraphics = true
	}
	// File transfer is only claimed on an explicit OK for the i=2 probe. A
	// terminal that ignores the probe, answers without an id, or reports an
	// error leaves this false, and the passthrough re-encodes file
	// transmissions as direct ones. That costs a copy but always renders;
	// guessing the other way renders nothing at all.
	caps.KittyFileTransfer = probedFile && kittyProbeOK(response, 2)
	// Same rule for animation, doubled: the frame edit that must be accepted
	// has to be accepted, and the one that must be refused has to be refused.
	// See writeAnimationProbe for why one answer is not enough.
	caps.KittyAnimation = caps.KittyGraphics &&
		kittyProbeOK(response, animationProbeAccept) &&
		kittyProbeRefused(response, animationProbeReject)
}

// animationProbeAccept and animationProbeReject are the image ids of the two
// halves of the animation probe. They are told apart in the reply the same way
// as the other probes, by the id the terminal echoes back.
const (
	animationProbeAccept = 3
	animationProbeReject = 4
)

// writeAnimationProbe appends a test of a=f that a host which gets frame edits
// wrong has to fail.
//
// Animation cannot be asked about with a=q, so it is tried. The obvious try --
// a one-pixel image with a one-pixel patch -- cannot fail. The patch covers the
// whole image, so a host that reads s= and v= as the patch rectangle (which is
// right) and a host that reads them as the image's new size (which is wrong,
// and leaves the image the size of the patch) behave identically and both
// answer OK. That probe could not fail for the terminal it existed to catch.
//
// So here the patch is smaller than the image, and what is checked afterwards
// is the image's size:
//
//   - id 3 is four pixels wide, is patched one pixel wide, and is then asked to
//     take a four-pixel-wide frame. A host that kept the image four wide
//     accepts. A host that shrank it to the patch has to answer that the frame
//     is wider than the image.
//   - id 4 is four pixels wide, is never patched, and is asked to take a
//     nine-pixel-wide frame. That is out of bounds on any host that checks, so
//     the answer must be an error. This half catches a relay that acknowledges
//     frame edits and drops them: it answers OK to everything, including this.
//
// Support is claimed only when the first is accepted and the second refused.
// Neither image is ever placed, so nothing reaches the screen either way, and
// both are deleted afterwards.
//
// Every payload fits one escape. A payload split across m= continuations is
// not a frame edit at all: a continuation carries no a= key, so the terminal
// routes it to the transmit handler and finishes the load as a new image.
func writeAnimationProbe(q *strings.Builder) {
	px := func(n int) string {
		return base64.StdEncoding.EncodeToString(make([]byte, n*4))
	}
	fmt.Fprintf(q, "\x1b_Gi=%d,a=t,f=32,s=4,v=1,q=2;%s\x1b\\", animationProbeAccept, px(4))
	fmt.Fprintf(q, "\x1b_Gi=%d,a=f,r=1,X=1,x=0,y=0,s=1,v=1,f=32,q=2;%s\x1b\\", animationProbeAccept, px(1))
	fmt.Fprintf(q, "\x1b_Gi=%d,a=f,r=1,X=1,x=0,y=0,s=4,v=1,f=32;%s\x1b\\", animationProbeAccept, px(4))
	fmt.Fprintf(q, "\x1b_Gi=%d,a=t,f=32,s=4,v=1,q=2;%s\x1b\\", animationProbeReject, px(4))
	fmt.Fprintf(q, "\x1b_Gi=%d,a=f,r=1,X=1,x=0,y=0,s=9,v=1,f=32;%s\x1b\\", animationProbeReject, px(9))
	fmt.Fprintf(q, "\x1b_Gi=%d,a=d,d=I,q=2\x1b\\", animationProbeAccept)
	fmt.Fprintf(q, "\x1b_Gi=%d,a=d,d=I,q=2\x1b\\", animationProbeReject)
}

// kittyProbeOK reports whether the host answered "OK" to the probe sent with
// the given image id.
func kittyProbeOK(response string, id int) bool {
	answer, answered := kittyProbeAnswer(response, id)
	return answered && strings.HasPrefix(answer, "OK")
}

// kittyProbeRefused reports whether the host answered the probe sent with the
// given image id, and answered it with an error.
//
// Silence is not a refusal. A host that says nothing has not shown it can tell
// a valid frame edit from an invalid one, so it earns no claim of support.
func kittyProbeRefused(response string, id int) bool {
	answer, answered := kittyProbeAnswer(response, id)
	return answered && !strings.HasPrefix(answer, "OK")
}

// kittyProbeResponse matches one graphics reply: its parameters and its
// message. Compiled once because the probe walks it several times.
var kittyProbeResponse = regexp.MustCompile(`\x1b_G([^;\x1b]*);([^\x1b]*)\x1b\\`)

// kittyProbeAnswer returns the message the host sent for the given image id,
// and whether it answered at all. The first answer for an id wins: a terminal
// echoes the id it was asked about, so a later reply belongs to a later
// command.
func kittyProbeAnswer(response string, id int) (string, bool) {
	want := fmt.Sprintf("i=%d", id)
	for _, m := range kittyProbeResponse.FindAllStringSubmatch(response, -1) {
		if !slices.Contains(strings.Split(m[1], ","), want) {
			continue
		}
		return m[2], true
	}
	return "", false
}

// writeGraphicsProbeFile stages a one-pixel RGB payload on disk for the
// file-transmission capability probe.
func writeGraphicsProbeFile() (string, error) {
	f, err := os.CreateTemp("", "tuios-gfx-probe-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write([]byte{0, 0, 0}); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// readTTYResponse reads from tty with poll-based I/O until done reports the
// reply is complete, or the timeout expires.
//
// done is given the whole response so far rather than each byte, so a caller
// can recognise the shape of the answer it is waiting for. Matching the reply
// itself is what keeps the timeout a backstop: a byte that merely tends to end
// a reply also appears inside other replies, and a reply the host was never
// going to send makes a counting reader wait out the clock every time.
func readTTYResponse(tty *os.File, timeout time.Duration, done func(string) bool) string {
	buf := make([]byte, 512)
	var result strings.Builder
	deadline := time.Now().Add(timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		// Use poll to wait for data with timeout
		ready, err := pollReadable(tty.Fd(), remaining)
		if err != nil || !ready {
			break
		}

		n, err := tty.Read(buf)
		if err != nil {
			break
		}
		if n > 0 {
			result.Write(buf[:n])
			if done(result.String()) {
				break
			}
		}
	}

	return result.String()
}

func applyEnvironmentOverrides(caps *HostCapabilities) {
	switch os.Getenv("TUIOS_KITTY_GRAPHICS") {
	case "1":
		caps.KittyGraphics = true
	case "0":
		caps.KittyGraphics = false
	}

	switch os.Getenv("TUIOS_SIXEL_GRAPHICS") {
	case "1":
		caps.SixelGraphics = true
	case "0":
		caps.SixelGraphics = false
	}

	switch os.Getenv("TUIOS_KITTY_ANIMATION") {
	case "1":
		caps.KittyAnimation = true
	case "0":
		caps.KittyAnimation = false
	}
}

// windowPixels and cellPixels match the two XTWINOPS replies the probe asks
// for: the window's size in pixels and one cell's, both height before width.
var (
	windowPixels = regexp.MustCompile(`\x1b\[4;(\d+);(\d+)t`)
	cellPixels   = regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)
)

// parsePixelGeometry reads the window and cell pixel sizes out of a probe
// response, falling back to a derived or default cell size when the host
// answered with neither.
func parsePixelGeometry(caps *HostCapabilities, response string) {
	if matches := windowPixels.FindStringSubmatch(response); len(matches) == 3 {
		caps.PixelHeight, _ = strconv.Atoi(matches[1])
		caps.PixelWidth, _ = strconv.Atoi(matches[2])
	}
	if matches := cellPixels.FindStringSubmatch(response); len(matches) == 3 {
		caps.CellHeight, _ = strconv.Atoi(matches[1])
		caps.CellWidth, _ = strconv.Atoi(matches[2])
	}

	// Calculate cell size from pixel dimensions if needed
	if caps.PixelWidth > 0 && caps.CellWidth == 0 && caps.Cols > 0 {
		caps.CellWidth = caps.PixelWidth / caps.Cols
	}
	if caps.PixelHeight > 0 && caps.CellHeight == 0 && caps.Rows > 0 {
		caps.CellHeight = caps.PixelHeight / caps.Rows
	}

	if caps.CellWidth == 0 || caps.CellHeight == 0 {
		setDefaultCellSize(caps)
	}
}

func setDefaultCellSize(caps *HostCapabilities) {
	if caps.PixelWidth > 0 && caps.Cols > 0 && caps.CellWidth == 0 {
		caps.CellWidth = caps.PixelWidth / caps.Cols
	}
	if caps.PixelHeight > 0 && caps.Rows > 0 && caps.CellHeight == 0 {
		caps.CellHeight = caps.PixelHeight / caps.Rows
	}

	if caps.CellWidth == 0 {
		caps.CellWidth = 9
	}
	if caps.CellHeight == 0 {
		caps.CellHeight = 20
	}
}
