//go:build unix

package app

import "github.com/Gaurav-Gosain/tuios/pkg/applist"

func scanLauncherSources(path *applist.Cache, desktop *desktopCache) []applist.Entry {
	entries, _ := path.Refresh()
	if desktop == nil {
		return entries
	}
	apps, _ := desktop.Refresh()
	return applist.Merge(entries, apps)
}

type desktopCache = applist.DesktopCache

func newDesktopCache() *desktopCache { return applist.NewDesktopCache() }
