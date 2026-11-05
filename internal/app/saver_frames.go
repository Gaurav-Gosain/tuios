package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/charmbracelet/x/ansi"
)

// A frames file: a screen saver a plugin brings as data, which tuios plays
// with no process. docs/PLUGINS.md has the format, so a tool can write it.
//
// The file is read and every frame drawn once when the saver starts, as its
// own box with no margin. Playing it costs one timer per frame and placing
// one box on the screen. The box keeps no blank rows or trailing blanks, so
// what the saver holds grows with the file, not with the screen.

// PluginSavers, when set, maps a saver's name, plugin/ID/SAVER, to its frames
// file, for the enabled plugins. The binary sets it, since this package does
// not read plugins.
var PluginSavers func() map[string]string

// Bounds on a frames file, so a file cannot make tuios hold or draw much.
const (
	saverMaxBytes  = 4 << 20
	saverMaxCols   = 400
	saverMaxRows   = 200
	saverMaxFrames = 2000
	saverMinDelay  = 20 * time.Millisecond
	// saverMaxDrawn bounds the drawn frames together. A file of single-cell
	// colour runs draws to about twenty bytes a cell, so this is what keeps
	// a file under saverMaxBytes from drawing to a hundred megabytes.
	saverMaxDrawn = 16 << 20
)

// saverColorNames are the ANSI colours a colors block can name, in index
// order. They follow the theme.
var saverColorNames = []string{
	"black", "red", "green", "yellow", "blue", "purple", "cyan", "white",
	"bright_black", "bright_red", "bright_green", "bright_yellow", "bright_blue", "bright_purple", "bright_cyan", "bright_white",
}

// saverFrame is one frame of a frames file: its text, its colour letters and
// how long it shows.
type saverFrame struct {
	text, colors []string
	delay        time.Duration
}

// saverFile is a parsed frames file. Every frame has rows lines of text,
// and of colours when it has any: size comes once, before the first frame.
type saverFile struct {
	cols, rows int
	palette    map[byte]string // the SGR that starts a run of each letter
	frames     []saverFrame
}

