package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/Gaurav-Gosain/tuios/skills"
)

// The skill stub: a SKILL.md that makes a harness load the tuios skill.
//
// The stub holds the name and description of the real skill and one line that
// tells the agent to run tuios --skill. The text the agent reads comes from
// the installed binary, so it always matches the build. A copy of the whole
// skill, made with tuios --skill > SKILL.md, describes the build it was made
// with and goes stale on the next upgrade.
//
// Install writes it with the hooks. A harness reads skills from one of two
// places:
//
//   - Claude Code: skills/tuios/SKILL.md under ~/.claude, or under
//     $CLAUDE_CONFIG_DIR.
//   - Codex, opencode and Pi: ~/.agents/skills/tuios/SKILL.md, a place all
//     three read. Uninstall removes it only when no other of the three still
//     has its integration installed.
//
// The stub carries TUIOS_INTEGRATION_ID=skill, a version, and a checksum of
// the rest of the file. That is how a later install, uninstall or status
// tells it from a tuios skill the person wrote, copied or edited. These are
// never overwritten or removed: a SKILL.md without the marker, one whose text
// no longer matches its checksum, one a newer tuios wrote (install only), and
// a SKILL.md that is a symlink.

// SkillVersion is the version of the skill stub this build writes.
const SkillVersion = 1

// skillDirs is where each harness reads the stub from: the directory that
// holds the tuios skill directory.
var skillDirs = map[string]func(t *Target, env Env) string{
	ClaudeCode: func(t *Target, env Env) string { return filepath.Join(t.ConfigDir(env), "skills") },
	Codex:      agentsSkillDir,
	OpenCode:   agentsSkillDir,
	Pi:         agentsSkillDir,
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

// skillSum is the checksum the marker carries: of the file without its
// marker line, so a change anywhere else in the file shows.
func skillSum(rest string) string {
	sum := sha256.Sum256([]byte(rest))
	return hex.EncodeToString(sum[:])
}

// skillMarkerRe finds the stub's marker line with the blank lines around it.
var skillMarkerRe = lazyre.New(`\n<!-- tuios integration install wrote this file\. TUIOS_INTEGRATION_ID=skill TUIOS_INTEGRATION_VERSION=(\d+) TUIOS_SKILL_SUM=([0-9a-f]{64}) -->\n\n`)

// skillOwnership is what install and uninstall may do with a SKILL.md.
type skillOwnership int

const (
	skillForeign skillOwnership = iota // no stub marker: the person's own skill
	skillEdited                        // the stub, changed since tuios wrote it
	skillOurs                          // the stub as tuios wrote it
)

// classifySkill says whose a SKILL.md is, with the stub version it carries.
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

// readSkill reads the SKILL.md at path. A symlink is reported and not read:
// the person manages that file, so tuios neither replaces nor removes it.
func readSkill(path string) (data []byte, link bool, err error) {
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, true, nil
	}
	data, err = readOptional(path)
	return data, false, err
}

// skillFrontmatter is the frontmatter of a skill file, with its fences.
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
		return res, fmt.Errorf("%s: %w", res.Path, ErrSkillNotOurs)
	}
	want := SkillStub()
	if have != nil {
		owner, version := classifySkill(have)
		switch {
		case owner != skillOurs:
			return res, fmt.Errorf("%s: %w", res.Path, ErrSkillNotOurs)
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
		return res, fmt.Errorf("failed to remove %s: %w", res.Path, err)
	}
	// Only removes an empty directory; one holding anything else stays.
	_ = os.Remove(filepath.Dir(res.Path))
	res.Changed = true
	res.Paths = []string{res.Path}
	return res, nil
}

// writeOwned replaces a file tuios owns whole. It keeps no backup: the file
// it replaces is tuios's own, and a copy beside a SKILL.md stays in the
// skill's directory.
func writeOwned(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a skills directory, made the way the harness makes it
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tuios-*")
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, 0o644) //nolint:gosec // a skill file the harness and the person read, like the files beside it
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		return fmt.Errorf("failed to write %s: %w", path, werr)
	}
	return nil
}
