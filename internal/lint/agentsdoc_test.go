package lint

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// agentsCoreLimit is the most AGENTS.md may hold. Every agent that works on
// tuios reads the whole file on each request, so a rule that does not apply to
// most changes belongs in a topic file under docs/agents, named from the core.
const agentsCoreLimit = 12 * 1024

// topicDir is the folder of the topic files AGENTS.md points at.
const topicDir = "docs/agents"

var (
	mdLink    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	topicLink = regexp.MustCompile(`\]\((` + topicDir + `/[^)#\s]+)\)`)
)

// TestAgentsCoreNamesEveryTopicFile holds AGENTS.md and the topic files under
// docs/agents together: the core stays under its size limit, every topic file
// it names exists, every file in the folder is named in the core, and every
// relative link in the core and in each topic file resolves. A topic file the
// core does not name is a file no agent finds, and a link that points nowhere
// is a rule no agent reads.
func TestAgentsCoreNamesEveryTopicFile(t *testing.T) {
	root := repoRoot(t)
	core, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(core); n > agentsCoreLimit {
		t.Errorf("AGENTS.md is %d bytes, over the %d byte limit: move a section to a topic file under %s and name it in the core", n, agentsCoreLimit, topicDir)
	}

	named := map[string]bool{}
	for _, m := range topicLink.FindAllStringSubmatch(string(core), -1) {
		named[m[1]] = true
	}
	entries, err := os.ReadDir(filepath.Join(root, topicDir))
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, e := range entries {
		rel := topicDir + "/" + e.Name()
		present[rel] = true
		if !named[rel] {
			t.Errorf("%s is not named in AGENTS.md: add it to the topic file table", rel)
		}
	}
	for rel := range named {
		if !present[rel] {
			t.Errorf("AGENTS.md names %s, which does not exist", rel)
		}
	}

	files := []string{"AGENTS.md"}
	for rel := range present {
		files = append(files, rel)
	}
	sort.Strings(files)
	for _, rel := range files {
		text, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Error(err)
			continue
		}
		for _, target := range relativeLinks(string(text)) {
			path := filepath.Join(root, filepath.Dir(rel), filepath.FromSlash(target))
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s links to %s, which does not resolve", rel, target)
			}
		}
	}
}

// relativeLinks returns the targets of the Markdown links in text that point
// at a file in the repository, without their anchors. A link inside a fenced
// code block is code, and a link with a scheme or only an anchor is skipped.
func relativeLinks(text string) []string {
	var out []string
	inFence := false
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			out = append(out, target)
		}
	}
	return out
}
