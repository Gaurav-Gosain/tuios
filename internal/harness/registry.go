package harness

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

//go:generate go run gen.go

// bundledArchive is manifests/*.toml packed by gen.go, each file under its
// base name. The gzipped tar is about a quarter of the files' size. Run `go
// generate ./internal/harness` after editing a bundled manifest.
// TestBundledArchiveMatchesFiles fails while the archive is stale.
//
//go:embed manifests.tar.gz
var bundledArchive []byte

// bundledFile is one manifest from the archive, named "manifests/<file>".
type bundledFile struct {
	name string
	data []byte
}

// maxBundledBytes bounds what bundledFiles unpacks, in all and per entry. The
// manifests are about 100 KB.
const maxBundledBytes = 4 << 20

// bundledFiles unpacks the archive in name order. It unpacks on each call and
// keeps nothing, because Load runs once per daemon start and a kept copy would
// sit in memory for the life of the process.
func bundledFiles() ([]bundledFile, error) {
	zr, err := gzip.NewReader(bytes.NewReader(bundledArchive))
	if err != nil {
		return nil, fmt.Errorf("bundled manifests: %w", err)
	}
	// The archive is built into the binary, so these limits guard against a
	// bad gen.go or a corrupt commit, not an attacker: the whole stream stops
	// at maxBundledBytes, and a header cannot ask for more than that.
	tr := tar.NewReader(io.LimitReader(zr, maxBundledBytes))
	var out []bundledFile
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("bundled manifests: %w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > maxBundledBytes ||
			h.Name != path.Base(h.Name) || strings.ContainsAny(h.Name, `/\`) || !strings.HasSuffix(h.Name, ".toml") {
			return out, fmt.Errorf("bundled manifests: unexpected entry %q", h.Name)
		}
		data := make([]byte, h.Size)
		if _, err := io.ReadFull(tr, data); err != nil {
			return out, fmt.Errorf("bundled manifests: %s: %w", h.Name, err)
		}
		out = append(out, bundledFile{name: "manifests/" + h.Name, data: data})
	}
}

// Registry is the loaded set of harness manifests, ordered so a lookup is
// deterministic.
type Registry struct {
	manifests []*Manifest
}

// LoadError is one manifest that failed to load. Loading never fails as a whole:
// one bad file must not cost a user every other harness, so the errors come back
// alongside a working registry for the caller to log.
type LoadError struct {
	Source string
	Err    error
}

func (e LoadError) Error() string { return e.Err.Error() }

// Load builds the registry from the manifests compiled into the binary, then
// lets any manifest in dirs replace a bundled one with the same id.
//
// Replacement is whole-file rather than a merge: a user file with a bundled id
// takes the bundled manifest's place entirely, its detect, screen, title,
// notify, transcript and input blocks alike, and a block the user file leaves
// out is absent rather than inherited. Merging nested rule lists is a bug
// farm, because a rule has no name to merge by and its priority is relative to
// the rules around it, and a user who wants to start from the bundled rules
// can copy the file (the bundled copies live in internal/harness/manifests).
// Manifest.Source reports the replacement, so a diagnostic can say which file
// is in force. Later directories win over earlier ones.
func Load(dirs ...string) (*Registry, []LoadError) {
	byID := map[string]*Manifest{}
	var errs []LoadError

	files, err := bundledFiles()
	if err != nil {
		errs = append(errs, LoadError{Source: "manifests.tar.gz", Err: err})
	}
	for _, f := range files {
		m, err := parseManifest(f.name, f.data)
		if err != nil {
			errs = append(errs, LoadError{Source: f.name, Err: err})
			continue
		}
		m.source = "bundled"
		byID[m.ID] = m
	}

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
		if err != nil {
			continue
		}
		sort.Strings(files)
		for _, f := range files {
			data, err := os.ReadFile(f) //nolint:gosec // a path the user chose to put manifests in
			if err != nil {
				errs = append(errs, LoadError{Source: f, Err: err})
				continue
			}
			m, err := parseManifest(f, data)
			if err != nil {
				errs = append(errs, LoadError{Source: f, Err: err})
				continue
			}
			m.source = f
			if prev := byID[m.ID]; prev != nil {
				m.replacedBundled = prev.replacedBundled || prev.source == "bundled"
			}
			byID[m.ID] = m
		}
	}

	r := &Registry{manifests: make([]*Manifest, 0, len(byID))}
	for _, m := range byID {
		r.manifests = append(r.manifests, m)
	}
	// Highest priority first, then by id, so two manifests that both match the
	// same process always resolve to the same one.
	sort.Slice(r.manifests, func(i, j int) bool {
		if r.manifests[i].Priority != r.manifests[j].Priority {
			return r.manifests[i].Priority > r.manifests[j].Priority
		}
		return r.manifests[i].ID < r.manifests[j].ID
	})
	return r, errs
}

// UserDir is where a user's own manifests live. It follows XDG, so a manifest
// dropped there is picked up on the next daemon start with no rebuild.
func UserDir() string {
	if dir := os.Getenv("TUIOS_HARNESS_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "tuios", "harnesses")
}

// IDs lists the harness ids the registry knows, in lookup order.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.manifests))
	for _, m := range r.manifests {
		out = append(out, m.ID)
	}
	return out
}

// Lookup returns the manifest with an id, or nil.
func (r *Registry) Lookup(id string) *Manifest {
	for _, m := range r.manifests {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// SharedServer reports whether the harness with id runs every conversation's
// hooks from one shared server process. See Manifest.SharedServer.
// A nil registry knows no harness.
func (r *Registry) SharedServer(id string) bool {
	if r == nil {
		return false
	}
	m := r.Lookup(id)
	return m != nil && m.SharedServer
}

// CanProveIdle reports whether the harness has an enabled screen or title rule
// that reports idle, which is what lets a pane of it show positive evidence that
// the agent waits at its prompt. A harness without one can never reach idle
// from its screen, only from a hook or its own report.
func (r *Registry) CanProveIdle(id string) bool {
	return r.canShow(id, "idle")
}

// CanProveWorking reports whether the harness has an enabled screen or title
// rule that reports working. A pane of such a harness shows that it took a
// prompt by turning working, so for it new output alone is not evidence that a
// prompt was submitted: a TUI that read the carriage return as a newline also
// redraws.
func (r *Registry) CanProveWorking(id string) bool {
	return r.canShow(id, "working")
}

// canShow reports whether the harness has an enabled screen or title rule that
// reports state.
func (r *Registry) canShow(id, state string) bool {
	m := r.Lookup(id)
	if m == nil {
		return false
	}
	for _, rules := range []struct {
		enabled bool
		rules   []ScreenRule
	}{{m.Screen.Enabled, m.Screen.Rule}, {m.Title.Enabled, m.Title.Rule}} {
		if !rules.enabled {
			continue
		}
		for _, rule := range rules.rules {
			if rule.State == state {
				return true
			}
		}
	}
	return false
}

// Identify names the harness a process is, or "" when it is none of them. It is
// a pure function of its inputs and does no I/O, so it is safe to call on every
// detection tick.
//
// comm is /proc/<pid>/comm, argv the full command line, exe the resolved
// /proc/<pid>/exe. Any of the three may be empty; a predicate that needs a
// missing input simply does not match.
func (r *Registry) Identify(comm string, argv []string, exe string) (string, bool) {
	id, _, ok := r.IdentifyDetail(ProcInfo{Comm: comm, Argv: argv, Exe: exe})
	return id, ok
}

// IdentifyDetail is Identify with the predicate that decided it, for the
// diagnostic. The rule reads as it appears in the manifest ("comm=claude",
// "exe_glob=**/claude"), so what fired can be found in the file by searching for
// it.
func (r *Registry) IdentifyDetail(p ProcInfo) (id, rule string, ok bool) {
	run := p.RunToken()
	for _, m := range r.manifests {
		if rule, ok := m.Detect.matches(p, run); ok {
			return m.ID, rule, true
		}
	}
	return "", "", false
}

// Resolve finds the harness a person named, by manifest id ("claude-code") or
// by the name of the program that runs it ("claude"), and returns it with the
// command that starts it. The command is the first name the manifest detects
// the harness under, which is the name it is installed as.
//
// It exists so a command that starts an agent for the person can take the
// name they already type at their shell, and refuse a name nothing here knows
// rather than exec something and hope it is an agent.
func (r *Registry) Resolve(name string) (m *Manifest, command string, ok bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", false
	}
	if m := r.Lookup(name); m != nil {
		return m, m.Command(), true
	}
	for _, m := range r.manifests {
		if slices.Contains(m.Detect.Argv0, name) || slices.Contains(m.Detect.Comm, name) {
			return m, m.Command(), true
		}
	}
	return nil, "", false
}

// Command is the program name that starts this harness: the first argv0 the
// manifest detects it under, or the first comm when it names no argv0.
func (m *Manifest) Command() string {
	if len(m.Detect.Argv0) > 0 {
		return m.Detect.Argv0[0]
	}
	if len(m.Detect.Comm) > 0 {
		return m.Detect.Comm[0]
	}
	return m.ID
}
