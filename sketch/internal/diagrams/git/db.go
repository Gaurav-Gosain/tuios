// Ported from pele src/diagrams/git/db.ts at 8aff183be57021b533f319a29341a529be934b08.
//
// pele's GitDbParseProvider is the closures of buildGit; the port keeps them as methods of
// a builder. Maps that pele reads in insertion order keep that order in a slice beside them.

package git

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/sketch/internal/js"
	"tuios.dev/sketch/internal/types"
)

const (
	NORMAL = iota
	REVERSE
	HIGHLIGHT
	MERGE
	CHERRY_PICK
)

type GitCommit struct {
	id      string
	message string
	seq     int
	typ     int
	tags    []string
	parents []string
	branch  string
	// customType is -1 for none.
	customType int
	customId   bool
}

type GitModel struct {
	title    string
	accTitle string
	accDescr string
	// LR, TB or BT.
	direction string
	// Keyed by commit id.
	commits map[string]*GitCommit
	// The id of each branch's latest commit, or "" while the branch has none.
	branches map[string]string
	// Branch names in drawing order: by `order`, then by creation.
	lanes         []string
	currentBranch string
	head          *GitCommit
	warnings      []string
}

type CommitDB struct {
	msg  string
	id   string
	typ  int
	tags []string
}

type BranchDB struct {
	name  string
	order *float64
}

type MergeDB struct {
	branch string
	id     string
	// typ is -1 for none.
	typ  int
	tags []string
}

type CherryPickDB struct {
	id       string
	targetId string
	// parent is "" for none.
	parent string
	// tags is nil for none.
	tags []string
}

func fail(message string) {
	types.Throw(message, types.CodeSemantic, types.ErrorInfo{Type: "gitGraph"})
}

func typeCode(name CommitTypeName) int {
	switch name {
	case "REVERSE":
		return REVERSE
	case "HIGHLIGHT":
		return HIGHLIGHT
	}
	return NORMAL
}

func populate(ast *GitGraphAst, db *gitBuilder) {
	if ast.dir != "" {
		db.setDirection(ast.dir)
	}
	for i := range ast.statements {
		parseStatement(&ast.statements[i], db)
	}
}

func parseStatement(statement *StatementAst, db *gitBuilder) {
	switch statement.typ {
	case "Commit":
		typ := NORMAL
		if statement.commitType != "" {
			typ = typeCode(statement.commitType)
		}
		db.commit(CommitDB{id: statement.id, msg: statement.message, typ: typ, tags: statement.tags})
	case "Branch":
		order := 0.0
		if statement.order != nil {
			order = *statement.order
		}
		db.branch(BranchDB{name: statement.name, order: &order})
	case "Merge":
		typ := -1
		if statement.commitType != "" {
			typ = typeCode(statement.commitType)
		}
		db.merge(MergeDB{branch: statement.branch, id: statement.id, typ: typ, tags: statement.tags})
	case "Checkout":
		db.checkout(statement.branch)
	case "CherryPicking":
		var tags []string
		if len(statement.tags) > 0 {
			tags = statement.tags
		}
		db.cherryPick(CherryPickDB{id: statement.id, targetId: "", tags: tags, parent: statement.parent})
	}
}

type lane struct {
	name  string
	order *float64
}

type gitBuilder struct {
	commits  map[string]*GitCommit
	branches map[string]string
	// A branch with no commit yet is in branches with "".
	orders    []lane
	warnings  []string
	head      *GitCommit
	current   string
	direction string
	seq       int
}

// nextId: Mermaid appends seven random hex digits to the sequence number. These digits are derived
// from the sequence number instead, so the same text always gives the same ids.
func (b *gitBuilder) nextId() string {
	for salt := int64(b.seq) + 1; ; salt += 0x9e3779b1 {
		h := strconv.FormatUint(uint64(uint32(salt)*0x9e3779b1), 16)
		id := strconv.Itoa(b.seq) + "-" + (strings.Repeat("0", max(0, 8-len(h))) + h)[:7]
		if _, ok := b.commits[id]; !ok {
			return id
		}
	}
}

func (b *gitBuilder) add(commit *GitCommit) {
	b.head = commit
	b.commits[commit.id] = commit
	b.branches[b.current] = commit.id
}

func (b *gitBuilder) checkout(branch string) {
	id, ok := b.branches[branch]
	if !ok {
		fail(`Trying to checkout branch which is not yet created. (Help try using "branch ` + branch + `")`)
	}
	b.current = branch
	b.head = nil
	if id != "" {
		b.head = b.commits[id]
	}
}

func (b *gitBuilder) setDirection(dir string) { b.direction = dir }

func (b *gitBuilder) commit(c CommitDB) {
	id := c.id
	if id == "" {
		id = b.nextId()
	}
	var parents []string
	if b.head != nil {
		parents = []string{b.head.id}
	}
	commit := &GitCommit{id: id, message: c.msg, seq: b.seq, typ: c.typ, tags: slices.Clone(c.tags), parents: parents, branch: b.current, customType: -1}
	b.seq++
	if _, ok := b.commits[commit.id]; ok {
		b.warnings = append(b.warnings, "Commit ID "+commit.id+" already exists")
	}
	b.add(commit)
}

func (b *gitBuilder) branch(br BranchDB) {
	if _, ok := b.branches[br.name]; ok {
		fail(`Trying to create an existing branch. (Help: Either use a new name if you want create a new branch or try using "checkout ` + br.name + `")`)
	}
	b.branches[br.name] = ""
	if b.head != nil {
		b.branches[br.name] = b.head.id
	}
	b.orders = append(b.orders, lane(br))
	b.checkout(br.name)
}

