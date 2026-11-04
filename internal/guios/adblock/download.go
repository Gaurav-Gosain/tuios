package adblock

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source is a filter list kero downloads: one of Catalog, by its ID, or
// one the user names by its URL.
type Source struct {
	ID, Name, URL string
}

// Catalog are the filter lists Settings offers, in Adblock Plus's syntax.
// EasyList and EasyPrivacy are the defaults (DefaultLists).
var Catalog = []Source{
	{"easylist", "EasyList", "https://easylist.to/easylist/easylist.txt"},
	{"easyprivacy", "EasyPrivacy", "https://easylist.to/easylist/easyprivacy.txt"},
	{"peter-lowe", "Peter Lowe's Ad and tracking server list", "https://pgl.yoyo.org/adservers/serverlist.php?hostformat=adblockplus&showintro=0&mimetype=plaintext"},
	{"easylist-china", "EasyList China", "https://easylist-downloads.adblockplus.org/easylistchina.txt"},
	{"fanboy-cookie", "EasyList Cookie List", "https://secure.fanboy.co.nz/fanboy-cookiemonster.txt"},
	{"fanboy-social", "Fanboy's Social Blocking List", "https://easylist.to/easylist/fanboy-social.txt"},
	{"fanboy-annoyance", "Fanboy's Annoyance List", "https://secure.fanboy.co.nz/fanboy-annoyance.txt"},
}

// DefaultLists are the lists blocking starts with: ads, and trackers.
var DefaultLists = []string{"easylist", "easyprivacy"}

// Find returns the source a setting names: a list of Catalog by its ID,
// or an http or https URL of another.
func Find(id string) (Source, bool) {
	for _, s := range Catalog {
		if s.ID == id {
			return s, true
		}
	}
	if u, err := url.Parse(id); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return Source{ID: id, Name: u.Host, URL: id}, true
	}
	return Source{}, false
}

// file names the file a source is downloaded into: a list of another's
// URL by a hash of it.
func (s Source) file() string {
	if s.ID != s.URL {
		return s.ID + ".txt"
	}
	h := fnv.New64a()
	h.Write([]byte(s.URL))
	return fmt.Sprintf("url-%016x.txt", h.Sum64())
}

// Refresh is how old a list downloaded gets before it is downloaded again.
const Refresh = 7 * 24 * time.Hour

// Load returns the filters of the sources, downloaded into dir: a list
// is downloaded when dir has none, or one older than Refresh, and a list
// that cannot be downloaded is the one dir has, if any.
func Load(ctx context.Context, dir string, sources []Source) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var lists []string
	var errs []error
	for _, s := range sources {
		path := filepath.Join(dir, s.file())
		info, statErr := os.Stat(path)
		if statErr != nil || time.Since(info.ModTime()) > Refresh {
			var since time.Time
			if statErr == nil {
				since = info.ModTime()
			}
			if err := download(ctx, s.URL, path, since); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", s.ID, err))
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue // not downloaded, which errs tells
		}
		lists = append(lists, string(b))
	}
	if len(lists) == 0 {
		return nil, errors.Join(errs...)
	}
	return lists, nil
}

// download saves url's body at path, unless it did not change since a
// copy of that time, which then counts as downloaded now.
func download(ctx context.Context, rawURL, path string, since time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if !since.IsZero() {
		req.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	now := time.Now()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return os.Chtimes(path, now, now)
	case http.StatusOK:
	default:
		return fmt.Errorf("%s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	// Not a captive portal's page, say: lists start with a header, as
	// "[Adblock Plus 2.0]", or a comment.
	if t := strings.TrimSpace(string(body[:min(len(body), 64)])); !strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "!") {
		return fmt.Errorf("%s is no filter list", rawURL)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
