// Package diffview draws the lines of a diff: syntax highlighting by file
// type, line numbers in a gutter, added and removed lines on tinted grounds,
// the changed part of a changed line marked, and a side by side layout.
//
// The design follows the diff view of Crush (github.com/charmbracelet/crush,
// internal/ui/diffview): chroma picks a lexer from the file name and
// tokenises the code, every token is drawn with its colour on the ground of
// the line it sits on so no cell is left without a background, and a
// changed line is paired with the line it replaced for the split layout. No
// code is copied from Crush, whose licence (FSL-1.1-MIT) is not yet MIT for
// any version of that view. What differs here: the colours come from the
// active tuios theme rather than a fixed style, the lines of one side of a
// hunk are tokenised together so a comment or string that spans lines keeps
// its colour, and the part of a line that changed is marked.
//
// The package draws; it does not lay out. The caller decides which rows are
// on screen and asks for those only, so a file of five thousand lines costs
// the rows that show. Tokenising is the expensive part, so the caller asks
// for a chunk of lines at a time and keeps what Highlight returned for as
// long as the diff does not change.
//
// The browser build (GOOS=js) leaves chroma out and draws every line plain:
// it never shows a review, and the lexers would add megabytes to the page.
package diffview

// Class is what a run of code is, as far as its colour goes. It is a small
// set on purpose: a theme has sixteen colours to say it with, and a class
// that has no colour of its own is drawn as Plain.
type Class uint8

const (
	// Plain is text with no class: names, and anything the lexer does not
	// know.
	Plain Class = iota
	// Keyword is a keyword of the language.
	Keyword
	// Type is a type name or a keyword that names one.
	Type
	// Func is the name of a function being declared or called.
	Func
	// Builtin is a name the language provides: len, print, self.
	Builtin
	// String is a string or character literal.
	String
	// Number is a number, and a constant such as true or nil.
	Number
	// Comment is a comment.
	Comment
	// Operator is an operator.
	Operator
	// Punct is punctuation: brackets, commas, semicolons.
	Punct
	// Preproc is a preprocessor line, a decorator or an annotation.
	Preproc
	// Tag is a markup tag name.
	Tag
	// Attr is a markup attribute name, or a key in a data file.
	Attr
	// Heading is a heading in prose markup.
	Heading
	// Meta is text tuios adds to a line, such as the mark for a missing
	// newline at the end of a file.
	Meta
	// NumClasses is how many classes there are.
	NumClasses
)

// Span is a run of one class in a line, in bytes of the line's text.
type Span struct {
	Start, End int
	Class      Class
}

// Kind is what a line of a diff is, which picks its ground.
type Kind uint8

const (
	// Context is a line both sides have.
	Context Kind = iota
	// Add is a line only the new side has.
	Add
	// Delete is a line only the old side has.
	Delete
	// Missing is the empty half of a side by side row whose line is on the
	// other side only.
	Missing
	numKinds
)
