package main

import (
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/actions"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// searchEntry is a setting the Settings window's search finds: its page,
// its label as its row shows it, and words that find it besides, as its
// key in the configuration file.
type searchEntry struct {
	page, label, words string
}

// settingsIndex is the settings the search finds, labelled in the
// language the app shows, as their rows are.
func settingsIndex() []searchEntry {
	entries := []searchEntry{
		{"general", locales.SetNewSessionsStartIn(), "new-session-directory folder current project home tab"},
		{"general", locales.SetSessionName(), "session-name title sidebar focused lead folder pane"},
		{"general", locales.SetSidebarRows(), "sidebar-rows compact detailed status message branch two lines"},
		{"general", locales.SetFocusFollowsMouse(), "focus-follows-mouse hover pointer niri hyprland sloppy"},
		{"general", locales.SetAskBeforeClosing(), "confirm-close quit kill"},
		{"general", locales.SetKeepHistory(), "persist_scrollback history scrollback reboot restore persist"},
		{"general", locales.SetResumeAgents(), "resume_agents claude codex pi opencode conversation restore reboot"},
		{"general", locales.SetSessionHistory(), "session-history ended sessions archived resume claude codex conversation"},
		{"general", locales.SetGitStatus(), "git-status branch changes diff repository worktree"},
		{"general", locales.SetDiffLayout(), "diff-layout diff unified split side by side changes"},
		{"general", locales.SetOpenAtLogin(), "startup launch login items"},
		{"general", locales.SetVersion(), "updates check for updates software update about"},
		{"general", locales.SetAutoCheck(), "updates automatic background daily"},
		{"general", locales.SetAutoInstall(), "updates automatic install download"},

		{"appearance", locales.SetAppearance(), "window-theme dark light mode system"},
		{"appearance", locales.SetLanguage(), "language locale translation chinese english 语言 中文"},
		{"appearance", locales.SetTranslucent(), "window-translucent vibrancy blur material transparency"},
		{"appearance", locales.SetBackground(), "theme background wash colors terminal petal mist meadow dusk tide apricot aurora dune paper plain gradient color"},

		{"terminal", locales.SetFont(), "font-family typeface jetbrains mono nerd"},
		{"terminal", locales.SetSize(), "font-size zoom bigger smaller text"},
		{"terminal", locales.SetLineHeight(), "line-height spacing leading cell"},
		{"terminal", locales.SetWeight(), "font-weight bold light"},
		{"terminal", locales.SetLigatures(), "font-ligatures calt arrows"},
		{"terminal", locales.SetThicken(), "font-thicken stroke smoothing"},
		{"appearance", locales.SetLightWindows(), "theme colors color scheme ghostty palette terminal"},
		{"appearance", locales.SetDarkWindows(), "theme colors color scheme ghostty palette terminal"},
		{"terminal", locales.SetDimUnfocused(), "dim-unfocused-terminals panes fade inactive split opacity veil blur"},
		{"terminal", locales.SetShape(), "cursor-style cursor block bar underline caret"},
		{"terminal", locales.SetBlink(), "cursor-style-blink cursor caret"},
		{"terminal", locales.SetOptionMeta(), "macos-option-as-alt alt meta emacs readline keyboard"},
		{"terminal", locales.SetCopyOnSelect(), "copy-on-select clipboard selection"},
		{"terminal", locales.SetRightClick(), "right-click-action context menu paste mouse"},
		{"terminal", locales.SetClipboardRead(), "clipboard-read osc 52 paste privacy"},
		{"terminal", locales.SetWheelScroll(), "mouse-scroll-multiplier discrete scroll speed wheel notch rows"},
		{"terminal", locales.SetTrackpadScroll(), "mouse-scroll-multiplier precision scroll speed trackpad touchpad"},

		{"shell", locales.SetShell(), "command fish zsh bash login"},
		{"shell", locales.SetScrollback(), "scrollback-limit history buffer lines memory"},
		{"shell", locales.SetEnvironment(), "env variables path"},

		{"browser", locales.SetBlockAds(), "browser-block-ads browser-filter-list ad blocker adblock trackers filter lists easylist easyprivacy peter lowe fanboy cookie annoyances social china privacy web"},

		{"notifications", locales.SetNotifyWaiting(), "notify-on-waiting attention blocked permission"},
		{"notifications", locales.SetNotifyDone(), "notify-on-done done complete"},
		{"notifications", locales.SetNotifyFailed(), "notify-on-error error"},
		{"notifications", locales.SetPlaySound(), "notify-sound audio"},
		{"notifications", locales.SetDockBadge(), "dock-badge count"},
		{"notifications", locales.SetDockBounce(), "dock-bounce attention"},

		{"agents", locales.SetRemoteControlRow(), "remote-control kero do focus browser automation"},
		{"agents", locales.SetCliRow(), "cli command line tool install path"},
		{"agents", "Claude Code", "skill agents install"},
		{"agents", "Codex, OpenCode, Pi", "skill agents install"},
		{"agents", locales.SetProgramStatus(), "osc 7501 report progress"},

		{"hosts", locales.SetHosts(), "host hosts remote server ssh tailscale tailnet wsl machine linux devbox -H kero hosts"},
		{"hosts", locales.SetHostAdd(), "add host address ssh wsl https"},
		{"hosts", locales.SetHostRemoteControl(), "remote-control host kero do focus"},
		{"hosts", locales.SetListen(), "listen tailnet tailscale share this computer host port 7373 listen-allow"},

		{"advanced", locales.SetConfigFile(), "configuration ghostty edit file dotfiles"},
		{"advanced", "kerod", "daemon background service restart sessions"},
		{"advanced", locales.SetLog(), "kerod log debug"},
		{"advanced", locales.SetFolder(), "data directory finder"},
		{"advanced", locales.SetReset(), "defaults reset"},
	}
	if runtime.GOOS == "darwin" {
		entries = append(entries, searchEntry{"general", locales.SetAppIcon(), "macos-show-app-icon dock menu bar menubar tray status item hide"})
	}
	return entries
}

// searchEntries are the index and the actions, which the keyboard page
// lists, with the shortcuts they have.
func searchEntries(shortcuts map[string]string) []searchEntry {
	out := settingsIndex()
	for _, act := range actions.All {
		keys := actions.Keys(act, shortcuts)
		out = append(out, searchEntry{"keyboard", strings.TrimSuffix(actionTitle(act.ID), "…"),
			act.ID + " shortcut keybind " + act.Menu + " " + strings.ToLower(keys) + " " + actions.Glyphs(keys) + " " + actionWords[act.ID]})
	}
	return out
}

// actionWords find actions besides their titles: what one looks for an
// action by when its title doesn't say it.
var actionWords = map[string]string{
	"settings.open":       "preferences options config configure",
	"cli.install":         "kero command line shell path terminal",
	"app.quitAll":         "exit quit kill stop end all sessions",
	"session.new":         "create add tab terminal shell",
	"session.newHome":     "create add tab terminal shell home directory ~",
	"session.newWorktree": "create add worktree branch checkout agent isolate parallel",
	"conversation.resume": "history agent claude codex pi opencode continue reopen restore past chat",
	"project.open":        "folder directory repo repository workspace add",
	"palette.open":        "switch jump search find command palette",
	"session.attention":   "waiting blocked attention notification needs input agent",
	"agents.toggle":       "agents cockpit inbox approvals thread mail board",
	"session.next":        "switch tab forward",
	"session.previous":    "switch tab back backward",
	"session.rename":      "name title label",
	"pane.restart":        "rerun relaunch reload reset shell",
	"pane.close":          "pane remove kill exit",
	"session.close":       "remove kill exit end tab",
	"find.open":           "search look up text grep",
	"find.next":           "search again",
	"find.previous":       "search again back",
	"pane.split.right":    "vertical horizontal side by side new pane",
	"pane.split.down":     "horizontal vertical below new pane",
	"pane.browser":        "browser web page url preview localhost open",
	"browser.address":     "browser web url address go to navigate",
	"pane.zoom":           "maximize fullscreen focus expand",
	"pane.next":           "switch focus forward",
	"pane.previous":       "switch focus back backward",
	"pane.left":           "focus move navigate",
	"pane.right":          "focus move navigate",
	"pane.up":             "focus move navigate above",
	"pane.down":           "focus move navigate below",
	quickAction:           "quake doom drop down dropdown hotkey global visor guake iterm console",
	"sidebar.toggle":      "show hide sidebar sessions list",
	"changes.toggle":      "git diff branch panel changes review",
	"files.toggle":        "explorer tree folder browse project files",
	"rightSidebar.toggle": "show hide right sidebar panel changes files",
	"text.bigger":         "zoom in font size larger increase text",
	"text.smaller":        "zoom out font size decrease text",
	"text.reset":          "zoom reset font size default text",
}

// pageWords find Settings' pages in the palette besides their titles.
var pageWords = map[string]string{
	"general":       "preferences startup",
	"appearance":    "theme color dark light font look",
	"terminal":      "font cursor scrollback ghostty",
	"shell":         "env environment variables path command",
	"browser":       "web ad block adblock trackers privacy",
	"notifications": "alerts sound badge dock notify",
	"keyboard":      "shortcuts keybindings keys hotkeys",
	"agents":        "claude codex ai skill cli remote control",
	"hosts":         "remote ssh server machine tailscale wsl",
	"advanced":      "config file daemon kerod log reset",
}

// matches reports whether every word of a query is in a text.
func matches(text string, terms []string) bool {
	text = strings.ToLower(text)
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return len(terms) > 0
}

// searchResults returns what a query finds, by page: the labels of the
// settings it finds, an empty list for a page whose title it is, and
// nothing for the others. It returns nil for an empty query.
func searchResults(query string, shortcuts map[string]string) map[string][]string {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	out := map[string][]string{}
	for _, p := range settingsPages {
		if matches(p.title(), terms) {
			out[p.id] = []string{}
		}
	}
	for _, e := range searchEntries(shortcuts) {
		if matches(e.label+" "+e.words, terms) {
			out[e.page] = append(out[e.page], e.label)
		}
	}
	return out
}

// The search's finds on the page shown, by label, which settingRow and
// settingsGroup highlight as they build; nil while not searching. The
// Settings window is the only view that builds them, and sets these
// before it does.
var (
	searchHits   map[string]bool
	searchReveal string // the find to scroll to, in this build only
)

// searchHighlight marks a find of the search behind el, a row or a
// title, as a tint wider than it by dx on each side, and taller by dy.
func searchHighlight(c *ui.Context, el ui.Element, label string, pal *palette, dx, dy float32) {
	hit := searchHits[label]
	t := el.Animate("hit", b2f(hit), 180*time.Millisecond)
	if label == searchReveal {
		el.ScrollIntoView()
	}
	if t > 0 {
		ui.Box(c.Key("hit")).Absolute().Top(-dy).Bottom(-dy).Left(-dx).Right(-dx).Radius(8).PassThrough().
			Background(pal.accent.Alpha(0.13*t)).InsetShadow(0, 0, 0, 1, pal.accent.Alpha(0.28*t))
	}
}
