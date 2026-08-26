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
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
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

// HTTPError is any other refusal from the release API, kept with its status so
// a caller can tell a missing repository from a server having a bad day.
type HTTPError struct {
	Status int
	URL    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s returned %d %s", e.URL, e.Status, http.StatusText(e.Status))
}

// GitHub reads releases from api.github.com.
type GitHub struct {
	// Client is the HTTP client. Zero means a client with Timeout, which is the
	// only setting that matters here: the default client has none at all, so a
	// hung connection would hang the command forever.
	Client *http.Client
	// Repo is owner/name. Zero means Repo.
	Repo string
	// Token is an optional API token, which raises the rate limit from sixty
	// requests an hour to five thousand. Read from the environment by
	// NewGitHub; never required.
	Token string
	// BaseURL is the API root, overridable so a test can point at a local
	// server. Zero means the real one.
	BaseURL string
}

// httpTimeout bounds the whole request. Generous enough for a release archive
// on a slow link and short enough that a black hole is reported rather than
// waited on.
const httpTimeout = 60 * time.Second

// NewGitHub is a GitHub source with the usual settings, including a token from
// the environment when one is there.
//
// GITHUB_TOKEN and GH_TOKEN are read because the unauthenticated limit is sixty
// requests an hour per address, which a shared network exhausts without anyone
// doing anything unusual. Neither is required and neither is created here.
func NewGitHub() *GitHub {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return &GitHub{
		Client: &http.Client{Timeout: httpTimeout},
		Repo:   Repo,
		Token:  token,
	}
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: httpTimeout}
}

func (g *GitHub) repo() string {
	if g.Repo != "" {
		return g.Repo
	}
	return Repo
}

func (g *GitHub) baseURL() string {
	if g.BaseURL != "" {
		return strings.TrimSuffix(g.BaseURL, "/")
	}
	return "https://api.github.com"
}

// ghRelease is the subset of the API's release object this reads. Everything
// else in that payload is ignored rather than modelled: a field this does not
// need is a field that can change shape without breaking anything.
type ghRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (r ghRelease) toRelease() Release {
	out := Release{Tag: r.TagName, Prerelease: r.Prerelease, Draft: r.Draft, URL: r.HTMLURL}
	for _, a := range r.Assets {
		out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return out
}

// Latest implements Source.
//
// Without prereleases it asks for releases/latest, which is GitHub's own answer
// and already excludes drafts and prereleases. With them it lists releases and
// takes the newest non-draft, because releases/latest would skip exactly the
// one that was asked for.
func (g *GitHub) Latest(ctx context.Context, withPrerelease bool) (Release, error) {
	if !withPrerelease {
		var rel ghRelease
		if err := g.getJSON(ctx, g.baseURL()+"/repos/"+g.repo()+"/releases/latest", &rel); err != nil {
			return Release{}, err
		}
		if rel.TagName == "" {
			return Release{}, ErrNoRelease
		}
		return rel.toRelease(), nil
	}

	var list []ghRelease
	if err := g.getJSON(ctx, g.baseURL()+"/repos/"+g.repo()+"/releases?per_page=20", &list); err != nil {
		return Release{}, err
	}
	// The API returns newest first, so the first non-draft is the answer. A
	// draft is skipped rather than reported: its assets are not public and
	// downloading one would need the token that listed it.
	for _, rel := range list {
		if !rel.Draft && rel.TagName != "" {
			return rel.toRelease(), nil
		}
	}
	return Release{}, ErrNoRelease
}

// Fetch implements Source.
func (g *GitHub) Fetch(ctx context.Context, url string) (io.ReadCloser, error) {
	resp, err := g.do(ctx, url, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (g *GitHub) getJSON(ctx context.Context, url string, into any) error {
	resp, err := g.do(ctx, url, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Bounded, so a wrong URL that answers with something enormous cannot be
	// read into memory. A release listing is a few tens of kilobytes.
	body := io.LimitReader(resp.Body, 4<<20)
	if err := json.NewDecoder(body).Decode(into); err != nil {
		return fmt.Errorf("failed to read the release listing: %w", err)
	}
	return nil
}

func (g *GitHub) do(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "tuios-update")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	_ = resp.Body.Close()
	// 403 and 429 are both how a spent allowance arrives, and the remaining
	// header is what tells them apart from a genuine refusal.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, &RateLimitError{Reset: parseResetHeader(resp.Header.Get("X-RateLimit-Reset"))}
		}
	}
	return nil, &HTTPError{Status: resp.StatusCode, URL: url}
}

// parseResetHeader reads GitHub's reset stamp, which is seconds since the
// epoch. A header that is missing or unparsable gives the zero time, and
// RateLimitError says nothing about a time it does not have.
func parseResetHeader(v string) time.Time {
	secs, err := strconv.ParseInt(v, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}
