package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
	"github.com/Gaurav-Gosain/tuios/internal/release"
)

type firstPartyPlugin struct {
	binary string
}

var firstParty = map[string]firstPartyPlugin{
	"ovnfs":     {},
	"pr-status": {},
	"sketch":    {binary: "sketch"},
	"themes":    {},
}

func pluginsAsset(version string) string {
	return "tuios-plugins_" + strings.TrimPrefix(version, "v") + ".tar.gz"
}

func firstPartyNames() string {
	names := make([]string, 0, len(firstParty))
	for n := range firstParty {
		names = append(names, n)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

func releaseSource() *release.GitHub {
	g := release.NewGitHub()
	g.BaseURL = os.Getenv("TUIOS_RELEASE_API")
	return g
}

func runningTag() (string, error) {
	if _, ok := release.ParseVersion(version); !ok {
		return "", errfmt.Errorf("this tuios is a %s build, which no release made, so it has no plugin assets. Install the plugin from its folder: tuios plugins install Gaurav-Gosain/tuios/plugins/NAME", version)
	}
	return "v" + strings.TrimPrefix(version, "v"), nil
}

func fetchFirstParty(name, tag, dir string) (string, error) {
	fp := firstParty[name]
	ctx, cancel := context.WithTimeout(context.Background(), networkTimeout)
	defer cancel()
	src := releaseSource()
	rel, err := src.Tag(ctx, tag)
	if err != nil {
		return "", errfmt.Errorf("%w %s %s: %v", errFetchFailed, "the release", tag, err)
	}
	sums, err := fetchChecksums(ctx, src, rel)
	if err != nil {
		return "", err
	}
	get := func(asset string) ([]byte, error) {
		a, ok := rel.AssetNamed(asset)
		if !ok {
			return nil, errfmt.Errorf("the release %s has no %s. Install the plugin from its folder: tuios plugins install Gaurav-Gosain/tuios/plugins/%s", tag, asset, name)
		}
		data, err := readAsset(ctx, src, a)
		if err != nil {
			return nil, err
		}
		if err := sums.Verify(asset, data); err != nil {
			return nil, explainChecksumError(err, rel)
		}
		return data, nil
	}
	pin := sha256.New()
	data, err := get(pluginsAsset(tag))
	if err != nil {
		return "", err
	}
	pin.Write(data)
	if err := extractPluginFolder(data, name, dir); err != nil {
		return "", err
	}
	if fp.binary != "" {
		asset, err := release.AssetName(fp.binary, tag, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return "", err
		}
		data, err := get(asset)
		if err != nil {
			return "", err
		}
		pin.Write(data)
		exe := release.ExecutableName(fp.binary)
		bin, err := release.BinaryFromArchive(bytes.NewReader(data), exe)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", exe), bin, 0o755); err != nil { //nolint:gosec // the plugin's own program
			return "", err
		}
	}
	return hex.EncodeToString(pin.Sum(nil)), nil
}

const maxPluginFile = 16 << 20

func extractPluginFolder(data []byte, name, dir string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return errfmt.Errorf("the plugin archive cannot be read: %w", err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errfmt.Errorf("the plugin archive cannot be read: %w", err)
		}
		rel, ok := strings.CutPrefix(path.Clean(hdr.Name), name+"/")
		if !ok || !filepath.IsLocal(rel) {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o700)
		case tar.TypeReg:
			found = true
			var body []byte
			body, err = io.ReadAll(io.LimitReader(tr, maxPluginFile+1))
			if err == nil && len(body) > maxPluginFile {
				err = errfmt.Errorf("%s in the plugin archive is too large", hdr.Name)
			}
			if err == nil {
				err = os.MkdirAll(filepath.Dir(target), 0o700)
			}
			if err == nil {
				err = os.WriteFile(target, body, os.FileMode(hdr.Mode)&0o755|0o600) //nolint:gosec // inside dir, checked above
			}
		}
		if err != nil {
			return err
		}
	}
	if !found {
		return errfmt.Errorf("the plugin archive has no plugin %s", name)
	}
	return nil
}

func (in *installer) installFirstParty(name string) error {
	tag, err := runningTag()
	if err != nil {
		return err
	}
	return in.firstPartyAt(name, tag, nil)
}

func (in *installer) firstPartyAt(name, tag string, old *herdrplugin.LockEntry) error {
	work, err := os.MkdirTemp(in.dirs.ManagedDir(), herdrplugin.FetchPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	fmt.Fprintf(in.out, "tuios gets the plugin %s of tuios %s.\n", name, tag)
	sum, err := fetchFirstParty(name, tag, work)
	if err != nil {
		return err
	}
	p, err := loadInside(work, work)
	if err != nil {
		return err
	}
	e := herdrplugin.LockEntry{ID: p.PluginID, Source: herdrplugin.ReleasePrefix + name, Ref: tag, Commit: sum[:40], Tree: sum}
	if old != nil {
		if old.ID != e.ID {
			return errfmt.Errorf("the release names the plugin %s, not %s", e.ID, old.ID)
		}
		e.Approved = old.Approved
	}
	if err := herdrplugin.CheckLockEntry(e); err != nil {
		return err
	}
	return in.finish(e, p, work)
}

func (in *installer) pinnedRelease(e herdrplugin.LockEntry) error {
	work, err := os.MkdirTemp(in.dirs.ManagedDir(), herdrplugin.FetchPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	name := strings.TrimPrefix(e.Source, herdrplugin.ReleasePrefix)
	if _, ok := firstParty[name]; !ok {
		return errfmt.Errorf("this tuios has no plugin %s in its release. Update tuios first", name)
	}
	sum, err := fetchFirstParty(name, e.Ref, work)
	if err != nil {
		return err
	}
	if sum != e.Tree {
		return errfmt.Errorf("the files of %s %s are different from the ones plugins.lock pins. Nothing was installed", name, e.Ref)
	}
	p, err := loadInside(work, work)
	if err != nil {
		return err
	}
	return in.finish(e, p, work)
}
