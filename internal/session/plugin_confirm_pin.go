package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/herdrplugin"
)

// What a Yes to a plugin change pins.
//
// The pane that asked owns the plugin's folder, so after the Yes it could
// change what the dialog showed before the change runs. The grant keeps the
// manifest's trust hash, which covers what the plugin runs and its grants,
// and a digest of every file in the folder at the moment the dialog opened.
// Each trust verb that takes the token reads the folder again and refuses
// on any difference. A build writes its outputs after the Yes, so for a
// plugin with build commands a file may appear or change under an output
// path: bin/, or a path a build command names after -o or --output. Any
// other new or changed file, such as a source file the build would
// compile, is a difference. A plugin with no build has no output path, so
// every new or changed file is one.

// The bounds of a folder the daemon reads for a pin.
const (
	pinMaxFiles = 20000
	pinMaxBytes = 1 << 30
)

// pluginPin is what the dialog showed of the plugin in a folder.
type pluginPin struct {
	manifest string
	files    map[string]string
	// outputs are the paths, in the folder, a build may write.
	outputs []string
}

// errPinTooLarge is a folder too large to read for a pin.
var errPinTooLarge = errors.New("the plugin folder is too large for tuios to check")

// pinPlugin reads the plugin in root: its manifest and the digest of each
// file, by its path in root. A folder named .git is left out.
func pinPlugin(root string) (pluginPin, error) {
	p, perr := herdrplugin.Load(root)
	if perr != nil {
		return pluginPin{}, errors.New(perr.Msg)
	}
	pin := pluginPin{manifest: herdrplugin.TrustHash("", p, p.Grants), files: map[string]string{}, outputs: buildOutputs(p)}
	var total int64
	err := filepath.WalkDir(p.PluginRoot, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if de.Name() == ".git" && path != p.PluginRoot {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(p.PluginRoot, path)
		if err != nil {
			return err
		}
		if len(pin.files) >= pinMaxFiles {
			return errPinTooLarge
		}
		switch {
		case de.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			pin.files[rel] = "link " + target
		case de.Type().IsRegular():
			info, err := de.Info()
			if err != nil {
				return err
			}
			if total += info.Size(); total > pinMaxBytes {
				return errPinTooLarge
			}
			sum, err := fileDigest(path)
			if err != nil {
				return err
			}
			pin.files[rel] = sum + " " + info.Mode().Perm().String()
		}
		return nil
	})
	if err != nil {
		return pluginPin{}, err
	}
	return pin, nil
}

// buildOutputs are the paths in the plugin folder that its build may
// write: bin/, and each path a build command names after -o or --output.
// A path outside the folder is left out. A plugin with no build writes
// nothing, so it has none.
func buildOutputs(p *herdrplugin.Plugin) []string {
	if len(p.Build) == 0 {
		return nil
	}
	out := []string{"bin"}
	for _, b := range p.Build {
		args := b.Command
		for i, a := range args {
			var path string
			switch {
			case (a == "-o" || a == "--output") && i+1 < len(args):
				path = args[i+1]
			case strings.HasPrefix(a, "-o="):
				path = strings.TrimPrefix(a, "-o=")
			case strings.HasPrefix(a, "--output="):
				path = strings.TrimPrefix(a, "--output=")
			default:
				continue
			}
			if filepath.IsAbs(path) {
				continue
			}
			if c := filepath.Clean(path); c != "." && c != ".." && !strings.HasPrefix(c, ".."+string(filepath.Separator)) {
				out = append(out, c)
			}
		}
	}
	return out
}

// output reports whether rel is a build output, or inside one.
func (pin *pluginPin) output(rel string) bool {
	for _, o := range pin.outputs {
		if rel == o || strings.HasPrefix(rel, o+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// changedIn says what in root differs from the pin, and "" when nothing
// does.
func (pin *pluginPin) changedIn(root string) string {
	now, err := pinPlugin(root)
	if err != nil {
		return "tuios cannot read it again (" + strings.TrimSuffix(err.Error(), ".") + ")"
	}
	if now.manifest != pin.manifest {
		return "what it runs or its grants are not what the dialog showed"
	}
	var changed []string
	for rel, sum := range pin.files {
		// A build rewrites its outputs, so a file under one may change.
		if now.files[rel] != sum && !pin.output(rel) {
			changed = append(changed, rel)
		}
	}
	for rel := range now.files {
		if _, had := pin.files[rel]; !had && !pin.output(rel) {
			changed = append(changed, rel)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	sort.Strings(changed)
	if len(changed) > 3 {
		changed = append(changed[:3], "and more")
	}
	return "these files are not what they were: " + strings.Join(changed, ", ")
}
