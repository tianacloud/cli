#!/usr/bin/env python3
"""Exercise creation receipts, observation and recovery with a finite TLS peer."""
import argparse
import errno
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import signal
import socket
import ssl
import subprocess
import tempfile
import threading
from urllib.parse import parse_qs, urlsplit

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("--output", required=True, type=Path)
parser.add_argument("--only")
parser.add_argument("--boundary-filesystem", type=Path, help="Empty isolated bounded filesystem for ENOSPC and stdout-pipe cases only")
parser.add_argument("--boundary-fault", choices=["sigpipe", "enospc"])
args = parser.parse_args()
binary = args.binary.resolve()
rows, state = [], {}


class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.respond()

    def do_POST(self):
        self.respond()

    def respond(self):
        path = urlsplit(self.path).path
        query = parse_qs(urlsplit(self.path).query)
        mode = state["mode"]
        branch_mode = mode["kind"] == "branch"
        payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"null")
        status = 200
        if path == "/api/v1/app-types":
            stage, body = "catalog", {"items": [{"engine": mode["kind"]}]}
        elif path == "/api/v1/instances" and self.command == "POST":
            stage, status = "create", 202
            body = {"instance_id": "fixture-id", "job_id": 17}
            assert payload["display_name"] == "fixture-name" and payload["engine"] == mode["kind"]
            assert payload.get("notes", "") == mode.get("message", "") and payload["config"] == {}
            key = payload["request_id"]
            assert key
            if state.get("key"):
                assert state["key"] == key, "recovery changed business request ID"
            state["key"] = key
        elif path == "/api/v1/jobs/17":
            stage = "job"
            body = {"job_id": 17, "instance_id": "fixture-id", "job_kind": "instance_create", "request_id": state["key"], "status": "complete"}
        elif path == "/api/v1/instances/fixture-id":
            stage = "instance" if branch_mode else "result"
            body = {"id": "fixture-id", "engine": "sqlite" if branch_mode else mode["kind"], "product_state": "ACTIVE"}
        elif path == "/api/v1/instances/fixture-id/branches":
            if state.get("observe"):
                stage, body = "observe", {"items": [{"branch_id": "child", "name": "child-name", "root": False}]}
            else:
                stage = "parent-list"
                assert query == {"name": ["preview + /&"]}
                body = {"items": [{"branch_id": "source", "name": "preview + /&"}]}
        elif path.endswith("/children"):
            stage, status = "create", 202
            body = {"instance_id": "fixture-id", "operation_id": "23"}
            assert path.endswith("/" + ("source" if mode.get("parent") else "main") + "/children")
            assert payload == {"name": "child-name", **({"notes": mode["message"]} if mode.get("message") else {}),
                               "ttl_seconds": mode.get("ttl"), **({"timestamp": mode["timestamp"]} if "timestamp" in mode else {})}
            assert self.headers.get("Idempotency-Key")
            state["key"] = self.headers["Idempotency-Key"]
        elif "/branches/" in path:
            stage = "parent"
            body = {"instance_id": "fixture-id", "branch": {"branch_id": "source" if mode.get("parent") else "main", "name": "preview + /&" if mode.get("parent") else "production"}}
        elif path == "/api/v1/instances/fixture-id/operations/23":
            stage = "operation"
            body = {"instance_id": "fixture-id", "operation_id": "23", "kind": "CREATE_BRANCH", "state": "success",
                    "parent_branch_id": "source" if mode.get("parent") else "main", "branch_id": "child"}
        else:
            raise AssertionError(path)
        rid = self.headers.get("X-Request-ID")
        assert rid and self.headers.get("Authorization") == "Bearer create-access-fixture"
        event = {"stage": stage, "method": self.command, "path": self.path, "request_id": rid}
        if stage == "create":
            event["business_request_id"] = state["key"]
            event["payload_sha256"] = hashlib.sha256(json.dumps(payload, sort_keys=True).encode()).hexdigest()
            if branch_mode:
                event["ttl_seconds"] = payload["ttl_seconds"]
                event["timestamp"] = payload.get("timestamp")
        state["requests"].append(event)
        if state.get("fill_stage") == stage:
            filler = state["filler"]
            try:
                with filler.open("wb", buffering=0) as stream:
                    while True:
                        stream.write(b"x" * 65536)
            except OSError as error:
                assert error.errno == errno.ENOSPC, error
                state["enospc"] = True
        fault = state["fault"] if stage == state["fault_stage"] else ""
        if fault.startswith("http"):
            status = int(fault[4:])
            body = {"error": {"code": "FIXTURE_REJECTED", "message": "fixture"}}
        elif fault == "empty-catalog":
            body = {"items": []}
        elif fault == "receipt":
            body = {"instance_id": "wrong"} if branch_mode else {"instance_id": "fixture-id"}
        elif fault == "failed":
            body["state" if branch_mode else "status"] = "failed" if branch_mode else "fail"
        elif fault == "identity":
            body["instance_id"] = "another-instance"
        elif fault == "unknown":
            body["state" if branch_mode else "status"] = "unknown"
        elif fault == "deleted":
            body["product_state"] = "DELETED"
        elif fault == "result-id":
            body["id"] = "another-instance"
        elif fault == "child":
            body["branch_id"] = body["parent_branch_id"]
        elif fault == "progress" and sum(e["stage"] == stage for e in state["requests"]) == 1:
            body["state" if branch_mode else "status"] = "running"
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
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True


