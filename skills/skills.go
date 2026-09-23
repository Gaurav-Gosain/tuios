// Package skills holds the agent skill files tuios ships, embedded so the copy
// a binary prints is the copy that was built into it.
//
// A skill fetched from anywhere else can describe commands the running build
// does not have. Embedding removes that failure mode: `tuios --skill` and
// skills/tuios/SKILL.md are the same bytes by construction.
//
// The skill is split so an agent loads only what it needs. SKILL.md is the
// core every agent in a pane reads. Each other file in skills/tuios is a topic,
// printed by `tuios --skill <topic>`, and the core's last section lists them.
package skills

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// TUIOS is the core skill that teaches an agent to drive tuios from inside a
// pane. It is what `tuios --skill` prints.
//
//go:embed tuios/SKILL.md
var TUIOS string

//go:embed tuios/*.md
var files embed.FS

// Topic is one file of the skill besides the core.
type Topic struct {
	// Name is what `tuios --skill <name>` takes: the file name without .md.
	Name string
	// Text is the whole file.
	Text string
}

// Topics returns every topic, sorted by name. The core is not one of them.
func Topics() []Topic {
	entries, err := files.ReadDir("tuios")
	if err != nil {
		return nil
	}
	var out []Topic
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".md")
		if e.IsDir() || name == e.Name() || name == "SKILL" {
			continue
		}
		data, err := files.ReadFile(path.Join("tuios", e.Name()))
		if err != nil {
			continue
		}
		out = append(out, Topic{Name: name, Text: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the text `tuios --skill <name>` prints. The empty name and
// "core" are the core, "all" is the core followed by every topic, and any
// other name is a topic. An unknown name is an error that lists the topics.
func Lookup(name string) (string, error) {
	switch name {
	case "", "core":
		return TUIOS, nil
	case "all":
		return All(), nil
	}
	for _, t := range Topics() {
		if t.Name == name {
			return t.Text, nil
		}
	}
	return "", fmt.Errorf("no skill topic %q; the topics are core, all, %s", name, strings.Join(TopicNames(), ", "))
}

// TopicNames returns the names Topics carries, in the same order.
func TopicNames() []string {
	var names []string
	for _, t := range Topics() {
		names = append(names, t.Name)
	}
	return names
}

// All returns the core and then every topic, each separated by a blank line.
// It is the whole skill, for a reader that wants it in one go.
func All() string {
	parts := []string{strings.TrimRight(TUIOS, "\n")}
	for _, t := range Topics() {
		parts = append(parts, strings.TrimRight(t.Text, "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
}
