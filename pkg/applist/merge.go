//go:build unix

package applist

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
	byName := make(map[string]Entry, len(path))
	for _, p := range path {
		if _, dup := byName[p.Name]; dup {
			continue
		}
		byName[p.Name] = p
	}

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

func entryFromDesktop(d DesktopEntry) (Entry, bool) {
	name := desktopName(d)
	if name == "" || len(d.Argv) == 0 {
		return Entry{}, false
	}
	detail := d.Generic
	if detail == "" {
		detail = d.Comment
	}
	return Entry{
		Name:     name,
		Path:     d.Path,
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

func desktopName(d DesktopEntry) string {
	file, action, isAction := strings.Cut(d.ID, ":")
	name := strings.TrimSuffix(file, ".desktop")
	if isAction {
		return name + ":" + action
	}
	return name
}

var genericLauncher = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"env": true, "xdg-open": true, "gio": true, "kioclient": true,
	"flatpak": true, "snap": true, "appimagelauncher": true,
	"sudo": true, "pkexec": true, "gtk-launch": true, "dbus-send": true,
	"dbus-launch": true, "systemd-run": true, "nohup": true, "setsid": true,
}

func supersedes(d DesktopEntry, byName map[string]Entry) (string, bool) {
	if strings.Contains(d.ID, ":") {
		return "", false
	}
	if len(d.Argv) == 0 {
		return "", false
	}
	argv0 := d.Argv[0]
	name := filepath.Base(argv0)
	if genericLauncher[name] {
		return "", false
	}
	p, ok := byName[name]
	if !ok {
		return "", false
	}
	if !strings.ContainsRune(argv0, filepath.Separator) {
		return name, true
	}
	if filepath.Clean(argv0) == filepath.Clean(p.Path) {
		return name, true
	}
	return "", false
}
