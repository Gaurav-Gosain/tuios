// Package procinfo reads what the kernel says about the process on the other
// end of a unix socket: its pid, and a start time that pins that pid to one
// process. A pid can be reused after its process exits; the pair of pid and
// start time cannot.
package procinfo
