package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// TestPluginsInstallReadsWhatListShows: each SOURCE that tuios plugins list
// prints, and the same source with or without https:// and @ref, names the
// plugin it was installed from.
//
// Negative control: with the host case taken out of parsePluginSource,
// github.com/owner/repo fetches https://github.com/github.com/owner and the
// github.com rows fail.
func TestPluginsInstallReadsWhatListShows(t *testing.T) {
	dir := t.TempDir()
	const repo = "https://github.com/Gaurav-Gosain/tuios"
	commit := "0123456789abcdef0123456789abcdef01234567"
	for _, e := range []herdrplugin.LockEntry{
		{Source: repo, Commit: commit},
		{Source: repo, Ref: "v0.12.0", Commit: commit},
		{Source: repo, Subdir: "plugins/pr-status", Ref: "main", Commit: commit},
		{Source: repo + ".git", Ref: commit, Commit: commit},
		{Source: "https://gitlab.com/group/sub/plugin", Ref: "v1", Commit: commit},
		{Source: "git@codeberg.org:owner/plugin.git", Ref: "v2", Commit: commit},
		{Source: herdrplugin.LocalPrefix + dir},
		{Source: herdrplugin.ReleasePrefix + "pr-status", Ref: "v0.12.0", Commit: commit},
	} {
		listed := listedSource(&e)
		got, err := parsePluginSource(listed, installOpts{})
		if err != nil {
			t.Errorf("list shows %q, and install refuses it: %v", listed, err)
			continue
		}
		var source string
		switch {
		case got.release != "":
			source = herdrplugin.ReleasePrefix + got.release
		case got.local != "":
			source = herdrplugin.LocalPrefix + got.local
		default:
			source = got.url
			if got.ref != e.Ref || got.subdir != e.Subdir {
				t.Errorf("list shows %q, and install reads ref %q and folder %q, want %q and %q", listed, got.ref, got.subdir, e.Ref, e.Subdir)
			}
		}
		if !sameSource(source, e.Source) {
			t.Errorf("list shows %q, and install reads the source %q, want %q", listed, source, e.Source)
		}
	}

	for _, arg := range []string{
		"github.com/Gaurav-Gosain/tuios",
		"GitHub.com/Gaurav-Gosain/tuios",
		"https://github.com/Gaurav-Gosain/tuios",
		"Gaurav-Gosain/tuios",
	} {
		for _, ref := range []string{"", "v0.12.0", commit} {
			src := arg
			if ref != "" {
				src += "@" + ref
			}
			got, err := parsePluginSource(src, installOpts{})
			if err != nil {
				t.Errorf("%s: %v", src, err)
				continue
			}
			if !sameSource(got.url, repo) || got.ref != ref || got.subdir != "" {
				t.Errorf("%s reads as %q at %q in %q, want %s at %q", src, got.url, got.ref, got.subdir, repo, ref)
			}
		}
	}

	for _, bad := range []string{"github.com", "github.com/Gaurav-Gosain", "github.com//tuios"} {
		if got, err := parsePluginSource(bad, installOpts{}); err == nil {
			t.Errorf("%s reads as %q, want an error", bad, got.url)
		}
	}
}
