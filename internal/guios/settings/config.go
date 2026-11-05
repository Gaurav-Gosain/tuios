package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"
	"github.com/Gaurav-Gosain/tuios/internal/guios/adblock"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/guios/remote"
)

// kero's configuration is a file of lines `key = value`, as Ghostty's:
// `#` starts a comment, and keys that hold lists, as env and keybind,
// repeat. The keys are Ghostty's where they mean what Ghostty's do. A
// file holds what the user changed; the rest is the defaults. The
// Settings window edits it in place, keeping its comments and order.

// ConfigPath returns the configuration file: $KERO_CONFIG, else
// $XDG_CONFIG_HOME/kero/config, else ~/.config/kero/config; kero-dev in
// place of kero for a development build (proto.Name).
func ConfigPath() string {
	if p := os.Getenv("GUIOS_CONFIG"); p != "" {
		return p
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, proto.Name(), "config")
}

// key is a key of the configuration: how it reads and writes a setting.
type key struct {
	name string
	doc  string
	list bool // repeats, one value a line
	get  func(*Settings) []string
	set  func(*Settings, []string) error
}

func boolKey(name, doc string, f func(*Settings) *bool) key {
	return key{name: name, doc: doc,
		get: func(s *Settings) []string { return []string{strconv.FormatBool(*f(s))} },
		set: func(s *Settings, v []string) error {
			b, err := strconv.ParseBool(v[len(v)-1])
			if err != nil {
				return fmt.Errorf("%q is not true or false", v[len(v)-1])
			}
			*f(s) = b
			return nil
		}}
}

func choiceKey(name, doc string, f func(*Settings) *string, choices ...string) key {
	return key{name: name, doc: doc + " (" + strings.Join(choices, ", ") + ")",
		get: func(s *Settings) []string { return []string{*f(s)} },
		set: func(s *Settings, v []string) error {
			c := v[len(v)-1]
			if !slices.Contains(choices, c) {
				return fmt.Errorf("%q is none of %s", c, strings.Join(choices, ", "))
			}
			*f(s) = c
			return nil
		}}
}

func stringKey(name, doc string, f func(*Settings) *string) key {
	return key{name: name, doc: doc,
		get: func(s *Settings) []string { return []string{*f(s)} },
		set: func(s *Settings, v []string) error { *f(s) = v[len(v)-1]; return nil }}
}

func numberKey(name, doc string, get func(*Settings) float64, set func(*Settings, float64)) key {
	return key{name: name, doc: doc,
		get: func(s *Settings) []string { return []string{strconv.FormatFloat(get(s), 'f', -1, 64)} },
		set: func(s *Settings, v []string) error {
			n, err := strconv.ParseFloat(v[len(v)-1], 64)
			if err != nil {
				return fmt.Errorf("%q is not a number", v[len(v)-1])
			}
			set(s, n)
			return nil
		}}
}

// mapChoice maps the values of a choice in the file to those of Settings.
func mapChoice(name, doc string, f func(*Settings) *string, file, ours []string) key {
	return key{name: name, doc: doc + " (" + strings.Join(file, ", ") + ")",
		get: func(s *Settings) []string { return []string{file[max(slices.Index(ours, *f(s)), 0)]} },
		set: func(s *Settings, v []string) error {
			i := slices.Index(file, v[len(v)-1])
			if i < 0 {
				return fmt.Errorf("%q is none of %s", v[len(v)-1], strings.Join(file, ", "))
			}
			*f(s) = ours[i]
			return nil
		}}
}

