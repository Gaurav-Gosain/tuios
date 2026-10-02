//go:build !slim

package edition

// Slim is true in tuios-slim.
const Slim = false

// Name is the edition the welcome message and the version report carry. It
// is empty for the full build, which is what a build that predates editions
// sends, so an old peer and a full peer read the same.
const Name = ""
