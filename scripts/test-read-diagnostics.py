#!/usr/bin/env python3
"""Released CLI read modes against a finite isolated TLS management peer."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlsplit

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("--output", required=True, type=Path)
parser.add_argument("--only", help="Run mode names containing this substring")
parser.add_argument("--fault", help="Run only this fault at each reached stage")
args = parser.parse_args()
binary = args.binary.resolve()
rows, state = [], {}
endpoint = "ep-01j5c9m7q2v8x4k6n3r0t1w2yz"
hostname = endpoint + ".db.example.test"
connection = {"hostname": hostname, "url": "https://" + hostname}
secrets = ["read-access-fixture", "read-refresh-fixture"]


def instance(engine, name="fixture-name", identity="fixture-id"):
    return {"id": identity, "display_name": name, "engine": engine, "product_state": "ACTIVE",
            "endpoint_id": endpoint, "connection": connection}


def branch(name="production", identity="main"):
    return {"branch_id": identity, "name": name, "root": identity == "main",
            "endpoint_id": endpoint, "lifecycle_state": "ACTIVE", "runtime_state": "READY"}


class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        parsed = urlsplit(self.path)
        path, query = parsed.path, parse_qs(parsed.query)
        mode = state["mode"]
        engine = mode.get("engine", "sqlite")
        if path.startswith("/api/v1/apps/"):
            stage = "app-status"
            body = {"app_id": "fixture-app", "version_id": "fixture-version", "status": "published"}
        elif path == "/api/v1/instances":
            stage = "lookup" if "display_name" in query else "list" + query["page"][0]
            if stage == "lookup":
                assert query["display_name"] == ["fixture-name"]
                items = [instance(engine)]
                if mode.get("business") == "missing":
                    items = []
                if mode.get("business") == "duplicate":
                    items.append(instance(engine, identity="fixture-second"))
                body = {"items": items, "total": len(items), "total_pages": 1}
            elif mode.get("business") == "empty":
                body = {"items": [], "total": 0, "total_pages": 0}
            else:
                page = int(query["page"][0])
                assert 1 <= page <= 2
                items = [instance("git" if engine == "sqlite" else "sqlite", "other-engine")] if page == 1 else [instance(engine)]
                body = {"items": items, "page": page, "total": 2, "total_pages": 2}
        elif path.endswith("/branches"):
            stage = "branch-list"
            if mode.get("named_branch"):
                assert query == {"name": ["preview"]}
                body = {"items": [] if mode.get("business") == "branch-missing" else [branch("preview", "child")]}
            else:
                if mode.get("filtered"):
                    assert query == {"after": ["cursor+1"], "search": ["预览 + /&"]}
                else:
                    assert query == {}
                body = {"items": [branch()], "next_cursor": "cursor+2"}
        elif "/branches/" in path:
            stage = "branch-detail"
            name = "preview" if mode.get("named_branch") else "production"
            if mode.get("business") == "branch-renamed":
                name = "renamed"
            body = {"instance_id": "fixture-id", "branch": branch(name, "child" if mode.get("named_branch") else "main"),
                    "connection": None if mode.get("business") == "no-url" else connection}
        elif path.startswith("/api/v1/instances/"):
            stage = "detail"
            body = instance(engine)
            if mode.get("business") == "wrong-engine":
                body["engine"] = "git" if engine == "sqlite" else "sqlite"
            if mode.get("business") == "deleted":
                body["product_state"] = "DELETED"
            if mode.get("business") == "no-url":
                body["connection"] = None
        else:
            raise AssertionError(path)
        rid = self.headers.get("X-Request-ID")
        assert rid and self.headers.get("Authorization") == "Bearer " + secrets[0]
        assert stage in mode["stages"], (stage, mode["stages"])
        state["requests"].append({"stage": stage, "path": self.path, "request_id": rid})
        code = 404 if stage == "detail" and mode.get("by_name") else 200
        if code == 404:
            body = {"error": {"code": "INSTANCE_NOT_FOUND", "message": "fixture"}}
        fault = state["fault"] if stage == state["fault_stage"] else ""
        if fault.startswith("http"):
            code = int(fault[4:])
            body = {"error": {"code": "FIXTURE_REJECTION", "message": "fixture"}}
        if fault == "disconnect":
            self.connection.shutdown(socket.SHUT_RDWR)
            self.connection.close()
            return
        if fault == "cancel":
            state["reached"].set()
            state["release"].wait(10)
            self.close_connection = True
            return
        data = ("{invalid" if fault == "invalid" else json.dumps(body)).encode()
        length = len(data)
        if fault == "truncated":
            data = data[:-2]
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-ID", rid)
        self.send_header("Content-Length", str(length))
        self.end_headers()
        try:
            self.wfile.write(data)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True


def invoke(mode, env, fault):
    command = [str(binary), *mode["argv"]]
    if not mode.get("tty"):
        process = subprocess.Popen(command, env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True)
        try:
            if fault == "cancel":
                assert state["reached"].wait(10), "cancel boundary not reached"
                process.send_signal(signal.SIGINT)
            out, err = process.communicate(timeout=15)
            return process.returncode, out, err
        finally:
            state["release"].set()
            if process.poll() is None:
                process.kill()
                process.wait()
    master, slave = pty.openpty()
    process = subprocess.Popen(command, env=env, stdin=slave, stdout=slave, stderr=subprocess.PIPE)
    os.close(slave)
    output = bytearray()
    sent = False
    cancelled = False
    deadline = time.monotonic() + 15
    try:
        while time.monotonic() < deadline:
            if fault == "cancel" and state["reached"].is_set() and not cancelled:
                process.send_signal(signal.SIGINT)
                cancelled = True
            if select.select([master], [], [], .05)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if b"-- More --" in output and not sent:
                    os.write(master, b"q\n" if mode["tty"] == "quit" else b"n\n")
                    sent = True
            if process.poll() is not None:
                break
        _out, err = process.communicate(timeout=2)
        return process.returncode, output.decode(), err.decode()
    finally:
        state["release"].set()
        os.close(master)
        if process.poll() is None:
            process.kill()
            process.wait()


def check(mode, diagnostic, fault_stage, fault, root, origin, ca):
    state.clear()
    state.update(mode=mode, fault_stage=fault_stage, fault=fault, requests=[], reached=threading.Event(), release=threading.Event())
    credential_path = root / "credentials.json"
    credential_path.write_text(json.dumps({"credentials": {origin: {"access_token": secrets[0],
        "refresh_token": secrets[1], "expires_at": "2099-01-01T00:00:00Z", "token_type": "Bearer",
        "user": {"user_id": "fixture-user", "tenant_id": "fixture-tenant"}}}}))
    credential_path.chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("TIANA_") and "proxy" not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin, TIANA_CREDENTIALS_FILE=str(credential_path), TIANA_CA_FILE=str(ca))
    if diagnostic:
        env["TIANA_DIAGNOSTICS"] = "1"
    code, stdout, stderr = invoke(mode, env, fault)
    want = 130 if fault == "cancel" else 5 if fault == "http401" and mode["name"].startswith("apps-") else 1 if fault else mode.get("exit", 0)
    expected = mode["stages"][:mode["stages"].index(fault_stage) + 1] if fault else mode["stages"]
    failures = []
    requests = state["requests"]
    if code != want:
        failures.append(f"exit {code}, expected {want}")
    if [r["stage"] for r in requests] != expected:
        failures.append("request sequence mismatch or retry")
    ids = [r["request_id"] for r in requests]
    printed = set(re.findall(r"req-[A-Za-z0-9_-]+", stderr))
    if len(ids) != len(set(ids)):
        failures.append("request ID reused")
    if printed - set(ids):
        failures.append("invented ID in diagnostics")
    if diagnostic or want != 0:
        if set(ids) - printed:
            failures.append("sent ID missing from diagnostics")
    elif printed:
        failures.append("default successful command emitted diagnostic ID")
    if any(secret in stdout + stderr for secret in secrets):
        failures.append("synthetic secret printed")
    if code == 0 and not fault:
        for expected_text in mode.get("output", []):
            if expected_text not in stdout:
                failures.append("expected output missing: " + expected_text)
        if mode.get("url") and stdout.strip() != mode["url"]:
            failures.append("URL stdout contains extra or wrong data")
        if "list" in mode["name"] and "other-engine" in stdout:
            failures.append("wrong-engine instance shown")
    observed_json = None
    if mode.get("json"):
        try:
            body = json.loads(stdout)
            observed_json = {"status": body.get("status"), "error_code": (body.get("error") or {}).get("code"),
                             "error_request_id": (body.get("error") or {}).get("request_id")}
            if body["status"] != ("succeeded" if want == 0 else "failed"):
                failures.append("JSON status mismatch")
            if fault and body["error"].get("request_id") != ids[-1]:
                failures.append("JSON error ID mismatches failing request")
        except (ValueError, KeyError, IndexError):
            failures.append("JSON result invalid")
    rows.append({"mode": mode["name"], "argv": mode["argv"], "diagnostics_environment": "1" if diagnostic else "unset",
                 "fault_stage": fault_stage, "fault": fault or "none", "exit": code, "requests": list(requests),
                 "stdout_sha256": hashlib.sha256(stdout.encode()).hexdigest(), "stderr_sha256": hashlib.sha256(stderr.encode()).hexdigest(),
                 "json_diagnostic": observed_json, "failures": failures})
    args.output.write_text(json.dumps({"in_progress": True, "rows": rows}, indent=2) + "\n")


modes = []
for engine in ["sqlite", "git"]:
    modes.append({"name": engine + "-list-pipe", "engine": engine, "argv": [engine, "list"], "stages": ["list1", "list2"], "output": ["fixture-name"]})
    for tty in ["next", "quit"]:
        modes.append({"name": engine + "-list-tty-" + tty, "engine": engine, "argv": [engine, "list"],
                      "stages": ["list1", "list2"] if tty == "next" else ["list1"], "tty": tty, "output": ["-- More --"]})
    modes.append({"name": engine + "-list-empty", "engine": engine, "argv": [engine, "list"], "stages": ["list1"], "business": "empty", "output": ["No "]})
    for by_name in [False, True]:
        for url_only in [False, True]:
            mode = {"name": engine + "-show-" + ("name" if by_name else "id") + ("-url" if url_only else "-text"),
                    "engine": engine, "argv": [engine, "show", *(["--url"] if url_only else []), "fixture-name" if by_name else "fixture-id"],
                    "by_name": by_name, "stages": ["detail", *(["lookup"] if by_name else []), *(["branch-detail"] if engine == "sqlite" else [])]}
            if url_only:
                mode["url"] = ("tiana://" + hostname + "/repo.git") if engine == "git" else connection["url"]
            else:
                mode["output"] = ["fixture-name", "fixture-id"]
            modes.append(mode)
    for business in ["missing", "duplicate"]:
        modes.append({"name": engine + "-show-" + business, "engine": engine, "argv": [engine, "show", "fixture-name"],
                      "by_name": True, "stages": ["detail", "lookup"], "business": business, "exit": 1})
    modes.append({"name": engine + "-show-wrong-engine", "engine": engine, "argv": [engine, "show", "fixture-id"],
                  "stages": ["detail"], "business": "wrong-engine", "exit": 1})
    modes.append({"name": engine + "-show-no-url", "engine": engine, "argv": [engine, "show", "--url", "fixture-id"],
                  "stages": ["detail", *(["branch-detail"] if engine == "sqlite" else [])], "business": "no-url", "exit": 1})
    modes.append({"name": engine + "-list-json-rejected", "engine": engine, "argv": [engine, "list", "--json"], "stages": [], "exit": 2})
    modes.append({"name": engine + "-show-json-rejected", "engine": engine, "argv": [engine, "show", "--json", "fixture-id"], "stages": [], "exit": 2})
for url_only in [False, True]:
    modes.append({"name": "sqlite-show-branch-" + ("url" if url_only else "text"), "engine": "sqlite", "named_branch": True,
                  "argv": ["sqlite", "show", "--branch", "preview", *(["--url"] if url_only else []), "fixture-id"],
                  "stages": ["detail", "branch-list", "branch-detail"],
                  **({"url": connection["url"]} if url_only else {"output": ["preview", "child"]})})
for business in ["branch-missing", "branch-renamed"]:
    modes.append({"name": "sqlite-show-" + business, "engine": "sqlite", "named_branch": True,
                  "argv": ["sqlite", "show", "--branch", "preview", "fixture-id"], "business": business, "exit": 1,
                  "stages": ["detail", "branch-list", *(["branch-detail"] if business == "branch-renamed" else [])]})
modes.append({"name": "sqlite-show-deleted", "engine": "sqlite", "argv": ["sqlite", "show", "fixture-id"], "business": "deleted", "stages": ["detail"], "output": ["DELETED"]})
for filtered in [False, True]:
    modes.append({"name": "sqlite-branch-list-" + ("filtered" if filtered else "default"), "engine": "sqlite", "filtered": filtered,
                  "argv": ["sqlite", "branch", "list", *(["--after", "cursor+1", "--search", "预览 + /&"] if filtered else []), "fixture-id"],
                  "stages": ["detail", "branch-list"], "output": ["production", "Next cursor: cursor+2"]})
modes.append({"name": "sqlite-branch-list-json-rejected", "argv": ["sqlite", "branch", "list", "--json", "fixture-id"], "stages": [], "exit": 2})
for json_mode in [False, True]:
    modes.append({"name": "apps-status-" + ("json" if json_mode else "text"), "json": json_mode,
                  "argv": ["web", "status", "fixture-app", "--version", "fixture-version", *(["--json"] if json_mode else [])],
                  "stages": ["app-status"], "output": ["fixture-version", "published"]})

with tempfile.TemporaryDirectory(prefix="tiana-read-diag-") as temporary:
    root = Path(temporary)
    def openssl(parts):
        subprocess.run(["openssl", *parts], cwd=root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    openssl(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.pem", "-days", "1",
             "-subj", "/CN=Read Test CA", "-addext", "basicConstraints=critical,CA:TRUE"])
    openssl(["req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=localhost"])
    (root / "leaf.ext").write_text("basicConstraints=critical,CA:FALSE\nsubjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n")
    openssl(["x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial", "-out", "leaf.pem",
             "-days", "1", "-extfile", "leaf.ext"])
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Peer)
    server.daemon_threads = True
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(root / "leaf.pem", root / "leaf.key")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        for mode in modes:
            if args.only and args.only not in mode["name"]:
                continue
            for diagnostic in [False, True]:
                cases = [] if args.fault else [("", "")]
                if not mode.get("business"):
                    cases += [(stage, fault) for stage in mode["stages"] for fault in ([args.fault] if args.fault else ["http503", "http403", "http404", "invalid", "truncated", "disconnect", "cancel"])]
                for fault_stage, fault in cases:
                    with tempfile.TemporaryDirectory(dir=root) as case:
                        check(mode, diagnostic, fault_stage, fault, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
result = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "rows": rows,
          "passed": sum(not row["failures"] for row in rows), "failed": sum(bool(row["failures"]) for row in rows), "fixture_cleaned": True}
args.output.write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({key: result[key] for key in ["passed", "failed", "fixture_cleaned"]}))
for row in rows:
    if row["failures"]:
        print(json.dumps({key: row[key] for key in ["mode", "diagnostics_environment", "fault_stage", "fault", "failures"]}))
raise SystemExit(1 if result["failed"] else 0)