def invoke(argv, env, cancel):
    child = subprocess.Popen([str(binary), *argv], env=env, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        if cancel:
            assert state["reached"].wait(10), "cancellation stage not reached"
            child.send_signal(signal.SIGINT)
        out, err = child.communicate(timeout=15)
        return child.returncode, out, err
    finally:
        state["release"].set()
        if child.poll() is None:
            child.kill()
            child.wait()


def check(mode, diagnostic, stage, fault, root, origin, ca):
    state.clear()
    state.update(mode=mode, fault_stage=stage, fault=fault, requests=[], reached=threading.Event(), release=threading.Event())
    credentials, pending = root / "credentials.json", root / "pending.json"
    credentials.write_text(json.dumps({"credentials": {origin: {"access_token": "create-access-fixture", "refresh_token": "create-refresh-fixture",
        "expires_at": "2099-01-01T00:00:00Z", "user": {"user_id": "fixture-user", "tenant_id": "fixture-tenant"}}}}))
    credentials.chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("TIANA_") and "proxy" not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin, TIANA_CA_FILE=str(ca), TIANA_CREDENTIALS_FILE=str(credentials), TIANA_PENDING_COMMAND_FILE=str(pending))
    if diagnostic:
        env["TIANA_DIAGNOSTICS"] = "1"
    code, out, err = invoke(mode["argv"], env, fault == "cancel")
    want = mode.get("exit", 0) if not fault or fault == "progress" or fault == "http404" and stage == "job" else 130 if fault == "cancel" else 2 if fault == "empty-catalog" else 1
    expected = mode["stages"]
    if fault and fault not in ["progress"] and not (fault == "http404" and stage == "job"):
        expected = expected[:expected.index(stage) + 1]
    if fault == "progress":
        expected = expected[:expected.index(stage)] + [stage] + expected[expected.index(stage):]
    requests = list(state["requests"])
    failures = []
    def validate(returncode, stdout, stderr, observed, expected_code):
        if returncode != expected_code:
            failures.append(f"exit {returncode}, expected {expected_code}")
        ids = {r["request_id"] for r in observed}
        printed = set(re.findall(r"req-[A-Za-z0-9_-]+", stderr))
        if printed - ids or (diagnostic or expected_code != 0) and ids - printed:
            failures.append("diagnostic IDs mismatch")
        if not diagnostic and expected_code == 0 and printed:
            failures.append("default success emitted IDs")
        if "create-access-fixture" in stdout + stderr or "create-refresh-fixture" in stdout + stderr:
            failures.append("credential in output")
    validate(code, out, err, requests, want)
    if [r["stage"] for r in requests] != expected:
        failures.append("unexpected lifecycle requests")
    if want == 0 and not mode.get("local"):
        if "accepted:" not in out:
            failures.append("acceptance receipt missing")
        if mode.get("wait") and "succeeded:" not in out:
            failures.append("successful wait output missing")
    if want != 0 and "succeeded:" in out:
        failures.append("false terminal success")
    was_pending = pending.exists()
    branch_mode = mode["kind"] == "branch"
    expected_pending = not branch_mode and not mode.get("local") and want != 0 and "create" in [r["stage"] for r in requests] and not (stage == "create" and fault == "http400")
    if was_pending != expected_pending:
        failures.append("pending receipt lifecycle mismatch")
    recovery = None
    if expected_pending:
        persisted = json.loads(pending.read_text())
        start = len(state["requests"])
        state["fault"] = ""
        code2, out2, err2 = invoke(mode["argv"], env, False)
        observed = state["requests"][start:]
        validate(code2, out2, err2, observed, 0)
        if pending.exists():
            failures.append("completed recovery retained pending file")
        writes = [r for r in requests + observed if r["stage"] == "create"]
        if len({r["business_request_id"] for r in writes}) != 1:
            failures.append("recovery changed mutation identity")
        accepted = bool(persisted.get("instance_id"))
        if accepted and any(r["stage"] in ["catalog", "create"] for r in observed):
            failures.append("accepted creation replayed")
        recovery = {"exit": code2, "requests": observed, "accepted_receipt_saved": accepted, "business_identity_unchanged": True}
    elif branch_mode and stage in ["create", "operation"] and fault:
        start = len(state["requests"])
        state["observe"], state["fault"] = True, ""
        code2, out2, err2 = invoke(["sqlite", "branch", "list", "fixture-id"], env, False)
        observed = state["requests"][start:]
        validate(code2, out2, err2, observed, 0)
        if [r["stage"] for r in observed] != ["instance", "observe"] or "child-name" not in out2:
            failures.append("branch recovery inspection failed or replayed")
        recovery = {"exit": code2, "requests": observed, "mode": "explicit read-only branch inspection;no create retry"}
    rows.append({"mode": mode["name"], "kind": mode["kind"], "argv_sha256": hashlib.sha256(json.dumps(mode["argv"]).encode()).hexdigest(),
                 "diagnostics_environment": "1" if diagnostic else "unset", "fault_stage": stage, "fault": fault or "none", "exit": code,
                 "requests": requests, "pending_after_first_command": was_pending, "recovery": recovery,
                 "stdout_sha256": hashlib.sha256(out.encode()).hexdigest(), "stderr_sha256": hashlib.sha256(err.encode()).hexdigest(), "failures": failures})
    args.output.write_text(json.dumps({"in_progress": True, "rows": rows}, indent=2) + "\n")


