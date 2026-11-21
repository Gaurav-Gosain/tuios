package tutor

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/learn/lessons"
)

func overlayReaction(name string) string {
	switch name {
	case "help":
		return "help"
	case "whichkey":
		return "menu"
	case "commandPalette":
		return "palette"
	case "launcher":
		return "launcher"
	case "settings", "keybinds":
		return "settings"
	case "themePicker":
		return "theme"
	case "copyMode", "search":
		return "copy"
	case "scrollback":
		return "scrollback"
	case "workspaceSwitcher":
		return "switcher"
	case "layoutPicker":
		return "layout"
	case "sidebar":
		return "sidebar"
	case "screensaver":
		return "screensaver"
	}
	return "generic"
}

func reactionFor(typ string, data map[string]any, action string) string {
	str := func(k string) string {
		s, _ := data[k].(string)
		return s
	}
	switch typ {
	case "":
		return "generic"
	case "window.open":
		if strings.Contains(action, "split") {
			return "split"
		}
		return "open"
	case "window.close":
		return "close"
	case "window.focus":
		return "focus"
	case "window.zoom":
		return "zoom"
	case "window.minimize":
		return "minimize"
	case "window.float":
		return "float"
	case "window.rename":
		return "rename"
	case "window.move":
		switch {
		case action == "rotate_split":
			return "rotate"
		case strings.HasPrefix(action, "swap_"):
			return "swap"
		case strings.HasPrefix(action, "resize_"):
			return "resize"
		}
		return "move"
	case "workspace":
		if strings.HasPrefix(action, "workspace_prefix_move") || strings.HasPrefix(action, "move_to_workspace") {
			return "send"
		}
		return "workspace"
	case "tiling", "layout", "theme":
		return typ
	case "setting":
		return "look"
	case "overlay.open", "overlay.close":
		return overlayReaction(str("name"))
	case "mode":
		if str("to") == "terminal" {
			return "typing"
		}
		return "windowMode"
	case "prefix":
		return "menu"
	case "shell.start", "shell.command":
		return "command"
	case "tape.start", "tape.finish":
		return "tape"
	case "agent":
		switch str("to") {
		case "working":
			return "agentWorking"
		case "done":
			return "agentDone"
		}
		return "agent"
	case "copy.yank":
		return "yank"
	}
	if strings.HasPrefix(action, "workspace_prefix_move") {
		return "send"
	}
	return "generic"
}

var (
	slotOnce sync.Once
	slotRe   *regexp.Regexp
)

func slotPattern() *regexp.Regexp {
	slotOnce.Do(func() { slotRe = regexp.MustCompile(`\{(\w+)\}`) })
	return slotRe
}

func coachLine(c lessons.Coach, result, typ string, data map[string]any, action string) string {
	reaction := reactionFor(typ, data, action)
	if result == string(lessons.Skipped) {
		reaction = "skipped"
	}
	values := map[string]string{}
	if s, ok := data["command"].(string); ok && s != "" {
		values["command"] = s
	}
	pick := func(pool []string) string {
		var fit []string
		for _, l := range pool {
			ok := true
			for _, m := range slotPattern().FindAllStringSubmatch(l, -1) {
				if values[m[1]] == "" {
					ok = false
				}
			}
			if ok {
				fit = append(fit, l)
			}
		}
		if len(fit) == 0 {
			return ""
		}
		l := fit[rand.IntN(len(fit))]
		return slotPattern().ReplaceAllStringFunc(l, func(m string) string { return values[m[1:len(m)-1]] })
	}
	line := pick(c.Reactions[reaction])
	if line == "" {
		line = pick(c.Reactions["generic"])
	}
	if result == string(lessons.Hinted) && line != "" {
		if lead := pick(c.HintedLead); lead != "" {
			line = lead + " " + line
		}
	}
	return line
}

func finishLine(c lessons.Coach, t *lessons.Track) string {
	var pool []string
	if t.Coach != nil {
		pool = append(pool, t.Coach.Finish...)
	}
	pool = append(pool, c.Finish...)
	var fit []string
	for _, l := range pool {
		if !strings.Contains(l, "{") {
			fit = append(fit, l)
		}
	}
	if len(fit) == 0 {
		return ""
	}
	return fit[rand.IntN(len(fit))]
}
