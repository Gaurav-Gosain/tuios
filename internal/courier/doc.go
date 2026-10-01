// Package courier is agent mail between people's machines that does not go
// through the tuios daemon.
//
// The daemon already carries mail between machines, over ssh links. Where ssh
// is blocked, tuios-courier carries it instead, as a separate program, so that
// the daemon gains no new way in. It is store-and-forward: a client seals a
// message to the recipient's key and posts it to a relay over HTTPS, and the
// recipient's client fetches it later. The relay holds ciphertext and the
// sender's signed claim of who it is, and nothing else.
//
// This package is everything both ends share and the client itself: the
// identity keys, the sealed envelope, request signing, the configuration, the
// local mail store and the HTTP client. The server is in courier/relay. Neither
// imports the daemon, and a test in cmd/tuios-courier keeps it that way.
package courier
