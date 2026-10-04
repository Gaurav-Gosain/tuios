package refreshrate

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The ways detection can go wrong, which this table is written against:
//
//   - It runs over SSH and reports the rate of a desk nobody is sitting at.
//   - It runs with no display at all and waits on a tool that cannot answer.
//   - It takes a rate from a display that is turned off.
//   - It takes a mode the display offers instead of the one it is in.
//   - It reports the first display rather than the fastest.
//   - A missing tool, or output it cannot read, stops it trying the next one.
//   - A fractional rate (59.94, 143.98) comes back as 59 or 143.
//   - It runs a tool on a compositor that tool does not speak for.
//
// The e2e suite drives the real binary through a stand-in hyprctl; this covers
// the other tools, which no machine running the suite has all of.

// fakeRunner answers from a table keyed by the command line, and records what
// it was asked to run.
type fakeRunner struct {
	out map[string]string
	ran []string
}

func (f *fakeRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.ran = append(f.ran, line)
	out, ok := f.out[line]
	if !ok {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	if out == "!fail" {
		return nil, errors.New("exit status 1")
	}
	return []byte(out), nil
}

const hyprctlTwo = `[
 {"name": "DP-2", "refreshRate": 143.97900, "disabled": false, "availableModes": ["1920x1080@240.00Hz"]},
 {"name": "HDMI-A-1", "refreshRate": 239.76000, "disabled": false},
 {"name": "eDP-1", "refreshRate": 360.0, "disabled": true}
]`

const niriTwo = `{
 "DP-1": {"modes": [{"width": 2560, "height": 1440, "refresh_rate": 59951}, {"width": 2560, "height": 1440, "refresh_rate": 164999}], "current_mode": 1},
 "HDMI-A-1": {"modes": [{"width": 1920, "height": 1080, "refresh_rate": 240000}], "current_mode": null}
}`

const wlrRandr = `DP-2 "LG Electronics 27GN7 (DP-2)"
  Enabled: yes
  Modes:
    1920x1080 px, 240.001007 Hz (preferred)
    1920x1080 px, 143.979996 Hz (current)
    1920x1080 px, 60.000000 Hz
HDMI-A-1 "Dell"
  Enabled: no
  Modes:
    1920x1080 px, 239.000000 Hz (preferred, current)
`

const xrandr = `Screen 0: minimum 16 x 16, current 3840 x 1080, maximum 32767 x 32767
DP-2 connected 1920x1080+0+0 (normal left inverted right x axis y axis) 610mm x 360mm
   1920x1080    119.88 +  59.94*
   1440x1080    239.87
HDMI-1 disconnected (normal left inverted right x axis y axis)
`

const systemProfiler = `Graphics/Displays:
    Apple M2 Pro:
      Displays:
        Color LCD:
          Display Type: Built-in Liquid Retina XDR Display
          Resolution: 3456 x 2234 Retina
        LG ULTRAGEAR:
          Resolution: 2560 x 1440 (QHD/WQHD - Wide Quad High Definition)
          UI Looks like: 2560 x 1440 @ 143.86Hz
`

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		out  map[string]string
		want int
		// ran, when set, is exactly the commands that must have run.
		ran []string
	}{
		{
			name: "over ssh nothing runs",
			goos: "linux",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1", "HYPRLAND_INSTANCE_SIGNATURE": "x", "SSH_CONNECTION": "1 2 3 4"},
			out:  map[string]string{"hyprctl monitors -j": hyprctlTwo},
			want: 0, ran: []string{},
		},
		{
			name: "no display nothing runs",
			goos: "linux",
			env:  map[string]string{},
			want: 0, ran: []string{},
		},
		{
			name: "windows is left out",
			goos: "windows",
			env:  map[string]string{"DISPLAY": ":0"},
			want: 0, ran: []string{},
		},
		{
			name: "hyprland takes the fastest enabled monitor",
			goos: "linux",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1", "HYPRLAND_INSTANCE_SIGNATURE": "x"},
			out:  map[string]string{"hyprctl monitors -j": hyprctlTwo},
			want: 240, ran: []string{"hyprctl monitors -j"},
		},
		{
			name: "niri reads the current mode in millihertz and skips an output that is off",
			goos: "linux",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1", "NIRI_SOCKET": "/run/niri"},
			out:  map[string]string{"niri msg --json outputs": niriTwo},
			want: 165,
		},
		{
			name: "wlr-randr reads the current mode of an enabled output",
			goos: "linux",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1"},
			out:  map[string]string{"wlr-randr": wlrRandr},
			want: 144, ran: []string{"wlr-randr"},
		},
		{
			name: "a failing compositor tool falls through to the next",
			goos: "linux",
			env:  map[string]string{"WAYLAND_DISPLAY": "wayland-1", "HYPRLAND_INSTANCE_SIGNATURE": "x", "DISPLAY": ":1"},
			out:  map[string]string{"hyprctl monitors -j": "!fail", "wlr-randr": "garbage", "xrandr --current": xrandr},
			want: 60, ran: []string{"hyprctl monitors -j", "wlr-randr", "xrandr --current"},
		},
		{
			name: "x11 reads the starred mode",
			goos: "linux",
			env:  map[string]string{"DISPLAY": ":0"},
			out:  map[string]string{"xrandr --current": xrandr},
			want: 60, ran: []string{"xrandr --current"},
		},
		{
			name: "macos takes the rate it prints and skips a panel with none",
			goos: "darwin",
			env:  map[string]string{},
			out:  map[string]string{"system_profiler SPDisplaysDataType": systemProfiler},
			want: 144,
		},
		{
			name: "macos over ssh",
			goos: "darwin",
			env:  map[string]string{"SSH_TTY": "/dev/ttys001"},
			out:  map[string]string{"system_profiler SPDisplaysDataType": systemProfiler},
			want: 0, ran: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{out: tc.out}
			p := Probe{GOOS: tc.goos, Getenv: func(k string) string { return tc.env[k] }, Run: f.run}
			if got := p.Detect(context.Background()); got != tc.want {
				t.Errorf("Detect = %d, want %d (ran %q)", got, tc.want, f.ran)
			}
			if tc.ran != nil && !slices.Equal(f.ran, tc.ran) && !(len(tc.ran) == 0 && len(f.ran) == 0) {
				t.Errorf("ran %q, want %q", f.ran, tc.ran)
			}
		})
	}
}
