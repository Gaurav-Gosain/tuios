//go:build darwin || freebsd || openbsd

package app

// fionread is the ioctl that says how many bytes wait to be read:
// _IOR('f', 127, int), the same number on these three. x/sys/unix does not
// export it for them.
const fionread = 0x4004667f
