package webshell

import "sync"

// The fake files, programs and agents are built on first use, not at package
// init. tuios links this package for tuios tutor, and every other tuios
// process (the daemon, a client, a one-shot command) would otherwise build
// the tables at start and never read them.
var setupOnce sync.Once

// ready builds the package's tables once. Every way in from outside the
// package calls it first: NewPty, and the exported reads and writes of the
// files, the repository and the agents.
func ready() { setupOnce.Do(setup) }

func setup() {
	initStarted()
	initFiles()
	initFsDirs()
	initFileDirs()
	initRepo()
	initShipPRs()
	initSamples()
	initGroups()
	initSampleAliases()
	initShellNames()
	initCommands()
	initPersonas()
	initTitleSkip()
	initHolders()
	initGoKeywords()
	initPlotSlots()
	initAgentsBy()
}
