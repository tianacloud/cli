#!/usr/bin/env python3
"""Exercise a released CLI against an isolated TLS peer, without live accounts."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import signal
import socket
import ssl
import subprocess
import tempfile
import threading

parser = argparse.ArgumentParser()
parser.add_argument("binary", type=Path)
parser.add_argument("--output", required=True, type=Path)
parser.add_argument("--diagnostics", choices=["all", "default", "enabled"], default="all")
parser.add_argument("--login-pipe-only", action="store_true")
parser.add_argument("--login-success-only", action="store_true")
args = parser.parse_args()
binary = args.binary.resolve()
rows = []
state = {}
quota = {"tenant_id": "test-tenant", "compute_used": "123", "storage_used": "456",
         "instances_used": "2", "limits": {"compute": "10000", "storage_bytes": "2000000000",
         "max_instances": "3", "period": "week"}, "blocked": False,
         "period_start": 1790000000000, "period_end": 1790600000000}
secret_values = ["fixture-access-secret", "fixture-refresh-secret", "fixture-client-secret", "fixture-code-secret"]


class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.handle_request()

    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        rid = self.headers.get("X-Request-ID")
        assert rid
        path = self.path.split("?")[0]
        stage = {"/api/v1/auth/transactions": "create", "/api/v1/auth/token": "exchange",
                 "/api/v1/auth/transactions/whoami": "whoami", "/api/v1/usage": "quota",
                 "/api/v1/auth/transactions/logout": "logout", "/api/v1/auth/refresh": "refresh"}.get(path)
        if path.endswith("/poll"):
            stage = "poll"
        assert stage, path
        state["requests"].append({"path": self.path, "stage": stage, "request_id": rid})
        scenario = state["scenario"]
        if scenario == "pipe-after-exchange" and stage == "exchange":
            state["reached"].set()
            assert state["release"].wait(10), "output pipe boundary not released"
        hit = sum(x["stage"] == stage for x in state["requests"])
        body, code = {}, 200
        if stage == "create":
            body = {"transaction_id": "fixture", "client_secret": secret_values[2], "user_code": "TEST",
                    "verification_uri_complete": "https://console.example.test/approve", "expires_in": 600, "poll_interval": 1}
        elif stage == "poll":
            body = {"status": "approved", "authorization_code": secret_values[3]}
            if scenario in ["denied", "expired", "completed", "unknown", "empty-status"]:
                body = {"status": "" if scenario == "empty-status" else scenario}
            if scenario in ["pending-approved", "authenticated-approved"] and hit == 1:
                body = {"status": scenario.split("-")[0], "retry_after": 1}
            if scenario == "approved-no-code":
                body = {"status": "approved"}
            if scenario == "retry-after" and hit == 1:
                body, code = {"error": {"code": "RATE_LIMITED", "message": "fixture"}}, 429
        elif stage in ["exchange", "refresh"]:
            body = {"access_token": secret_values[0], "refresh_token": secret_values[1], "expires_in": 3600,
                    "user": {"user_id": "fixture-user", "tenant_id": "test-tenant", "email": "test@example.test"}}
        elif stage == "whoami":
            body = {"user": {"user_id": "fixture-user", "email": "test@example.test"}}
            if scenario.startswith("refresh-") and hit == 1:
                code = 401
            if scenario == "refresh-still-unauthorized":
                code = 401
        elif stage == "quota":
            body = dict(quota)
        if scenario == stage + "-semantic":
            body = {}
        fault = scenario.removeprefix(stage + "-") if scenario.startswith(stage + "-") else ""
        if fault == "http":
            code = 503
        if code >= 400:
            body = {"error": {"code": "UNAVAILABLE", "message": "fixture"}}
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
        if scenario == "retry-after" and stage == "poll" and hit == 1:
            self.send_header("Retry-After", "1")
        self.end_headers()
        try:
            self.wfile.write(data)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True


def check(command, scenario, diagnostic, root, origin, certificate, want):
    state.clear()
    state.update(scenario=scenario, requests=[], reached=threading.Event(), release=threading.Event())
    credential_path = root/'config'/'tiana'/'credentials.json'
    credential_path.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    if command != "login" and scenario not in ["signed-out", "json-rejected"]:
        credential = {"access_token": secret_values[0], "refresh_token": secret_values[1],
                      "token_type": "Bearer", "expires_at": "2099-01-01T00:00:00Z",
                      "user": {"user_id": "fixture-user", "tenant_id": "test-tenant"}}
        credential_path.write_text(json.dumps({"credentials": {origin: credential}}))
        credential_path.chmod(0o600)
    env = {k: v for k, v in os.environ.items() if not k.startswith("TIANA_") and "proxy" not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin, XDG_CONFIG_HOME=str(root/'config'), TIANA_CA_FILE=str(certificate))
    if diagnostic:
        env["TIANA_DIAGNOSTICS"] = "1"
    argv = [str(binary), command] + (["--json"] if scenario == "json-rejected" else [])
    process = subprocess.Popen(argv, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        if scenario.startswith("pipe-"):
            if scenario == "pipe-after-exchange":
                assert state["reached"].wait(10), "exchange boundary not reached"
            process.stdout.close()
            process.stdout = None
            state["release"].set()
        if scenario.endswith("-cancel"):
            assert state["reached"].wait(10), "cancel boundary not reached"
            process.send_signal(signal.SIGINT)
        stdout, stderr = process.communicate(timeout=20)
        stdout = stdout or ""
    finally:
        state["release"].set()
        if process.poll() is None:
            process.kill()
            process.wait()
    requests = list(state["requests"])
    failures = []
    if scenario in ["signed-out", "json-rejected"]:
        expected_stages = []
    elif command == "login":
        expected_stages = ["create", "poll", "exchange"]
        if scenario == "pipe-before-output":
            expected_stages = ["create"]
        elif scenario == "pipe-after-exchange":
            pass
        elif scenario in ["pending-approved", "authenticated-approved", "retry-after"]:
            expected_stages = ["create", "poll", "poll", "exchange"]
        elif scenario in ["denied", "expired", "completed", "unknown", "empty-status", "approved-no-code"]:
            expected_stages = ["create", "poll"]
        elif "-" in scenario:
            expected_stages = expected_stages[:expected_stages.index(scenario.split("-")[0]) + 1]
    elif command == "status":
        expected_stages = ["whoami", "quota"]
        if scenario == "refresh-success":
            expected_stages = ["whoami", "refresh", "whoami", "quota"]
        elif scenario == "refresh-still-unauthorized":
            expected_stages = ["whoami", "refresh", "whoami"]
        elif scenario.startswith("refresh-"):
            expected_stages = ["whoami", "refresh"]
        elif scenario.startswith("whoami-"):
            expected_stages = ["whoami"]
    else:
        expected_stages = ["logout"]
    if [request["stage"] for request in requests] != expected_stages:
        failures.append("unexpected request stages or replay")
    if process.returncode != want:
        failures.append(f"exit {process.returncode}, expected {want}")
    ids = [x["request_id"] for x in requests]
    if len(ids) != len(set(ids)):
        failures.append("request ID reused")
    if diagnostic or want != 0:
        if any(rid not in stderr for rid in ids):
            failures.append("sent request ID missing from stderr")
    elif "Request ID:" in stderr:
        failures.append("default successful command unexpectedly emitted diagnostic ID")
    if not requests and "Request ID:" in stderr:
        failures.append("local-only command invented ID")
    if any(value in stdout + stderr for value in secret_values):
        failures.append("synthetic secret in output")
    if scenario in ["signed-out", "json-rejected"] and requests:
        failures.append("local-only outcome made a request")
    if scenario not in ["signed-out", "json-rejected"] and not requests:
        failures.append("expected network boundary not reached")
    if command == "logout" and scenario != "json-rejected" and credential_path.exists():
        failures.append("logout retained local credential")
    if command == "login" and want == 0 and not credential_path.exists():
        failures.append("successful login did not save credential")
    if command == "login" and want != 0 and credential_path.exists() and scenario != "pipe-after-exchange":
        failures.append("failed login saved credential")
    if scenario == "pipe-after-exchange" and not credential_path.exists():
        failures.append("output failure removed issued credential")
    if command == "status" and want == 0 and "RESOURCE" not in stdout:
        failures.append("status omitted quota")
    if command == "status" and scenario.startswith("quota-") and "Quota: unavailable" not in stdout:
        failures.append("status quota failure omitted unavailable result")
    if command == "status" and scenario == "signed-out" and "Not signed in" not in stdout:
        failures.append("signed-out status missing")
    rows.append({"command": command, "scenario": scenario, "diagnostics": diagnostic,
                 "diagnostics_environment": "1" if diagnostic else "unset",
                 "requested_json": scenario == "json-rejected", "exit": process.returncode,
                 "requests": requests, "stdout_sha256": hashlib.sha256(stdout.encode()).hexdigest(),
                 "stderr_sha256": hashlib.sha256(stderr.encode()).hexdigest(), "failures": failures})
    args.output.write_text(json.dumps({"in_progress": True, "rows": rows}, indent=2) + "\n")


with tempfile.TemporaryDirectory(prefix="tiana-session-diag-") as temporary:
    root = Path(temporary)
    def openssl(parts):
        subprocess.run(["openssl", *parts], cwd=root, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    openssl(["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.pem",
             "-days", "1", "-subj", "/CN=Session Test CA", "-addext", "basicConstraints=critical,CA:TRUE"])
    openssl(["req", "-new", "-newkey", "rsa:2048", "-nodes", "-keyout", "leaf.key", "-out", "leaf.csr", "-subj", "/CN=localhost"])
    (root / "leaf.ext").write_text("basicConstraints=critical,CA:FALSE\nsubjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n")
    openssl(["x509", "-req", "-in", "leaf.csr", "-CA", "ca.pem", "-CAkey", "ca.key", "-CAcreateserial",
             "-out", "leaf.pem", "-days", "1", "-extfile", "leaf.ext"])
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Peer)
    server.daemon_threads = True
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(root / "leaf.pem", root / "leaf.key")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        for diagnostic in ([False, True] if args.diagnostics == "all" else [args.diagnostics == "enabled"]):
            cases = []
            if args.login_success_only:
                with tempfile.TemporaryDirectory(dir=root) as case:
                    check("login", "approved", diagnostic, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem", 0)
                continue
            if args.login_pipe_only:
                for scenario in ["pipe-before-output", "pipe-after-exchange"]:
                    with tempfile.TemporaryDirectory(dir=root) as case:
                        check("login", scenario, diagnostic, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem", 1)
                continue
            for scenario in ["approved", "pending-approved", "authenticated-approved", "retry-after"]:
                cases.append(("login", scenario, 0))
            for scenario in ["denied", "expired", "completed", "unknown", "empty-status", "approved-no-code"]:
                cases.append(("login", scenario, 1))
            for stage in ["create", "poll", "exchange"]:
                for fault in ["http", "invalid", "truncated", "disconnect", "semantic", "cancel"]:
                    cases.append(("login", stage + "-" + fault, 130 if fault == "cancel" else 1))
            cases += [("status", "success", 0), ("status", "refresh-success", 0), ("status", "signed-out", 1),
                      ("status", "refresh-still-unauthorized", 1)]
            for stage in ["whoami", "quota", "refresh"]:
                for fault in ["http", "invalid", "truncated", "disconnect", "semantic", "cancel"]:
                    cases.append(("status", stage + "-" + fault, 130 if fault == "cancel" else 1))
            for scenario in ["success", "signed-out", "logout-invalid", "logout-semantic", "logout-truncated"]:
                cases.append(("logout", scenario, 0))
            for fault in ["http", "disconnect", "cancel"]:
                cases.append(("logout", "logout-" + fault, 130 if fault == "cancel" else 1))
            cases += [(command, "json-rejected", 2) for command in ["login", "status", "logout"]]
            for command, scenario, want in cases:
                with tempfile.TemporaryDirectory(dir=root) as case:
                    check(command, scenario, diagnostic, Path(case), f"https://127.0.0.1:{server.server_port}", root / "ca.pem", want)
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
result = {"binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(), "rows": rows,
          "passed": sum(not row["failures"] for row in rows), "failed": sum(bool(row["failures"]) for row in rows),
          "fixture_cleaned": True, "transport": "Isolated TLS HTTP peer; explicit trusted CA, no TLS bypass"}
args.output.write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({key: result[key] for key in ["passed", "failed", "fixture_cleaned"]}))
for row in rows:
    if row["failures"]:
        print(json.dumps({key: row[key] for key in ["command", "scenario", "diagnostics", "failures"]}))
raise SystemExit(1 if result["failed"] else 0)
