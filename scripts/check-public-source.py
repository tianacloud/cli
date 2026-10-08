#!/usr/bin/env python3
"""Reject unconfigured private addresses in source and decoded blocks; print paths only."""
import argparse
import base64
import ipaddress
import pathlib
import re

# Construct labels separately so the scanner does not embed a private address.
FORBIDDEN = re.compile(rb"(?:[a-z0-9-]+\.)*internal(?:\.[a-z0-9-]+)+|(?:[a-z0-9-]+\.)+internal\b", re.I)
PEM = re.compile(rb"-----BEGIN ([A-Z0-9 ]+)-----\s+([A-Za-z0-9+/=\s]+)-----END \1-----")
IPV4 = re.compile(rb"(?<![\w.])(?:[0-9]{1,3}\.){3}[0-9]{1,3}(?![\w.])")
# RFC1918, shared-address space and link-local; allow loopback and TEST-NET.
PRIVATE_V4 = [ipaddress.IPv4Network((prefix, bits)) for prefix, bits in [
    (10 << 24, 8), ((172 << 24) | (16 << 16), 12),
    ((192 << 24) | (168 << 16), 16), ((100 << 24) | (64 << 16), 10),
    ((169 << 24) | (254 << 16), 16),
]]


def contains_private_address(block):
    if FORBIDDEN.search(block):
        return True
    for match in IPV4.finditer(block):
        try:
            address = ipaddress.IPv4Address(match.group().decode("ascii"))
        except ipaddress.AddressValueError:
            continue
        if any(address in network for network in PRIVATE_V4):
            return True
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("root", nargs="?", type=pathlib.Path,
                        default=pathlib.Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    root = args.root.resolve()
    failed = []
    count = 0
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if any(part in {".git", ".artifacts", "__pycache__"} for part in relative.parts):
            continue
        if path.is_symlink():
            failed.append(str(relative) + " (symlink)")
            continue
        if not path.is_file():
            continue
        count += 1
        data = path.read_bytes()
        blocks = [data]
        # Literal wire fixtures can hide hostnames inside hex encodings.
        for encoded in re.findall(rb"(?<![0-9a-fA-F])[0-9a-fA-F]{32,}(?![0-9a-fA-F])", data):
            if len(encoded) % 2 == 0:
                blocks.append(bytes.fromhex(encoded.decode("ascii")))
        for match in PEM.finditer(data):
            blocks.append(base64.b64decode(re.sub(rb"\s+", b"", match.group(2)), validate=True))
        if any(contains_private_address(block) for block in blocks):
            failed.append(str(relative))
    if failed:
        for path in failed:
            print("Private address or unsafe source path:", path)
        raise SystemExit(1)
    print(f"Public hostname/IPv4 scan passed: {count} files, including decoded PEM and hex blocks")


if __name__ == "__main__":
    main()
