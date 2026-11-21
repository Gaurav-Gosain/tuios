package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

var witnessRe = regexp.MustCompile(`MK([0-9a-f]{6})-([0-9]+)`)

func altMarkerIn(grid []string, tag string) bool {
	full := "ALT" + tag
	for _, line := range grid {
		line = strings.TrimRight(line, " ")
		if line != "" && strings.HasPrefix(full, line) {
			return true
		}
	}
	return false
}

var mainEchoRe = regexp.MustCompile(`\\[0-7]{3}|printf '`)

type witness struct {
	row int
	tag string
	seq int
}

func witnessesIn(lines []string) []witness {
	var out []witness
	for row, line := range lines {
		m := witnessRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out = append(out, witness{row: row, tag: m[1], seq: n})
	}
	return out
}

func spliceIn(lines []string) (a, b witness, found bool) {
	ws := witnessesIn(lines)
	for i := 1; i < len(ws); i++ {
		prev, cur := ws[i-1], ws[i]
		if prev.tag != cur.tag || cur.row != prev.row+1 {
			continue
		}
		if cur.seq != prev.seq+1 {
			return prev, cur, true
		}
	}
	return witness{}, witness{}, false
}

func seqRange(lines []string, tag string) (lo, hi int, any bool) {
	for _, w := range witnessesIn(lines) {
		if w.tag != tag {
			continue
		}
		if !any || w.seq < lo {
			lo = w.seq
		}
		if !any || w.seq > hi {
			hi = w.seq
		}
		any = true
	}
	return lo, hi, any
}

func screenLines(s tuitest.Screen) []string {
	_, rows := s.Size()
	out := make([]string, rows)
	for r := range rows {
		out[r] = s.Line(r)
	}
	return out
}

type daemonWindow struct {
	ID        string `json:"window_id"`
	Workspace int    `json:"workspace"`
	Focused   bool   `json:"focused"`
	Minimized bool   `json:"minimized"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

func (w daemonWindow) tag() string {
	id := strings.ToLower(strings.ReplaceAll(w.ID, "-", ""))
	if len(id) < 6 {
		return "000000"
	}
	return id[:6]
}

type daemonWindowList struct {
	Windows          []daemonWindow `json:"windows"`
	Total            int            `json:"total"`
	FocusedWindowID  string         `json:"focused_window_id"`
	CurrentWorkspace int            `json:"current_workspace"`
	Success          bool           `json:"success"`
}

type daemonSessionInfo struct {
	CurrentWorkspace int    `json:"current_workspace"`
	NumWorkspaces    int    `json:"num_workspaces"`
	WindowCount      int    `json:"window_count"`
	TilingMode       string `json:"tiling_mode"`
	LayoutMode       string `json:"layout_mode"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	TUIAttached      bool   `json:"tui_attached"`
	Success          bool   `json:"success"`
}

func tuiosOut(base string, args ...string) (string, error) {
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err,
			strings.TrimSpace(errBuf.String()))
	}
	return string(out), nil
}

func daemonJSON[T any](base string, args ...string) (T, error) {
	var v T
	out, err := tuiosOut(base, append(args, "--json")...)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return v, fmt.Errorf("%s: decode %q: %w", strings.Join(args, " "), out, err)
	}
	return v, nil
}

func daemonWindows(base, session string) (daemonWindowList, error) {
	return daemonJSON[daemonWindowList](base, "list-windows", "-s", session)
}

func daemonInfo(base, session string) (daemonSessionInfo, error) {
	return daemonJSON[daemonSessionInfo](base, "session-info", "-s", session)
}

func daemonPane(base, session, window string) ([]string, error) {
	out, err := tuiosOut(base, "capture-pane", "-s", session, "-w", window)
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func daemonScrollback(base, session, window string, lines int) ([]string, error) {
	out, err := tuiosOut(base, "capture-pane", "-s", session, "-w", window,
		"--scrollback", "--lines", strconv.Itoa(lines))
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func paneWitnessCmd(tag string, lo, hi int) string {
	return fmt.Sprintf("printf 'MK%s-%%d\\n' $(seq %d %d)\n", tag, lo, hi)
}

func paneAltCmd(tag string, enter bool) string {
	if !enter {
		return paneEmitCmd("\x1b[?1049l")
	}
	return paneEmitCmd("\x1b[?1049h\x1b[2J\x1b[H\nALT" + tag + "\n")
}

func paneEmitCmd(s string) string {
	var b strings.Builder
	b.WriteString("printf '")
	for i := range len(s) {
		fmt.Fprintf(&b, "\\%03o", s[i])
	}
	b.WriteString("'\n")
	return b.String()
}

func paneSend(base, session, window, text string) error {
	_, err := tuiosOut(base, "send-text", "-s", session, "-w", window, text)
	return err
}

func dockWorkspace(s tuitest.Screen) int {
	_, rows := s.Size()
	for r := rows - 1; r >= max(0, rows-3); r-- {
		if m := dockStatus.FindStringSubmatch(s.Line(r)); m != nil {
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			return n
		}
	}
	return -1
}

func waitDock(t *testing.T, term *tuitest.Terminal) error {
	t.Helper()
	return term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) >= 0
	}, bootTimeout)
}
