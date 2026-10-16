package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

const memfdSize = 2 << 20

func probeMemfd() {
	fmt.Println("== probe 5: sealed memfd manifest across exec ==")
	fd, err := unix.MemfdCreate("handoff-manifest", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	must(err)
	ctl, err := unix.MemfdCreate("control-left-cloexec", unix.MFD_CLOEXEC)
	must(err)
	data := make([]byte, memfdSize)
	for i := range data {
		data[i] = byte((i * 7) % 253)
	}
	w, err := unix.Write(fd, data)
	must(err)
	sum := sha256.Sum256(data)
	_, err = unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL)
	must(err)
	_, werr := unix.Pwrite(fd, []byte("x"), 0)
	fmt.Printf("  wrote %d bytes, sha256=%s, sealed; write after seal -> %v\n", w, hex.EncodeToString(sum[:8]), werr)
	check(errors.Is(werr, unix.EPERM), "negative control: sealed memfd rejects writes")
	syscall.ForkLock.Lock()
	_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0)
	must(err)
	env := append(os.Environ(), "MEMFD="+strconv.Itoa(fd), "MEMFD_CTL="+strconv.Itoa(ctl), "MEMFD_SUM="+hex.EncodeToString(sum[:]))
	err = syscall.Exec("/proc/self/exe", []string{os.Args[0], "memfd-after"}, env)
	syscall.ForkLock.Unlock()
	panic(err)
}

func memfdAfter() {
	fd, _ := strconv.Atoi(os.Getenv("MEMFD"))
	ctl, _ := strconv.Atoi(os.Getenv("MEMFD_CTL"))
	link, _ := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	fmt.Printf("  after exec: fd %d -> %s\n", fd, link)
	seals, err := unix.FcntlInt(uintptr(fd), unix.F_GET_SEALS, 0)
	fmt.Printf("  seals=%#x err=%v\n", seals, err)
	check(err == nil && seals&unix.F_SEAL_WRITE != 0 && seals&unix.F_SEAL_SEAL != 0, "seals survived exec")
	off, _ := unix.Seek(fd, 0, 1)
	fmt.Printf("  inherited file offset=%d (shared open file description, so readers must pread or seek to 0)\n", off)
	var st unix.Stat_t
	must(unix.Fstat(fd, &st))
	buf := make([]byte, st.Size)
	n, err := unix.Pread(fd, buf, 0)
	sum := sha256.Sum256(buf[:n])
	fmt.Printf("  size=%d read=%d err=%v sha256 match=%v\n", st.Size, n, err, hex.EncodeToString(sum[:]) == os.Getenv("MEMFD_SUM"))
	check(n == memfdSize && hex.EncodeToString(sum[:]) == os.Getenv("MEMFD_SUM"), "2 MB read back intact after exec")
	_, err = unix.FcntlInt(uintptr(ctl), unix.F_GETFD, 0)
	fmt.Printf("  negative control: CLOEXEC memfd fd %d after exec -> %v\n", ctl, err)
	check(errors.Is(err, unix.EBADF), "the memfd left CLOEXEC is gone, so survival above is due to clearing CLOEXEC")
	finish("memfd")
}
