package session

import "syscall"

// changeTime is a stat result's change time.
func changeTime(st *syscall.Stat_t) int64 { return st.Ctim.Nano() }
