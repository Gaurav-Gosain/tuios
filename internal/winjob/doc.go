// Package winjob holds a Windows program and every process it starts in one
// job object, so that they can be killed together. Windows has no process
// group to kill with one call. A job is the equivalent.
//
// The herdr plugin runner and the opencode export use it. On other systems
// the package is empty.
package winjob
