package main

import (
	"log"
	"net/http"
	_ "net/http/pprof" // served only when GUIOS_PPROF names an address
	"os"
)

// servePprof serves /debug/pprof on $GUIOS_PPROF, for profiling a build.
func servePprof() {
	if addr := os.Getenv("GUIOS_PPROF"); addr != "" {
		go func() { log.Println(http.ListenAndServe(addr, nil)) }()
	}
}

// onNewApp, when set, runs once the main window is open: the e2e build's
// latency probe (automation_e2e.go).
var onNewApp func(*App)
