package app

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/adrg/xdg"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"tuios.dev/vt"
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
	// KittyPlaceholders is whether the host draws kitty Unicode placeholders.
	// There is no way to ask, so it is read off the terminal's own name and
	// version; see kitty_placeholder_caps.go for why, and for why guessing
	// wrong here costs nothing.
	KittyPlaceholders bool
	SixelGraphics     bool
	// SixelPinned says SixelGraphics was set by TUIOS_SIXEL_GRAPHICS, or by a
	// host that stores images in its cells, so no later answer from the
	// terminal changes it.
	SixelPinned bool
	// KittyPinned says KittyGraphics was set by TUIOS_KITTY_GRAPHICS, so a
	// late answer from the terminal does not change it.
	KittyPinned bool
	// CellBoundImages says the host stores images in its cells, and its
	// graphics are off for that reason. See cell_bound_images.go.
	CellBoundImages bool
	// DA1Late says the startup probe asked for DA1 and gave up before the
	// answer came. A slow terminal, a console on a loaded machine say,
	// still answers, and the program's input reader then gets the reply;
	// see handleSixelProbe.
	DA1Late bool
	// Warnings are problems with the overrides, shown once the UI is up.
	Warnings     []string
	TrueColor    bool
	TerminalName string
	// Term is the host's TERM. It picks the glyph set a picture is drawn
	// with on a host without graphics; see imageSymbolKind.
	Term string
	// FontFamily and BoldFontFamily are the faces the host draws with, as it
	// named them itself. kitty answers a documented XTGETTCAP key with them,
	// and fontconfig turns the name into a file, so a PNG capture can be drawn
	// in the same font the terminal it pictures is drawn in. Empty on a host
	// that does not answer, which falls back to the configured family and then
	// to the embedded face.
	FontFamily     string
	BoldFontFamily string
	PixelWidth     int
	PixelHeight    int
	CellWidth      int
	CellHeight     int
	Cols           int
	Rows           int
	// ANSI is the host terminal's own sixteen palette colours, packed as
	// 0xRRGGBB. Fg and Bg are its default
	// foreground and background, on the same terms.
	//
	// A palette index cannot say what colour it is: only the terminal drawing
	// it can. Everything tuios renders unthemed comes out as indices, the host
	// resolves them, and anything that has to turn a finished frame back into
	// cells of its own has no way to follow unless it asks. It used to guess
	// with the xterm defaults, where index 4 is a navy so dark it is hard to
	// read, so the screen saver over an unthemed session redrew every directory
	// name in a blue the terminal had never shown.
	ANSI [16]uint32
	// ANSIMask has a bit set for each slot the terminal actually answered for.
	// A terminal may answer for some and not others, and a slot it said nothing
	// about has to keep the fallback rather than the zero value, which is
	// black.
	ANSIMask uint16
	Fg, Bg   uint32
	HasFg    bool
	HasBg    bool
}

var cachedCapabilities atomicHostCaps

var clientCapabilities atomicHostCaps

type atomicHostCaps = atomic.Pointer[HostCapabilities]

func GetHostCapabilities() *HostCapabilities {
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
// This is a process-global, and it is now only the seed for a connection that
// arrives with nothing better. Every consumer inside a session reads OS.Caps,
// which the server sets per connection, so two clients on different terminals
// no longer share the last-installed value. See OS.Caps.
func SetClientCapabilities(caps *HostCapabilities) {
	clientCapabilities.Store(caps)
}

func (m *OS) hostCaps() *HostCapabilities {
	if m.Caps != nil {
		return m.Caps
	}
	return GetHostCapabilities()
}

func (m *OS) hostCellSize() (w, h int) {
	caps := m.hostCaps()
	if caps.CellWidth <= 0 || caps.CellHeight <= 0 {
		return 0, 0
	}
	return caps.CellWidth, caps.CellHeight
}

func DetectHostCapabilities() *HostCapabilities {
	var seed HostCapabilities
	queryTerminalSize(&seed)
	return DetectHostCapabilitiesWith(seed, probeTerminal)
}

// DetectHostCapabilitiesWith is DetectHostCapabilities for a terminal the
// probe reaches: seed holds the size known of it, and probe asks it the rest
// (probeTerminal for this process's terminal). A client the e2e suite runs in
// its own process probes the terminal it is given through ProbeTerminal.
func DetectHostCapabilitiesWith(seed HostCapabilities, probe func(*HostCapabilities)) *HostCapabilities {
	caps := &seed
	caps.Term = os.Getenv("TERM")

	detectTerminalName(caps)

	detectTrueColor(caps)

	probe(caps)

	applyEnvironmentOverrides(caps)

	if os.Getenv("TUIOS_DEBUG_CAPS") == "1" {
		writeCapabilitiesDebug(caps)
	}

	return caps
}

func capabilitiesDebugPath() string {
	name := fmt.Sprintf("tuios-caps.%d.log", os.Getpid())
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" && serverscope.Name() == "" {
		return filepath.Join(dir, "tuios", name)
	}
	return filepath.Join(xdg.StateHome, "tuios", name)
}

