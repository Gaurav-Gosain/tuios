package session

import "syscall"

func changeTime(st *syscall.Stat_t) int64 { return st.Ctim.Nano() }
