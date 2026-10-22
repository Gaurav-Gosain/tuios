package session

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The machine's load, as this daemon's hello says it, so a daemon linked to
// this one can rank the machines for a new agent without a call of its own.
// The heartbeat of a link already sends a hello when the link is quiet, so
// the load rides on it and costs no timer here.

// machineLoadEvery is how long a load read is reused: the heartbeat's
// interval, so the ping of each link reads the system at most once, and a
// hello from every CLI call between two pings reads nothing.
const machineLoadEvery = 15 * time.Second

// machineLoadCache holds the last read.
var machineLoadCache struct {
	sync.Mutex
	at            time.Time
	load1, memPct float64
	loadOK        bool
}

// machineLoad is this machine's load with working agents counted from infos.
func machineLoad(infos []SessionInfo) federation.Load {
	c := &machineLoadCache
	c.Lock()
	if time.Since(c.at) >= machineLoadEvery {
		c.at = time.Now()
		c.load1, c.memPct, c.loadOK = readMachineLoad()
		// TUIOS_LOAD1 stands in for the one-minute load, for tests and
		// pictures that need a known load.
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
