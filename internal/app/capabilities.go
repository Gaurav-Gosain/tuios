package app

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// HostCapabilities holds information about the host terminal's capabilities.
// These are used to determine which features TUIOS can use for rendering.
type HostCapabilities struct {
	KittyGraphics bool
	SixelGraphics bool
	TrueColor     bool
	TerminalName  string
	PixelWidth    int
	PixelHeight   int
	CellWidth     int
	CellHeight    int
	Cols          int
	Rows          int
}

var cachedCapabilities *HostCapabilities

// clientCapabilities holds capabilities received from the daemon client.
// These override detected capabilities when running in daemon mode.
var clientCapabilities *HostCapabilities

func GetHostCapabilities() *HostCapabilities {
	// Prefer client-provided capabilities (for daemon mode)
	if clientCapabilities != nil {
		return clientCapabilities
	}
	if cachedCapabilities == nil {
		cachedCapabilities = DetectHostCapabilities()
	}
	return cachedCapabilities
}

func ResetHostCapabilities() {
	cachedCapabilities = nil
	clientCapabilities = nil
}

// SetClientCapabilities sets capabilities received from a daemon client.
// This is used in daemon mode where the client has access to the real terminal.
func SetClientCapabilities(caps *HostCapabilities) {
	clientCapabilities = caps
}

// GetClientCapabilities returns the client-provided capabilities, or nil if not set.
func GetClientCapabilities() *HostCapabilities {
	return clientCapabilities
}

func UpdateHostDimensions(cols, rows, pixelWidth, pixelHeight int) {
	caps := GetHostCapabilities()
	caps.Cols = cols
	caps.Rows = rows
	caps.PixelWidth = pixelWidth
	caps.PixelHeight = pixelHeight
	if cols > 0 && pixelWidth > 0 {
		caps.CellWidth = pixelWidth / cols
	}
	if rows > 0 && pixelHeight > 0 {
		caps.CellHeight = pixelHeight / rows
	}
}

func DetectHostCapabilities() *HostCapabilities {
	caps := &HostCapabilities{}

	// Detect terminal name from environment
	detectTerminalName(caps)

	// Detect truecolor from environment
	detectTrueColor(caps)

	// Query terminal size (cols/rows)
	queryTerminalSize(caps)

	// Query terminal for dimensions (fast, well-supported)
	queryPixelDimensions(caps)

	// Query graphics support - use fast method with fallback
	queryGraphicsSupport(caps)

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

func queryGraphicsSupport(caps *HostCapabilities) {
	if !isTerminal(os.Stdin.Fd()) {
		return
	}

	// Open /dev/tty for queries to avoid messing with stdin
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer func() { _ = tty.Close() }()

	oldState, err := makeRaw(tty.Fd())
	if err != nil {
		return
	}
	defer restoreTerminal(tty.Fd(), oldState)

	// Send both queries at once
	_, _ = tty.WriteString("\x1b[c")                                    // DA1 for sixel
	_, _ = tty.WriteString("\x1b_Gi=1,a=q,t=d,f=24,s=1,v=1;AAAA\x1b\\") // Kitty graphics query

	// Read response with timeout (300ms to account for slower terminals)
	response := readTTYResponse(tty, 300*time.Millisecond)

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
}

// readTTYResponse reads from tty with a timeout using poll-based I/O
func readTTYResponse(tty *os.File, timeout time.Duration) string {
	buf := make([]byte, 512)
	var result strings.Builder
	terminators := 0
	deadline := time.Now().Add(timeout)

	for terminators < 2 {
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
			for i := range n {
				if buf[i] == 'c' || buf[i] == '\\' {
					terminators++
				}
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
}

func queryPixelDimensions(caps *HostCapabilities) {
	if !isTerminal(os.Stdin.Fd()) {
		setDefaultCellSize(caps)
		return
	}

	// Use /dev/tty for queries
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

	// Query window size in pixels
	_, _ = tty.WriteString("\x1b[14t")
	response := readTTYResponse(tty, 100*time.Millisecond)
	if re := regexp.MustCompile(`\x1b\[4;(\d+);(\d+)t`); response != "" {
		if matches := re.FindStringSubmatch(response); len(matches) == 3 {
			caps.PixelHeight, _ = strconv.Atoi(matches[1])
			caps.PixelWidth, _ = strconv.Atoi(matches[2])
		}
	}

	// Query cell size
	_, _ = tty.WriteString("\x1b[16t")
	response = readTTYResponse(tty, 100*time.Millisecond)
	if re := regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`); response != "" {
		if matches := re.FindStringSubmatch(response); len(matches) == 3 {
			caps.CellHeight, _ = strconv.Atoi(matches[1])
			caps.CellWidth, _ = strconv.Atoi(matches[2])
		}
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
