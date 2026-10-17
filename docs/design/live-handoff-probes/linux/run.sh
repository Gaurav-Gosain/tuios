#!/bin/sh
# Rerun (colima does not share /private/tmp, so stream the dir in): tar c . | docker run --rm -i --init golang:1.26 sh -c "mkdir /src && tar x -C /src && cd /src && sh run.sh"
set -e
export GOWORK=off GOFLAGS=-mod=mod
uname -a; go version
[ -f go.sum ] || { go get golang.org/x/sys@latest github.com/creack/pty@v1.1.24 >/dev/null 2>&1; }
go vet . && go build -o /tmp/hp .
rc=0
for p in ptybuf execinplace nonchild ptypoll memfd; do echo; /tmp/hp $p || rc=1; done
exit $rc