// keys are the configuration's keys, in the order a new file and
// `kero config show` list them.
var keys = []key{
	mapChoice("new-session-directory", "where a new session starts", func(s *Settings) *string { return &s.NewSessionDir },
		[]string{"current", "project", "home"}, []string{"current", "project", "home"}),
	choiceKey("session-name", "what names a session you or an agent didn't: the program in the pane you're in, else in the one that most needs you; the program in the one that most needs you; or the first pane's folder",
		func(s *Settings) *string { return &s.SessionName }, proto.NameFocused, proto.NameLead, proto.NameFolder),
	choiceKey("sidebar-rows", "how the sidebar shows a session: detailed, with its status message, else its branch and changes, on a second line; or compact, on one line",
		func(s *Settings) *string { return &s.SidebarRows }, "detailed", "compact"),
	boolKey("focus-follows-mouse", "focus the pane the pointer moves into", func(s *Settings) *bool { return &s.FocusFollowsMouse }),
	boolKey("confirm-close", "ask before closing a pane a program runs in", func(s *Settings) *bool { return &s.ConfirmClose }),
	boolKey("session-history", "list the coding agents' ended conversations in the sidebar, to resume or archive", func(s *Settings) *bool { return &s.SessionHistory }),
	choiceKey("macos-show-app-icon", "where guios's icon shows; closing the window keeps guios running in the menu bar while it shows there. Windows and Linux always show it in the notification area",
		func(s *Settings) *string { return &s.AppIcon }, AppIcons...),

	choiceKey("window-theme", "the app's appearance", func(s *Settings) *string { return &s.Appearance }, "system", "light", "dark"),
	choiceKey("language", "the app's language; system follows the system's", func(s *Settings) *string { return &s.Language }, append([]string{"system"}, Languages...)...),
	boolKey("window-translucent", "let the desktop show through the window", func(s *Settings) *bool { return &s.Vibrancy }),

	stringKey("font-family", "the terminals' font: empty for JetBrainsMono Nerd Font Mono, which guios carries; monospace for the system's. A font that is not installed or not monospaced falls back to the one guios carries", func(s *Settings) *string { return &s.FontFamily }),
	numberKey("font-size", "in points", func(s *Settings) float64 { return float64(s.FontSize) }, func(s *Settings, n float64) { s.FontSize = float32(n) }),
	numberKey("font-weight", "100 to 900", func(s *Settings) float64 { return float64(s.FontWeight) }, func(s *Settings, n float64) { s.FontWeight = int(n) }),
	numberKey("line-height", "a multiple of the font's", func(s *Settings) float64 { return float64(s.LineHeight) }, func(s *Settings, n float64) { s.LineHeight = float32(n) }),
	boolKey("font-ligatures", "join characters as the font draws them", func(s *Settings) *bool { return &s.Ligatures }),
	boolKey("font-thicken", "a heavier stroke, as Ghostty's", func(s *Settings) *bool { return &s.Thicken }),
	numberKey("minimum-contrast", "the least contrast text keeps with its background, 1 (none) to 21, as Ghostty's", func(s *Settings) float64 { return float64(s.MinContrast) }, func(s *Settings, n float64) { s.MinContrast = float32(min(max(n, 1), 21)) }),
	boolKey("dim-unfocused-terminals", "fade the terminals you're not in, when a session has several panes", func(s *Settings) *bool { return &s.DimUnfocused }),
	{name: "theme", doc: "the theme of the window, its panes and terminals: tuios for the theme tuios uses (config.toml's appearance.theme, Omarchy's while tuios follows it), a tuios theme by its id (tuios list-themes), or light:<theme>,dark:<theme>",
		get: func(s *Settings) []string {
			if s.LightTheme == s.DarkTheme {
				return []string{s.LightTheme}
			}
			return []string{"light:" + s.LightTheme + ",dark:" + s.DarkTheme}
		},
		set: func(s *Settings, v []string) error {
			val := v[len(v)-1]
			if !strings.Contains(val, ":") {
				s.LightTheme, s.DarkTheme = val, val
				return nil
			}
			for _, part := range strings.Split(val, ",") {
				k, t, ok := strings.Cut(strings.TrimSpace(part), ":")
				switch {
				case ok && k == "light":
					s.LightTheme = strings.TrimSpace(t)
				case ok && k == "dark":
					s.DarkTheme = strings.TrimSpace(t)
				default:
					return fmt.Errorf("%q: write light:<theme>,dark:<theme>", val)
				}
			}
			return nil
		}},
	choiceKey("cursor-style", "programs may change it", func(s *Settings) *string { return &s.Cursor }, "block", "bar", "underline"),
	boolKey("cursor-style-blink", "", func(s *Settings) *bool { return &s.CursorBlink }),
	boolKey("macos-option-as-alt", "Option sends Alt (Meta) to programs", func(s *Settings) *bool { return &s.OptionAsAlt }),
	boolKey("tuios-keys", "the tuios prefix key and its menus work in the panes, as in the tuios TUI", func(s *Settings) *bool { return &s.TuiosKeys }),
	boolKey("copy-on-select", "what you select goes to the clipboard", func(s *Settings) *bool { return &s.CopyOnSelect }),
	choiceKey("clipboard-read", "programs reading the clipboard (OSC 52)", func(s *Settings) *string { return &s.Clipboard }, "ask", "allow", "deny"),
	mapChoice("right-click-action", "", func(s *Settings) *string { return &s.RightClick },
		[]string{"context-menu", "paste", "copy-or-paste"}, []string{"menu", "paste", "copyOrPaste"}),

	{name: "mouse-scroll-multiplier", doc: "how far scrolling goes, as Ghostty's: precision:<n> scales a touchpad's, discrete:<n> is the rows a notch of a mouse wheel scrolls; both comma-separated, or one number for both",
		get: func(s *Settings) []string {
			f := func(n float32) string { return strconv.FormatFloat(float64(n), 'f', -1, 32) }
			return []string{"precision:" + f(s.ScrollPrecision) + ",discrete:" + f(s.ScrollDiscrete)}
		},
		set: func(s *Settings, v []string) error {
			val := v[len(v)-1]
			n := func(t string) (float32, error) {
				f, err := strconv.ParseFloat(strings.TrimSpace(t), 32)
				if err != nil {
					return 0, fmt.Errorf("%q is not a number", strings.TrimSpace(t))
				}
				return float32(min(max(f, MinScrollMultiplier), MaxScrollMultiplier)), nil
			}
			if !strings.Contains(val, ":") {
				f, err := n(val)
				if err != nil {
					return err
				}
				s.ScrollPrecision, s.ScrollDiscrete = f, f
				return nil
			}
			for _, part := range strings.Split(val, ",") {
				k, t, _ := strings.Cut(strings.TrimSpace(part), ":")
				var dst *float32
				switch k {
				case "precision":
					dst = &s.ScrollPrecision
				case "discrete":
					dst = &s.ScrollDiscrete
				default:
					return fmt.Errorf("%q: write precision:<n>,discrete:<n>", val)
				}
				f, err := n(t)
				if err != nil {
					return err
				}
				*dst = f
			}
			return nil
		}},

	{name: "env", doc: "added to every pane's environment, as NAME=value; repeat it", list: true,
		get: func(s *Settings) []string { return slices.Clone(s.Env) },
		set: func(s *Settings, v []string) error {
			s.Env = nil
			for _, kv := range v {
				if !strings.Contains(kv, "=") {
					return fmt.Errorf("%q is not NAME=value", kv)
				}
				s.Env = append(s.Env, kv)
			}
			return nil
		}},
	numberKey("scrollback-limit", "the output each pane keeps, in megabytes; 0 for none", func(s *Settings) float64 { return float64(s.Scrollback) }, func(s *Settings, n float64) { s.Scrollback = int(n) }),

	boolKey("notify-on-waiting", "when a session needs you", func(s *Settings) *bool { return &s.NotifyWaiting }),
	boolKey("notify-on-done", "when an agent finishes", func(s *Settings) *bool { return &s.NotifyDone }),
	boolKey("notify-on-error", "when a program reports it failed", func(s *Settings) *bool { return &s.NotifyFailed }),
	boolKey("notify-sound", "", func(s *Settings) *bool { return &s.NotifySound }),
	boolKey("dock-badge", "the number of sessions that need you", func(s *Settings) *bool { return &s.DockBadge }),
	boolKey("dock-bounce", "once, when a session comes to need you", func(s *Settings) *bool { return &s.BounceDock }),

	{name: "host", doc: "another machine whose sessions the app shows, as name=address: [user@]host or ssh://… over SSH, wsl://distro, http(s)://… for the tuios daemon's tailnet listener; add color=red, orange, amber, green, teal, blue, purple or pink to mark its sessions; repeat it", list: true,
		get: func(s *Settings) []string {
			var out []string
			for _, h := range s.Hosts {
				v := h.Name + "=" + h.Addr
				if h.Color != "" {
					v += " color=" + h.Color
				}
				out = append(out, v)
			}
			return out
		},
		set: func(s *Settings, v []string) error {
			s.Hosts = nil
			var bad []string
			for _, line := range v {
				h, err := ParseHost(line)
				if err == nil && s.Host(h.Name) != nil {
					err = fmt.Errorf("%q is named twice", h.Name)
				}
				if err != nil {
					bad = append(bad, err.Error())
					continue
				}
				s.Hosts = append(s.Hosts, h)
			}
			if bad != nil {
				return fmt.Errorf("%s", strings.Join(bad, "; "))
			}
			return nil
		}},
	{name: "listen", doc: "let guios on other machines reach this one's sessions: tailnet://:7373 on its Tailscale addresses, tailnet-serve:// for Tailscale Serve; repeat it", list: true,
		get: func(s *Settings) []string { return slices.Clone(s.Listen) },
		set: func(s *Settings, v []string) error {
			s.Listen = nil
			var bad []string
			for _, l := range v {
				if _, err := remote.ParseListen(l); err != nil {
					bad = append(bad, err.Error())
					continue
				}
				s.Listen = append(s.Listen, strings.TrimSpace(l))
			}
			if bad != nil {
				return fmt.Errorf("%s", strings.Join(bad, "; "))
			}
			return nil
		}},
	{name: "listen-allow", doc: "who besides this machine's owner may connect to a listener: a tailnet login, tag:name, or * for anyone on the tailnet; repeat it", list: true,
		get: func(s *Settings) []string { return slices.Clone(s.ListenAllow) },
		set: func(s *Settings, v []string) error {
			s.ListenAllow = nil
			for _, a := range v {
				if a = strings.TrimSpace(a); a != "" {
					s.ListenAllow = append(s.ListenAllow, a)
				}
			}
			return nil
		}},
	boolKey("remote-updates", "let guios on another machine that connects update the guios app here to its version, which restarts it", func(s *Settings) *bool { return &s.RemoteUpdates }),
	boolKey("git-status", "show the git branch and changes of each session", func(s *Settings) *bool { return &s.GitStatus }),
	choiceKey("worktree-base", "what a session's own git worktree branches from: the repository's default branch, or the branch of the session you're in", func(s *Settings) *string { return &s.WorktreeBase }, "default", "current"),
	stringKey("worktree-dir", "where sessions' own git worktrees go, each under its repository's name; empty for where tuios puts them", func(s *Settings) *string { return &s.WorktreeDir }),
	choiceKey("diff-layout", "the changes panel's diffs: unified while the panel is narrow and side by side once it's wide, or always one", func(s *Settings) *string { return &s.DiffLayout }, "auto", "unified", "split"),
	boolKey("browser-block-ads", "block ads and trackers in browser panes, with the browser-filter-list lists", func(s *Settings) *bool { return &s.BlockAds }),
	{name: "browser-filter-list", doc: "a filter list browser-block-ads blocks with: " + strings.Join(adListIDs(), ", ") + ", or a list's https URL; repeat it, and leave it empty for none", list: true,
		get: func(s *Settings) []string {
			if len(s.AdLists) == 0 {
				return []string{""} // none, rather than the defaults
			}
			return slices.Clone(s.AdLists)
		},
		set: func(s *Settings, v []string) error {
			s.AdLists = []string{}
			var bad []string
			for _, id := range v {
				switch _, ok := adblock.Find(id); {
				case id == "":
					s.AdLists = s.AdLists[:0] // an empty value clears the lists before it
				case !ok:
					bad = append(bad, fmt.Sprintf("%q is no list", id))
				case !slices.Contains(s.AdLists, id):
					s.AdLists = append(s.AdLists, id)
				}
			}
			if bad != nil {
				return fmt.Errorf("%s", strings.Join(bad, "; "))
			}
			return nil
		}},
	{name: "keybind", doc: "<keys>=<action>, over the defaults, as cmd+shift+d=pane.split.down; <keys>=unbind frees keys", list: true,
		get: func(s *Settings) []string {
			var out []string
			for _, a := range actions.All {
				k, ok := s.Shortcuts[a.ID]
				if !ok {
					continue
				}
				if k == "" {
					if a.Keys != "" {
						out = append(out, configKeys(a.Keys)+"=unbind")
					}
					continue
				}
				out = append(out, configKeys(k)+"="+a.ID)
			}
			return out
		},
		set: func(s *Settings, v []string) error {
			s.Shortcuts = nil
			sc := map[string]string{}
			var bad []string
			for _, b := range v {
				i := strings.LastIndex(b, "=")
				if i <= 0 {
					bad = append(bad, fmt.Sprintf("%q is not <keys>=<action>", b))
					continue
				}
				// Ghostty marks a global shortcut global:; kero's actions
				// know which are.
				keys, id := accelerator(strings.TrimPrefix(strings.TrimSpace(b[:i]), "global:")), strings.TrimSpace(b[i+1:])
				if id == "unbind" {
					for _, a := range actions.All {
						if strings.EqualFold(actions.Keys(a, sc), keys) {
							sc[a.ID] = ""
						}
					}
					continue
				}
				if _, ok := actions.Find(id); !ok {
					bad = append(bad, fmt.Sprintf("no action %q", id))
					continue
				}
				sc[id] = keys
			}
			if len(sc) > 0 {
				s.Shortcuts = sc
			}
			if bad != nil {
				return fmt.Errorf("%s", strings.Join(bad, "; "))
			}
			return nil
		}},
}

