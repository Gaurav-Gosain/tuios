package app

import (
	"bufio"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HostCapabilities holds information about the host terminal's capabilities.
// These are used to determine which features TUIOS can use for rendering.
type HostCapabilities struct {
	KittyGraphics bool
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

func GetHostCapabilities() *HostCapabilities {
	if cachedCapabilities == nil {
		cachedCapabilities = DetectHostCapabilities()
	}
	return cachedCapabilities
}

func ResetHostCapabilities() {
	cachedCapabilities = nil
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
	detectFromEnvironment(caps)
	queryPixelDimensions(caps)
	return caps
}

func detectFromEnvironment(caps *HostCapabilities) {
	term := strings.ToLower(os.Getenv("TERM"))
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))

	switch {
	case strings.Contains(termProgram, "ghostty"):
		caps.TerminalName = "ghostty"
		caps.KittyGraphics = true
		caps.TrueColor = true
	case strings.Contains(termProgram, "kitty"):
		caps.TerminalName = "kitty"
		caps.KittyGraphics = true
		caps.TrueColor = true
	case strings.Contains(termProgram, "wezterm"):
		caps.TerminalName = "wezterm"
		caps.KittyGraphics = true
		caps.TrueColor = true
	case strings.Contains(termProgram, "konsole"):
		caps.TerminalName = "konsole"
		caps.KittyGraphics = true
		caps.TrueColor = true
	case strings.Contains(termProgram, "iterm"):
		caps.TerminalName = "iterm2"
		caps.TrueColor = true
	case strings.Contains(termProgram, "alacritty"):
		caps.TerminalName = "alacritty"
		caps.TrueColor = true
	case strings.Contains(term, "xterm"):
		caps.TerminalName = "xterm"
		caps.TrueColor = strings.Contains(term, "256color") || strings.Contains(term, "direct")
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" {
		caps.TerminalName = "kitty"
		caps.KittyGraphics = true
		caps.TrueColor = true
	}

	if os.Getenv("WEZTERM_PANE") != "" {
		caps.TerminalName = "wezterm"
		caps.KittyGraphics = true
		caps.TrueColor = true
	}

	if colorterm == "truecolor" || colorterm == "24bit" {
		caps.TrueColor = true
	}

	if strings.Contains(term, "256color") || strings.Contains(term, "truecolor") || strings.Contains(term, "direct") {
		caps.TrueColor = true
	}

	if os.Getenv("TUIOS_KITTY_GRAPHICS") == "1" {
		caps.KittyGraphics = true
	} else if os.Getenv("TUIOS_KITTY_GRAPHICS") == "0" {
		caps.KittyGraphics = false
	}
}

func queryPixelDimensions(caps *HostCapabilities) {
	if !isTerminal(os.Stdin.Fd()) {
		setDefaultCellSize(caps)
		return
	}

	oldState, err := makeRaw(os.Stdin.Fd())
	if err != nil {
		setDefaultCellSize(caps)
		return
	}
	defer restoreTerminal(os.Stdin.Fd(), oldState)

	pixelWidth, pixelHeight := queryWindowSize()
	if pixelWidth > 0 && pixelHeight > 0 {
		caps.PixelWidth = pixelWidth
		caps.PixelHeight = pixelHeight
	}

	cellWidth, cellHeight := queryCellSize()
	if cellWidth > 0 && cellHeight > 0 {
		caps.CellWidth = cellWidth
		caps.CellHeight = cellHeight
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
	// Calculate from pixel/cell ratio if available
	if caps.PixelWidth > 0 && caps.Cols > 0 && caps.CellWidth == 0 {
		caps.CellWidth = caps.PixelWidth / caps.Cols
	}
	if caps.PixelHeight > 0 && caps.Rows > 0 && caps.CellHeight == 0 {
		caps.CellHeight = caps.PixelHeight / caps.Rows
	}

	// Terminal-specific defaults based on common font sizes
	if caps.CellWidth == 0 {
		switch caps.TerminalName {
		case "kitty", "wezterm":
			caps.CellWidth = 9 // Common for these terminals
		case "ghostty":
			caps.CellWidth = 10
		default:
			caps.CellWidth = 9 // Common monospace default
		}
	}
	if caps.CellHeight == 0 {
		switch caps.TerminalName {
		case "kitty", "wezterm":
			caps.CellHeight = 20 // Common for these terminals
		case "ghostty":
			caps.CellHeight = 22
		default:
			caps.CellHeight = 20 // Common monospace default
		}
	}
}

func queryWindowSize() (width, height int) {
	os.Stdout.WriteString("\x1b[14t")

	response := readTerminalResponse(100 * time.Millisecond)
	if response == "" {
		return 0, 0
	}

	re := regexp.MustCompile(`\x1b\[4;(\d+);(\d+)t`)
	matches := re.FindStringSubmatch(response)
	if len(matches) == 3 {
		height, _ = strconv.Atoi(matches[1])
		width, _ = strconv.Atoi(matches[2])
	}

	return width, height
}

func queryCellSize() (width, height int) {
	os.Stdout.WriteString("\x1b[16t")

	response := readTerminalResponse(100 * time.Millisecond)
	if response == "" {
		return 0, 0
	}

	re := regexp.MustCompile(`\x1b\[6;(\d+);(\d+)t`)
	matches := re.FindStringSubmatch(response)
	if len(matches) == 3 {
		height, _ = strconv.Atoi(matches[1])
		width, _ = strconv.Atoi(matches[2])
	}

	return width, height
}

func readTerminalResponse(timeout time.Duration) string {
	done := make(chan string, 1)

	go func() {
		reader := bufio.NewReader(os.Stdin)
		var result strings.Builder
		deadline := time.Now().Add(timeout)

		for time.Now().Before(deadline) {
			if reader.Buffered() == 0 {
				time.Sleep(5 * time.Millisecond)
				continue
			}

			b, err := reader.ReadByte()
			if err != nil {
				break
			}
			result.WriteByte(b)

			if b >= 'a' && b <= 'z' {
				break
			}
		}

		done <- result.String()
	}()

	select {
	case result := <-done:
		return result
	case <-time.After(timeout):
		return ""
	}
}