func (b *gitBuilder) merge(m MergeDB) {
	other := m.branch
	currentHead := b.branches[b.current]
	otherHead, otherExists := b.branches[other]
	var currentCommit, otherCommit *GitCommit
	if currentHead != "" {
		currentCommit = b.commits[currentHead]
	}
	if otherHead != "" {
		otherCommit = b.commits[otherHead]
	}
	if currentCommit != nil && otherCommit != nil && currentCommit.branch == other {
		fail("Cannot merge branch '" + other + "' into itself.")
	}
	if b.current == other {
		fail(`Incorrect usage of "merge". Cannot merge a branch to itself`)
	}
	if currentCommit == nil {
		fail(`Incorrect usage of "merge". Current branch (` + b.current + `)has no commits`)
	}
	if !otherExists {
		fail(`Incorrect usage of "merge". Branch to be merged (` + other + `) does not exist`)
	}
	if otherCommit == nil {
		fail(`Incorrect usage of "merge". Branch to be merged (` + other + `) has no commits`)
	}
	if currentCommit == otherCommit {
		fail(`Incorrect usage of "merge". Both branches have same head`)
	}
	if _, ok := b.commits[m.id]; m.id != "" && ok {
		fail(`Incorrect usage of "merge". Commit with id:` + m.id + ` already exists, use different custom id`)
	}
	id := m.id
	if id == "" {
		id = b.nextId()
	}
	var parents []string
	if b.head != nil {
		parents = []string{b.head.id, otherHead}
	}
	b.add(&GitCommit{
		id:         id,
		message:    "merged branch " + other + " into " + b.current,
		seq:        b.seq,
		parents:    parents,
		branch:     b.current,
		typ:        MERGE,
		customType: m.typ,
		customId:   m.id != "",
		tags:       slices.Clone(m.tags),
	})
	b.seq++
}

func (b *gitBuilder) cherryPick(c CherryPickDB) {
	var source *GitCommit
	if c.id != "" {
		source = b.commits[c.id]
	}
	if source == nil {
		fail(`Incorrect usage of "cherryPick". Source commit id should exist and provided`)
	}
	if c.parent != "" && !slices.Contains(source.parents, c.parent) {
		fail("Invalid operation: The specified parent commit is not an immediate parent of the cherry-picked commit.")
	}
	if source.typ == MERGE && c.parent == "" {
		fail("Incorrect usage of cherry-pick: If the source commit is a merge commit, an immediate parent commit must be specified.")
	}
	if _, ok := b.commits[c.targetId]; c.targetId != "" && ok {
		return
	}
	if source.branch == b.current {
		fail(`Incorrect usage of "cherryPick". Source commit is already on current branch`)
	}
	if currentHead := b.branches[b.current]; currentHead == "" || b.commits[currentHead] == nil {
		fail(`Incorrect usage of "cherry-pick". Current branch (` + b.current + `)has no commits`)
	}
	var parents []string
	if b.head != nil {
		parents = []string{b.head.id, source.id}
	}
	var tags []string
	if c.tags != nil {
		tags = []string{}
		for _, tag := range c.tags {
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	} else {
		tag := "cherry-pick:" + source.id
		if source.typ == MERGE {
			tag += "|parent:" + c.parent
		}
		tags = []string{tag}
	}
	// pele sets message to `cherry-picked ${source.message} into ${current}`. A pick of
	// a pick nests the text, which JavaScript ropes share but Go copies, so a chain of
	// picks costs memory in the square of its length. The renderer shows a message only
	// below MERGE, so this one is never read and is left out.
	b.add(&GitCommit{
		id:         b.nextId(),
		seq:        b.seq,
		parents:    parents,
		branch:     b.current,
		typ:        CHERRY_PICK,
		customType: -1,
		tags:       tags,
	})
	b.seq++
}

func buildGit(ast *GitGraphAst, config types.Config, title *string) *GitModel {
	git, _ := config["gitGraph"].(map[string]any)
	mainName, ok := git["mainBranchName"].(string)
	if !ok {
		mainName = "main"
	}
	mainOrder, ok := git["mainBranchOrder"].(float64)
	if !ok {
		mainOrder = 0
	}

	b := &gitBuilder{
		commits:   map[string]*GitCommit{},
		branches:  map[string]string{mainName: ""},
		orders:    []lane{{mainName, &mainOrder}},
		current:   mainName,
		direction: "LR",
	}
	populate(ast, b)

	// A branch made without an order sorts by creation among those of order zero, as in Mermaid.
	type ordered struct {
		name  string
		order float64
	}
	list := make([]ordered, len(b.orders))
	for i, l := range b.orders {
		order := js.ParseFloat("0." + strconv.Itoa(i))
		if l.order != nil {
			order = *l.order
		}
		list[i] = ordered{l.name, order}
	}
	js.Sort(list, func(a, b ordered) float64 { return a.order - b.order })
	lanes := make([]string, len(list))
	for i, l := range list {
		lanes[i] = l.name
	}

	model := &GitModel{
		title:         ast.title,
		direction:     b.direction,
		commits:       b.commits,
		branches:      b.branches,
		lanes:         lanes,
		currentBranch: b.current,
		head:          b.head,
		warnings:      b.warnings,
	}
	if model.title == "" {
		model.title = types.Or(title, "")
	}
	model.accTitle = js.TrimStart(ast.accTitle)
	model.accDescr = joinLines(ast.accDescr)
	return model
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
