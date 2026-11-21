package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// SkillVersion is the version of the skill stub this build writes.
const SkillVersion = 1

var skillDirs = map[string]func(t *Target, env Env) string{
	ClaudeCode: func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "skills") },
	Codex:      agentsSkillDir,
	OpenCode:   agentsSkillDir,
	Pi:         agentsSkillDir,
	Vibe:       func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "skills") },
}

func agentsSkillDir(_ *Target, env Env) string { return filepath.Join(env.Home, ".agents", "skills") }

// SupportsSkill reports whether install writes the skill stub for the harness.
func (t *Target) SupportsSkill() bool {
	_, ok := skillDirs[t.ID]
	return ok
}

// SkillPath is the stub's path for the harness, "" for a harness without one.
func (t *Target) SkillPath(env Env) string {
	dir, ok := skillDirs[t.ID]
	if !ok {
		return ""
	}
	return filepath.Join(dir(t, env), "tuios", "SKILL.md")
}

// SkillStub is the stub this build writes.
func SkillStub() []byte {
	front := skillFrontmatter(skills.Core())
	body := "Run `tuios --skill` and follow what it prints. It is the skill for the tuios you have installed.\n" +
		"Run `tuios --skill TOPIC` to print one of the topics it lists.\n"
	return []byte(front + "\n<!-- tuios integration install wrote this file. TUIOS_INTEGRATION_ID=skill TUIOS_INTEGRATION_VERSION=" +
		strconv.Itoa(SkillVersion) + " TUIOS_SKILL_SUM=" + skillSum(front+body) + " -->\n\n" + body)
}

func skillSum(rest string) string {
	sum := sha256.Sum256([]byte(rest))
	return hex.EncodeToString(sum[:])
}

var skillMarkerRe = lazyre.New(`\n<!-- tuios integration install wrote this file\. TUIOS_INTEGRATION_ID=skill TUIOS_INTEGRATION_VERSION=(\d+) TUIOS_SKILL_SUM=([0-9a-f]{64}) -->\n\n`)

type skillOwnership int

const (
	skillForeign skillOwnership = iota
	skillEdited
	skillOurs
)

func classifySkill(data []byte) (skillOwnership, int) {
	m := skillMarkerRe().FindSubmatchIndex(data)
	if m == nil {
		return skillForeign, 0
	}
	version, _ := strconv.Atoi(string(data[m[2]:m[3]]))
	rest := string(data[:m[0]]) + string(data[m[1]:])
	if skillSum(rest) != string(data[m[4]:m[5]]) {
		return skillEdited, version
	}
	return skillOurs, version
}

func readSkill(path string) (data []byte, link bool, err error) {
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, true, nil
	}
	data, err = readOptional(path)
	return data, false, err
}

func skillFrontmatter(core string) string {
	rest, ok := strings.CutPrefix(core, "---\n")
	if !ok {
		return "---\nname: tuios\n---\n"
	}
	body, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return "---\nname: tuios\n---\n"
	}
	return "---\n" + body + "\n---\n"
}

// SkillStatus is whether the stub is in place and current.
type SkillStatus struct {
	Path      string `json:"path,omitempty"`
	Installed bool   `json:"installed"`
	Current   bool   `json:"current"`
	Version   int    `json:"version,omitempty"`
	// Foreign says a tuios SKILL.md is there that tuios leaves alone: one it
	// did not write, one that was edited, or a symlink.
	Foreign bool `json:"foreign,omitempty"`
	// Edited says the foreign file is the stub, changed since tuios wrote it.
	Edited bool `json:"edited,omitempty"`
	// Newer says a newer tuios wrote the stub. Install leaves it as it is.
	Newer bool `json:"newer,omitempty"`
}

// SkillState reports the stub at the harness's path.
func (t *Target) SkillState(env Env) SkillStatus {
	st := SkillStatus{Path: t.SkillPath(env)}
	if st.Path == "" {
		return st
	}
	have, link, err := readSkill(st.Path)
	if link {
		st.Foreign = true
		return st
	}
	if err != nil || have == nil {
		return st
	}
	owner, version := classifySkill(have)
	switch owner {
	case skillForeign:
		st.Foreign = true
		return st
	case skillEdited:
		st.Foreign, st.Edited, st.Version = true, true, version
		return st
	}
	st.Installed = true
	st.Version = version
	st.Newer = version > SkillVersion
	st.Current = bytes.Equal(have, SkillStub())
	return st
}

// ErrSkillNotOurs is a tuios SKILL.md that tuios did not write, that was
// edited, or that is a symlink.
var ErrSkillNotOurs = errors.New("the tuios skill there is not the one tuios wrote, so tuios leaves it alone")

// InstallSkill writes the stub. A stub that is current, or that a newer tuios
// wrote, is left as it is. A SKILL.md tuios does not own is refused with
// ErrSkillNotOurs.
func (t *Target) InstallSkill(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.SkillPath(env)}
	if res.Path == "" {
		return res, nil
	}
	have, link, err := readSkill(res.Path)
	if err != nil {
		return res, err
	}
	if link {
		return res, errfmt.Errorf("%s: %w", res.Path, ErrSkillNotOurs)
	}
	want := SkillStub()
	if have != nil {
		owner, version := classifySkill(have)
		switch {
		case owner != skillOurs:
			return res, errfmt.Errorf("%s: %w", res.Path, ErrSkillNotOurs)
		case version > SkillVersion:
			res.Notes = append(res.Notes, "a newer tuios wrote the skill in "+res.Path+", so it stays")
			return res, nil
		case bytes.Equal(have, want):
			return res, nil
		}
	}
	if err := writeOwned(res.Path, want); err != nil {
		return res, err
	}
	res.Changed = true
	res.Paths = []string{res.Path}
	return res, nil
}

// UninstallSkill removes the stub tuios wrote, and its directory once it is
// empty. A stub in the place several harnesses share stays while another of
// them has its integration installed.
func (t *Target) UninstallSkill(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.SkillPath(env)}
	if res.Path == "" {
		return res, nil
	}
	have, link, err := readSkill(res.Path)
	if err != nil || link || have == nil {
		return res, err
	}
	if owner, _ := classifySkill(have); owner != skillOurs {
		return res, nil
	}
	for _, o := range targets {
		if o != t && o.SkillPath(env) == res.Path && o.Status(env, "tuios").Installed {
			res.Notes = append(res.Notes, "the skill stays in "+res.Path+": "+o.Name+" uses it too")
			return res, nil
		}
	}
	if err := os.Remove(res.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, errfmt.Errorf("failed to remove %s: %w", res.Path, err)
	}
	_ = os.Remove(filepath.Dir(res.Path))
	res.Changed = true
	res.Paths = []string{res.Path}
	return res, nil
}

func writeOwned(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a skills directory, made the way the harness makes it
		return errfmt.Errorf("failed to create %s: %w", dir, err)
	}
	//nolint:gosec // a skill file the harness and the person read, like the files beside it
	if err := atomicfile.Write(path, data, 0o644, false); err != nil {
		return errfmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}
