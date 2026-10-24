// Package skills holds the agent skill files tuios ships, embedded so the copy
// a binary prints is the copy that was built into it.
//
// A skill fetched from anywhere else can describe commands the running build
// does not have. Embedding removes that failure mode: `tuios --skill` and
// skills/tuios/SKILL.md are the same bytes, which TestArchiveMatchesFiles holds.
//
// The skill is split so an agent loads only what it needs. SKILL.md is the
// core every agent in a pane reads. Each other file in skills/tuios is a topic,
// printed by `tuios --skill <topic>`, and the core's last section lists them.
//
// The files go into the binary as one gzipped tar, skills/tuios.tar.gz, about
// a third of their size. It is unpacked once, the first time a
// skill is read, so only `tuios --skill` pays for it. Run `go generate
// ./skills` after editing a file under skills/tuios. TestArchiveMatchesFiles
// fails while the archive is stale.
package skills

//go:generate go run gentml.go
//go:generate go run gen.go

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
)

// archive is skills/tuios/*.md packed by gen.go, each file under its base
// name.
//
//go:embed tuios.tar.gz
var archive []byte

// load unpacks the archive into the core and the topics, sorted by name. The
// archive is built into the binary and checked by TestArchiveMatchesFiles, so
// a read error is a broken build and panics.
var load = sync.OnceValues(func() (string, []Topic) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		panic(fmt.Sprintf("skills: embedded archive: %v", err))
	}
	tr := tar.NewReader(zr)
	var core string
	var out []Topic
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(fmt.Sprintf("skills: embedded archive: %v", err))
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			panic(fmt.Sprintf("skills: embedded archive: %s: %v", h.Name, err))
		}
		name := strings.TrimSuffix(h.Name, ".md")
		if name == "SKILL" {
			core = string(data)
			continue
		}
		out = append(out, Topic{Name: name, Text: string(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return core, out
})

// Core returns the core skill, SKILL.md, which teaches an agent to drive tuios
// from inside a pane. It is what `tuios --skill` prints.
func Core() string {
	core, _ := load()
	return core
}

// Topic is one file of the skill besides the core.
type Topic struct {
	// Name is what `tuios --skill <name>` takes: the file name without .md.
	Name string
	// Text is the whole file.
	Text string
}

// Topics returns every topic, sorted by name. The core is not one of them.
func Topics() []Topic {
	_, topics := load()
	return slices.Clone(topics)
}

// Lookup returns the text `tuios --skill <name>` prints. The empty name and
// "core" are the core, "all" is the core followed by every topic, and any
// other name is a topic. An unknown name is an error that lists the topics.
func Lookup(name string) (string, error) {
	switch name {
	case "", "core":
		return Core(), nil
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
	parts := []string{strings.TrimRight(Core(), "\n")}
	for _, t := range Topics() {
		parts = append(parts, strings.TrimRight(t.Text, "\n"))
	}
	return strings.Join(parts, "\n\n") + "\n"
}
