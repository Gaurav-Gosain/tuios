// Package refreshrate finds the refresh rate of the displays on this machine,
// which is what appearance.max_fps = "auto" draws at.
//
// It asks the tools the desktop already ships rather than speaking a display
// protocol itself: hyprctl or niri on those compositors, wlr-randr on any
// wlroots one, xrandr on X11 (and under XWayland, as a last resort), and
// system_profiler on macOS. Each is one short process, run once at startup off
// the Bubble Tea goroutine and again only when the config is reloaded.
//
// A terminal cannot tell which display its window is on, so with several
// displays the answer is the highest rate among them. Drawing faster than the
// display the window is actually on costs some CPU and shows nothing worse,
// while drawing slower than it would leave the faster display short.
package refreshrate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// Runner runs a program and returns its standard output. Detect reaches the
// machine only through it, so a test can hand it canned output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Probe is everything Detect reads from the machine.
type Probe struct {
	// GOOS is the operating system, as runtime.GOOS spells it.
	GOOS string
	// Getenv reads one environment variable.
	Getenv func(string) string
	// Run runs one of the display tools.
	Run Runner
}

// System is the probe for the machine this process runs on.
func System() Probe {
	return Probe{GOOS: runtime.GOOS, Getenv: os.Getenv, Run: runCommand}
}

// runCommand is the Runner System uses.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	// #nosec G204 - name is one of the fixed display tools named in Detect.
	return exec.CommandContext(ctx, name, args...).Output()
}

// Timeout is how long Detect is given before the caller gives up on it.
// system_profiler is the slow one, at about a second on a laptop; the Linux
// tools answer in a few milliseconds.
func Timeout(goos string) time.Duration {
	if goos == "darwin" {
		return 3 * time.Second
	}
	return time.Second
}

// Local reports whether this process runs on the same machine as a graphical
// session, which is the only case where the displays it can see are the ones
// the person is looking at. Over SSH the local displays belong to somebody
// else's desk, if there are any.
func (p Probe) Local() bool {
	for _, key := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if p.Getenv(key) != "" {
			return false
		}
	}
	switch p.GOOS {
	case "darwin":
		return true
	case "windows":
		// Left out: there is no tool to ask that ships with every Windows.
		return false
	default:
		return p.Getenv("WAYLAND_DISPLAY") != "" || p.Getenv("DISPLAY") != ""
	}
}

// Detect returns the highest refresh rate among the displays, rounded to a
// whole number of hertz, or 0 when it cannot tell. 0 is also the answer for a
// process that is not Local.
func (p Probe) Detect(ctx context.Context) int {
	if !p.Local() {
		return 0
	}
	if p.GOOS == "darwin" {
		return p.ask(ctx, parseSystemProfiler, "system_profiler", "SPDisplaysDataType")
	}
	if p.Getenv("WAYLAND_DISPLAY") != "" {
		if p.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
			if hz := p.ask(ctx, parseHyprctl, "hyprctl", "monitors", "-j"); hz > 0 {
				return hz
			}
		}
		if p.Getenv("NIRI_SOCKET") != "" {
			if hz := p.ask(ctx, parseNiri, "niri", "msg", "--json", "outputs"); hz > 0 {
				return hz
			}
		}
		if hz := p.ask(ctx, parseWlrRandr, "wlr-randr"); hz > 0 {
			return hz
		}
	}
	// X11, or a Wayland compositor none of the above speaks for. Under
	// XWayland xrandr reports the modes the compositor gave it, which is close
	// enough to be better than the fallback.
	if p.Getenv("DISPLAY") != "" {
		return p.ask(ctx, parseXrandr, "xrandr", "--current")
	}
	return 0
}

// ask runs one tool and parses its output, treating any failure (the tool is
// not installed, it timed out, its output did not parse) as no answer.
func (p Probe) ask(ctx context.Context, parse func([]byte) float64, name string, args ...string) int {
	if ctx.Err() != nil {
		return 0
	}
	out, err := p.Run(ctx, name, args...)
	if err != nil {
		return 0
	}
	return roundHz(parse(out))
}

