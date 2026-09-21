#!/usr/bin/env python3
"""Exercise the actual readline Shell in a PTY; no credentials or network.

Build: go test -mod=readonly -c -o <test-binary> ./internal/sqlitecli
Run: python3 scripts/test-readline-pty.py <test-binary>
"""
import fcntl
import os
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import termios
import time


class Session:
    def __init__(self, binary):
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
        self.original = termios.tcgetattr(self.slave)
        self.proc = subprocess.Popen(
            [binary, "-test.run=^TestShellPTYHelper$", "-test.timeout=15s"],
            stdin=self.slave, stdout=self.slave, stderr=self.slave,
            env={**os.environ, "TIANA_TEST_SHELL_PTY": "1", "TERM": "xterm"},
            start_new_session=True,
        )
        self.trace = b""

    def drain(self, duration=0.25):
        until = time.monotonic() + duration
        while time.monotonic() < until:
            if select.select([self.master], [], [], max(0, until-time.monotonic()))[0]:
                self.trace += os.read(self.master, 65536)

    def visible(self):
        # Tests fit one terminal row. Apply the CSI erases/moves readline emits
        # instead of merely searching for a prompt that might have been erased.
        line, col = [], 0
        for part in re.findall(rb"\x1b\[[0-9;?]*[ -/]*[@-~]|[^\x1b]", self.trace):
            if part.startswith(b"\x1b["):
                code = part[-1:]
                n = int(part[2:-1] or b"1")
                if code == b"K" and n == 2:
                    line = []
                elif code in (b"J", b"K"):
                    line = line[:col]
                elif code == b"D":
                    col = max(0, col-n)
                elif code == b"C":
                    col += n
            elif part == b"\r":
                col = 0
            elif part == b"\n":
                line, col = [], 0
            elif part == b"\b":
                col = max(0, col-1)
            elif part >= b" ":
                while len(line) <= col:
                    line.append(" ")
                line[col] = part.decode("ascii")
                col += 1
        return "".join(line)

    def expect(self, line):
        deadline = time.monotonic() + 4
        while time.monotonic() < deadline:
            self.drain(0.1)
            if self.visible().rstrip() == line.rstrip():
                self.drain(0.1)
                break
        assert self.visible().rstrip() == line.rstrip(), (line, self.visible(), self.trace[-1000:])

    def send(self, text):
        os.write(self.master, text)

    def exited(self):
        assert self.proc.wait(timeout=4) == 0, "PTY subprocess failed"
        self.drain(0.05)
        assert b"PASS" in self.trace, self.trace[-1000:]
        restored = termios.tcgetattr(self.slave)
        # BSD may set PENDIN when returning from raw to canonical mode. This
        # is a transient kernel retype flag, not a change to terminal settings.
        restored[3] &= ~getattr(termios, "PENDIN", 0)
        self.original[3] &= ~getattr(termios, "PENDIN", 0)
        assert restored == self.original, ("terminal mode was not restored", self.original, restored)

    def close(self):
        if self.proc.poll() is None:
            self.proc.kill()
            self.proc.wait()
        os.close(self.master)
        os.close(self.slave)


def run(binary, ending):
    s = Session(binary)
    try:
        prompt = "sqlite[tx=?]> "
        s.expect(prompt)
        s.send(b".help\r")
        s.expect(prompt)
        assert b".tables" in s.trace
        s.send(b"\x1b[A")
        s.expect(prompt+".help")
        s.send(b"\x1b[B")
        s.expect(prompt)
        # Left, Delete, insertion, Right, Backspace: .helx -> .help.
        s.send(b".helx\x1b[D\x1b[3~pX\x1b[D\x1b[C\x7f\r")
        s.expect(prompt)
        # A partial SQL statement must show a continuation prompt. Ctrl-C
        # discards it; .help then works again without issuing database traffic.
        s.send(b"SELECT\r")
        s.expect("...> ")
        s.send(b"discard this\x03")
        s.expect(prompt)
        s.send(b".help\r")
        s.expect(prompt)
        if ending == "quit":
            s.send(b".quit\r")
        elif ending == "eof":
            s.send(b"\x04")
        else:
            os.kill(s.proc.pid, signal.SIGINT)
        s.exited()
    finally:
        s.close()
    print("PASS: visible prompts, history, cursor editing, multiline/Ctrl-C, " + ending)


if __name__ == "__main__":
    for ending in ("quit", "eof", "signal"):
        run(os.path.abspath(sys.argv[1]), ending)
