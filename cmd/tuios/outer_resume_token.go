package main

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"tuios.dev/progresume"
)

// outerResumeToken is the OSC 7577 token of the terminal this client runs in,
// taken out of the environment when the client starts. See
// internal/app/host_program_resume.go.
var outerResumeToken string

// takeOuterResumeToken takes the token out of this process's environment and
// keeps it for the client and for the build a restart or an upgrade execs.
func takeOuterResumeToken() string {
	if tok := app.TakeHostResumeToken(); tok != "" {
		outerResumeToken = tok
	}
	return outerResumeToken
}

// clientExecEnv is the environment of the build this client execs in its own
// place. That build is the same program on the same terminal, so it gets the
// token back to report with.
func clientExecEnv() []string {
	env := os.Environ()
	if outerResumeToken != "" {
		env = append(env, progresume.TokenEnv+"="+outerResumeToken)
	}
	return env
}
