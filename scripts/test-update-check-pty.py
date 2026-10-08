#!/usr/bin/env python3
"""Verify cached reminders in the actual CLI terminal, isolated from account state."""
import datetime
import errno
import json
import os
import pathlib
import pty
import select
import subprocess
import sys
import tempfile
import time

binary = str(pathlib.Path(sys.argv[1]).resolve())
with tempfile.TemporaryDirectory(prefix="tiana-update-terminal-") as root:
    directory = pathlib.Path(root) / "tiana"
    directory.mkdir()
    state_path = directory / "update-state.json"
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    initial = {"packages": {"@tianacloud/cli": {"last_check_attempt_at": now, "last_check_success_at": now, "latest_version": "99.0.0"}}}
    environment = dict(os.environ, XDG_CONFIG_HOME=root)
    def seed():
        state_path.write_text(json.dumps(initial))
        state_path.chmod(0o600)
    def terminal(args):
        master, slave = pty.openpty()
        try:
            process = subprocess.Popen([binary, *args], stdin=slave, stdout=slave, stderr=slave, env=environment)
            output = b""
            deadline = time.monotonic() + 5
            while process.poll() is None and time.monotonic() < deadline:
                if select.select([master], [], [], .1)[0]:
                    output += os.read(master, 65536)
            if process.poll() is None:
                process.kill()
                raise AssertionError("CLI waited unexpectedly")
            while select.select([master], [], [], 0)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO: break
                    raise
                if not chunk: break
                output += chunk
            return process.returncode, output
        finally:
            os.close(master)
            os.close(slave)
    seed()
    code, output = terminal(["web"])
    assert code == 0 and output.count(b"Updates available:") == 1, (code, output)
    saved = json.loads(state_path.read_text())
    assert saved.get("last_notified_at"), saved
    code, output = terminal(["web"])
    assert code == 0 and b"Updates available:" not in output, (code, output)
    seed()
    piped = subprocess.run([binary, "web"], capture_output=True, env=environment, timeout=5)
    assert piped.returncode == 0 and b"Updates available:" not in piped.stderr, piped
    assert not json.loads(state_path.read_text()).get("last_notified_at")
    for args in (["version"], ["web", "--help"]):
        code, output = terminal(args)
        assert code == 0 and b"Updates available:" not in output
        assert not json.loads(state_path.read_text()).get("last_notified_at")
print("Actual CLI PTY: one cached reminder, daily suppression, piped/version/help silence passed.")
