package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The render trace is a diagnostic for panes that go blank when focus moves to
// another window. It is off unless TUIOS_RENDER_TRACE names it, and the check
// is a single package-level bool read on the hot path, resolved once at
// startup, so a normal run pays one predictable branch per window per frame and
// no syscall.
//
// Set TUIOS_RENDER_TRACE to 1, true, on, or yes to write to the default path,
// or to an explicit file path to choose where it lands.
var (
	renderTraceEnabled bool
	renderTracePath    string

	renderTraceMu sync.Mutex
	renderTraceFH *os.File
	renderTraceT0 time.Time
)

func init() {
	v := strings.TrimSpace(os.Getenv("TUIOS_RENDER_TRACE"))
	if v == "" || v == "0" || strings.EqualFold(v, "false") || strings.EqualFold(v, "off") {
		return
	}
	switch {
	case v == "1", strings.EqualFold(v, "true"), strings.EqualFold(v, "on"), strings.EqualFold(v, "yes"):
		renderTracePath = defaultRenderTracePath()
	default:
		renderTracePath = v
	}

	if dir := filepath.Dir(renderTracePath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	fh, err := os.OpenFile(renderTracePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		// Tracing is a diagnostic; never take the session down over it. stdout
		// is the TUI, so there is nowhere useful to report this.
		return
	}
	renderTraceFH = fh
	renderTraceT0 = time.Now()
	renderTraceEnabled = true
	fmt.Fprintf(fh, "\n=== tuios render trace started %s pid=%d ===\n",
		renderTraceT0.Format(time.RFC3339), os.Getpid())
}

// defaultRenderTracePath puts the trace under the state directory when the
// environment defines one, and falls back to the temp directory. The pid keeps
// a daemon and its attached clients in separate files.
func defaultRenderTracePath() string {
	name := fmt.Sprintf("tuios-render-trace.%d.log", os.Getpid())
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tuios", name)
	}
	return filepath.Join(os.TempDir(), name)
}

// RenderTracePath reports where the trace is being written, or the empty string
// when tracing is off.
func RenderTracePath() string {
	if !renderTraceEnabled {
		return ""
	}
	return renderTracePath
}

func traceWrite(line string) {
	renderTraceMu.Lock()
	defer renderTraceMu.Unlock()
	if renderTraceFH == nil {
		return
	}
	fmt.Fprintf(renderTraceFH, "%8.3f %s\n", time.Since(renderTraceT0).Seconds(), line)
}

// shortID trims a window ID to something readable in a log line.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// traceSample summarises a rendered string so the fast and slow paths can be
// compared directly. The blank case is the one under investigation: the fast
// path drops trailing blank lines and trailing spaces and does not pad to the
// content box, so a collapsed or empty render shows up here as a small byte
// count and a low line count against the slow path's full padded grid.
func traceSample(s string) string {
	lines := 0
	if s != "" {
		lines = strings.Count(s, "\n") + 1
	}
	head, tail := s, ""
	if len(s) > 60 {
		head = s[:60]
		if len(s) > 120 {
			tail = s[len(s)-60:]
		} else {
			tail = s[60:]
		}
	}
	blank := strings.TrimSpace(stripANSIForTrace(s)) == ""
	return fmt.Sprintf("bytes=%d lines=%d blank=%t head=%q tail=%q",
		len(s), lines, blank, head, tail)
}

// stripANSIForTrace removes escape sequences so the blank check reports whether
// a frame carries visible text, not whether it carries styling.
func stripANSIForTrace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// traceRender records one window's render for one frame: which early return the
// renderer took, the emulator and window geometry it was working against, the
// alternate screen state as both the mode bits and the active buffer pointer
// report it, and a summary of the bytes produced.
//
// branch is the path taken through renderTerminal, and out is what that path
// returned, so a focus change can be read as a pair of lines for the same
// window that differ only in branch.
func traceRender(window *terminal.Window, isFocused, inTerminalMode bool, branch, out string) {
	if !renderTraceEnabled || window == nil {
		return
	}
	emuW, emuH := -1, -1
	altMode, altBuf := false, false
	if window.Terminal != nil {
		emuW = window.Terminal.Width()
		emuH = window.Terminal.Height()
		altMode = window.Terminal.IsAltScreen()
		altBuf = window.Terminal.ActiveScreenIsAlt()
	}
	traceWrite(fmt.Sprintf(
		"render id=%s title=%q focused=%t termMode=%t branch=%-22s "+
			"contentDirty=%t cachedLen=%d altMode=%t altBuf=%t winAlt=%t "+
			"emu=%dx%d content=%dx%d tiled=%t zoomed=%t min=%t %s",
		shortID(window.ID), window.Title(), isFocused, inTerminalMode, branch,
		window.ContentDirty, len(window.CachedContent), altMode, altBuf, window.IsAltScreen(),
		emuW, emuH, window.ContentWidth(), window.ContentHeight(),
		window.Tiled, window.Zoomed, window.Minimized,
		traceSample(out),
	))
}

// traceSync records one window's pass through the daemon state sync. This path
// only runs when the daemon broadcasts, which it does when more than one client
// is attached, so these lines also answer whether the session had a second
// client at all.
func traceSync(w *terminal.Window, incomingAlt bool, resized bool, newW, newH int, note string) {
	if !renderTraceEnabled || w == nil {
		return
	}
	altMode, altBuf := false, false
	if w.Terminal != nil {
		altMode = w.Terminal.IsAltScreen()
		altBuf = w.Terminal.ActiveScreenIsAlt()
	}
	traceWrite(fmt.Sprintf(
		"sync   id=%s title=%q incomingAlt=%t resized=%t newSize=%dx%d "+
			"altMode=%t altBuf=%t winAlt=%t tiled=%t %s",
		shortID(w.ID), w.Title(), incomingAlt, resized, newW, newH,
		altMode, altBuf, w.IsAltScreen(), w.Tiled, note,
	))
}
