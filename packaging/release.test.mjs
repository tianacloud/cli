import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

const checksum = b => createHash('sha256').update(b).digest('hex');
test('npm token configuration is selected without storing the token', async () => {
  const { npmEnvironment } = await import('../scripts/publish-cli.mjs');
  const login = { PATH: process.env.PATH, NPM_CONFIG_USERCONFIG: '/existing/npmrc' };
  assert.equal(npmEnvironment(login), login);
  const env = npmEnvironment({ ...login, NPM_TOKEN: 'synthetic-publish-token' });
  assert.equal(readFileSync(env.NPM_CONFIG_USERCONFIG, 'utf8'), '//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n');
  assert.equal(env.NPM_TOKEN, 'synthetic-publish-token');
  assert.equal(login.NPM_CONFIG_USERCONFIG, '/existing/npmrc');
  if (process.platform !== 'win32') {
    const selected = execFileSync('npm', ['config', 'get', 'userconfig'], { env, encoding: 'utf8' }).trim();
    assert.equal(selected, env.NPM_CONFIG_USERCONFIG);
  }
});

test('Gitee upload resumes and verifies anonymous bytes before completion', async t => {
  const { uploadRelease } = await import('../scripts/publish-cli.mjs');
  const directory = mkdtempSync(path.join(tmpdir(), 'tiana-publish-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  mkdirSync(path.join(directory, 'package'));
  let created = false;
  let uploads = 0;
  let publicStatus = 200;
  let existingBytes = Buffer.from('binary');
  const attachments = [];
  const server = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/binary' || req.url === '/SHA256SUMS') {
      assert.equal(req.headers.authorization, undefined, 'downloads must work without publisher token');
      res.writeHead(publicStatus); res.end(req.url === '/binary' ? existingBytes : readFileSync(path.join(directory, 'SHA256SUMS'))); return;
    }
    assert.equal(req.headers.authorization, 'Bearer fixture-token');
    if (req.url === '/repos/tianacloud/cli-releases') { res.end(JSON.stringify({ public: true, default_branch: 'master' })); return; }
    if (req.url.endsWith('/releases/tags/v1.2.3')) { res.writeHead(created ? 200 : 404); res.end(JSON.stringify({ id: 42 })); return; }
    if (req.url.endsWith('/releases') && req.method === 'POST') { created = true; res.end(JSON.stringify({ id: 42 })); return; }
    if (req.url.includes('/attach_files')) {
      if (req.method === 'POST') {
        let body = ''; for await (const part of req) body += part.toString();
        const name = /filename="([^"]+)"/.exec(body)[1];
        attachments.push({ name }); uploads++; res.end(JSON.stringify({ name }));
      } else res.end(JSON.stringify(attachments));
      return;
    }
    res.writeHead(404); res.end('{}');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  const base = `http://127.0.0.1:${server.address().port}`;
  const filename = 'native.tar.gz';
  writeFileSync(path.join(directory, filename), existingBytes);
  writeFileSync(path.join(directory, 'SHA256SUMS'), `${checksum(existingBytes)}  ${filename}\n`);
  writeFileSync(path.join(directory, 'package/release.json'), JSON.stringify({ version: '1.2.3', assets: { 'linux-amd64': { filename, url: base + '/binary', sha256: checksum(existingBytes) } } }));
  const options = { directory, version: '1.2.3', token: 'fixture-token', apiBase: base };
  await uploadRelease(options);
  assert.equal(uploads, 2);
  await uploadRelease(options);
  assert.equal(uploads, 2, 'a rerun must not upload duplicate attachments');
  existingBytes = Buffer.from('different');
  await assert.rejects(uploadRelease(options), /checksum/);
  assert.equal(uploads, 2);
  publicStatus = 403;
  await assert.rejects(uploadRelease(options), /403/);
});