// roundHz turns a measured rate such as 143.98 or 59.94 into the number the
// display is sold as.
func roundHz(hz float64) int {
	if hz <= 0 || math.IsNaN(hz) || math.IsInf(hz, 0) {
		return 0
	}
	return int(math.Round(hz))
}

// parseHyprctl reads `hyprctl monitors -j`: a list of monitors, each with the
// refresh rate of its current mode in hertz.
func parseHyprctl(out []byte) float64 {
	var monitors []struct {
		RefreshRate float64 `json:"refreshRate"`
		Disabled    bool    `json:"disabled"`
	}
	if json.Unmarshal(out, &monitors) != nil {
		return 0
	}
	best := 0.0
	for _, m := range monitors {
		if !m.Disabled {
			best = max(best, m.RefreshRate)
		}
	}
	return best
}

// parseNiri reads `niri msg --json outputs`: outputs by name, each with its
// modes (refresh in millihertz, as wl_output reports it) and the index of the
// current one, which is null for an output that is off.
func parseNiri(out []byte) float64 {
	var outputs map[string]struct {
		Modes []struct {
			RefreshRate int `json:"refresh_rate"`
		} `json:"modes"`
		CurrentMode *int `json:"current_mode"`
	}
	if json.Unmarshal(out, &outputs) != nil {
		return 0
	}
	best := 0.0
	for _, o := range outputs {
		if o.CurrentMode == nil || *o.CurrentMode < 0 || *o.CurrentMode >= len(o.Modes) {
			continue
		}
		best = max(best, float64(o.Modes[*o.CurrentMode].RefreshRate)/1000)
	}
	return best
}

// parseWlrRandr reads the plain output of wlr-randr, which every version
// prints (--json is newer):
//
//	DP-2 "LG Electronics 27GN7 (DP-2)"
//	  Enabled: yes
//	  Modes:
//	    1920x1080 px, 240.001007 Hz (preferred, current)
func parseWlrRandr(out []byte) float64 {
	best := 0.0
	enabled := true
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line != "" && line[0] != ' ' && line[0] != '\t' {
			// A new output starts at the left margin.
			enabled = true
			continue
		}
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, "Enabled:"); ok {
			enabled = strings.TrimSpace(v) == "yes"
			continue
		}
		if !enabled || !strings.Contains(trimmed, "current") {
			continue
		}
		_, rest, ok := strings.Cut(trimmed, ",")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 || fields[1] != "Hz" {
			continue
		}
		if hz, err := strconv.ParseFloat(fields[0], 64); err == nil {
			best = max(best, hz)
		}
	}
	return best
}

// parseXrandr reads `xrandr --current`, where the current mode of each
// connected output carries a star: "   1920x1080    239.88*+  143.98".
func parseXrandr(out []byte) float64 {
	best := 0.0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		for _, field := range strings.Fields(sc.Text()) {
			if !strings.Contains(field, "*") {
				continue
			}
			if hz, err := strconv.ParseFloat(strings.Trim(field, "*+"), 64); err == nil {
				best = max(best, hz)
			}
		}
	}
	return best
}

// systemProfilerRate matches the rate macOS prints for a display, as in
// "UI Looks like: 2560 x 1440 @ 144.00Hz" or "Resolution: 1920 x 1080 @ 60Hz".
var systemProfilerRate = lazyre.New(`@\s*([0-9]+(?:\.[0-9]+)?)\s*Hz`)

// parseSystemProfiler reads `system_profiler SPDisplaysDataType`. A display
// that prints no rate, which some built-in panels do, adds nothing.
func parseSystemProfiler(out []byte) float64 {
	best := 0.0
	for _, m := range systemProfilerRate().FindAllSubmatch(out, -1) {
		if hz, err := strconv.ParseFloat(string(m[1]), 64); err == nil {
			best = max(best, hz)
		}
	}
	return best
}
