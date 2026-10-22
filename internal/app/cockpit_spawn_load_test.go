package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/federation"
)

// The new-agent form ranks machines by the load their daemons report. A
// load a daemon could not read comes with its cores and a load of 0, which
// must rank as unknown, not as an idle machine.
func TestSpawnRankTakesOnlyALoadTheDaemonRead(t *testing.T) {
	yes, no := true, false
	m := &OS{AttachedHost: "far", LocalLoad: federation.Load{Cores: 4, Load1: 3.5, LoadOK: &yes}}
	s := &spawnSheet{}
	m.spawnRank(s)
	if l := s.machines[0]; !l.loadOK || l.load != 3.5 || l.cores != 4 {
		t.Errorf("attached, the local row is cores %d load %.2f known %v, want the daemon's 4, 3.50, true", l.cores, l.load, l.loadOK)
	}
	m.LocalLoad = federation.Load{Cores: 4, LoadOK: &no}
	m.spawnRank(s)
	if l := s.machines[0]; l.loadOK {
		t.Errorf("a load the daemon could not read ranks as known, load %.2f", l.load)
	}
	// A daemon from before load_ok sent a load it read.
	m.LocalLoad = federation.Load{Cores: 4, Load1: 1}
	m.spawnRank(s)
	if l := s.machines[0]; !l.loadOK || l.load != 1 {
		t.Errorf("a load from an older daemon ranks as unknown")
	}
}
