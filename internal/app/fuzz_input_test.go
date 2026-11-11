package app_test

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/input"
)

// FuzzModel sends keys and the mouse through Update, and Update drops them
// unless an input handler is registered. Package app cannot import the
// handler, so this file, in the external test package of the same binary,
// hands it over before any test runs.
func init() { app.SetFuzzInput(input.HandleInput) }
