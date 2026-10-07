#!/usr/bin/env python3
"""Sends commands to a running tuios-gpui control socket and prints replies.

    scripts/ctl.py SOCKET 'dump' 'cellclick 0 3 1'
"""
import socket, sys
s = socket.socket(socket.AF_UNIX)
s.connect(sys.argv[1])
f = s.makefile("rw")
for cmd in sys.argv[2:]:
    f.write(cmd + "\n"); f.flush()
    print(f.readline().rstrip("\n"))