func keroThemeNames() []string {
	var names []string
	for _, t := range KeroThemes {
		names = append(names, t.Name)
	}
	return names
}

// legacyBackground is the key that chose kero's theme before theme took
// kero's themes by name, for theme = kero, then its default. parse reads
// it, and format writes theme in its place.
const legacyBackground = "background"

// legacyRemoteControl let programs drive the app through kero focus and
// kero do, which tuios has no commands for: the key, and the host option of
// that name, are read and dropped, so an older configuration still loads.
const legacyRemoteControl = "remote-control"

func adListIDs() []string {
	var ids []string
	for _, s := range adblock.Catalog {
		ids = append(ids, s.ID)
	}
	return ids
}

func findKey(name string) *key {
	for i := range keys {
		if keys[i].name == name {
			return &keys[i]
		}
	}
	return nil
}

// configKeys writes an accelerator as the file does: "CmdOrCtrl+Shift+D"
// as "cmd+shift+d".
func configKeys(accel string) string {
	parts := strings.Split(accel, "+")
	if strings.HasSuffix(accel, "++") {
		parts = append(parts[:len(parts)-2], "plus")
	}
	for i, p := range parts {
		switch strings.ToLower(p) {
		case "cmdorctrl", "commandorcontrol", "command":
			parts[i] = "cmd"
		case "control":
			parts[i] = "ctrl"
		case "option":
			parts[i] = "alt"
		default:
			parts[i] = strings.ToLower(p)
		}
	}
	return strings.Join(parts, "+")
}

