package session

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

const machineLoadEvery = 15 * time.Second

var machineLoadCache struct {
	sync.Mutex
	at            time.Time
	load1, memPct float64
	loadOK        bool
}

func machineLoad(infos []SessionInfo) federation.Load {
	c := &machineLoadCache
	c.Lock()
	if time.Since(c.at) >= machineLoadEvery {
		c.at = time.Now()
		c.load1, c.memPct, c.loadOK = readMachineLoad()
		if v, err := strconv.ParseFloat(os.Getenv("TUIOS_LOAD1"), 64); err == nil && v >= 0 {
			c.load1, c.loadOK = v, true
		}
	}
	ok := c.loadOK
	l := federation.Load{Cores: runtime.NumCPU(), Load1: c.load1, MemPct: c.memPct, LoadOK: &ok}
	c.Unlock()
	for _, s := range infos {
		for _, w := range s.Windows {
			if w.AgentState == string(AgentStateWorking) {
				l.Working++
			}
		}
	}
	return l
}
