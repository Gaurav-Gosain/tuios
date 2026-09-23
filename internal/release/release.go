// Package release finds published tuios releases and turns one into a verified
// binary on disk.
//
// It exists as its own package for one reason: the lookup has to be faked in
// tests. A release check that reached api.github.com from `go test` would be a
// test that fails on a plane, fails behind a proxy, and fails when the rate
// limit is spent, so Source is an interface and the GitHub client is one
// implementation of it.
//
// Nothing here decides whether an update should happen. That is provenance.go's
// job, and it is deliberately separate: knowing what the newest release is and
// knowing whether this particular binary is ours to replace are different
// questions, and the second one is the one that has to be answered first.
//
// The GitHub client lives in github.go, which the browser build leaves out: a
// browser tab never updates tuios, and net/http is a large part of a download.
package release

import (
	"context"
	"errors"
	"io"
	"time"
)

// Repo is the repository releases are published to.
const Repo = "Gaurav-Gosain/tuios"

// Release is one published release, reduced to what an update needs.
type Release struct {
	// Tag is the git tag, "v0.7.0". Release archives spell the version without
	// the leading v; see AssetName.
	Tag        string
	Prerelease bool
	Draft      bool
	Assets     []Asset
	// URL is the release's page, for a message that has to send the user
	// somewhere a browser can open.
	URL string
}

// Asset is one file attached to a release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// AssetNamed returns the asset with this exact name.
func (r Release) AssetNamed(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Source is where release information comes from. The whole of the network is
// behind this, so a test supplies a fake and never opens a socket.
type Source interface {
	// Latest is the newest release. withPrerelease decides whether a
	// prerelease counts as newest.
	Latest(ctx context.Context, withPrerelease bool) (Release, error)
	// Fetch reads one asset. The caller closes the reader.
	Fetch(ctx context.Context, url string) (io.ReadCloser, error)
}

// ErrNoRelease is returned when the repository has published nothing that fits
// the request. It is not a failure of the lookup, so a caller can say "there is
// nothing to update to" rather than reporting an error.
var ErrNoRelease = errors.New("no published release")

// RateLimitError is GitHub refusing to answer because the caller has spent its
// hourly allowance. It carries the reset time because "try again later" without
// a time is not an instruction.
type RateLimitError struct {
	Reset time.Time
}

func (e *RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return "GitHub is rate limiting this address"
	}
	return "GitHub is rate limiting this address until " + e.Reset.Format(time.Kitchen)
}