// accelerator reads keys as the file writes them: "cmd+shift+d" as
// "CmdOrCtrl+Shift+D".
func accelerator(keys string) string {
	keys = strings.TrimSpace(keys)
	parts := strings.Split(keys, "+")
	if strings.HasSuffix(keys, "++") {
		parts = append(parts[:len(parts)-2], "plus")
	}
	names := map[string]string{
		"cmd": "CmdOrCtrl", "super": "CmdOrCtrl", "command": "CmdOrCtrl", "ctrl": "Ctrl", "control": "Ctrl",
		"alt": "Alt", "opt": "Alt", "option": "Alt", "shift": "Shift",
		"enter": "Enter", "return": "Enter", "tab": "Tab", "space": "Space", "backspace": "Backspace", "delete": "Delete",
		"escape": "Escape", "esc": "Escape", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
		"home": "Home", "end": "End", "pageup": "PageUp", "pagedown": "PageDown", "plus": "Plus",
	}
	for i, p := range parts {
		l := strings.ToLower(strings.TrimSpace(p))
		if n, ok := names[l]; ok {
			parts[i] = n
		} else {
			parts[i] = strings.ToUpper(l)
		}
	}
	return strings.Join(parts, "+")
}

// line is a line of a configuration file.
type line struct {
	text       string
	key, value string // for a line setting a key
}