// parseSaverFile reads a frames file. An error says the line and what is
// wrong, for tuios plugins check.
func parseSaverFile(data []byte) (*saverFile, error) {
	if len(data) > saverMaxBytes {
		return nil, errfmt.Errorf("the file is larger than %d bytes", saverMaxBytes)
	}
	f := &saverFile{palette: map[byte]string{}}
	delay := 100 * time.Millisecond
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), saverMaxBytes)
	n := 0
	next := func() (string, bool) {
		if !sc.Scan() {
			return "", false
		}
		n++
		return strings.TrimRight(sc.Text(), "\r"), true
	}
	fail := func(format string, a ...any) error {
		return errfmt.Errorf("line %d: %s", n, fmt.Sprintf(format, a...))
	}
	if line, ok := next(); !ok || strings.TrimSpace(line) != "tuios-saver 1" {
		return nil, fail("the first line must be tuios-saver 1")
	}
	block := func() ([]string, error) {
		lines := make([]string, f.rows)
		for i := range lines {
			line, ok := next()
			if !ok {
				return nil, fail("the file ends inside a block of %d lines", f.rows)
			}
			lines[i] = ansi.Strip(line)
		}
		return lines, nil
	}
	for {
		line, ok := next()
		if !ok {
			break
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch fields[0] {
		case "size":
			if len(fields) != 3 {
				return nil, fail("size takes the columns and the rows, such as size 40 10")
			}
			c, err1 := strconv.Atoi(fields[1])
			r, err2 := strconv.Atoi(fields[2])
			if err1 != nil || err2 != nil || c < 1 || r < 1 || c > saverMaxCols || r > saverMaxRows {
				return nil, fail("size must be 1 to %d columns and 1 to %d rows", saverMaxCols, saverMaxRows)
			}
			// render reads every frame at the one size, so a second size
			// would leave the frames before it short.
			if len(f.frames) > 0 {
				return nil, fail("size comes once, before the first frame")
			}
			f.cols, f.rows = c, r
		case "fps":
			v, err := strconv.Atoi(get(fields, 1))
			if err != nil || v < 1 || v > 50 {
				return nil, fail("fps must be 1 to 50")
			}
			delay = time.Second / time.Duration(v)
		case "color":
			if len(fields) != 3 || len(fields[1]) != 1 || fields[1] == " " {
				return nil, fail("color takes one letter and a colour, such as color a #ff5f87")
			}
			st, err := saverStyle(fields[2])
			if err != nil {
				return nil, fail("%v", err)
			}
			f.palette[fields[1][0]] = st
		case "frame":
			if f.rows == 0 {
				return nil, fail("size must come before the first frame")
			}
			if len(f.frames) == saverMaxFrames {
				return nil, fail("a file can have %d frames", saverMaxFrames)
			}
			fr := saverFrame{delay: delay}
			if len(fields) > 1 {
				ms, err := strconv.Atoi(fields[1])
				if err != nil || ms < 1 {
					return nil, fail("frame takes a time in milliseconds, such as frame 500")
				}
				fr.delay = time.Duration(ms) * time.Millisecond
			}
			fr.delay = max(fr.delay, saverMinDelay)
			var err error
			if fr.text, err = block(); err != nil {
				return nil, err
			}
			f.frames = append(f.frames, fr)
		case "colors":
			if len(f.frames) == 0 || f.frames[len(f.frames)-1].colors != nil {
				return nil, fail("a colors block follows its frame, once")
			}
			lines, err := block()
			if err != nil {
				return nil, err
			}
			f.frames[len(f.frames)-1].colors = lines
		default:
			return nil, fail("unknown line %q. Use size, fps, color, frame or colors", fields[0])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(f.frames) == 0 {
		return nil, errors.New("the file has no frame")
	}
	return f, nil
}

func get(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}

// saverStyle reads a colour, #rrggbb or an ANSI colour by name, into the
// SGR that starts a run in it.
func saverStyle(spec string) (string, error) {
	for i, name := range saverColorNames {
		if spec == name {
			return ansi.Style{}.ForegroundColor(lipgloss.ANSIColor(uint8(i))).String(), nil //nolint:gosec // i < 16
		}
	}
	if len(spec) == 7 && spec[0] == '#' {
		if _, err := strconv.ParseUint(spec[1:], 16, 32); err == nil {
			return ansi.Style{}.ForegroundColor(lipgloss.Color(spec)).String(), nil
		}
	}
	return "", errfmt.Errorf("unknown colour %q. Use #rrggbb or an ANSI name such as bright_blue", spec)
}

// render draws every frame as its box, cut to a screen of width by height.
// A box line keeps no trailing blank and the box no margin: framesSaver.show
// places it. It fails when the frames draw to more than saverMaxDrawn.
func (f *saverFile) render(width, height int) ([]string, error) {
	out := make([]string, len(f.frames))
	rows, cols := min(f.rows, height), min(f.cols, width)
	total := 0
	for i, fr := range f.frames {
		var b strings.Builder
		for r := range rows {
			if r > 0 {
				b.WriteByte('\n')
			}
			line := []rune(fr.text[r])
			colors := ""
			if fr.colors != nil {
				colors = fr.colors[r]
			}
			// The cells past the last one that shows anything are left out.
			end := 0
			for x := range cols {
				if (x < len(line) && line[x] > ' ') || (x < len(colors) && f.palette[colors[x]] != "") {
					end = x + 1
				}
			}
			var on string
			for x := range end {
				sgr := ""
				if x < len(colors) {
					sgr = f.palette[colors[x]]
				}
				// One SGR per run of the same colour.
				if sgr != on {
					if on != "" {
						b.WriteString(ansi.ResetStyle)
					}
					b.WriteString(sgr)
					on = sgr
				}
				ch := ' '
				if x < len(line) && line[x] >= ' ' {
					ch = line[x]
				}
				b.WriteRune(ch)
			}
			if on != "" {
				b.WriteString(ansi.ResetStyle)
			}
		}
		if total += b.Len(); total > saverMaxDrawn {
			return nil, errfmt.Errorf("the frames draw to more than %d bytes. Use fewer frames or fewer colour changes", saverMaxDrawn)
		}
		out[i] = b.String()
	}
	return out, nil
}

// framesSaver is a frames file playing.
type framesSaver struct {
	frames        []string // each frame's box, from render
	delays        []time.Duration
	at            int
	width, height int // the screen
	cols, rows    int // the box, cut to the screen
}

// show is the screen with frame i centred on it. It runs once per frame, so
// only one screen of text is held at a time.
func (p *framesSaver) show(i int) string {
	top := max((p.height-p.rows)/2, 0)
	pad := strings.Repeat(" ", max((p.width-p.cols)/2, 0))
	blank := strings.Repeat(" ", p.width)
	var b strings.Builder
	b.Grow(p.width * p.height)
	lines := strings.Split(p.frames[i], "\n")
	for y := range p.height {
		if y > 0 {
			b.WriteByte('\n')
		}
		r := y - top
		if r < 0 || r >= len(lines) {
			b.WriteString(blank)
			continue
		}
		b.WriteString(pad)
		b.WriteString(lines[r])
		if rest := p.width - len(pad) - ansi.StringWidth(lines[r]); rest > 0 {
			b.WriteString(blank[:rest])
		}
	}
	return b.String()
}

// startFramesSaver plays the frames file name names. It reports whether it
// started.
func (m *OS) startFramesSaver(name string, width, height int) bool {
	if PluginSavers == nil {
		return false
	}
	path, ok := PluginSavers()[name]
	if !ok {
		return false
	}
	data, err := ReadSaverFile(path)
	if err != nil {
		m.LogInfo("the screen saver %s could not be read: %v", name, err)
		return false
	}
	f, err := parseSaverFile(data)
	if err != nil {
		m.LogInfo("the screen saver %s cannot play: %v", name, err)
		return false
	}
	frames, err := f.render(width, height)
	if err != nil {
		m.LogInfo("the screen saver %s cannot play: %v", name, err)
		return false
	}
	p := &framesSaver{frames: frames, width: width, height: height, cols: min(f.cols, width), rows: min(f.rows, height)}
	for _, fr := range f.frames {
		p.delays = append(p.delays, fr.delay)
	}
	m.screensaver.player = p
	m.screensaver.name = name
	m.screensaver.width, m.screensaver.height = width, height
	m.screensaver.active = true
	m.screensaver.frame = p.show(0)
	m.renderSkipped = false
	return true
}

// nextSaverFrameCmd schedules the next frame: at a frames file's own time,
// or at the effect rate.
func (m *OS) nextSaverFrameCmd() tea.Cmd {
	if p := m.screensaver.player; p != nil {
		return tea.Tick(p.delays[p.at], func(time.Time) tea.Msg { return screensaverFrameMsg{} })
	}
	return screensaverFrameCmd(&m.Settings)
}

// CheckSaverFile says what is wrong with a frames file, for tuios plugins
// check, or nil.
func CheckSaverFile(data []byte) error {
	f, err := parseSaverFile(data)
	if err == nil {
		_, err = f.render(f.cols, f.rows)
	}
	return err
}

// ReadSaverFile reads a frames file, at most one byte past saverMaxBytes,
// so parseSaverFile refuses a larger file without it all being read.
// A FIFO or a device is refused before the open, which would block on it.
func ReadSaverFile(path string) ([]byte, error) {
	if fi, err := os.Stat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, errors.New("the file is not a regular file")
	}
	fh, err := os.Open(path) //nolint:gosec // a saver file of a plugin the person installed, inside its folder
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, saverMaxBytes+1))
}