func writeCapabilitiesDebug(caps *HostCapabilities) {
	path := capabilitiesDebugPath()
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "Terminal: %s\nKitty: %v\nSixel: %v\nTrueColor: %v\nCell: %dx%d\nPixel: %dx%d\n",
		caps.TerminalName, caps.KittyGraphics, caps.SixelGraphics, caps.TrueColor,
		caps.CellWidth, caps.CellHeight, caps.PixelWidth, caps.PixelHeight)
	if caps.ANSIMask != 0 || caps.HasFg || caps.HasBg {
		_, _ = fmt.Fprintf(f, "Palette: mask=%04x fg=%06x(%t) bg=%06x(%t)\n",
			caps.ANSIMask, caps.Fg, caps.HasFg, caps.Bg, caps.HasBg)
		for i, c := range caps.ANSI {
			if caps.ANSIMask&(1<<uint(i)) != 0 {
				_, _ = fmt.Fprintf(f, "  ansi[%2d] = #%06x\n", i, c)
			}
		}
	} else {
		_, _ = fmt.Fprintf(f, "Palette: the terminal answered no colour queries\n")
	}
	_ = f.Close()
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
	if termProgram == "tern" {
		caps.TerminalName = "tern"
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

const probeTimeout = 300 * time.Millisecond

var da1Response = lazyre.New(`\x1b\[\?[0-9;]*c`)

type probeTTY struct {
	in, out *os.File
	close   func()
}

func probeTerminal(caps *HostCapabilities) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		setDefaultCellSize(caps)
		return
	}

	tty, err := openProbeTTY()
	if err != nil {
		setDefaultCellSize(caps)
		return
	}
	defer tty.close()

	oldState, err := term.MakeRaw(int(tty.in.Fd()))
	if err != nil {
		setDefaultCellSize(caps)
		return
	}
	defer func() { _ = term.Restore(int(tty.in.Fd()), oldState) }()

	ProbeTerminal(caps, tty.out, func(timeout time.Duration, done func(string) bool) string {
		return readTTYResponse(tty.in, timeout, done)
	})
}

// ProbeTerminal writes the probe's queries to out and reads the replies with
// read, which returns what arrived once done reports the reply complete or
// the timeout runs out. See probeTerminal.
func ProbeTerminal(caps *HostCapabilities, out io.Writer, read func(timeout time.Duration, done func(string) bool) string) {
	probeFile, probeFileErr := writeGraphicsProbeFile()
	if probeFileErr == nil {
		defer func() { _ = os.Remove(probeFile) }()
	}

	var q strings.Builder
	q.WriteString("\x1b[14t")
	q.WriteString("\x1b[16t")
	q.WriteString("\x1b_Gi=1,a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\")
	if probeFileErr == nil {
		fmt.Fprintf(&q, "\x1b_Gi=2,a=q,t=f,f=24,s=1,v=1;%s\x1b\\",
			base64.StdEncoding.EncodeToString([]byte(probeFile)))
	}
	writeAnimationProbe(&q)
	writeFontQuery(&q)
	writePaletteQuery(&q)
	q.WriteString(xtversionQuery)
	q.WriteString("\x1b[c")
	_, _ = io.WriteString(out, q.String())

	response := read(probeTimeout, da1Response().MatchString)
	caps.DA1Late = !da1Response().MatchString(response)

	parsePixelGeometry(caps, response)
	parseGraphicsSupport(caps, response, probeFileErr == nil)
	parseHostFont(caps, response)
	parseHostPalette(caps, response)
	caps.KittyPlaceholders = caps.KittyGraphics && hostDrawsPlaceholders(response)
	dropCellBoundGraphics(caps, response, os.Getenv)
}

func writePaletteQuery(q *strings.Builder) {
	for i := range 16 {
		fmt.Fprintf(q, "\x1b]4;%d;?\x1b\\", i)
	}
	q.WriteString("\x1b]10;?\x1b\\")
	q.WriteString("\x1b]11;?\x1b\\")
}

var oscColorReply = lazyre.New(`\x1b\](4;(\d+)|10|11);rgb:([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})(?:\x1b\\|\x07)`)

func parseHostPalette(caps *HostCapabilities, response string) {
	for _, m := range oscColorReply().FindAllStringSubmatch(response, -1) {
		packed := packOSCColor(m[3], m[4], m[5])
		switch {
		case strings.HasPrefix(m[1], "4;"):
			idx, err := strconv.Atoi(m[2])
			if err != nil || idx < 0 || idx > 15 {
				continue
			}
			caps.ANSI[idx] = packed
			caps.ANSIMask |= 1 << uint(idx)
		case m[1] == "10":
			caps.Fg, caps.HasFg = packed, true
		case m[1] == "11":
			caps.Bg, caps.HasBg = packed, true
		}
	}
}

func packOSCColor(r, g, b string) uint32 {
	return uint32(scaleOSCChannel(r))<<16 | uint32(scaleOSCChannel(g))<<8 | uint32(scaleOSCChannel(b))
}

func scaleOSCChannel(s string) uint8 {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) == 0 {
		return 0
	}
	maxV := uint64(1)<<(4*len(s)) - 1
	if maxV == 0 {
		return 0
	}
	return uint8((v*255 + maxV/2) / maxV)
}

