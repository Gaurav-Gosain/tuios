package harness

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed manifests/*.toml
var bundled embed.FS

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
// Replacement is whole-file rather than a merge. Merging nested rule lists is a
// bug farm, and a user who wants to start from the bundled rules can copy the
// file. Later directories win over earlier ones.
func Load(dirs ...string) (*Registry, []LoadError) {
	byID := map[string]*Manifest{}
	var errs []LoadError

	entries, _ := fs.ReadDir(bundled, "manifests")
	for _, e := range entries {
		name := "manifests/" + e.Name()
		data, err := fs.ReadFile(bundled, name)
		if err != nil {
			errs = append(errs, LoadError{Source: name, Err: err})
			continue
		}
		m, err := parseManifest(name, data)
		if err != nil {
			errs = append(errs, LoadError{Source: name, Err: err})
			continue
		}
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
