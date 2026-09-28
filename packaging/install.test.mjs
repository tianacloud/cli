import assert from 'node:assert/strict';
import { execFileSync, execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { cp } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { binaryName, platformDirectory } from './npm/bin/platforms.mjs';
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
    const zip = path.join(root, 'fixture.zip');
    execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', 'Compress-Archive -LiteralPath $env:TIANA_FIXTURE_BINARY -DestinationPath $env:TIANA_FIXTURE_ZIP'], { env: { ...process.env, TIANA_FIXTURE_BINARY: native, TIANA_FIXTURE_ZIP: zip } });
    archive = readFileSync(zip);
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
