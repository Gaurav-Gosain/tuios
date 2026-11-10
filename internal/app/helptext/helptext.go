// Package helptext holds the prose hover help shows: one table per language,
// keyed by the component help mode explains, so the text a person reads is
// data rather than switch arms in the renderer. It is a package of its own
// because a second language is a second table beside the first, never an edit
// to the engine.
//
// An entry carries the static part of an explanation. What varies with the
// running session does not live here: the engine substitutes the pane's name,
// its position, the counts it reads, and the keys the configuration binds,
// because those are values of the screen and not of the sentence. Nor is
// anything translated that a person types or that names a key: a CLI line, a
// config path and a key label stay as they are in every language.
package helptext

import (
	"log"
	"os"
	"slices"
	"strings"
)

// Entry is the text of one component's page.
type Entry struct {
	// Title is the page heading, such as "Dock" or "Session name".
	Title string
	// Purpose is the paragraph under the heading.
	Purpose string
	// Hidden is the sentence that opens the page of a component the layout
	// has no room for, such as a rail that is put away. It names the key that
	// brings the component back, which the engine passes as %s. A language
	// that has no sentence of its own reads the English one.
	Hidden func(key string) string
	// Plural is what the count after a page's name reads, in this language,
	// for a component drawn in n parts: the engine writes the number. Nil
	// reads "parts", which is the English of every language whose count does
	// not inflect.
	Plural func(n int) string
}

// The ids of the components this package holds text for. A dock cell's id is
// DockCell of the name its [dock] list uses.
const (
	IDDock = "dock"

	IDDockCustom        = "dock/custom"
	IDDockPlugin        = "dock/plugin"
	IDDockWorkspaces    = "dock/workspaces"
	IDDockWindows       = "dock/windows"
	IDDockSessionPanel  = "dock/session-controls"
	IDDockMessages      = "dock/messages"
	IDDockOverflow      = "dock/overflow"
	IDDockClock         = "dock/clock"
	IDDockCPU           = "dock/cpu"
	IDDockRAM           = "dock/ram"
	IDDockTransfers     = "dock/transfers"
	IDDockSession       = "dock/session"
	IDDockMode          = "dock/mode"
	IDDockTrail         = "dock/trail"
	IDDockTape          = "dock/tape"
	IDDockNotifications = "dock/notifications"
	IDDockCopyHelp      = "dock/copy-help"

	// The rail and its own controls.
	IDRail         = "rail"
	IDRailControls = "rail/controls"

	// The rail's sections, named as the row kind that draws them.
	IDRailSessions = "rail/sessions"
	IDRailPanes    = "rail/panes"
	IDRailAgents   = "rail/agents"
	IDRailFiles    = "rail/files"

	// The panes and the desktop. A pane's id carries the window it explains,
	// so its page is found by the kind alone: PanePage.
	IDPane    = "pane"
	IDDesktop = "desktop"
)

// PanePage is the entry id for the kind of a pane component: "pane:abc/title"
// reads pane/title, and "pane:abc/content" reads pane/content.
func PanePage(id string) string {
	kind := id
	if i := strings.Index(kind, "/"); i >= 0 {
		kind = kind[i:]
	} else {
		kind = ""
	}
	return IDPane + kind
}

// RailSection is the entry id for a rail section, named by the row kind that
// draws it: "sessions" reads rail/sessions.
func RailSection(kind string) string { return "rail/" + kind }

// DockCell is the entry id for a dock cell, whose name comes from the [dock]
// list the configuration carries: "mode" reads dock/mode.
func DockCell(name string) string { return "dock/" + name }

// Language is one language's table. A table covers what it covers: an id it
// lacks is read from English, so a translation may land a page at a time.
type Language struct {
	// Code is the language's own code, "en" or "fr".
	Code string
	// Entries is every page this language writes.
	Entries map[string]Entry
}

