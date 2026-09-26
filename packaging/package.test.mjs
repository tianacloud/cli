import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { copyFileSync, cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const repo = fileURLToPath(new URL('..', import.meta.url));
const packager = path.join(repo, 'scripts/package-cli.mjs');
const { version } = JSON.parse(readFileSync(path.join(repo, 'packaging/npm/package.json'), 'utf8'));
const digest = filename => createHash('sha256').update(readFileSync(filename)).digest('hex');
const platforms = ['darwin-arm64', 'linux-amd64'];

test('two-platform packaging with explicitly synthetic native fixtures', { timeout: 120000 }, async t => {
  const root = mkdtempSync(path.join(tmpdir(), 'tiana-package-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const assets = path.join(root, 'assets');
  for (const platform of platforms) {
    const [os, arch] = platform.split('-');
    const directory = path.join(assets, platform);
    mkdirSync(directory, { recursive: true });
    execFileSync('go', ['build', '-o', path.join(directory, 'tiana'), './packaging/testdata/native.go'], {
      cwd: repo, env: { ...process.env, GOOS: os, GOARCH: arch, CGO_ENABLED: '0' },
    });
    copyFileSync(path.join(repo, 'scripts/git-remote-tiana'), path.join(directory, 'git-remote-tiana'));
    const files = Object.fromEntries(['tiana', 'git-remote-tiana'].map(name => [name, digest(path.join(directory, name))]));
    writeFileSync(path.join(directory, 'manifest.json'), JSON.stringify({ version, platform: os, arch, insecure_tls: false, files }));
    writeFileSync(path.join(directory, 'SHA256SUMS'), Object.entries(files).map(([name, hash]) => `${hash}  ${name}\n`).join(''));
  }
  await t.test('packs every asset and installs offline with a new cache', () => {
    const output = path.join(root, 'packed');
    execFileSync(process.execPath, [packager, assets, output]);
    const tarball = path.join(output, `tiana-cli-${version}.tgz`);
    const contents = execFileSync('tar', ['-tzf', tarball], { encoding: 'utf8' });
    for (const platform of platforms) {
      for (const name of ['tiana', 'git-remote-tiana', 'manifest.json', 'SHA256SUMS']) {
        assert.ok(contents.split('\n').includes(`package/platforms/${platform}/${name}`));
      }
    }
    const pkg = JSON.parse(execFileSync('tar', ['-xOzf', tarball, 'package/package.json'], { encoding: 'utf8' }));
    assert.equal(pkg.scripts, undefined);
    assert.equal(pkg.dependencies, undefined);
    assert.equal(pkg.optionalDependencies, undefined);
    assert.equal(pkg.version, version);
    assert.equal(pkg.bin['git-remote-tiana'], 'bin/git-remote-tiana.mjs');
    const prefix = path.join(root, 'install');
    execFileSync('npm', ['install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund', '--cache', path.join(root, 'empty-cache'), '--prefix', prefix, tarball]);
    const outputText = execFileSync(path.join(prefix, 'node_modules/.bin/tiana'), [], { encoding: 'utf8' });
    assert.equal(outputText, `packaging fixture ${process.platform}/${process.arch === 'x64' ? 'amd64' : process.arch}\n`);
    const gitOutput = execFileSync(path.join(prefix, 'node_modules/.bin/git-remote-tiana'), ['origin', 'tiana://example/repo.git'], { encoding: 'utf8' });
    assert.equal(gitOutput, outputText);
    assert.equal(readFileSync(`${tarball}.sha256`, 'utf8'), `${digest(tarball)}  tiana-cli-${version}.tgz\n`);
  });
  for (const kind of ['missing-platform', 'wrong-version', 'tampered-cli', 'wrong-architecture']) {
    await t.test(`detects ${kind}`, () => {
      const altered = path.join(root, kind);
      cpSync(assets, altered, { recursive: true });
      const directory = path.join(altered, 'darwin-arm64');
      const manifestPath = path.join(directory, 'manifest.json');
      const manifest = JSON.parse(readFileSync(manifestPath));
      if (kind === 'missing-platform') rmSync(directory, { recursive: true });
      if (kind === 'wrong-version') {
        manifest.version = '0.1.0';
        writeFileSync(manifestPath, JSON.stringify(manifest));
      }
      if (kind === 'tampered-cli') writeFileSync(path.join(directory, 'tiana'), 'incomplete');
      if (kind === 'wrong-architecture') {
        copyFileSync(path.join(altered, 'linux-amd64/tiana'), path.join(directory, 'tiana'));
        manifest.files.tiana = digest(path.join(directory, 'tiana'));
        writeFileSync(manifestPath, JSON.stringify(manifest));
      }
      const output = spawnSync(process.execPath, [packager, altered, path.join(root, `${kind}-output`)], { encoding: 'utf8' });
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
