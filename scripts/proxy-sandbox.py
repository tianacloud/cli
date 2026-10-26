#!/usr/bin/env python3
"""Linux acceptance sandbox with no client Internet route.

Run `host DIR` outside a network namespace, then run
`unshare -Urn python3 scripts/proxy-sandbox.py run DIR http -- COMMAND ...`.
Use socks5h instead of http for the second route. The namespace contains only
loopback; private Unix sockets reach the two host-side proxy implementations.
No certificate interception, system proxy changes or real credentials in logs.
"""
import argparse
import contextlib
import ipaddress
import json
import os
from pathlib import Path
import signal
import socket
import socketserver
import struct
import subprocess
import sys
import threading


class Connections:
    def __init__(self):
        self.lock = threading.Lock()
        self.sockets = set()
        self.limit = threading.BoundedSemaphore(64)
        self.counts = {"http": 0, "socks5h": 0, "failed": 0}

    def add(self, connection):
        with self.lock:
            self.sockets.add(connection)

    def remove(self, connection):
        with self.lock:
            self.sockets.discard(connection)
        connection.close()

    def close(self):
        with self.lock:
            for connection in self.sockets:
                with contextlib.suppress(OSError):
                    connection.shutdown(socket.SHUT_RDWR)
                connection.close()

    def count(self, kind):
        with self.lock:
            self.counts[kind] += 1


def exact(connection, size):
    data = bytearray()
    while len(data) < size:
        chunk = connection.recv(size - len(data))
        if not chunk:
            raise EOFError()
        data.extend(chunk)
    return bytes(data)


def relay(left, right):
    def copy(source, target):
        try:
            while chunk := source.recv(64 * 1024):
                target.sendall(chunk)
        except OSError:
            pass
        finally:
            with contextlib.suppress(OSError):
                target.shutdown(socket.SHUT_WR)
    worker = threading.Thread(target=copy, args=(left, right), daemon=True)
    worker.start()
    copy(right, left)
    with contextlib.suppress(OSError):
        left.shutdown(socket.SHUT_RDWR)
    with contextlib.suppress(OSError):
        right.shutdown(socket.SHUT_RDWR)
    worker.join(5)


class UnixServer(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


def proxy_handler(state, kind):
    class Handler(socketserver.BaseRequestHandler):
        def handle(self):
            client = self.request
            if not state.limit.acquire(blocking=False):
                return
            state.add(client)
            upstream = None
            try:
                client.settimeout(15)
                if kind == "http":
                    head = bytearray()
                    while not head.endswith(b"\r\n\r\n"):
                        head.extend(exact(client, 1))
                        if len(head) > 16 * 1024:
                            raise ValueError()
                    lines = head.decode("ascii").split("\r\n")
                    if len(lines) > 67:
                        raise ValueError()
                    method, authority, version = lines[0].split(" ")
                    if method != "CONNECT" or version != "HTTP/1.1":
                        raise ValueError()
                    host, port = authority.rsplit(":", 1)
                    host = host.removeprefix("[").removesuffix("]")
                    port = int(port)
                else:
                    version, count = exact(client, 2)
                    if version != 5 or 0 not in exact(client, count):
                        client.sendall(bytes([5, 255]))
                        return
                    client.sendall(bytes([5, 0]))
                    version, command, reserved, address_type = exact(client, 4)
                    if (version, command, reserved) != (5, 1, 0):
                        raise ValueError()
                    if address_type == 3:
                        host = exact(client, exact(client, 1)[0]).decode("ascii")
                    elif address_type in (1, 4):
                        host = str(ipaddress.ip_address(exact(client, 4 if address_type == 1 else 16)))
                    else:
                        raise ValueError()
                    port = struct.unpack("!H", exact(client, 2))[0]
                if not host or not 1 <= port <= 65535:
                    raise ValueError()
                # Resolution is deliberately outside the client's namespace.
                upstream = socket.create_connection((host, port), timeout=15)
                state.add(upstream)
                if kind == "http":
                    client.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                else:
                    client.sendall(bytes([5, 0, 0, 1, 127, 0, 0, 1, 0, 0]))
                client.settimeout(None)
                upstream.settimeout(None)
                state.count(kind)
                relay(client, upstream)
            except (OSError, EOFError, ValueError, UnicodeError):
                state.count("failed")
            finally:
                if upstream is not None:
                    state.remove(upstream)
                state.remove(client)
                state.limit.release()
    return Handler


def host(directory):
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    if directory.stat().st_mode & 0o077:
        raise RuntimeError("sandbox directory must be private")
    state = Connections()
    servers, threads = [], []
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    try:
        for kind in ("http", "socks5h"):
            path = directory / (kind + ".sock")
            server = UnixServer(str(path), proxy_handler(state, kind))
            path.chmod(0o600)
            servers.append(server)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            threads.append(thread)
        (directory / "ready.json").write_text(json.dumps({"protocols": ["http", "socks5h"]}))
        print("HTTP CONNECT and SOCKS5 proxies ready", flush=True)
        stopped.wait()
    finally:
        state.close()
        for server in servers:
            server.shutdown()
            server.server_close()
            Path(server.server_address).unlink(missing_ok=True)
        for thread in threads:
            thread.join(5)
        (directory / "ready.json").unlink(missing_ok=True)
        (directory / "counts.json").write_text(json.dumps(state.counts))


def run(directory, kind, command):
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True)
    links = json.loads(subprocess.check_output(["ip", "-j", "link", "show"]))
    routes = json.loads(subprocess.check_output(["ip", "-j", "route", "show"]))
    if routes or {link["ifname"] for link in links} != {"lo"}:
        raise RuntimeError("client namespace must have only loopback and no routes")
    # A numeric address avoids relying on DNS failure as isolation evidence.
    try:
        direct = socket.create_connection(("192.0.2.1", 443), timeout=0.2)
    except OSError:
        pass
    else:
        direct.close()
        raise RuntimeError("direct external TCP unexpectedly succeeded")
    state = Connections()
    path = directory / (kind + ".sock")

    class Bridge(socketserver.BaseRequestHandler):
        def handle(self):
            if not state.limit.acquire(blocking=False):
                return
            peer = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            state.add(peer)
            state.add(self.request)
            try:
                peer.connect(str(path))
                relay(self.request, peer)
            finally:
                state.remove(peer)
                state.remove(self.request)
                state.limit.release()

    class TCPServer(socketserver.ThreadingMixIn, socketserver.TCPServer):
        daemon_threads = True

    server = TCPServer(("127.0.0.1", 0), Bridge)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    environment = {k: v for k, v in os.environ.items()
                   if k.upper() not in {"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "REQUEST_METHOD"}}
    environment["ALL_PROXY"] = f"{kind}://127.0.0.1:{server.server_address[1]}"
    environment["NO_PROXY"] = "localhost,127.0.0.1,[::1]"
    environment["TIANA_TEST_PROXY_SANDBOX"] = "loopback-only"
    try:
        return subprocess.run(command, env=environment).returncode
    finally:
        state.close()
        server.shutdown()
        server.server_close()
        worker.join(5)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("host", "run"))
    parser.add_argument("directory", type=Path)
    parser.add_argument("rest", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.mode == "host":
        if args.rest:
            parser.error("host takes only DIRECTORY")
        host(args.directory.resolve())
        return 0
    if len(args.rest) < 3 or args.rest[0] not in ("http", "socks5h") or args.rest[1] != "--":
        parser.error("run takes DIRECTORY http|socks5h -- COMMAND [ARGS ...]")
    return run(args.directory.resolve(), args.rest[0], args.rest[2:])


if __name__ == "__main__":
    sys.exit(main())