var english = Language{
	Code: "en",
	Entries: map[string]Entry{
		IDDock: {
			Title: "Dock",
			Purpose: "The dock is the status bar: the mode pill, the workspace strip, " +
				"minimized panes, session controls, notifications and whatever cells the " +
				"configuration puts on it. Clicking a cell acts on the row it names.",
		},
		IDDockCustom: {
			Title: "Dock cell: {name}",
			Purpose: "A custom dock cell. Its command is run on the refresh rule set for it, " +
				"and clicking it runs the on-click command.",
		},
		IDDockPlugin: {
			Title: "Dock cell: plugin",
			Purpose: "A cell supplied by a plugin. The plugin owns what it draws and what a " +
				"click does; its manifest and cells are described in the plugin's own " +
				"documentation.",
		},
		IDDockWorkspaces: {
			Title: "Workspaces",
			Purpose: "The workspace strip. Each pill is a workspace and its panes; clicking " +
				"one switches to it, and the arrows step a strip too long for the dock.",
		},
		IDDockWindows: {
			Title: "Minimized panes",
			Purpose: "One entry per minimized pane. Clicking an entry restores that pane; the " +
				"overflow marker opens the list of entries the dock had no room for.",
		},
		IDDockSessionPanel: {
			Title: "Session controls",
			Purpose: "The client controls: open the session list, leave this client (detach), " +
				"close the session, or start a new one. They act on the daemon session, not " +
				"on one pane.",
		},
		IDDockMessages: {
			Title: "Messages",
			Purpose: "The newest notification. Enter or a click acts on it, esc dismisses it, " +
				"and a cut message can be held to read the rest. Notifications also carry " +
				"agent asks.",
		},
		IDDockOverflow: {
			Title: "Minimized overflow",
			Purpose: "The marker for minimized panes the dock could not fit. It opens the " +
				"aggregate list, which names every one of them.",
		},
		IDDockClock: {
			Title: "Clock",
			Purpose: "The current time, drawn by the dock. Its format and whether it appears " +
				"are both appearance settings.",
		},
		IDDockCPU: {
			Title: "CPU",
			Purpose: "The CPU readout. It samples the machine while the dock is shown; turn " +
				"it off when the dock is not the place you watch load.",
		},
		IDDockRAM: {
			Title: "RAM",
			Purpose: "The memory readout, in the same units the dock uses for the machine's " +
				"total. It is a dock cell, not a per-pane figure.",
		},
		IDDockTransfers: {
			Title: "Transfers",
			Purpose: "The copy and paste buffer strip: what is held, and whether a transfer is " +
				"in flight. Clicking it opens the buffer list.",
		},
		IDDockSession: {
			Title: "Session name",
			Purpose: "The daemon session this client is showing, its accent, and whether other " +
				"clients are attached. Clicking it opens the session switcher.",
		},
		IDDockMode: {
			Title: "Mode",
			Purpose: "The input mode: window management or terminal. It is the dock's answer to " +
				"where a key will go, and it changes as the prefix menu opens.",
		},
		IDDockTrail: {
			Title: "Trail",
			Purpose: "The trail cell records where this client has been: the panes and " +
				"workspaces it visited, newest first. It is built into the dock and is not a " +
				"command runner.",
		},
		IDDockTape: {
			Title: "Tape",
			Purpose: "The tape cell reports a tape playing in this session, with its status. " +
				"It is drawn only while a tape is running.",
		},
		IDDockNotifications: {
			Title: "Notifications",
			Purpose: "The notification cell carries the newest message and its level. Its " +
				"duration and whether errors stay until dismissed are notification settings.",
		},
		IDDockCopyHelp: {
			Title: "Copy help",
			Purpose: "The copy-help cell is the dock's reminder of the copy-mode chord while " +
				"copy mode is available. It is documentation, not a control.",
		},

		IDRail: {
			Title: "Session rail",
			Purpose: "The session rail is the column beside the panes. It lists the sessions " +
				"on this machine and on other hosts, the panes of the session it is " +
				"following, the agents that need a person, the files below the focused pane, " +
				"and the controls that add and collapse.",
			Hidden: func(key string) string {
				return "This rail is hidden, so it has no rectangle to hover. Press " + key +
					" to show it, and the pane follows the layout as it changes."
			},
		},
		IDRailControls: {
			Title: "Rail controls",
			Purpose: "The rail's own controls: collapse it to a strip, widen or narrow it, put " +
				"the files section on and off, and move keyboard focus into the rail.",
		},
		IDRailSessions: {
			Title: "Sessions",
			Purpose: "One row per daemon session. Enter opens the session this client shows; " +
				"n makes a new one; r renames; x kills. The number ahead of a name is the " +
				"switch key.",
		},
		IDRailPanes: {
			Title: "Panes",
			Purpose: "The panes of the session the rail follows. Enter focuses one; the + makes " +
				"another. A pane row is also the drag handle for moving the pane between " +
				"workspaces.",
		},
		IDRailAgents: {
			Title: "Agents",
			Purpose: "Agents that are working, waiting for an answer, or finished. a cycles " +
				"all/here, s cycles priority/recent, m opens the mailbox, and Enter opens the " +
				"agent's pane.",
		},
		IDPane: {
			Title: "Pane: {name}",
			Purpose: "One terminal window. It owns a pty and a shell; keys go to the shell " +
				"in terminal mode and to the window manager in window mode. The title bar " +
				"names it and carries its buttons, and the body is the terminal.",
		},
		IDPane + "/title": {
			Title: "Title bar: {name}",
			Purpose: "The pane's title bar. It names the pane, shows which pane has focus " +
				"and which workspace it is on, and carries the close and minimize buttons. " +
				"Dragging it moves the pane; the edge under the pointer resizes it.",
		},
		IDPane + "/content": {
			Title: "Terminal body: {name}",
			Purpose: "The terminal itself. In terminal mode every key but the leader goes " +
				"to the shell; copy mode moves over the scrollback without sending " +
				"anything. The scroll bar and the selection are drawn here too.",
		},
		IDDesktop: {
			Title: "Workspace",
			Purpose: "The desktop behind the panes, on the current workspace. Clicking it " +
				"leaves terminal mode; the leader then a workspace key switches " +
				"workspaces. Tiling arranges the panes here, and the scratch pane opens " +
				"over them.",
		},
		IDRailFiles: {
			Title: "Files",
			Purpose: "The directory below the focused pane. Enter opens a name, e edits it in " +
				"the configured editor, / searches, and p pastes a copied or cut entry. The " +
				"header's cd sends the pane there.",
		},
	},
}

