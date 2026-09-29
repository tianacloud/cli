import assert from 'node:assert/strict';
import { execFileSync, execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { cp } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { binaryName, platformDirectory } from './npm/bin/platforms.mjs';
import { createRequire } from 'node:module';
import { extractWindowsBinary } from './npm/bin/extract-zip.mjs';
const { zipSync } = createRequire(new URL('./npm/package.json', import.meta.url))('fflate');
const run = promisify(execFile);
const repo = fileURLToPath(new URL('..', import.meta.url));

test('download, checksum, install and invoke a native CLI', { timeout: 60000 }, async t => {
  const root = mkdtempSync(path.join(tmpdir(), 'tiana install 中文-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const target = platformDirectory(process.platform, process.arch);
  const name = binaryName(target);
  const native = path.join(root, name);
  execFileSync('go', ['build', '-o', native, './packaging/testdata/native.go'], { cwd: repo });
  let archive;
  if (process.platform === 'win32') {
    archive = Buffer.from(zipSync({ [name]: readFileSync(native) }));
  } else archive = execFileSync('tar', ['-czf', '-', name], { cwd: root, maxBuffer: 32 * 1024 * 1024 });
  let status = 200;
  const server = createServer((req, res) => { res.writeHead(status); res.end(archive); });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const url = `http://127.0.0.1:${server.address().port}/native.tar.gz`;
  const sha256 = createHash('sha256').update(archive).digest('hex');
  for (const kind of ['success', 'bad-checksum', 'not-found']) {
    await t.test(kind, async () => {
      const destination = path.join(root, kind);
      await cp(path.join(repo, 'packaging/npm'), destination, { recursive: true });
      const { version } = JSON.parse(readFileSync(path.join(destination, 'package.json'), 'utf8'));
      writeFileSync(path.join(destination, 'release.json'), JSON.stringify({ version, assets: { [target]: { filename: 'native.tar.gz', url, sha256: kind === 'bad-checksum' ? '0'.repeat(64) : sha256 } } }));
      status = kind === 'not-found' ? 404 : 200;
      const install = run(process.execPath, [path.join(destination, 'bin/install.mjs')]);
      if (kind === 'success') {
        await install;
        const { stdout } = await run(process.execPath, [path.join(destination, 'bin/tiana.mjs')]);
        assert.match(stdout, /packaging fixture/);
      } else {
        await assert.rejects(install, e => { assert.match(e.stderr, kind === 'bad-checksum' ? /checksum/i : /404/); return true; });
        assert.equal(existsSync(path.join(destination, 'native', name)), false);
      }
    });
  }
});


test('extract the Windows executable from a release ZIP', async t => {
  const root = mkdtempSync(path.join(tmpdir(), 'tiana zip 中文-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const binary = Buffer.from('fixture executable bytes');
  extractWindowsBinary(zipSync({ 'tiana.exe': binary, 'manifest.json': Buffer.from('{}') }), root);
  assert.deepEqual(readFileSync(path.join(root, 'tiana.exe')), binary);
  assert.equal(existsSync(path.join(root, 'manifest.json')), false);
  await t.test('reject malformed ZIP and missing executable without replacing the installed bytes', () => {
    assert.throws(() => extractWindowsBinary(Buffer.from('not a ZIP'), root));
    assert.throws(() => extractWindowsBinary(zipSync({ 'manifest.json': Buffer.from('{}') }), root), /missing tiana.exe/);
    assert.deepEqual(readFileSync(path.join(root, 'tiana.exe')), binary);
  });
});


test('Windows download and ZIP installation on every test host', { timeout: 60000 }, async t => {
  const root = mkdtempSync(path.join(tmpdir(), 'tiana windows install-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const preload = path.join(root, 'windows.mjs');
  writeFileSync(preload, "Object.defineProperty(process, 'platform', { value: 'win32' }); Object.defineProperty(process, 'arch', { value: 'x64' });");
  const binary = Buffer.from('Windows executable fixture');
  let archive;
  const server = createServer((_req, res) => res.end(archive));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  for (const kind of ['success', 'bad-checksum', 'malformed-zip', 'missing-executable']) {
    await t.test(kind, async () => {
      const destination = path.join(root, kind);
      await cp(path.join(repo, 'packaging/npm'), destination, { recursive: true });
      archive = kind === 'malformed-zip' ? Buffer.from('not a ZIP') : Buffer.from(zipSync(
        kind === 'missing-executable' ? { 'manifest.json': Buffer.from('{}') } : { 'tiana.exe': binary }
      ));
      const { version } = JSON.parse(readFileSync(path.join(destination, 'package.json'), 'utf8'));
      writeFileSync(path.join(destination, 'release.json'), JSON.stringify({ version, assets: { 'windows-amd64': {
        url: `http://127.0.0.1:${server.address().port}/native.zip`,
        sha256: kind === 'bad-checksum' ? '0'.repeat(64) : createHash('sha256').update(archive).digest('hex'),
      } } }));
      const install = run(process.execPath, ['--import', pathToFileURL(preload).href, path.join(destination, 'bin/install.mjs')]);
      if (kind === 'success') {
        await install;
        assert.deepEqual(readFileSync(path.join(destination, 'native/tiana.exe')), binary);
      } else {
        await assert.rejects(install, error => {
          assert.match(error.stderr, kind === 'bad-checksum' ? /checksum/ : kind === 'missing-executable' ? /missing tiana.exe/ : /invalid zip/i);
          return true;
        });
        assert.equal(existsSync(path.join(destination, 'native/tiana.exe')), false);
      }
      assert.ok(!readdirSync(destination).some(name => name.startsWith('.install-')));
    });
  }
});
