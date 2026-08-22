//go:build !unix

package app

import "github.com/Gaurav-Gosain/tuios/pkg/applist"

// Desktop entries are a freedesktop concept, so off unix $PATH is the whole of
// what a launcher can offer. That is not a loss: $PATH is the list a shell
// would run, which is the list this is really about.

// desktopCache is the placeholder for a source this platform does not have.
type desktopCache struct{}

// scanLauncherSources rescans $PATH and returns it. See the unix file for the
// version that has a second source to merge.
func scanLauncherSources(path *applist.Cache, _ *desktopCache) []applist.Entry {
	entries, _ := path.Refresh()
	return entries
}

func newDesktopCache() *desktopCache { return nil }
