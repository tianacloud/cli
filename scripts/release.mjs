#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { targets } from '../packaging/npm/bin/platforms.mjs';
import { uploadRelease, publishNpm, npmEnvironment, usesTrustedPublishing } from './publish-cli.mjs';

async function main() {
  const { values, positionals } = parseArgs({ allowPositionals: true, options: {
    'dry-run': { type: 'boolean', default: false },
    'upload-only': { type: 'boolean', default: false },
    tag: { type: 'string', default: 'latest' },
    'gitee-token-file': { type: 'string', default: path.join(homedir(), 'tmp/gitee_token.txt') },
    help: { type: 'boolean', default: false },
  } });
  if (values.help) { console.log('Usage: ./scripts/release.mjs [VERSION] [--dry-run] [--upload-only] [--tag latest|next] [--gitee-token-file PATH]\nVERSION defaults to packaging/npm/package.json.'); return; }
  const repo = fileURLToPath(new URL('..', import.meta.url));
  const version = positionals[0] ?? JSON.parse(readFileSync(path.join(repo, 'packaging/npm/package.json'), 'utf8')).version;
  if (positionals.length > 1 || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(version)) throw new Error('Provide a semantic version, e.g. 0.2.2-beta.0.');
  let token;
  if (!values['dry-run']) {
    token = readFileSync(values['gitee-token-file'], 'utf8').trim();
    if (!token) throw new Error('The Gitee token file is empty.');
    if (!values['upload-only'] && !usesTrustedPublishing()) {
      try { execFileSync('npm', ['whoami', '--registry=https://registry.npmjs.org/'], { env: npmEnvironment(), stdio: 'pipe' }); }
      catch { throw new Error('npm authentication is unavailable. Set NPM_TOKEN or run npm login --registry=https://registry.npmjs.org/ before publishing.'); }
    }
  }
  const parent = path.join(repo, 'dist/releases');
  mkdirSync(parent, { recursive: true });
  const directory = mkdtempSync(path.join(parent, `${version}-`));
  console.log(`Release output: ${directory}`);
  for (const target of targets) execFileSync(process.execPath, [path.join(repo, 'scripts/build-platform.mjs'), target, path.join(directory, 'assets'), version], { cwd: repo, stdio: 'inherit' });
  const packed = path.join(directory, 'packed');
  execFileSync(process.execPath, [path.join(repo, 'scripts/package-cli.mjs'), path.join(directory, 'assets'), packed, version], { cwd: repo, stdio: 'inherit' });
  if (values['dry-run']) { console.log(`Dry run complete: ${packed}. No upload performed.`); return; }
  await uploadRelease({ directory: packed, version, token });
  if (values['upload-only']) {
    const checksum = readFileSync(path.join(packed, `tiana-cli-${version}.tgz.sha256`), 'utf8').split(/\s+/)[0];
    console.log(`Gitee upload complete. Run release.yml with version=${version}, tag=${values.tag}, npm_sha256=${checksum}.`);
    return;
  }
  publishNpm(packed, version, values.tag);
  console.log(`Published @tianacloud/cli@${version} to ${values.tag}.`);
}
main().catch(error => { console.error(`Release failed: ${error.message}`); process.exitCode = 1; });