func parseLines(data string) []line {
	var out []line
	for _, raw := range strings.Split(strings.TrimRight(data, "\n"), "\n") {
		l := line{text: raw}
		t := strings.TrimSpace(raw)
		if t != "" && !strings.HasPrefix(t, "#") {
			if k, v, ok := strings.Cut(t, "="); ok {
				l.key = strings.TrimSpace(k)
				l.value = strings.TrimSpace(v)
				if len(l.value) >= 2 && l.value[0] == '"' && l.value[len(l.value)-1] == '"' {
					l.value = l.value[1 : len(l.value)-1]
				}
			} else {
				l.key = t // a key without a value: a problem
			}
		}
		if raw != "" || len(out) > 0 {
			out = append(out, l)
		}
	}
	if len(data) == 0 {
		return nil
	}
	return out
}

// parse reads a configuration over the defaults, with what it got wrong.
func parse(data string) (Settings, []string) {
	s := Default()
	var problems []string
	values := map[string][]string{}
	var order []string
	for n, l := range parseLines(data) {
		if l.key == "" || l.key == legacyRemoteControl {
			continue
		}
		if findKey(l.key) == nil && l.key != legacyBackground {
			problems = append(problems, fmt.Sprintf("line %d: no setting %q", n+1, l.key))
			continue
		}
		if _, seen := values[l.key]; !seen {
			order = append(order, l.key)
		}
		values[l.key] = append(values[l.key], l.value)
	}
	if v := values[legacyBackground]; len(v) > 0 {
		order = slices.DeleteFunc(order, func(k string) bool { return k == legacyBackground })
		if bg := keroThemeName(v[len(v)-1]); bg != "" {
			s.LightTheme, s.DarkTheme = bg, bg
		} else if v[len(v)-1] != "" {
			problems = append(problems, fmt.Sprintf("%s: %q is none of %s", legacyBackground, v[len(v)-1], strings.Join(keroThemeNames(), ", ")))
		}
	}
	bg := s.LightTheme
	for _, name := range order {
		k := findKey(name)
		v := values[name]
		if !k.list && v[len(v)-1] == "" && name != "font-family" && name != "command" {
			continue // a key left empty keeps its default
		}
		if err := k.set(&s, v); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}
	s.LightTheme, s.DarkTheme = keroTheme(s.LightTheme, bg), keroTheme(s.DarkTheme, bg)
	s.normalize()
	return s, problems
}

