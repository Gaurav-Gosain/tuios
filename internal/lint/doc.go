// Package lint holds source checks that run with go test. They read the
// repository's Go files and reject patterns the compiler accepts but the
// project does not; they test no runtime behaviour.
//
// The colour check (colors_test.go) rejects a hard-coded colour in render
// code. Render code takes its colours from the tokens in internal/overlay and
// internal/theme, so a theme, a light terminal and a 256- or 16-colour
// terminal all reach every surface. A literal is a surface none of them reach.
// A deliberate exception carries a comment on its line or the line above:
//
//	//tuios:allow-color <reason>
package lint