def check_boundary(mode, diagnostic, fault, stage, root, origin, ca):
    state.clear()
    state.update(mode=mode, fault_stage="", fault="", requests=[], reached=threading.Event(), release=threading.Event())
    credentials = root / "credentials.json"
    credentials.write_text(json.dumps({"credentials": {origin: {"access_token": "create-access-fixture", "refresh_token": "create-refresh-fixture",
        "expires_at": "2099-01-01T00:00:00Z", "user": {"user_id": "fixture-user", "tenant_id": "fixture-tenant"}}}}))
    credentials.chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("TIANA_") and "proxy" not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin, TIANA_CA_FILE=str(ca), TIANA_CREDENTIALS_FILE=str(credentials))
    if diagnostic:
        env["TIANA_DIAGNOSTICS"] = "1"
    with tempfile.TemporaryDirectory(dir=args.boundary_filesystem) as storage:
        pending = Path(storage) / "pending.json"
        filler = Path(storage) / "filler"
        env["TIANA_PENDING_COMMAND_FILE"] = str(pending)
        if fault == "enospc":
            state.update(fill_stage=stage, filler=filler)
            code, out, err = invoke(mode["argv"], env, False)
        else:
            reader, writer = os.pipe()
            os.close(reader)
            try:
                child = subprocess.Popen([str(binary), *mode["argv"]], env=env, stdin=subprocess.DEVNULL, stdout=writer, stderr=subprocess.PIPE, text=True)
            finally:
                os.close(writer)
            _, err = child.communicate(timeout=15)
            code, out = child.returncode, ""
        first = list(state["requests"])
        failures = []
        persisted = json.loads(pending.read_text()) if pending.exists() else None
        if fault == "enospc":
            if code != 1 or not state.get("enospc") or "no space left on device" not in err:
                failures.append("ENOSPC not surfaced as exit1")
            if stage == "catalog" and (any(r["stage"] == "create" for r in first) or persisted):
                failures.append("mutation before durable identity")
            if stage == "create" and (not persisted or persisted.get("instance_id") or persisted["idempotency_key"] != state["key"]):
                failures.append("receipt save failure lost original intent")
            if out:
                failures.append("printed acceptance before durable receipt")
        elif code != 1:
            failures.append(f"expected handled stdout pipe exit1, got {code}")
        if mode["kind"] != "branch" and fault == "sigpipe" and (not persisted or persisted.get("instance_id") != "fixture-id"):
            failures.append("SIGPIPE lost accepted receipt")
        if any(secret in err for secret in ["create-access-fixture", "create-refresh-fixture"]):
            failures.append("credential exposed")
        if (diagnostic or fault == "sigpipe") and any(r["request_id"] not in err for r in first):
            failures.append("diagnostics lost original request ID")
        if filler.exists():
            filler.unlink()
        state.pop("fill_stage", None)
        offset = len(state["requests"])
        if mode["kind"] == "branch":
            state["observe"] = True
            recovery_args = ["sqlite", "branch", "list", "fixture-id"]
        else:
            recovery_args = mode["argv"]
        code2, out2, err2 = invoke(recovery_args, env, False)
        later = state["requests"][offset:]
        if code2 != 0 or pending.exists():
            failures.append("recovery did not complete")
        if fault == "sigpipe" and any(r["stage"] == "create" for r in later):
            failures.append("accepted mutation replayed after SIGPIPE")
        if stage == "create" and fault == "enospc" and any(r.get("business_request_id", state["key"]) != persisted["idempotency_key"] for r in later):
            failures.append("uncertain retry changed business identity")
        rows.append({"mode": mode["name"], "diagnostics_environment": "1" if diagnostic else "unset", "fault": fault, "fault_stage": stage,
                     "exit": code, "requests": first, "pending_identity_retained": bool(persisted), "recovery": {"exit": code2, "requests": later},
                     "printed_request_ids": re.findall(r"req-[A-Za-z0-9_-]+", err), "recovery_printed_request_ids": re.findall(r"req-[A-Za-z0-9_-]+", err2),
                     "stderr_sha256": hashlib.sha256(err.encode()).hexdigest(), "failures": failures})
        args.output.write_text(json.dumps({"in_progress": True, "rows": rows}, indent=2) + "\n")