var fontQueryKeys = []string{"kitty-query-font_family", "kitty-query-bold_font"}

func writeFontQuery(q *strings.Builder) {
	for _, key := range fontQueryKeys {
		fmt.Fprintf(q, "\x1bP+q%s\x1b\\", hex.EncodeToString([]byte(key)))
	}
}

var xtgettcapReply = lazyre.New(`\x1bP1\+r([0-9a-fA-F]+)=([0-9a-fA-F]*)\x1b\\`)

func parseHostFont(caps *HostCapabilities, response string) {
	for _, m := range xtgettcapReply().FindAllStringSubmatch(response, -1) {
		key, kerr := hex.DecodeString(m[1])
		value, verr := hex.DecodeString(m[2])
		if kerr != nil || verr != nil || len(value) == 0 {
			continue
		}
		switch string(key) {
		case "kitty-query-font_family":
			caps.FontFamily = string(value)
		case "kitty-query-bold_font":
			caps.BoldFontFamily = string(value)
		}
	}
}

func parseGraphicsSupport(caps *HostCapabilities, response string, probedFile bool) {
	da1Re := regexp.MustCompile(`\x1b\[\?([0-9;]+)c`)
	if matches := da1Re.FindStringSubmatch(response); len(matches) >= 2 {
		if slices.Contains(slices.Collect(strings.SplitSeq(matches[1], ";")), "4") {
			caps.SixelGraphics = true
		}
	}

	if strings.Contains(response, "OK") {
		caps.KittyGraphics = true
	}
	caps.KittyFileTransfer = probedFile && kittyProbeOK(response, 2)
	caps.KittyAnimation = caps.KittyGraphics &&
		kittyProbeOK(response, animationProbeAccept) &&
		kittyProbeRefused(response, animationProbeReject)
}

const (
	animationProbeAccept = 3
	animationProbeReject = 4
)

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

func kittyProbeOK(response string, id int) bool {
	answer, answered := kittyProbeAnswer(response, id)
	return answered && strings.HasPrefix(answer, "OK")
}

func kittyProbeRefused(response string, id int) bool {
	answer, answered := kittyProbeAnswer(response, id)
	return answered && !strings.HasPrefix(answer, "OK")
}

var kittyProbeResponse = lazyre.New(`\x1b_G([^;\x1b]*);([^\x1b]*)\x1b\\`)

func kittyProbeAnswer(response string, id int) (string, bool) {
	want := fmt.Sprintf("i=%d", id)
	for _, m := range kittyProbeResponse().FindAllStringSubmatch(response, -1) {
		if !slices.Contains(strings.Split(m[1], ","), want) {
			continue
		}
		return m[2], true
	}
	return "", false
}

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

var errPollInterrupted = errors.New("poll interrupted by a signal")

func readTTYResponse(tty *os.File, timeout time.Duration, done func(string) bool) string {
	buf := make([]byte, 512)
	var result strings.Builder
	deadline := time.Now().Add(timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}

		ready, err := pollReadable(tty.Fd(), remaining)
		if errors.Is(err, errPollInterrupted) {
			continue
		}
		if err != nil || !ready {
			break
		}

		n, err := tty.Read(buf)
		if err != nil {
			break
		}
		if n > 0 {
			result.Write(buf[:n])
			if done(result.String()) && !endsInsideSequence(result.String()) {
				break
			}
		}
	}

	return result.String()
}

