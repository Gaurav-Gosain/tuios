//go:build unix

package app

import "github.com/Gaurav-Gosain/tuios/pkg/applist"

// scanLauncherSources rescans everything the launcher can start and returns one
// merged list.
//
// Both sources are refreshed here, in the command's own goroutine, because both
// do filesystem work: $PATH is a readdir per directory whose mtime moved, and
// the desktop entries are a parse per file that changed. Merging here too means
// the Update goroutine is handed a finished list rather than two lists and a
// job.
func scanLauncherSources(path *applist.Cache, desktop *desktopCache) []applist.Entry {
	entries, _ := path.Refresh()
	if desktop == nil {
		return entries
	}
	apps, _ := desktop.Refresh()
	return applist.Merge(entries, apps)
}

// desktopCache is the cache of .desktop entries. Desktop entries are where a
// launcher row gets a human name and an icon, neither of which an executable on
// $PATH has. The alias exists so OS can name the field on every platform.
type desktopCache = applist.DesktopCache

func newDesktopCache() *desktopCache { return applist.NewDesktopCache() }
