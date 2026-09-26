#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [target, outputArgument, ...extra] = process.argv.slice(2);
if (!['darwin-arm64', 'linux-amd64'].includes(target) || !outputArgument || extra.length) {
  console.error('Usage: node scripts/build-platform.mjs darwin-arm64|linux-amd64 OUTPUT-DIRECTORY');
  process.exit(2);
}
if (process.env.TIANA_INSECURE_TLS && process.env.TIANA_INSECURE_TLS !== 'false') throw new Error('npm builds require verified TLS');
const repo = fileURLToPath(new URL('..', import.meta.url));
const bundle = path.join(path.resolve(outputArgument), target);
const [platform, arch] = target.split('-');
const { version } = JSON.parse(readFileSync(path.join(repo, 'packaging/npm/package.json'), 'utf8'));
if (!/^[0-9A-Za-z._+-]+$/.test(version)) throw new Error('Invalid release version');
mkdirSync(path.dirname(bundle), { recursive: true });
mkdirSync(bundle);
execFileSync('go', ['build', '-mod=readonly', '-buildvcs=false', '-trimpath', '-ldflags', `-s -w -X main.version=${version} -X github.com/tianacloud/cli/internal/buildconfig.InsecureTLS=false`, '-o', path.join(bundle, 'tiana'), './cmd/tiana'], {
  cwd: repo, stdio: 'inherit', env: { ...process.env, GOWORK: 'off', GOFLAGS: '', GOOS: platform, GOARCH: arch, CGO_ENABLED: '0' },
});
copyFileSync(path.join(repo, 'scripts/git-remote-tiana'), path.join(bundle, 'git-remote-tiana'));
for (const name of ['tiana', 'git-remote-tiana']) chmodSync(path.join(bundle, name), 0o755);
const files = Object.fromEntries(['tiana', 'git-remote-tiana'].map(name => [name, createHash('sha256').update(readFileSync(path.join(bundle, name))).digest('hex')]));
const revision = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
const dirty = execFileSync('git', ['status', '--porcelain'], { cwd: repo, encoding: 'utf8' }).length > 0;
writeFileSync(path.join(bundle, 'manifest.json'), JSON.stringify({ version, platform, arch, cli: { revision, dirty }, runtime: 'native-go', insecure_tls: false, files }, null, 2) + '\n', { flag: 'wx' });
writeFileSync(path.join(bundle, 'SHA256SUMS'), Object.entries(files).map(([name, hash]) => `${hash}  ${name}\n`).join(''), { flag: 'wx' });
console.log(`Built ${bundle}; no publication performed.`);
