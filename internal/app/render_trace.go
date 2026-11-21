package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/debuglog"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

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
		_ = os.MkdirAll(dir, 0o700)
	}
	fh, err := debuglog.Open(renderTracePath)
	if err != nil {
		return
	}
	renderTraceFH = fh
	renderTraceT0 = time.Now()
	renderTraceEnabled = true
	terminal.AnnounceTrace = traceAnnounce
	fmt.Fprintf(fh, "\n=== tuios render trace started %s pid=%d ===\n",
		renderTraceT0.Format(time.RFC3339), os.Getpid())
}

func defaultRenderTracePath() string {
	name := fmt.Sprintf("tuios-render-trace.%d.log", os.Getpid())
	if serverscope.Name() != "" {
		return filepath.Join(xdg.StateHome, "tuios", name)
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tuios", name)
	}
	return filepath.Join(os.TempDir(), name)
}

func traceWrite(line string) {
	renderTraceMu.Lock()
	defer renderTraceMu.Unlock()
	if renderTraceFH == nil {
		return
	}
	fmt.Fprintf(renderTraceFH, "%8.3f %s\n", time.Since(renderTraceT0).Seconds(), line)
}

func (m *OS) traceFrame() {
	var b strings.Builder
	fmt.Fprintf(&b, "frame anims=%d", len(m.Animations))
	for _, w := range m.Windows {
		if w == nil || w.Workspace != m.CurrentWorkspace || w.Minimized {
			continue
		}
		fmt.Fprintf(&b, " %s=%d,%d,%d,%d", shortID(w.ID), w.X, w.Y, w.Width, w.Height)
	}
	traceWrite(b.String())
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

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

func traceRender(window *terminal.Window, isFocused, inTerminalMode, entryDirty bool, branch, out string) {
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
			"dirtyIn=%t dirtyOut=%t cachedLen=%d altMode=%t altBuf=%t winAlt=%t "+
			"emu=%dx%d content=%dx%d tiled=%t zoomed=%t min=%t %s",
		shortID(window.ID), window.Title(), isFocused, inTerminalMode, branch,
		entryDirty, window.ContentDirty, len(window.CachedContent), altMode, altBuf, window.IsAltScreen(),
		emuW, emuH, window.ContentWidth(), window.ContentHeight(),
		window.Tiled, window.Zoomed, window.Minimized,
		traceSample(out),
	))
}

func traceLayerBuild(window *terminal.Window, isFocused bool, boxContent, clipped string,
	wx, wy, finalX, finalY, z, viewportW, viewportH int,
) {
	if !renderTraceEnabled || window == nil {
		return
	}
	traceWrite(fmt.Sprintf(
		"layerb id=%s title=%q focused=%t tiled=%t win=(%d,%d) %dx%d final=(%d,%d) z=%d "+
			"viewport=%dx%d boxBytes=%d boxLines=%d clipBytes=%d clipLines=%d clipBlank=%t "+
			"boxHead=%q",
		shortID(window.ID), window.Title(), isFocused, window.Tiled,
		wx, wy, window.Width, window.Height, finalX, finalY, z,
		viewportW, viewportH,
		len(boxContent), strings.Count(boxContent, "\n")+1,
		len(clipped), strings.Count(clipped, "\n")+1,
		strings.TrimSpace(stripANSIForTrace(clipped)) == "",
		firstN(boxContent, 40),
	))
}

func traceLayerHold(window *terminal.Window, isFocused bool, reason string) {
	if !renderTraceEnabled || window == nil {
		return
	}
	syncActive := false
	altBuf := false
	if window.Terminal != nil {
		syncActive = window.Terminal.IsSyncActive()
		altBuf = window.Terminal.ActiveScreenIsAlt()
	}
	cachedBlank := true
	if window.CachedLayer != nil {
		cachedBlank = strings.TrimSpace(stripANSIForTrace(window.CachedContent)) == ""
	}
	traceWrite(fmt.Sprintf(
		"hold   id=%s title=%q focused=%t reason=%-16s dirty=%t contentDirty=%t posDirty=%t "+
			"syncActive=%t altBuf=%t cachedLen=%d cachedBlank=%t hasLayer=%t",
		shortID(window.ID), window.Title(), isFocused, reason,
		window.Dirty, window.ContentDirty, window.PositionDirty,
		syncActive, altBuf, len(window.CachedContent), cachedBlank,
		window.CachedLayer != nil,
	))
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

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

func traceAnnounce(w *terminal.Window, cols, rows int) {
	traceWrite(fmt.Sprintf("announce id=%s title=%q size=%dx%d rect=%dx%d tiled=%t via %s",
		shortID(w.ID), w.Title(), cols, rows, w.Width, w.Height, w.Tiled, traceCallers(3, 8)))
}

func traceCallers(skip, n int) string {
	pcs := make([]uintptr, n)
	got := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:got])
	var names []string
	for {
		f, more := frames.Next()
		name := f.Function
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		names = append(names, name)
		if !more {
			break
		}
	}
	return strings.Join(names, " < ")
}
