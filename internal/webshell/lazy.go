package webshell

import "sync"

var setupOnce sync.Once

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
