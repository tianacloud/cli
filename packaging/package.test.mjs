import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { targets, binaryName, archiveName } from './npm/bin/platforms.mjs';

const repo = fileURLToPath(new URL('..', import.meta.url));
const packager = path.join(repo, 'scripts/package-cli.mjs');
const version = '9.8.7-test.1';
const digest = filename => createHash('sha256').update(readFileSync(filename)).digest('hex');

test('seven-platform archives and a small npm downloader package', { timeout: 180000, skip: process.platform === 'win32' }, async t => {
  const root = mkdtempSync(path.join(tmpdir(), 'tiana-package-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const assets = path.join(root, 'assets');
  for (const target of targets) {
    const [os, arch] = target.split('-');
    const directory = path.join(assets, target);
    mkdirSync(directory, { recursive: true });
    const name = binaryName(target);
    execFileSync('go', ['build', '-o', path.join(directory, name), './packaging/testdata/native.go'], {
      cwd: repo, env: { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: '0', GOWORK: 'off' },
    });
    const files = { [name]: digest(path.join(directory, name)) };
    writeFileSync(path.join(directory, 'manifest.json'), JSON.stringify({ version, platform: os, arch, insecure_tls: false, files }));
  }
  await t.test('archives all seven binaries and pins their URLs and hashes', () => {
    const output = path.join(root, 'packed');
    execFileSync(process.execPath, [packager, assets, output, version]);
    const tarball = path.join(output, `tiana-cli-${version}.tgz`);
    const contents = execFileSync('tar', ['-tzf', tarball], { encoding: 'utf8' });
    assert.ok(contents.includes('package/bin/install.mjs'));
    assert.ok(contents.includes('package/release.json'));
    const pkg = JSON.parse(execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' }));
    assert.equal(pkg.scripts.postinstall, 'node bin/install.mjs');
    assert.equal(pkg.name, '@tianacloud/cli');
    assert.equal(pkg.version, version);
    assert.equal(pkg.bin['git-remote-tiana'], 'bin/git-remote-tiana.mjs');
    const release = JSON.parse(readFileSync(path.join(output, 'package/release.json')));
    assert.deepEqual(Object.keys(release.assets), targets);
    for (const target of targets) {
      const name = archiveName(version, target);
      assert.equal(release.assets[target].sha256, digest(path.join(output, name)));
      assert.equal(release.assets[target].url, `https://gitee.com/tianacloud/cli-releases/releases/download/v${version}/${name}`);
      const archiveFiles = execFileSync(target.startsWith('windows-') ? 'unzip' : 'tar', target.startsWith('windows-') ? ['-Z1', path.join(output, name)] : ['-tzf', path.join(output, name)], { encoding: 'utf8' });
      assert.ok(archiveFiles.split('\n').includes(binaryName(target)));
    }
    assert.equal(readFileSync(`${tarball}.sha256`, 'utf8'), `${digest(tarball)}  tiana-cli-${version}.tgz\n`);
    assert.ok(readFileSync(tarball).length < 100000, 'npm package only contains downloader and metadata');
  });
  for (const kind of ['missing-platform', 'wrong-version', 'tampered-cli', 'wrong-architecture']) {
    await t.test(`detects ${kind}`, () => {
      const altered = path.join(root, kind);
      cpSync(assets, altered, { recursive: true });
      const directory = path.join(altered, 'windows-arm64');
      const manifestPath = path.join(directory, 'manifest.json');
      const manifest = JSON.parse(readFileSync(manifestPath));
      if (kind === 'missing-platform') rmSync(directory, { recursive: true });
      if (kind === 'wrong-version') {
        manifest.version = '0.1.0';
        writeFileSync(manifestPath, JSON.stringify(manifest));
      }
      if (kind === 'tampered-cli') writeFileSync(path.join(directory, 'tiana.exe'), 'incomplete');
      if (kind === 'wrong-architecture') {
        copyFileSync(path.join(altered, 'linux-amd64/tiana'), path.join(directory, 'tiana.exe'));
        manifest.files['tiana.exe'] = digest(path.join(directory, 'tiana.exe'));
        writeFileSync(manifestPath, JSON.stringify(manifest));
      }
      const output = spawnSync(process.execPath, [packager, altered, path.join(root, `${kind}-output`), version], { encoding: 'utf8' });
      assert.notEqual(output.status, 0);
      assert.match(output.stderr, {
        'missing-platform': /ENOENT/,
        'wrong-version': /does not match this release/,
        'tampered-cli': /checksum does not match/,
        'wrong-architecture': /wrong executable architecture/,
      }[kind]);
    });
  }
});
