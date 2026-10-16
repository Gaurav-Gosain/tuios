package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
// The stub carries TUIOS_INTEGRATION_ID and a version, which is how a later
// install, uninstall or status tells it from a tuios skill the person wrote or
// copied. A SKILL.md without the marker is never overwritten or removed.

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
	var b bytes.Buffer
	b.WriteString(skillFrontmatter(skills.Core()))
	b.WriteString("\n<!-- tuios integration install wrote this file. TUIOS_INTEGRATION_ID=skill TUIOS_INTEGRATION_VERSION=" + strconv.Itoa(SkillVersion) + " -->\n\n")
	b.WriteString("Run `tuios --skill` and follow what it prints. It is the skill for the tuios you have installed.\n")
	b.WriteString("Run `tuios --skill TOPIC` to print one of the topics it lists.\n")
	return b.Bytes()
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
	// Foreign says a tuios SKILL.md is there that tuios did not write.
	Foreign bool `json:"foreign,omitempty"`
}

// SkillState reports the stub at the harness's path.
func (t *Target) SkillState(env Env) SkillStatus {
	st := SkillStatus{Path: t.SkillPath(env)}
	if st.Path == "" {
		return st
	}
	have, err := readOptional(st.Path)
	if err != nil || have == nil {
		return st
	}
	if !isManagedFile(have) {
		st.Foreign = true
		return st
	}
	st.Installed = true
	st.Version = parseVersion(string(have))
	st.Current = bytes.Equal(have, SkillStub())
	return st
}

// ErrSkillNotOurs is a tuios SKILL.md that tuios did not write.
var ErrSkillNotOurs = errors.New("a tuios skill is there that tuios did not write, so it is left alone")

// InstallSkill writes the stub. A stub that is current is left as it is, and a
// SKILL.md tuios did not write is refused with ErrSkillNotOurs.
func (t *Target) InstallSkill(env Env) (Result, error) {
	res := Result{Harness: t.ID, Path: t.SkillPath(env)}
	if res.Path == "" {
		return res, nil
	}
	have, err := readOptional(res.Path)
	if err != nil {
		return res, err
	}
	want := SkillStub()
	switch {
	case have != nil && !isManagedFile(have):
		return res, fmt.Errorf("%s: %w", res.Path, ErrSkillNotOurs)
	case bytes.Equal(have, want):
		return res, nil
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
	have, err := readOptional(res.Path)
	if err != nil || have == nil || !isManagedFile(have) {
		return res, err
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
		werr = os.Chmod(name, 0o644)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		return fmt.Errorf("failed to write %s: %w", path, werr)
	}
	return nil
}