test('npm publication checks integrity and recovers an uncertain successful upload', { skip: process.platform === 'win32' }, async t => {
  const { publishNpm } = await import('../scripts/publish-cli.mjs');
  const directory = mkdtempSync(path.join(tmpdir(), 'tiana-npm-publish-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const tarball = Buffer.from('fixture npm tarball');
  writeFileSync(path.join(directory, 'tiana-cli-1.2.3.tgz'), tarball);
  const integrity = 'sha512-' + createHash('sha512').update(tarball).digest('base64');
  const stateFile = path.join(directory, 'state.json');
  const commandFile = path.join(directory, 'npm');
  writeFileSync(commandFile, `#!${process.execPath}
const fs = require('fs');
const p = process.env.TIANA_NPM_TEST_STATE;
const s = JSON.parse(fs.readFileSync(p));
const cmd = process.argv[2];
if (cmd === 'view') {
  if (process.argv[4] === 'dist-tags') console.log(JSON.stringify(s.tags || {}));
  else if (s.integrity) console.log(JSON.stringify(s.integrity));
  else { console.log(JSON.stringify({error:{code:'E404'}})); process.exitCode = 1; }
} else if (cmd === 'publish') {
  s.publish++;
  if (!s.fail) { s.integrity = s.expected; s.tags = { latest: '1.2.3' }; }
  fs.writeFileSync(p, JSON.stringify(s));
  if (s.uncertain || s.fail) process.exitCode = 1;
} else if (cmd === 'dist-tag') {
  s.tag++; fs.writeFileSync(p, JSON.stringify(s));
}
`, { mode: 0o755 });
  const oldPath = process.env.PATH;
  const oldOIDCURL = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const oldOIDCToken = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  delete process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  delete process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  t.after(() => {
    for (const [name, value] of [['ACTIONS_ID_TOKEN_REQUEST_URL', oldOIDCURL], ['ACTIONS_ID_TOKEN_REQUEST_TOKEN', oldOIDCToken]]) {
      if (value === undefined) delete process.env[name]; else process.env[name] = value;
    }
  });
  process.env.PATH = directory + path.delimiter + oldPath;
  process.env.TIANA_NPM_TEST_STATE = stateFile;
  t.after(() => { process.env.PATH = oldPath; delete process.env.TIANA_NPM_TEST_STATE; });
  for (const kind of ['success', 'uncertain', 'already-published', 'conflict', 'failure']) {
    await t.test(kind, () => {
      const state = { publish: 0, tag: 0, expected: integrity, uncertain: kind === 'uncertain', fail: kind === 'failure' };
      if (kind === 'already-published') state.integrity = integrity;
      if (kind === 'conflict') state.integrity = 'different';
      writeFileSync(stateFile, JSON.stringify(state));
      if (kind === 'conflict' || kind === 'failure') assert.throws(() => publishNpm(directory, '1.2.3', 'latest'), /different artifacts|did not complete/);
      else publishNpm(directory, '1.2.3', 'latest');
      const result = JSON.parse(readFileSync(stateFile));
      assert.equal(result.publish, kind === 'conflict' || kind === 'already-published' ? 0 : 1);
      assert.equal(result.tag, kind === 'already-published' ? 1 : 0);
    });
  }
  await t.test('OIDC publishes without a separate authenticated tag update', () => {
    process.env.ACTIONS_ID_TOKEN_REQUEST_URL = 'https://oidc.example.test/token';
    process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN = 'synthetic-oidc-token';
    writeFileSync(stateFile, JSON.stringify({ publish: 0, tag: 0, expected: integrity }));
    publishNpm(directory, '1.2.3', 'latest');
    publishNpm(directory, '1.2.3', 'latest');
    const state = JSON.parse(readFileSync(stateFile));
    assert.equal(state.publish, 1);
    assert.equal(state.tag, 0);
    state.tags = { latest: '1.2.4' };
    writeFileSync(stateFile, JSON.stringify(state));
    assert.throws(() => publishNpm(directory, '1.2.3', 'latest'), /OIDC cannot change existing dist-tags/);
    assert.equal(JSON.parse(readFileSync(stateFile)).tag, 0);
  });

});
