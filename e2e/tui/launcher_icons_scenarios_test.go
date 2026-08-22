package tuie2e

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The launcher draws its app icons as kitty graphics under image ids from
// launcherIconIDBase up, which is where these assertions look. A placement is
// the thing that is visible, so the state machine below tracks placements and
// not transmissions: an image left resident with nothing placed from it is
// invisible and costs only memory, while a placement left behind is the stray
// picture the reports are about.
const launcherIconIDBase = 0xF000_0000

// apcRE matches one kitty graphics escape and captures its parameter list.
var apcRE = regexp.MustCompile(`\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

// gfxKey identifies one placement: the image it draws and the placement id.
type gfxKey struct{ img, placement uint32 }

func (k gfxKey) String() string { return fmt.Sprintf("i=%d,p=%d", k.img, k.placement) }

// kittyParams splits a parameter list into its key/value pairs.
func kittyParams(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}

func atoiU32(s string) uint32 {
	n, _ := strconv.ParseUint(s, 10, 32)
	return uint32(n)
}

// liveLauncherPlacements replays every kitty graphics command in stream and
// returns the launcher placements still standing at the end.
//
// This is the only assertion that means anything about a stray image: a test
// that checks a delete was written proves the bytes were composed, not that the
// picture is gone. Replaying the whole conversation the way the host does is
// what tells the two apart, and it catches a delete that names the wrong id, a
// delete undone by a later re-place, and a placement made after the panel that
// owns it has closed.
func liveLauncherPlacements(stream []byte) []string {
	live := map[gfxKey]bool{}
	for _, m := range apcRE.FindAllSubmatch(stream, -1) {
		p := kittyParams(string(m[1]))
		id := atoiU32(p["i"])
		switch p["a"] {
		case "p":
			if id >= launcherIconIDBase {
				live[gfxKey{id, atoiU32(p["p"])}] = true
			}
		case "d":
			del(live, p, id)
		}
	}
	out := make([]string, 0, len(live))
	for k := range live {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return out
}

// del applies one a=d command. Only the forms tuios can emit or provoke are
// modelled; an unknown d value is treated as deleting nothing, which is the
// conservative answer for a leak check.
func del(live map[gfxKey]bool, p map[string]string, id uint32) {
	switch strings.ToLower(p["d"]) {
	case "a":
		// Every visible placement, whoever owns it.
		clear(live)
	case "i":
		hasP := p["p"] != ""
		pid := atoiU32(p["p"])
		for k := range live {
			if k.img == id && (!hasP || k.placement == pid) {
				delete(live, k)
			}
		}
	}
}

// plantApps writes n desktop entries with icons into an isolation root, and
// points the icon search at the same root, so the launcher has real pictures to
// draw and draws only these.
func plantApps(t *testing.T, base string, n int) {
	t.Helper()
	data := filepath.Join(base, "XDG_DATA_HOME")
	apps := filepath.Join(data, "applications")
	icons := filepath.Join(data, "icons", "hicolor", "apps")
	for _, d := range []string{apps, icons} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("plant dir %s: %v", d, err)
		}
	}
	index := "[Icon Theme]\nName=Hicolor\nDirectories=apps\n\n" +
		"[apps]\nSize=32\nMinSize=1\nMaxSize=512\nType=Scalable\nContext=Applications\n"
	if err := os.WriteFile(filepath.Join(data, "icons", "hicolor", "index.theme"), []byte(index), 0o644); err != nil {
		t.Fatalf("plant index.theme: %v", err)
	}
	for i := range n {
		name := fmt.Sprintf("zzapp%02d", i)
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=/bin/sh -c 'sleep 30'\nIcon=%s\n", name, name)
		if err := os.WriteFile(filepath.Join(apps, name+".desktop"), []byte(entry), 0o644); err != nil {
			t.Fatalf("plant desktop entry: %v", err)
		}
		writeIconPNG(t, filepath.Join(icons, name+".png"), uint8(i*7))
	}
}

// writeIconPNG writes a small solid PNG, distinct per app so a wrong picture is
// distinguishable from a missing one when a capture is inspected by hand.
func writeIconPNG(t *testing.T, path string, shade uint8) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.RGBA{R: shade, G: uint8(x * 8), B: uint8(y * 8), A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create icon: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode icon: %v", err)
	}
}

// iconEnv is the environment a launcher-icon test runs in: kitty graphics
// forced on, and the desktop search confined to the planted entries so the
// developer's own applications never reach the list.
func iconEnv(base string, extra ...string) []string {
	return append([]string{
		"TUIOS_KITTY_GRAPHICS=1",
		"TUIOS_SIXEL_GRAPHICS=0",
		"XDG_DATA_DIRS=" + filepath.Join(base, "no-such-data-dir"),
		"PATH=/usr/bin:/bin",
	}, extra...)
}

// distinctLauncherImages counts the launcher image ids ever transmitted, which
// is how a run that re-uploads the same icon per open is told from one that
// uploads it once.
func distinctLauncherImages(stream []byte) int {
	seen := map[uint32]bool{}
	for _, m := range apcRE.FindAllSubmatch(stream, -1) {
		p := kittyParams(string(m[1]))
		if p["a"] == "t" || p["a"] == "T" {
			if id := atoiU32(p["i"]); id >= launcherIconIDBase {
				seen[id] = true
			}
		}
	}
	return len(seen)
}

// plantExitingApp adds a desktop entry whose program exits the moment it runs,
// so the pane a launch creates goes away in the same breath as the panel.
func plantExitingApp(t *testing.T, base string) {
	t.Helper()
	data := filepath.Join(base, "XDG_DATA_HOME")
	entry := "[Desktop Entry]\nType=Application\nName=zzquit\nExec=/bin/true\nIcon=zzquit\n"
	if err := os.WriteFile(filepath.Join(data, "applications", "zzquit.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatalf("plant exiting app: %v", err)
	}
	writeIconPNG(t, filepath.Join(data, "icons", "hicolor", "apps", "zzquit.png"), 200)
}

// writeArgProbe puts an executable on $PATH that reports its first argument, so
// a test can prove that what was typed after the launcher closed reached the
// command rather than merely appearing on screen.
func writeArgProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho ARG:$1\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, probeName), []byte(script), 0o755); err != nil {
		t.Fatalf("arg probe: %v", err)
	}
	return dir
}

// manyPrograms fills a directory with executables so the first scan of $PATH
// takes long enough to be raced, the way a real machine's several thousand do,
// and puts the probe among them.
func manyPrograms(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	filler := []byte("#!/bin/sh\nexec sleep 30\n")
	for i := range n {
		p := filepath.Join(dir, "filler"+strings.Repeat("x", 1+i%12)+strconv.Itoa(i))
		if err := os.WriteFile(p, filler, 0o755); err != nil {
			t.Fatalf("filler: %v", err)
		}
	}
	probe := "#!/bin/sh\necho " + runAnythingMarker + "\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, probeName), []byte(probe), 0o755); err != nil {
		t.Fatalf("probe: %v", err)
	}
	return dir
}