// Tables is every language this build carries, English first. A language
// joins it with its own table and no change to the engine.
var Tables = []Language{english, french}

// Table returns the table for a language code: "fr", "fr-FR" and "fr_FR.UTF-8"
// all name French. An empty code is English, and so is a code no table
// carries, which the caller reports once rather than at every lookup.
func Table(code string) Language {
	base := baseCode(code)
	if base == "" || base == english.Code {
		return english
	}
	for _, t := range Tables {
		if t.Code == base {
			return t
		}
	}
	return english
}

// Lookup returns an id's text from a table, in that table's language, and
// from English when the table has not translated it. The English text is the
// one that lands, so a page still reads; found says which table it came from.
func Lookup(t Language, id string) (e Entry, found bool) {
	if e, ok := t.Entries[id]; ok {
		return e, true
	}
	if e, ok := english.Entries[id]; ok {
		return e, false
	}
	return Entry{}, false
}

// Text is the page a component's name and paragraph are read from in a
// language, in the fallback order dockText and the rail's sections both need:
// the language's own page, then its dock page for what the dock has words
// for, then English. It always returns a page, with fallback as the name when
// neither table knows the id, because a component added before its prose
// still opens.
func Text(t Language, id, fallback string) Entry {
	if e, ok := Lookup(t, id); ok {
		return e
	}
	// A section of the rail is explained by the rail's own page when the
	// language writes one for it, and by its own word when it does not.
	if e, ok := Lookup(t, strings.Split(id, "/")[0]); ok && e.Plural != nil {
		return Entry{Title: fallback, Plural: e.Plural}
	}
	return Entry{Title: fallback}
}

// Missing is the ids of the pages a translation has not written, in a stable
// order, so a load can warn about them together rather than a page at a time.
func Missing(t Language) []string {
	out := make([]string, 0, len(english.Entries))
	for id := range english.Entries {
		if _, ok := t.Entries[id]; !ok {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Load reads the language of this client from the configuration and the
// environment, in that order: [ui] language wins, then LC_ALL, LC_MESSAGES and
// LANG, the order a program asks for a locale. It reports the table to read
// and warns once when the choice cannot be honoured: an unknown code, or a
// language whose table is missing pages. A warning never fails a load, and
// every missing page still reads in English.
func Load(configured string, getenv func(string) string) Language {
	if getenv == nil {
		getenv = os.Getenv
	}
	code, fromEnv := strings.TrimSpace(configured), false
	if code == "" {
		for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
			if v := strings.TrimSpace(getenv(key)); v != "" {
				code, fromEnv = v, true
				break
			}
		}
	}
	base := baseCode(code)
	// No language asked for: "C", "POSIX", and "C.UTF-8", whose codeset is
	// set and whose language is not. That is English and not a mistake, so
	// it says nothing.
	if base == "" || base == "c" || base == "posix" {
		return english
	}
	if base == english.Code {
		return english
	}
	if !hasTable(base) {
		// A locale and a config can both name a language this build has no
		// pages for. Either way the request stands and only the pages are
		// missing, so the message says which one asked.
		if fromEnv {
			log.Printf("Warning: locale %q names a language tuios does not carry, so help reads in English", code)
		} else {
			log.Printf("Warning: [ui] language = %q does not name a language tuios carries, so help reads in English", code)
		}
		return english
	}
	t := Table(base)
	if missing := Missing(t); len(missing) > 0 {
		log.Printf("Warning: %s is missing %d help page(s), which read in English: %s",
			t.Code, len(missing), strings.Join(missing, ", "))
	}
	return t
}

func hasTable(code string) bool {
	for _, t := range Tables {
		if t.Code == code {
			return true
		}
	}
	return false
}

// baseCode is the language of a locale or a code: "fr_FR.UTF-8" and "fr-FR"
// are both "fr". A bare "C" or "POSIX" stays as it is, so the caller can tell
// it apart from a language.
func baseCode(code string) string {
	code = strings.TrimSpace(code)
	if i := strings.IndexAny(code, "._-@"); i > 0 {
		code = code[:i]
	}
	return strings.ToLower(code)
}
