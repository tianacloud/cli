import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { cp, mkdir, mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { platformDirectory } from './npm/bin/launcher.mjs';
import { fileURLToPath } from 'node:url';
import { binaryName, platformPackage } from './npm/bin/platforms.mjs';
let fixtureBinary;
test.before(async () => {
  fixtureBinary = await mkdtemp(path.join(tmpdir(), 'tiana-native-fixture-'));
  execFileSync('go', ['build', '-o', path.join(fixtureBinary, 'fixture.exe'), './packaging/testdata/launcher.go'], { cwd: fileURLToPath(new URL('..', import.meta.url)) });
});
test.after(async () => { if (fixtureBinary) await rm(fixtureBinary, { recursive: true, force: true }); });

async function fixture(t, body) {
  const directory = await mkdtemp(path.join(tmpdir(), 'tiana launcher 中文-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await cp(new URL('./npm/bin', import.meta.url), path.join(directory, 'bin'), { recursive: true });
  const platform = path.join(directory, 'node_modules',platformPackage(process.platform,process.arch),'bin');
  await mkdir(platform, { recursive: true });
  await writeFile(path.join(platform,'../package.json'),JSON.stringify({name:platformPackage(process.platform,process.arch),version:'1.0.0'}));
  if (body) {
    // A fixture executable, not a distributable native CLI.
    await cp(path.join(fixtureBinary, 'fixture.exe'), path.join(platform, binaryName(platformDirectory(process.platform, process.arch))));
    await writeFile(path.join(platform, 'fixture.mode'), body);
  }
  return directory;
}

function launch(directory, args = []) {
  return spawn(process.execPath, [path.join(directory, 'bin/tiana.mjs'), ...args], {
    cwd: directory, stdio: ['pipe', 'pipe', 'pipe'],
  });
}

async function result(child, input = '') {
  let stdout = '', stderr = '';
  child.stdout.setEncoding('utf8').on('data', data => { stdout += data; });
  child.stderr.setEncoding('utf8').on('data', data => { stderr += data; });
  child.stdin.end(input);
  const [code, signal] = await once(child, 'close');
  return { code, signal, stdout, stderr };
}

test('selects each packaged platform', () => {
  assert.equal(platformDirectory('darwin', 'arm64'), 'darwin-arm64');
  assert.equal(platformDirectory('linux', 'x64'), 'linux-amd64');
  assert.equal(platformDirectory('darwin', 'x64'), 'darwin-amd64');
  assert.equal(platformDirectory('linux', 'arm64'), 'linux-arm64');
  assert.equal(platformDirectory('linux', 'riscv64'), 'linux-riscv64');
  assert.equal(platformDirectory('win32', 'x64'), 'windows-amd64');
  assert.equal(platformDirectory('win32', 'arm64'), 'windows-arm64');
});

test('passes argv, stdin, cwd, stdout, stderr and exit code without a shell', async t => {
  const directory = await fixture(t, 'echo');
  const args = ['sql', 'execute', '--input-json', '-', '$NOT_EXPANDED', "quote' and spaces", '$(not-a-command)'];
  const input = '{"sql":"SELECT ?","params":[{"type":"text","value":"中文\\nquote\\\""}]}\n';
  const output = await result(launch(directory, args), input);
  assert.equal(output.code, 4);
  assert.equal(output.signal, null);
  const actual = JSON.parse(output.stdout);
  actual.cwd = await realpath(actual.cwd);
  assert.deepEqual(actual, { args, input, cwd: await realpath(directory) });
  assert.equal(output.stderr, 'fixture diagnostic\n');
});

test('reports an incomplete installation', async t => {
  const directory = await fixture(t);
  const output = await result(launch(directory));
  assert.equal(output.code, 1);
  assert.equal(output.stdout, '');
  assert.match(output.stderr, /include=optional/);
});

test('Git npm entry point uses its own CLI and preserves remote arguments', async t => {
  const directory = await fixture(t, 'args');
  const args = ['origin with spaces', 'tiana://ep-00000000000000000000000000.example.test:9443/repo.git'];
  const child = spawn(process.execPath, [path.join(directory, 'bin/git-remote-tiana.mjs'), ...args], {cwd: directory, stdio: ['pipe','pipe','pipe']});
  const output = await result(child);
  assert.equal(output.code, 0);
  assert.deepEqual(JSON.parse(output.stdout), ['git','remote-helper',...args]);
});

for (const signal of ['SIGINT', 'SIGTERM']) {
  test(`forwards ${signal} and preserves the CLI recovery exit code`, { timeout: 5000, skip: process.platform === 'win32' }, async t => {
    const directory = await fixture(t, 'wait');
    const child = launch(directory);
    t.after(() => { if (child.exitCode === null) child.kill('SIGKILL'); });
    const completed = result(child);
    await once(child.stdout, 'data');
    child.kill(signal);
    const output = await completed;
    assert.equal(output.code, 4);
    assert.equal(output.stdout, 'ready\ninterrupted\n');
  });
}

test('maps a native signal exit to a conventional shell exit code', { timeout: 5000, skip: process.platform === 'win32' }, async t => {
  const directory = await fixture(t, 'signal');
  const output = await result(launch(directory));
  assert.equal(output.code, 143);
});