modes = []
for engine in ["sqlite", "git"]:
    for name, flags, wait, message in [("accepted", [], False, ""), ("wait-long", ["--wait"], True, ""),
        ("wait-short-message", ["-w", "-m", "描述 + /&"], True, "描述 + /&"),
        ("wait-false-message", ["--wait=false", "--message", "-w"], False, "-w"),
        ("message-boundary", ["-m", "字" * 682 + "ab"], False, "字" * 682 + "ab")]:
        modes.append({"name": engine + "-" + name, "kind": engine, "argv": [engine, "create", *flags, "fixture-name"],
                      "message": message, "wait": wait, "stages": ["catalog", "create", *(["job", "result"] if wait else [])]})
    for name, flags in [("json", ["--json"]), ("long-message", ["-m", "a" * 2049]), ("arity", ["another-name"])]:
        modes.append({"name": engine + "-reject-" + name, "kind": engine, "argv": [engine, "create", *flags, "fixture-name"], "stages": [], "exit": 2, "local": True})
for name, flags, wait, parent, ttl, timestamp, message in [
    ("accepted", [], False, False, None, None, ""), ("wait", ["--wait"], True, False, None, None, ""),
    ("wait-false", ["--wait=false"], False, False, None, None, ""),
    ("parent-message", ["--parent", "preview + /&", "-m", "分支说明"], False, True, None, None, "分支说明"),
    ("ttl-min", ["--ttl", "1"], False, False, 1, None, ""),
    ("ttl-max-parent-wait", ["--ttl", "2592000", "--parent", "preview + /&", "-w"], True, True, 2592000, None, ""),
    ("timestamp-short", ["-ts", "1790000000", "--parent", "preview + /&", "--wait"], True, True, None, 1790000000, ""),
    ("timestamp-zero", ["--timestamp", "0"], False, False, None, 0, ""),
    ("all-options", ["--ttl", "600", "--timestamp", "1790000000", "--parent", "preview + /&", "-m", "描述", "-w"], True, True, 600, 1790000000, "描述"),
    ("message-boundary", ["--message", "a" * 2048], False, False, None, None, "a" * 2048),
]:
    modes.append({"name": "branch-" + name, "kind": "branch", "argv": ["sqlite", "branch", "create", *flags, "fixture-id", "child-name"],
                  "wait": wait, "parent": parent, "ttl": ttl, "message": message, **({"timestamp": timestamp} if timestamp is not None else {}),
                  "stages": ["instance", *(["parent-list"] if parent else []), "parent", "create", *(["operation"] if wait else [])]})
