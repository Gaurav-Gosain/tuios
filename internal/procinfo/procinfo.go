// Package procinfo reads what the kernel says about a process: the pid of the
// process on the other end of a unix socket, a start time that pins that pid
// to one process, its parent, its /proc/<pid>/stat fields, and one variable of
// the environment it was started with. A pid can be reused after its process
// exits; the pair of pid and start time cannot.
package procinfo