func quote(v string) string {
	if v == "" || strings.ContainsAny(v, "#") || strings.TrimSpace(v) != v {
		return strconv.Quote(v)
	}
	return v
}

// format writes s into a configuration, over the file's lines: a key the
// file sets takes its new values where it is; one the file leaves out is
// written at the end when it differs from its default. Comments, blank
// lines and keys kero does not know stay as they are.
func format(s Settings, data string) string {
	def := Default()
	lines := parseLines(data)
	written := map[string]bool{}
	var out []string
	emit := func(k *key) {
		for _, v := range k.get(&s) {
			out = append(out, k.name+" = "+quote(v))
		}
		written[k.name] = true
	}
	for _, l := range lines {
		k := findKey(l.key)
		if l.key == legacyBackground {
			k = findKey("theme") // theme holds it now
		}
		switch {
		case k == nil:
			out = append(out, l.text)
		case written[k.name]:
			// A list's other lines: its values went at its first.
		default:
			emit(k)
		}
	}
	if len(lines) == 0 {
		out = append(out, header...)
	}
	for i := range keys {
		k := &keys[i]
		if written[k.name] || slices.Equal(k.get(&s), k.get(&def)) {
			continue
		}
		emit(k)
	}
	return strings.Join(out, "\n") + "\n"
}

var header = []string{
	"# guios's configuration, as Ghostty's: one setting a line, `key = value`.",
	"# It holds what differs from the defaults. guios's Settings window edits",
	"# it in place, keeping your comments; what you save here applies at once.",
	"",
}
