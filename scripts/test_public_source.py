#!/usr/bin/env python3
"""Regression cases for the public-source address scan (synthetic inputs)."""
import base64
import pathlib
import subprocess
import sys
import tempfile
import unittest

SCANNER = pathlib.Path(__file__).with_name("check-public-source.py")


class PublicSourceScanTest(unittest.TestCase):
    def scan(self, content):
        with tempfile.TemporaryDirectory() as directory:
            pathlib.Path(directory, "fixture.txt").write_bytes(content)
            return subprocess.run([sys.executable, str(SCANNER), directory],
                                  capture_output=True).returncode

    def test_rejects_private_ipv4_in_plain_hex_and_pem(self):
        for octets in [(10, 2, 3, 4), (172, 16, 1, 2), (192, 168, 1, 2),
                       (100, 64, 1, 2), (169, 254, 1, 2)]:
            address = ".".join(map(str, octets)).encode()
            plain = b"https://" + address + b":443/endpoint"
            inputs = [plain, plain.hex().encode(),
                      b"-----BEGIN CERTIFICATE-----\n" + base64.b64encode(plain)
                      + b"\n-----END CERTIFICATE-----\n"]
            for encoding, content in enumerate(inputs):
                with self.subTest(octets=octets, encoding=encoding):
                    self.assertEqual(self.scan(content), 1)

    def test_rejects_private_hostname(self):
        address = b".".join([b"host", b"internal", b"example"])
        self.assertEqual(self.scan(address), 1)

    def test_allows_documentation_loopback_and_version_numbers(self):
        self.assertEqual(self.scan(b"203.0.113.10 192.0.2.8 198.51.100.4 "
                                   b"127.0.0.1 0.0.0.0 [2001:db8::1] "
                                   b"v1.2.3.4 go1.25.0 999.1.2.3"), 0)


if __name__ == "__main__":
    unittest.main()
