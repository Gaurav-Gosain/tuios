package tuie2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.max_fps takes a number up to 240, or "auto" for the refresh rate
// of the display. These drive the real binary: a config file on disk, the
// option list the daemon reports, and the settings row a person reads.

// maxFPSConfig is a config whose only setting is max_fps.
func maxFPSConfig(value string) string {
	return "[appearance]\nmax_fps = " + value + "\n"
}

// maxFPSRow opens the settings page, finds the Max FPS row by search, and
// waits for it to show want.
func maxFPSRow(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	maxFPSRowWithin(t, term, want, uiTimeout)
}

// maxFPSRowWithin is maxFPSRow with a longer wait, for a value a config reload
// or a detection is still on its way to.
func maxFPSRowWithin(t *testing.T, term *tuitest.Terminal, want string, timeout time.Duration) {
	t.Helper()
	openMaxFPSRow(t, term, want, timeout)
	closeSettingsSearch(t, term)
}

// openMaxFPSRow opens the settings page with the Max FPS row under the cursor
// and waits for it to show want.
func openMaxFPSRow(t *testing.T, term *tuitest.Terminal, want string, timeout time.Duration) {
	t.Helper()
	if err := term.SendKeys(",", "/", "max fps"); err != nil {
		t.Fatalf("search settings: %v", err)
	}
	waitMaxFPSRow(t, term, want, timeout)
}

// waitMaxFPSRow waits for the Max FPS row under the cursor to show want.
func waitMaxFPSRow(t *testing.T, term *tuitest.Terminal, want string, timeout time.Duration) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(selectedSettingsRow(s, "Max FPS"), want)
	}, timeout); err != nil {
		t.Fatalf("the Max FPS row never showed %q: %v\n%s", want, err, term.Snapshot())
	}
}

// closeSettingsSearch leaves the search, then the page. Two keys a moment
// apart, as a person presses them: sent together they read as one alt+esc.
func closeSettingsSearch(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("clear the search: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "esc clear search")
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not clear the settings search: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "←→ change")
	}, uiTimeout); err != nil {
		t.Fatalf("the settings page did not close: %v\n%s", err, term.Snapshot())
	}
}

