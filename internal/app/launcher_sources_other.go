//go:build !unix

package app

import "github.com/Gaurav-Gosain/tuios/pkg/applist"

type desktopCache struct{}

func scanLauncherSources(path *applist.Cache, _ *desktopCache) []applist.Entry {
	entries, _ := path.Refresh()
	return entries
}

func newDesktopCache() *desktopCache { return nil }
