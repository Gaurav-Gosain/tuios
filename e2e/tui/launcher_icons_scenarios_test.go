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

const launcherIconIDBase = 0xF000_0000

var apcRE = regexp.MustCompile(`\x1b_G([^;\x1b]*)(?:;([^\x1b]*))?\x1b\\`)

type gfxKey struct{ img, placement uint32 }

func (k gfxKey) String() string { return fmt.Sprintf("i=%d,p=%d", k.img, k.placement) }

func kittyParams(s string) map[string]string {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, ",") {
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

func del(live map[gfxKey]bool, p map[string]string, id uint32) {
	switch strings.ToLower(p["d"]) {
	case "a":
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

func iconEnv(base string, extra ...string) []string {
	return append([]string{
		"TUIOS_KITTY_GRAPHICS=1",
		"TUIOS_SIXEL_GRAPHICS=0",
		"XDG_DATA_DIRS=" + filepath.Join(base, "no-such-data-dir"),
		"PATH=" + agentFreePath("/usr/bin", "/bin"),
	}, extra...)
}

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

func plantExitingApp(t *testing.T, base string) {
	t.Helper()
	data := filepath.Join(base, "XDG_DATA_HOME")
	entry := "[Desktop Entry]\nType=Application\nName=zzquit\nExec=/bin/true\nIcon=zzquit\n"
	if err := os.WriteFile(filepath.Join(data, "applications", "zzquit.desktop"), []byte(entry), 0o644); err != nil {
		t.Fatalf("plant exiting app: %v", err)
	}
	writeIconPNG(t, filepath.Join(data, "icons", "hicolor", "apps", "zzquit.png"), 200)
}

func writeArgProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho ARG:$1\nexec sleep 30\n"
	if err := os.WriteFile(filepath.Join(dir, probeName), []byte(script), 0o755); err != nil {
		t.Fatalf("arg probe: %v", err)
	}
	return dir
}