// waitConfigHas waits for the config file to contain want, which a change on
// the settings page writes after the screen shows it.
func waitConfigHas(t *testing.T, base, want string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(configPathIn(base))
		if strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the config file never had %q:\n%s", want, data)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestMaxFPSLoadsAndShows: 240 and "auto" both load from the file, the option
// list says what the option takes, and the settings row shows the value in
// force. With no display to ask, which is how the suite runs, auto is 60.
func TestMaxFPSLoadsAndShows(t *testing.T) {
	base := spotlightConfigFile(t, maxFPSConfig("240"))
	if out, err := tuiosCLI(t, base, "new", "e2e-fps", "--detach"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}

	out, err := tuiosCLI(t, base, "list-options", "appearance.max_fps", "--json")
	if err != nil {
		t.Fatalf("list-options: %v\n%s", err, out)
	}
	var listed struct {
		Options []struct {
			Path        string `json:"path"`
			Description string `json:"description"`
			Max         int    `json:"max"`
			Auto        bool   `json:"auto"`
		} `json:"options"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil || len(listed.Options) != 1 {
		t.Fatalf("list-options did not return the one max_fps option (%v):\n%s", err, out)
	}
	opt := listed.Options[0]
	if opt.Max != 240 || !opt.Auto {
		t.Errorf("list-options says max_fps goes to %d, auto %v; want 240 and auto:\n%s", opt.Max, opt.Auto, out)
	}
	if !strings.Contains(opt.Description, "Your terminal and monitor can show fewer") {
		t.Errorf("the max_fps description does not say the terminal and monitor limit what shows: %q", opt.Description)
	}

	human, err := tuiosCLI(t, base, "list-options", "appearance.max_fps")
	if err != nil || !strings.Contains(human, "range: 0 to 240, or auto") {
		t.Errorf("list-options does not print the range with auto (%v):\n%s", err, human)
	}

	term := attachIn(t, base, "e2e-fps", startOpts{})
	maxFPSRow(t, term, "240")

	// auto, written the way a person would, through a reload. A file that
	// did not load would leave the row at 240.
	saveConfigLikeAnEditor(t, base, maxFPSConfig(`"auto"`))
	openMaxFPSRow(t, term, "Auto (60)", configWatchTimeout)

	// The row steps from Auto back round to 240 and on to Auto again, and each
	// step is saved to the file: a number bare, auto quoted.
	if err := term.SendKeys(tuitest.Left); err != nil {
		t.Fatalf("left: %v", err)
	}
	waitMaxFPSRow(t, term, "‹ 240 ›", uiTimeout)
	waitConfigHas(t, base, "max_fps = 240\n")
	if err := term.SendKeys(tuitest.Right); err != nil {
		t.Fatalf("right: %v", err)
	}
	waitMaxFPSRow(t, term, "Auto (60)", uiTimeout)
	waitConfigHas(t, base, "max_fps = \"auto\"\n")
	closeSettingsSearch(t, term)
	alive(t, term, "after max_fps went from 240 to auto")
}

// TestMaxFPSAutoReadsTheDisplay: on a Hyprland desktop auto asks hyprctl and
// draws at the fastest monitor's rate, rounded. Over SSH it asks nothing and
// draws at 60, because the displays on this machine are not the ones the
// person sees. The hyprctl here is a stand-in that prints two monitors.
func TestMaxFPSAutoReadsTheDisplay(t *testing.T) {
	base := spotlightConfigFile(t, maxFPSConfig(`"auto"`))
	if out, err := tuiosCLI(t, base, "new", "e2e-fps-auto", "--detach"); err != nil {
		t.Fatalf("new session: %v\n%s", err, out)
	}

	desktop := fakeHyprland(t, base, "59.951", "143.97900")

	local := attachIn(t, base, "e2e-fps-auto", startOpts{env: desktop})
	maxFPSRowWithin(t, local, "Auto (144)", configWatchTimeout)

	remote := attachIn(t, base, "e2e-fps-auto", startOpts{env: append(desktop, "SSH_CONNECTION=10.0.0.2 50000 10.0.0.1 22")})
	maxFPSRow(t, remote, "Auto (60)")
	alive(t, local, "after auto read the display")
}

// fakeHyprland puts a stand-in hyprctl under base that reports one monitor
// at each rate, and returns the environment of a Hyprland desktop that finds
// it first on the PATH.
func fakeHyprland(t *testing.T, base string, rates ...string) []string {
	t.Helper()
	bin := filepath.Join(base, "fakebin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	monitors := make([]string, 0, len(rates))
	for i, hz := range rates {
		monitors = append(monitors, fmt.Sprintf(`{"name":"DP-%d","refreshRate":%s,"disabled":false}`, i+1, hz))
	}
	hyprctl := "#!/bin/sh\n" +
		"[ \"$1 $2\" = 'monitors -j' ] || exit 1\n" +
		"echo '[" + strings.Join(monitors, ",") + "]'\n"
	if err := os.WriteFile(filepath.Join(bin, "hyprctl"), []byte(hyprctl), 0o700); err != nil {
		t.Fatalf("write hyprctl: %v", err)
	}
	return []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"WAYLAND_DISPLAY=wayland-e2e",
		"HYPRLAND_INSTANCE_SIGNATURE=e2e",
	}
}

// frameCounter counts the frames tuios writes to its terminal. Each frame is
// one synchronized update, so it opens with one DECSET 2026.
type frameCounter struct {
	mu     sync.Mutex
	frames int
	tail   []byte
}

func (c *frameCounter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Keep the end of the last write, so a marker split across two writes is
	// counted once.
	joined := append(c.tail, p...)
	c.frames += bytes.Count(joined, syncBegin) - bytes.Count(c.tail, syncBegin)
	keep := min(len(joined), len(syncBegin)-1)
	c.tail = append([]byte(nil), joined[len(joined)-keep:]...)
	return len(p), nil
}

func (c *frameCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frames
}

// measureFrameRate starts tuios with max_fps set to value, keeps a pane
// printing as fast as the shell can, and returns the frames a second tuios
// draws once it has settled.
func measureFrameRate(t *testing.T, value string, displayHz ...string) float64 {
	t.Helper()
	base := spotlightConfigFile(t, maxFPSConfig(value))
	frames := &frameCounter{}
	var env []string
	if len(displayHz) > 0 {
		env = fakeHyprland(t, base, displayHz...)
	}
	term := startIn(t, base, startOpts{out: frames, env: env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	if err := term.SendKeys("i=0; while :; do i=$((i+1)); printf '\\r%d' $i; done\r"); err != nil {
		t.Fatalf("start the counter: %v", err)
	}
	const settle, window = 2 * time.Second, 3 * time.Second
	_ = term.WaitFor(func(tuitest.Screen) bool { return false }, settle)
	before := frames.count()
	_ = term.WaitFor(func(tuitest.Screen) bool { return false }, window)
	return float64(frames.count()-before) / window.Seconds()
}

// TestMaxFPS240DrawsPastTheOldClamp measures the frame rate on the wire. Bubble
// Tea clamps its frame ticker to 120, so before BindProgram a max_fps of 240
// drew 120 frames a second. It needs a machine that can compose a frame in
// well under 4 ms, which a shared CI runner is not, so it runs only with
// TUIOS_E2E_PERF set. On the machine this was written on it measures 60 and
// 240; the build before the change measured 60 and 120.
//
// auto on a 240 Hz display is the third case. The rate arrives after the
// program has started, so it measures the ticker moving while tuios runs.
func TestMaxFPS240DrawsPastTheOldClamp(t *testing.T) {
	if os.Getenv("TUIOS_E2E_PERF") == "" {
		t.Skip("set TUIOS_E2E_PERF=1 to measure frame rates")
	}
	def := measureFrameRate(t, "0")
	fast := measureFrameRate(t, "240")
	auto := measureFrameRate(t, `"auto"`, "240.001")
	t.Logf("frames a second: max_fps 0 draws %.0f, max_fps 240 draws %.0f, auto on a 240 Hz display draws %.0f",
		def, fast, auto)
	if def < 50 || def > 70 {
		t.Errorf("max_fps 0 drew %.0f frames a second, want about 60", def)
	}
	if fast <= 132 {
		t.Errorf("max_fps 240 drew %.0f frames a second, which is not past the 120 Bubble Tea allows", fast)
	}
	if auto <= 132 {
		t.Errorf("auto on a 240 Hz display drew %.0f frames a second; the rate it found did not reach the ticker", auto)
	}
}
