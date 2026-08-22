//go:build unix

package applist

// Joining the two sources into the one list a launcher ranks.
//
// The list is not the concatenation of the sources. Almost every desktop entry
// runs a program that is already on $PATH, so concatenating them offers the
// same program twice under two names, and a fuzzy list full of near-synonyms is
// the exact thing that makes the right row hard to reach. Supersession is the
// answer: where the two sources describe the same program, the desktop entry
// takes the $PATH entry's place instead of standing beside it.
//
// Merge is pure and takes both lists as arguments. It touches no filesystem and
// keeps no cache of its own, because the caller already holds a Cache and a
// DesktopCache and is the only party that knows when either has moved.

import (
	"path/filepath"
	"strings"
)

// Merge combines the two sources into one list a launcher can rank.
//
// Desktop entries come first and the surviving $PATH entries follow. With no
// query typed, the head of the list is what a person sees, and the things they
// recognise are the applications their desktop shows them; several thousand
// $PATH names, most of which are libraries' helper binaries, are not a first
// impression of anything. Within each half the caller's order is kept, which is
// the menu order ScanDesktop sorted by and the $PATH precedence Scan resolved.
//
// The result is deduplicated by the resulting Name, first occurrence winning,
// which is the same rule Scan applies within $PATH.
func Merge(path []Entry, desktop []DesktopEntry) []Entry {
	// Index the $PATH entries by name so supersession costs one lookup per
	// desktop entry rather than a walk of several thousand rows.
	byName := make(map[string]Entry, len(path))
	for _, p := range path {
		if _, dup := byName[p.Name]; dup {
			continue
		}
		byName[p.Name] = p
	}

	// An action carries only the icon its own group declared, which is almost
	// always none, so the file it belongs to is what it borrows one from. The
	// spec is right that an action does not inherit Icon, but that rule is about
	// what the entry means, and a list of four Firefox rows where only the first
	// one has the Firefox mark is a list that looks broken.
	icons := make(map[string]string, len(desktop))
	for _, d := range desktop {
		if d.Action == "" && d.Icon != "" {
			icons[d.FileID] = d.Icon
		}
	}

	out := make([]Entry, 0, len(path)+len(desktop))
	seen := make(map[string]struct{}, len(path)+len(desktop))
	gone := make(map[string]struct{}, len(desktop))

	for _, d := range desktop {
		e, ok := entryFromDesktop(d)
		if !ok {
			continue
		}
		if e.Icon == "" {
			e.Icon = icons[d.FileID]
		}
		if _, dup := seen[e.Name]; dup {
			continue
		}
		seen[e.Name] = struct{}{}
		if name, sup := supersedes(d, byName); sup {
			gone[name] = struct{}{}
			// The row that replaces a program answers to that program's name,
			// because that is the name the user has always typed.
			e.Binary = name
		}
		out = append(out, e)
	}

	for _, p := range path {
		if _, dropped := gone[p.Name]; dropped {
			continue
		}
		if _, dup := seen[p.Name]; dup {
			continue
		}
		seen[p.Name] = struct{}{}
		out = append(out, p)
	}
	return out
}

// entryFromDesktop turns one desktop entry into a list row, reporting whether
// it is one worth offering.
//
// An entry with no Argv is dropped rather than kept. Path is the .desktop file
// and running that is not running the program, so the row could only fail when
// activated, and a row that fails is worse than a row that is not there.
// ScanDesktop never produces one; the check is for entries a caller built by
// hand.
func entryFromDesktop(d DesktopEntry) (Entry, bool) {
	name := desktopName(d)
	if name == "" || len(d.Argv) == 0 {
		return Entry{}, false
	}
	detail := d.Generic
	if detail == "" {
		// GenericName says what the program is ("Web Browser") and Comment says
		// what it does, so the shorter one is preferred and Comment fills in
		// only where there is no GenericName to prefer.
		detail = d.Comment
	}
	return Entry{
		Name: name,
		Path: d.Path,
		// The .desktop file's own directory, which for the same reason as a
		// $PATH entry's Dir is the only way to tell a user's override apart
		// from the system entry it shadows.
		Dir:      filepath.Dir(d.Path),
		Source:   SourceDesktop,
		Display:  d.Name,
		Detail:   detail,
		Icon:     d.Icon,
		Exec:     d.Argv,
		Cwd:      d.WorkDir,
		Terminal: d.Terminal,
		Keywords: d.Keywords,
	}, true
}

// desktopName is the entry's Name in the merged list: the desktop file ID with
// ".desktop" trimmed off, plus ":action" for an action.
//
// The suffix is on every ID and so distinguishes nothing, while the stem is
// what a person would type and what frecency records under. The action tail
// stays, because an action is a row of its own and needs an identity of its
// own.
func desktopName(d DesktopEntry) string {
	file, action, isAction := strings.Cut(d.ID, ":")
	name := strings.TrimSuffix(file, ".desktop")
	if isAction {
		return name + ":" + action
	}
	return name
}

// genericLauncher names the programs a desktop entry runs something through
// rather than is. Each is a common Exec prefix and each is a program a user
// would be annoyed to lose from a list that claims to hold what a shell can
// run, which is what superseding on one costs.
//
// The set is written out by hand because there is no property of a program that
// says "I am a wrapper". It does not have to be complete: a name missing from
// it costs one wrong supersession, which is what the list looked like before it
// existed, rather than anything worse.
var genericLauncher = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"env": true, "xdg-open": true, "gio": true, "kioclient": true,
	"flatpak": true, "snap": true, "appimagelauncher": true,
	"sudo": true, "pkexec": true, "gtk-launch": true, "dbus-send": true,
	"dbus-launch": true, "systemd-run": true, "nohup": true, "setsid": true,
}

// supersedes reports which $PATH name d takes the place of, if any.
//
// The match is on the basename of the program d actually runs, because that is
// the name the $PATH entry is listed under. A desktop entry wins the position
// because it carries a human name, a description and an icon, and the $PATH row
// it replaces carries none of those while running the same program.
func supersedes(d DesktopEntry, byName map[string]Entry) (string, bool) {
	// An action is an extra verb on an application, not the application. Two
	// of them can name the same program, and neither is a replacement for it:
	// hiding "firefox" because one of its actions opens a private window would
	// lose the plain way to start it.
	if strings.Contains(d.ID, ":") {
		return "", false
	}
	if len(d.Argv) == 0 {
		return "", false
	}
	argv0 := d.Argv[0]
	name := filepath.Base(argv0)
	if genericLauncher[name] {
		// The entry runs through a wrapper rather than being that wrapper. An
		// Exec of "sh -c ..." does not make the entry a replacement for /bin/sh,
		// and "xdg-open heroic://..." does not make a game the replacement for
		// xdg-open. Superseding here deletes a genuinely useful program from the
		// list and hands its name to something unrelated, which is exactly what
		// happened to sh and xdg-open on a real desktop.
		return "", false
	}
	p, ok := byName[name]
	if !ok {
		return "", false
	}
	if !strings.ContainsRune(argv0, filepath.Separator) {
		// A bare name is resolved by the shell's $PATH rule, which is the rule
		// Scan already applied, so it is this entry by construction.
		return name, true
	}
	// A path is the same program only when it is the very file Scan listed.
	// /opt/vendor/bin/code and /usr/bin/code share a basename and nothing else,
	// and hiding one because the other exists would lose a program.
	if filepath.Clean(argv0) == filepath.Clean(p.Path) {
		return name, true
	}
	return "", false
}
