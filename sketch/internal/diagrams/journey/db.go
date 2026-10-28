// Ported from pele src/diagrams/journey/db.ts at 8aff183be57021b533f319a29341a529be934b08.

package journey

import (
	"slices"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
)

type JourneyTask struct {
	task string
	// As written: any number, or NaN when the score is not one.
	score  float64
	people []string
	// Name of the section in force when the task was declared, "" before the first one.
	section string
}

type JourneyDb struct {
	title    string
	accTitle string
	accDescr string
	sections []string
	tasks    []JourneyTask

	currentSection string
}

// journeyActors is everyone named on a task, once each, in code unit order.
func journeyActors(tasks []JourneyTask) []string {
	seen := map[string]bool{}
	var names []string
	for _, task := range tasks {
		for _, name := range task.people {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	slices.SortFunc(names, js.Compare)
	return names
}

func (db *JourneyDb) setDiagramTitle(text string) { db.title = text }

func (db *JourneyDb) setAccTitle(text string) { db.accTitle = js.TrimStart(text) }

func (db *JourneyDb) setAccDescription(text string) { db.accDescr = joinLines(text) }

func (db *JourneyDb) addSection(text string) {
	db.currentSection = text
	db.sections = append(db.sections, text)
}

// addTask: data is the rest of the line from its first colon: `:score` or `:score:actor, actor`.
func (db *JourneyDb) addTask(task string, data string) {
	pieces := strings.Split(data[1:], ":")
	var people []string
	if len(pieces) > 1 {
		people = strings.Split(pieces[1], ",")
		for i, name := range people {
			people[i] = js.Trim(name)
		}
	}
	db.tasks = append(db.tasks, JourneyTask{task: task, score: js.Number(pieces[0]), people: people, section: db.currentSection})
}

// joinLines is text.replace(/\n\s+/g, '\n').
func joinLines(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	var out strings.Builder
	for i := 0; i < len(text); {
		if text[i] != '\n' {
			out.WriteByte(text[i])
			i++
			continue
		}
		j := i + 1
		for j < len(text) {
			c, size := utf8.DecodeRuneInString(text[j:])
			if !js.IsSpace(c) {
				break
			}
			j += size
		}
		out.WriteByte('\n')
		i = j
	}
	return out.String()
}
