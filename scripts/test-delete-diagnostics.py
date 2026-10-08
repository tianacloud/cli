#!/usr/bin/env python3
"""Actual deletion CLI modes against a verified, isolated TLS peer."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import select
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
from urllib.parse import urlsplit

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
binary = args.binary.resolve()
state, rows = {}, []


class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.respond()

    def do_DELETE(self):
        self.respond()

    def respond(self):
        path = urlsplit(self.path).path
        payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"null")
        instance = {"id": "fixture-id", "display_name": "fixture-name", "engine": state["engine"], "product_state": "ACTIVE"}
        status = 200
        if state.get("observe"):
            stage, body = "inspect", dict(instance, product_state="DELETED")
        elif path == "/api/v1/instances/fixture-name":
            stage, status, body = "name-lookup", 404, {"error": {"code": "INSTANCE_NOT_FOUND"}}
        elif path == "/api/v1/instances":
            stage, body = "name-list", {"items": [instance], "total_pages": 1}
        elif path == "/api/v1/instances/fixture-id" and self.command == "GET":
            stage, body = "lookup", instance
        elif path == "/api/v1/instances/fixture-id" and self.command == "DELETE":
            stage, status, body = "delete", 202, {"instance_id": "fixture-id", "operation_id": "17"}
            assert payload["request_id"]
        elif path == "/api/v1/instances/fixture-id/operations/17":
            stage, body = "operation", {"instance_id": "fixture-id", "operation_id": "17", "kind": "DELETE_INSTANCE", "state": "success"}
        else:
            raise AssertionError(path)
        request_id = self.headers.get("X-Request-ID")
        assert request_id and self.headers.get("Authorization") == "Bearer deletion-fixture-access"
        state["requests"].append({"stage": stage, "method": self.command, "path": self.path, "request_id": request_id})
        fault = state["fault"] if stage == state["stage"] else ""
        if fault.startswith("http"):
            status, body = int(fault[4:]), {"error": {"code": "FIXTURE_REJECTED", "message": "fixture"}}
        elif fault == "identity":
            body["instance_id"] = "another"
        elif fault == "kind":
            body["kind"] = "CREATE_INSTANCE"
        elif fault == "failed":
            body["state"] = "failed"
        elif fault == "unknown":
            body["state"] = "unknown"
        elif fault == "receipt":
            body = {"instance_id": "fixture-id"}
        elif fault == "wrong-engine":
            body["engine"] = "other"
        elif fault == "duplicate":
            body["items"].append(dict(instance, id="another"))
        elif fault == "missing":
            body["items"] = []
        elif fault == "progress" and sum(r["stage"] == stage for r in state["requests"]) == 1:
            body["state"] = "retry_wait"
        elif fault == "cancel":
            state["reached"].set()
            state["release"].wait(10)
            return
        elif fault == "disconnect":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        raw = b"{" if fault == "invalid" else json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        try:
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass


def run(argv, env, prompt, cancel, broken_pipe):
    master = slave = None
    if prompt:
        master, slave = os.openpty()
    reader = writer = None
    if broken_pipe:
        reader, writer = os.pipe()
        os.close(reader)
    child = subprocess.Popen([str(binary), *argv], env=env, stdin=slave if prompt else subprocess.DEVNULL,
                             stdout=writer if broken_pipe else subprocess.PIPE, stderr=subprocess.PIPE)
    if slave is not None:
        os.close(slave)
    if writer is not None:
        os.close(writer)
    prefix = b""
    try:
        if prompt:
            while b"[y/N]" not in prefix:
                assert select.select([child.stderr], [], [], 10)[0], "terminal confirmation not reached"
                data = os.read(child.stderr.fileno(), 4096)
                assert data, "exited before confirmation"
                prefix += data
            if prompt == "cancel":
                child.send_signal(signal.SIGINT)
            else:
                os.write(master, (prompt + "\n").encode())
        if cancel:
            assert state["reached"].wait(10), "cancellation boundary not reached"
            child.send_signal(signal.SIGINT)
        out, err = child.communicate(timeout=15)
        return child.returncode, (out or b"").decode(), (prefix + err).decode()
    finally:
        state["release"].set()
        if child.poll() is None:
            child.kill()
            child.wait()
        if master is not None:
            os.close(master)


def check(mode, diagnostic, stage, fault, root, origin, ca):
    state.clear()
    state.update(engine=mode["engine"], requests=[], stage=stage, fault=fault, reached=threading.Event(), release=threading.Event())
    credential = root/'config'/'tiana'/'credentials.json'
    credential.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    credential.write_text(json.dumps({"credentials": {origin: {"access_token": "deletion-fixture-access", "refresh_token": "deletion-fixture-refresh", "expires_at": "2099-01-01T00:00:00Z", "user": {"user_id": "fixture-user", "tenant_id": "fixture-tenant"}}}}))
    credential.chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("TIANA_") and "proxy" not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin, TIANA_CA_FILE=str(ca), XDG_CONFIG_HOME=str(root/'config'), TIANA_PENDING_COMMAND_FILE=str(root / "pending.json"))
    if diagnostic:
        env["TIANA_DIAGNOSTICS"] = "1"
    code, out, err = run(mode["argv"], env, mode.get("prompt"), fault == "cancel", fault == "pipe")
    want = 130 if fault == "cancel" or mode.get("prompt") == "cancel" else 1 if fault and fault != "progress" else mode.get("exit", 0)
    failures = []
    sent = list(state["requests"])
    if code != want:
        failures.append(f"exit {code}, expected {want}")
    expected = mode["stages"]
    if stage and fault not in ["progress", "pipe"]:
        expected = expected[:expected.index(stage) + 1]
    if fault == "progress":
        expected = [*expected, "operation"]
    if fault == "pipe":
        expected = expected[:expected.index("delete") + 1]
    if [r["stage"] for r in sent] != expected:
        failures.append("unexpected request stages/replay")
    printed = set(re.findall(r"req-[A-Za-z0-9_-]+", err))
    ids = {r["request_id"] for r in sent}
    if printed - ids or (diagnostic or want != 0) and ids - printed:
        failures.append("request ID mismatch")
    if want == 0 and not diagnostic and printed:
        failures.append("default success emitted IDs")
    if mode.get("wait") and want == 0 and "succeeded:" not in out:
        failures.append("missing deletion completion")
    if want != 0 and "succeeded:" in out:
        failures.append("false deletion completion")
    if "deletion-fixture-access" in out + err or "deletion-fixture-refresh" in out + err:
        failures.append("credential leaked")
    if (root / "pending.json").exists():
        failures.append("deletion unexpectedly persisted create intent")
    recovery = None
    if stage in ["delete", "operation"] and fault and fault != "progress":
        state["observe"], state["fault"] = True, ""
        offset = len(state["requests"])
        code2, _, err2 = run([mode["engine"], "show", "fixture-id"], env, None, False, False)
        observed = state["requests"][offset:]
        if code2 != 0 or [r["stage"] for r in observed] != ["inspect"]:
            failures.append("read-only recovery inspection failed")
        recovery = {"exit": code2, "requests": observed, "mode": "explicit inspection of immutable ID; fixture result is not backend recovery proof"}
    rows.append({"mode": mode["name"], "diagnostics": diagnostic, "stage": stage, "fault": fault or "none", "exit": code,
                 "requests": sent, "recovery": recovery, "stdout_sha256": hashlib.sha256(out.encode()).hexdigest(), "stderr_sha256": hashlib.sha256(err.encode()).hexdigest(), "failures": failures})
    args.output.write_text(json.dumps({"in_progress": True, "rows": rows}, indent=2) + "\n")


modes = []
for engine in ["sqlite", "git"]:
    for name, flags, reference, stages, wait, prompt in [
        ("accepted", ["--force"], "fixture-id", ["lookup", "delete"], False, None),
        ("wait", ["-f", "--wait"], "fixture-id", ["lookup", "delete", "operation"], True, None),
        ("name-short-wait", ["--force", "-w"], "fixture-name", ["name-lookup", "name-list", "delete", "operation"], True, None),
        ("wait-false", ["-f", "--wait=false"], "fixture-id", ["lookup", "delete"], False, None),
        ("terminal-yes", [], "fixture-id", ["lookup", "delete"], False, "yes"),
        ("terminal-no", [], "fixture-id", ["lookup"], False, "no"),
        ("terminal-cancel", [], "fixture-id", ["lookup"], False, "cancel"),
    ]:
        modes.append(dict(name=engine + "-" + name, engine=engine, argv=[engine, "delete", *flags, reference], stages=stages, wait=wait, prompt=prompt))
    for name, argv in [("no-force", [engine, "delete", "fixture-id"]), ("json", [engine, "delete", "-f", "fixture-id", "--json"]), ("arity", [engine, "delete", "-f"])]:
        modes.append(dict(name=engine + "-reject-" + name, engine=engine, argv=argv, stages=[], exit=2))

with tempfile.TemporaryDirectory(prefix="tiana-delete-peer-") as temp:
    root = Path(temp)
    def openssl(argv):
        subprocess.run(["openssl", *argv], cwd=root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    openssl(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.pem", "-days", "1", "-subj", "/CN=Delete Peer", "-addext", "basicConstraints=critical,CA:TRUE"])
    openssl(["req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=localhost"])
    (root / "leaf.ext").write_text("basicConstraints=critical,CA:FALSE\nsubjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n")
    openssl(["x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial", "-out", "leaf.pem", "-days", "1", "-extfile", "leaf.ext"])
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Peer)
    server.daemon_threads = True
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(root / "leaf.pem", root / "leaf.key")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        for mode in modes:
            cases = [("", "")]
            if mode["name"] == "sqlite-wait":
                cases += [(stage, fault) for stage in mode["stages"] for fault in ["http403", "http503", "invalid", "disconnect", "cancel"]]
                cases += [("lookup", "wrong-engine"), ("delete", "receipt"), ("delete", "pipe")]
                cases += [("operation", f) for f in ["http404", "identity", "kind", "failed", "unknown", "progress"]]
            if mode["name"] == "git-wait":
                cases += [(stage, "cancel") for stage in ["delete", "operation"]] + [("delete", "pipe")]
            if mode["name"] == "sqlite-name-short-wait":
                cases += [("name-list", f) for f in ["duplicate", "missing", "http503", "cancel"]]
            for diagnostic in [False, True]:
                for stage, fault in cases:
                    with tempfile.TemporaryDirectory(dir=root) as case:
                        check(mode, diagnostic, stage, fault, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
result = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "rows": rows, "passed": sum(not r["failures"] for r in rows), "failed": sum(bool(r["failures"]) for r in rows), "fixture_cleaned": True}
args.output.write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({k: result[k] for k in ["passed", "failed", "fixture_cleaned"]}))
for r in rows:
    if r["failures"]:
        print(json.dumps({k: r[k] for k in ["mode", "stage", "fault", "diagnostics", "failures"]}))
raise SystemExit(bool(result["failed"]))
