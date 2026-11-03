package app

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A frames file: a screen saver a plugin brings as data, which tuios plays
// with no process. docs/PLUGINS.md has the format, so a tool can write it.
//
// The file is read and every frame drawn once, at the screen's size, when
// the saver starts. Playing it costs one timer per frame and no work but
// handing over the next string.

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

// saverFile is a parsed frames file.
type saverFile struct {
	cols, rows int
	palette    map[byte]lipgloss.Style
	frames     []saverFrame
}

// parseSaverFile reads a frames file. An error says the line and what is
// wrong, for tuios plugins check.
func parseSaverFile(data []byte) (*saverFile, error) {
	if len(data) > saverMaxBytes {
		return nil, fmt.Errorf("the file is larger than %d bytes", saverMaxBytes)
	}
	f := &saverFile{palette: map[byte]lipgloss.Style{}}
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
		return fmt.Errorf("line %d: %s", n, fmt.Sprintf(format, a...))
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

// saverStyle reads a colour: #rrggbb or an ANSI colour by name.
func saverStyle(spec string) (lipgloss.Style, error) {
	for i, name := range saverColorNames {
		if spec == name {
			return lipgloss.NewStyle().Foreground(lipgloss.ANSIColor(uint8(i))), nil //nolint:gosec // i < 16
		}
	}
	if len(spec) == 7 && spec[0] == '#' {
		if _, err := strconv.ParseUint(spec[1:], 16, 32); err == nil {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(spec)), nil
		}
	}
	return lipgloss.Style{}, fmt.Errorf("unknown colour %q. Use #rrggbb or an ANSI name such as bright_blue", spec)
}

// render draws every frame at the screen's size, centred.
func (f *saverFile) render(width, height int) []string {
	out := make([]string, len(f.frames))
	top := max((height-f.rows)/2, 0)
	left := max((width-f.cols)/2, 0)
	pad := strings.Repeat(" ", left)
	blank := strings.Repeat(" ", width)
	for i, fr := range f.frames {
		var b strings.Builder
		for y := range height {
			if y > 0 {
				b.WriteByte('\n')
			}
			r := y - top
			if r < 0 || r >= f.rows {
				b.WriteString(blank)
				continue
			}
			line := []rune(fr.text[r])
			colors := ""
			if fr.colors != nil {
				colors = fr.colors[r]
			}
			b.WriteString(pad)
			cols := min(f.cols, width-left)
			for x := 0; x < cols; {
				// One style per run of the same colour letter.
				letter := byte(' ')
				if x < len(colors) {
					letter = colors[x]
				}
				end := x + 1
				for end < cols && end < len(colors) && colors[end] == letter {
					end++
				}
				if letter == ' ' || x >= len(colors) {
					end = max(end, x+1)
				}
				var run strings.Builder
				for c := x; c < end; c++ {
					ch := ' '
					if c < len(line) && line[c] >= ' ' {
						ch = line[c]
					}
					run.WriteRune(ch)
				}
				if st, ok := f.palette[letter]; ok {
					b.WriteString(st.Render(run.String()))
				} else {
					b.WriteString(run.String())
				}
				x = end
			}
			if rest := width - left - cols; rest > 0 {
				b.WriteString(strings.Repeat(" ", rest))
			}
		}
		out[i] = b.String()
	}
	return out
}

// framesSaver is a frames file playing.
type framesSaver struct {
	frames []string
	delays []time.Duration
	at     int
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
	data, err := os.ReadFile(path) //nolint:gosec // a saver file of a plugin the person installed
	if err != nil {
		m.LogInfo("the screen saver %s could not be read: %v", name, err)
		return false
	}
	f, err := parseSaverFile(data)
	if err != nil {
		m.LogInfo("the screen saver %s cannot play: %v", name, err)
		return false
	}
	p := &framesSaver{frames: f.render(width, height)}
	for _, fr := range f.frames {
		p.delays = append(p.delays, fr.delay)
	}
	m.screensaver.player = p
	m.screensaver.name = name
	m.screensaver.width, m.screensaver.height = width, height
	m.screensaver.active = true
	m.screensaver.frame = p.frames[0]
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
	_, err := parseSaverFile(data)
	return err
}
