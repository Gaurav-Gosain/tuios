// Package edition says which build of tuios this binary is: the full tuios,
// or tuios-slim, which is cmd/tuios built with the slim build tag.
//
// tuios-slim keeps the multiplexer and leaves out the features listed in
// docs/SLIM.md. Each left-out feature lives in files tagged !slim, and its
// slim counterpart is a small stub in a *_slim.go file. This package holds
// the one sentence every stub prints, so the wording is the same everywhere.
//
// Code should not branch on Slim. A feature that differs between the builds
// gets a pair of files instead; Slim is for the version report and for the
// handshake that tells a peer which build it talks to.
package edition

import "fmt"

// SlimName is the name of the slim binary, as its version line prints it.
const SlimName = "tuios-slim"

// MissingError is the error a stub returns for a feature tuios-slim does not
// have. Feature is what the user asked for: a command, a flag, a verb.
type MissingError struct {
	Feature string
}

// Error prints the one line the user reads.
func (e *MissingError) Error() string {
	return fmt.Sprintf("%s is not in %s. Install the full tuios to use it.", e.Feature, SlimName)
}

// Missing returns the error for a feature tuios-slim does not have.
func Missing(feature string) error {
	return &MissingError{Feature: feature}
}

// MissingCommandError is the error for a command tuios-slim does not have and
// no extension binary provides. Name is the command, which is also the name
// the extension would have: tuios-<name>.
type MissingCommandError struct {
	Name string
}

// Error prints the one line the user reads.
func (e *MissingCommandError) Error() string {
	return fmt.Sprintf("%s is not in %s. Install the full tuios to use this command.", e.Name, SlimName)
}

// MissingCommand returns the error for a command tuios-slim does not have.
func MissingCommand(name string) error {
	return &MissingCommandError{Name: name}
}

// MissingMessage is the same line as Missing, for a caller that wants text.
func MissingMessage(feature string) string {
	return Missing(feature).Error()
}