for name, flags in [("json", ["--json"]), ("ttl-zero", ["--ttl", "0"]), ("ttl-over", ["--ttl", "2592001"]),
                    ("ttl-fraction", ["--ttl", "1.5"]), ("timestamp-negative", ["-ts", "-1"]),
                    ("timestamp-over", ["--timestamp", "18446744073709551616"]), ("parent-empty", ["--parent", " "]),
                    ("long-message", ["-m", "字" * 683])]:
    modes.append({"name": "branch-reject-" + name, "kind": "branch", "argv": ["sqlite", "branch", "create", *flags, "fixture-id", "child-name"], "stages": [], "exit": 2, "local": True})

with tempfile.TemporaryDirectory(prefix="tiana-create-diag-") as temporary:
    root = Path(temporary)
    def openssl(parts):
        subprocess.run(["openssl", *parts], cwd=root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    openssl(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.pem", "-days", "1", "-subj", "/CN=Create Test CA", "-addext", "basicConstraints=critical,CA:TRUE"])
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
            if args.boundary_filesystem:
                if mode["name"] not in ["sqlite-wait-long", "git-wait-long", "branch-all-options"]:
                    continue
                for diagnostic in [False, True]:
                    cases = [("sigpipe", "output")]
                    if mode["kind"] != "branch":
                        cases += [("enospc", stage) for stage in ["catalog", "create"]]
                    for fault, stage in cases:
                        if args.boundary_fault and args.boundary_fault != fault:
                            continue
                        with tempfile.TemporaryDirectory(dir=root) as case:
                            check_boundary(mode, diagnostic, fault, stage, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem")
                continue
            if args.only and args.only not in mode["name"]:
                continue
            cases = [("", "")]
            if mode["name"] in ["sqlite-wait-long", "git-wait-long", "branch-all-options"]:
                cases += [(stage, fault) for stage in mode["stages"] for fault in ["http503", "invalid", "disconnect", "cancel"]]
                cases += [("create", "receipt"), ("create", "http400")]
                if mode["kind"] == "branch":
                    cases += [("operation", f) for f in ["failed", "identity", "unknown", "child", "progress"]]
                else:
                    cases += [("catalog", "empty-catalog"), ("job", "http404"), *[("job", f) for f in ["failed", "identity", "unknown", "progress"]],
                              ("result", "deleted"), ("result", "result-id")]
            for diagnostic in [False, True]:
                for stage, fault in cases:
                    with tempfile.TemporaryDirectory(dir=root) as case:
                        check(mode, diagnostic, stage, fault, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem")
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
result = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "modes": modes, "rows": rows,
          "passed": sum(not r["failures"] for r in rows), "failed": sum(bool(r["failures"]) for r in rows), "fixture_cleaned": True}
args.output.write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({k: result[k] for k in ["passed", "failed", "fixture_cleaned"]}))
for row in rows:
    if row["failures"]:
        print(json.dumps({k: row[k] for k in ["mode", "diagnostics_environment", "fault_stage", "fault", "failures"]}))
raise SystemExit(1 if result["failed"] else 0)