func endsInsideSequence(s string) bool {
	const (
		ground = iota
		escape
		csi
		str
		oscStr
		strEsc
	)
	state, inStr := ground, str
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case ground:
			if c == 0x1b {
				state = escape
			}
		case escape:
			switch c {
			case '[':
				state = csi
			case '_', 'P', '^', 'X':
				state, inStr = str, str
			case ']':
				state, inStr = oscStr, oscStr
			default:
				state = ground
			}
		case csi:
			if c >= 0x40 && c <= 0x7e {
				state = ground
			}
		case str, oscStr:
			switch {
			case c == 0x1b:
				state = strEsc
			case c == 0x07 && state == oscStr:
				state = ground
			}
		case strEsc:
			if c == '\\' {
				state = ground
			} else {
				state = inStr
			}
		}
	}
	return state != ground
}

func applyEnvironmentOverrides(caps *HostCapabilities) {
	switch os.Getenv("TUIOS_KITTY_GRAPHICS") {
	case "1":
		caps.KittyGraphics = true
		caps.KittyPinned = true
	case "0":
		caps.KittyGraphics = false
		caps.KittyPinned = true
	}

	switch os.Getenv("TUIOS_KITTY_PLACEHOLDERS") {
	case "1":
		caps.KittyPlaceholders = true
	case "0":
		caps.KittyPlaceholders = false
	}

	switch os.Getenv("TUIOS_SIXEL_GRAPHICS") {
	case "1":
		caps.SixelGraphics = true
		caps.SixelPinned = true
	case "0":
		caps.SixelGraphics = false
		caps.SixelPinned = true
	case "":
	default:
		caps.Warnings = append(caps.Warnings, fmt.Sprintf(
			"TUIOS_SIXEL_GRAPHICS is %q. Set it to 1 or 0. tuios ignores the value and asks the terminal.",
			os.Getenv("TUIOS_SIXEL_GRAPHICS")))
	}

	switch os.Getenv("TUIOS_KITTY_ANIMATION") {
	case "1":
		caps.KittyAnimation = true
	case "0":
		caps.KittyAnimation = false
	}

	applyCellSizeOverride(caps, os.Getenv("TUIOS_CELL_SIZE"))
}

func applyCellSizeOverride(caps *HostCapabilities, spec string) {
	cw, ch, ok := ParseCellSize(spec)
	if !ok {
		return
	}
	caps.CellWidth, caps.CellHeight = cw, ch
	if caps.Cols > 0 {
		caps.PixelWidth = cw * caps.Cols
	}
	if caps.Rows > 0 {
		caps.PixelHeight = ch * caps.Rows
	}
}

var (
	windowPixels = lazyre.New(`\x1b\[4;(\d+);(\d+)t`)
	cellPixels   = lazyre.New(`\x1b\[6;(\d+);(\d+)t`)
)

func parsePixelGeometry(caps *HostCapabilities, response string) {
	if matches := windowPixels().FindStringSubmatch(response); len(matches) == 3 {
		caps.PixelHeight, _ = strconv.Atoi(matches[1])
		caps.PixelWidth, _ = strconv.Atoi(matches[2])
	}
	if matches := cellPixels().FindStringSubmatch(response); len(matches) == 3 {
		caps.CellHeight, _ = strconv.Atoi(matches[1])
		caps.CellWidth, _ = strconv.Atoi(matches[2])
	}

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
		caps.CellWidth = vt.FallbackCellWidth
	}
	if caps.CellHeight == 0 {
		caps.CellHeight = vt.FallbackCellHeight
	}
}

// ParseCellSize reads a WIDTHxHEIGHT cell size in pixels, as TUIOS_CELL_SIZE
// gives it, for this client and for the SSH server alike. ok is false unless
// both numbers are positive.
func ParseCellSize(spec string) (w, h int, ok bool) {
	left, right, cut := strings.Cut(strings.ToLower(spec), "x")
	if !cut {
		return 0, 0, false
	}
	w, err := strconv.Atoi(strings.TrimSpace(left))
	if err != nil || w <= 0 {
		return 0, 0, false
	}
	h, err = strconv.Atoi(strings.TrimSpace(right))
	if err != nil || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}
