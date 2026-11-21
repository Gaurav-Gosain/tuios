package app

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"tuios.dev/vt"
)

var placeholderMinimum = map[string][3]int{
	"kitty":   {0, 28, 0},
	"ghostty": {1, 0, 0},
	"wezterm": {0, 0, 0},
	"rio":     {0, 0, 0},
}

var xtversionReply = lazyre.New(`\x1bP>\|([^\x1b]*)\x1b\\`)

const xtversionQuery = "\x1b[>q"

func parseHostIdentity(response string) (name string, version [3]int, ok bool) {
	m := xtversionReply().FindStringSubmatch(response)
	if m == nil {
		return "", version, false
	}
	return parseVersionName(m[1])
}

func parseVersionName(text string) (name string, version [3]int, ok bool) {
	s := strings.TrimSpace(text)
	s = strings.ReplaceAll(s, "(", " ")
	s = strings.TrimSuffix(s, ")")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", version, false
	}
	name = strings.ToLower(fields[0])
	if len(fields) > 1 {
		version = parseVersionTriple(fields[1])
	}
	return name, version, true
}

func parseVersionTriple(s string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			n = v
		}
		out[i] = n
	}
	return out
}

func hostDrawsPlaceholders(response string) bool {
	name, version, ok := parseHostIdentity(response)
	if !ok {
		return false
	}
	minimum, known := placeholderMinimum[name]
	if !known {
		return false
	}
	return !versionLess(version, minimum)
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (m *OS) placeholdersEnabled() bool {
	if m.KittyPassthrough == nil || !m.KittyPassthrough.IsEnabled() {
		return false
	}
	switch m.Settings.KittyPlaceholders {
	case config.KittyPlaceholdersOn:
		return true
	case config.KittyPlaceholdersOff:
		return false
	default:
		return m.hostCaps().KittyPlaceholders
	}
}

func (m *OS) refreshKittyPlaceholderMode() {
	mode := vt.KittyPlaceholdersDrop
	if m.placeholdersEnabled() {
		mode = vt.KittyPlaceholdersKeep
	}
	for _, w := range m.Windows {
		if w != nil && w.Terminal != nil {
			w.Terminal.SetKittyPlaceholderMode(mode)
		}
	}
}
