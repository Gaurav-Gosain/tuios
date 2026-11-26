package app

import (
	"slices"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/pathroot"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// # One listing model
//
// Four views show the rows of a folder: the rail's files section, the open
// folders of its tree, the quick look folder walk and the path picker. Each
// reads a folder through readListing and holds the answer as a fileListing.
// So a folder looks the same in each of them: the same order, the same icons,
// the same cap, and the same words when it cannot be read.
//
// What a view keeps beside the listing is its own: the rail's generation and
// the pane it follows (fileViewState), the tree's read stamp (fileTreeDir),
// quick look's selection and stats (quickLookState), the picker's filter.
// None of that is in the model, because no two views share a cursor.
//
// The Files view takes one fileListing for each side, read the same way. It
// asks for the details the rail does not draw (size, mode and time from
// file-list), and those go beside the entries here when it lands, so the rail
// and the view keep one value and one reader.

// fileListing is one folder's listing as a view holds it.
type fileListing struct {
	// Dir is the folder the entries belong to, absolute and cleaned.
	Dir string
	// Entries is the listing, folders first and then files, each group in the
	// order a person reads a name rather than the order a byte sorts.
	Entries []fileEntry
	// Capped says the folder held more names than were read, so Entries is
	// the first fileViewMaxEntries of it and not the whole thing.
	Capped bool
	// Err is why the listing is empty, when that is the reason.
	Err string
	// Spoofed says the pane named this folder over OSC 7 and /proc disagreed.
	// The listing stays; the file actions do not. See cwdIsSpoofed.
	Spoofed bool
}

// sameAs reports whether two listings draw the same rows. A quiet read that
// answers the same as what is on screen draws nothing again.
func (l *fileListing) sameAs(o *fileListing) bool {
	return l.Dir == o.Dir && l.Err == o.Err && l.Capped == o.Capped &&
		l.Spoofed == o.Spoofed && slices.Equal(l.Entries, o.Entries)
}

// readListing reads dir through the daemon when there is one, and answers
// with its listing. It runs off the update goroutine. Every view that lists a
// folder calls it, so no two of them can disagree about where a folder is
// read from or what its rows are.
func readListing(client *session.TUIClient, host, origin, dir string, pinned bool, pgid int, wasSpoofed bool) fileListing {
	if client != nil {
		listing, err := client.ReadDir(origin, dir, fileViewMaxEntries, pinned)
		switch {
		case err == nil && listing.Err != "":
			return fileListing{Dir: dir, Err: listing.Err, Spoofed: wasSpoofed || listing.Spoofed}
		case err == nil:
			entries := make([]fileEntry, 0, len(listing.Entries))
			for _, e := range listing.Entries {
				// Already in the daemon's order: directories first, then
				// names. Sorting again here would be sorting a capped
				// listing, which is an arbitrary subset of the directory.
				entries = append(entries, fileEntry{
					Name: e.Name, Dir: e.IsDir, Icon: fileIconFor(e.Name, e.IsDir),
				})
			}
			return fileListing{
				Dir: dir, Entries: entries,
				Capped: listing.Capped, Spoofed: wasSpoofed || listing.Spoofed,
			}
		case host != "":
			// A daemon on another machine that cannot answer is the end of
			// it. Falling through to read this machine's disk would list a
			// directory with nothing to do with the pane, which is the bug
			// this replaced rather than a fallback.
			return fileListing{Dir: dir, Err: "That machine could not list it."}
		}
		// A local daemon that could not answer falls through: a build older
		// than this message answers with an error, and reading the disk here
		// is the same disk it would have read.
	}
	spoofed := wasSpoofed || cwdIsSpoofed(pgid, dir)
	items, capped, err := readDirFunc(pathroot.ExpandHome(dir), fileViewMaxEntries)
	if err != nil {
		return fileListing{Dir: dir, Err: session.DirReadError(err), Spoofed: spoofed}
	}
	entries := make([]fileEntry, 0, len(items))
	for _, it := range items {
		// Type() is what the directory read already returned, so this costs
		// no stat. A symlink to a directory therefore reads as a file, which
		// is the price of not stat'ing every name in a large tree. The icon
		// is looked up off the same two facts and for the same reason.
		dir := it.IsDir()
		entries = append(entries, fileEntry{
			Name: it.Name(),
			Dir:  dir,
			Icon: fileIconFor(it.Name(), dir),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return fileListing{Dir: dir, Entries: entries, Capped: capped, Spoofed: spoofed}
}
