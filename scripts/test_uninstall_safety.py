#!/usr/bin/env python3
"""Exercise the real uninstaller without root or files outside a temporary tree."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().with_name("uninstall-linux-bundle.sh")


@unittest.skipUnless(os.name == "posix", "the bundle uninstaller requires Unix")
class UninstallSafetyTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="uninstall-safety-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.prefix = self.root / "install"
        for relative in ("bin", "libexec/tiana", "share/tiana"):
            (self.prefix / relative).mkdir(parents=True)
        self.files = [
            self.prefix / "bin/tiana",
            self.prefix / "bin/git-remote-tiana",
            self.prefix / "libexec/tiana/tiana-helper",
            self.prefix / "libexec/tiana/tiana-helper.manifest.json",
            self.prefix / "share/tiana/gateway-wildcard.der",
        ]
        for path in self.files:
            path.write_text("synthetic installed file\n")
        self.shims = self.root / "shims"
        self.shims.mkdir()
        self.rm_log = self.root / "rm.log"
        self.environment = os.environ.copy()
        self.environment.update({
            "PATH": str(self.shims) + os.pathsep + os.environ["PATH"],
            "UNINSTALL_TEST_ROOT": str(self.root),
            "UNINSTALL_TEST_RM_LOG": str(self.rm_log),
        })
        # Ownership is synthetic; directory types, symlinks, mode bits and
        # removals are real. Ancestors outside the fixture emulate a trusted
        # system install, so the tests never require root or chmod outside it.
        self.shim("id", "import sys\nassert sys.argv[1:] == ['-u']\nprint(0)\n")
        self.shim("stat", """import os, stat, sys
from pathlib import Path
assert sys.argv[1] == '-c' and sys.argv[3] == '--'
path = Path(sys.argv[4])
root = Path(os.environ['UNINSTALL_TEST_ROOT'])
inside = path == root or root in path.parents
if sys.argv[2] == '%u':
    print(1000 if str(path) == os.environ.get('UNINSTALL_TEST_UNTRUSTED_OWNER') else 0)
elif sys.argv[2] == '%A':
    print(stat.filemode(path.lstat().st_mode) if inside else 'drwxr-xr-x')
else:
    raise AssertionError('unexpected stat invocation')
""")
        real_rm = shutil.which("rm")
        self.assertIsNotNone(real_rm)
        self.shim("rm", "import os, sys\nfrom pathlib import Path\n"
                  "Path(os.environ['UNINSTALL_TEST_RM_LOG']).write_text('rm invoked\\n')\n"
                  f"os.execv({real_rm!r}, [{real_rm!r}] + sys.argv[1:])\n")

    def shim(self, name, body):
        path = self.shims / name
        path.write_text("#!" + sys.executable + "\n" + body)
        path.chmod(0o700)

    def run_uninstall(self, prefix=None):
        return subprocess.run(["/bin/sh", str(SCRIPT), "--prefix", str(prefix or self.prefix)],
                              env=self.environment, text=True, capture_output=True)

    def assert_refused(self, prefix=None):
        result = self.run_uninstall(prefix)
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.rm_log.exists(), "refusal must precede every rm")
        for path in self.files:
            self.assertEqual(path.read_text(), "synthetic installed file\n")

    def test_intermediate_directory_symlinks_preserve_targets(self):
        for relative in ("bin", "libexec", "libexec/tiana", "share", "share/tiana"):
            with self.subTest(relative=relative):
                path = self.prefix / relative
                target = self.root / "redirected"
                path.rename(target)
                path.symlink_to(target, target_is_directory=True)
                try:
                    self.assert_refused()
                finally:
                    path.unlink()
                    target.rename(path)

    def test_symlink_in_prefix_ancestor_is_not_resolved_away(self):
        alias = self.root / "alias"
        alias.symlink_to(self.root, target_is_directory=True)
        self.assert_refused(alias / "install")

    def test_prefix_and_leaf_symlinks_are_rejected(self):
        alias = self.root / "alias"
        alias.symlink_to(self.prefix, target_is_directory=True)
        self.assert_refused(alias)
        leaf = self.files[-1]
        target = self.root / "outside-ca"
        leaf.rename(target)
        leaf.symlink_to(target)
        self.assert_refused()

    def test_prefix_symlink_with_trailing_slashes_is_rejected(self):
        alias = self.root / "alias"
        alias.symlink_to(self.prefix, target_is_directory=True)
        self.assert_refused(str(alias) + "///")

    def test_dangling_optional_directory_symlink_is_rejected(self):
        shutil.rmtree(self.prefix / "share")
        (self.prefix / "share").symlink_to(self.root / "missing", target_is_directory=True)
        result = self.run_uninstall()
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.rm_log.exists())
        self.assertTrue(self.files[0].exists())

    def test_untrusted_owners_and_writable_directories_are_rejected(self):
        for path in (self.root, self.prefix, self.prefix / "libexec/tiana"):
            with self.subTest(owner=str(path)):
                self.environment["UNINSTALL_TEST_UNTRUSTED_OWNER"] = str(path)
                self.assert_refused()
                del self.environment["UNINSTALL_TEST_UNTRUSTED_OWNER"]
            for mode in (0o775, 0o757):
                with self.subTest(path=str(path), mode=oct(mode)):
                    original = path.stat().st_mode & 0o777
                    path.chmod(mode)
                    try:
                        self.assert_refused()
                    finally:
                        path.chmod(original)

    def test_normal_uninstall_removes_only_known_files(self):
        retained = self.prefix / "bin/unrelated"
        retained.write_text("keep\n")
        result = self.run_uninstall()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue(self.rm_log.exists())
        self.assertTrue(all(not path.exists() for path in self.files))
        self.assertEqual(retained.read_text(), "keep\n")

    def test_absent_optional_directories_are_safe(self):
        shutil.rmtree(self.prefix / "share")
        shutil.rmtree(self.prefix / "libexec")
        result = self.run_uninstall()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertTrue(all(not path.exists() for path in self.files))


if __name__ == "__main__":
    unittest.main()
