#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [assetsArgument, outputArgument, ...extra] = process.argv.slice(2);
if (!assetsArgument || !outputArgument || extra.length) {
  console.error('Usage: node scripts/package-cli.mjs PLATFORM-ASSETS OUTPUT-DIRECTORY');
  process.exit(2);
}
const assets = path.resolve(assetsArgument);
const output = path.resolve(outputArgument);
const template = fileURLToPath(new URL('../packaging/npm', import.meta.url));
const pkg = JSON.parse(readFileSync(path.join(template, 'package.json'), 'utf8'));
const platforms = ['darwin-arm64', 'linux-amd64'];
const files = ['tiana', 'git-remote-tiana', 'manifest.json', 'SHA256SUMS'];
const sha = filename => createHash('sha256').update(readFileSync(filename)).digest('hex');

for (const platform of platforms) {
  const directory = path.join(assets, platform);
  const manifest = JSON.parse(readFileSync(path.join(directory, 'manifest.json'), 'utf8'));
  if (manifest.version !== pkg.version || `${manifest.platform}-${manifest.arch}` !== platform || manifest.insecure_tls !== false) {
    throw new Error(`${platform}: version, platform or TLS policy does not match this release`);
  }
  for (const filename of ['tiana', 'git-remote-tiana']) {
    if (sha(path.join(directory, filename)) !== manifest.files?.[filename]) {
      throw new Error(`${platform}/${filename}: checksum does not match its manifest`);
    }
  }
  for (const filename of ['tiana']) {
    const type = execFileSync('file', ['-b', path.join(directory, filename)], { encoding: 'utf8' });
    const match = {
      'linux-amd64': /ELF 64-bit.*x86-64/,
      'darwin-arm64': /Mach-O 64-bit.*arm64/,
    }[platform];
    if (!match.test(type)) throw new Error(`${platform}/${filename}: wrong executable architecture`);
  }
}

mkdirSync(output);
const staging = path.join(output, 'package');
cpSync(template, staging, { recursive: true });
chmodSync(path.join(staging, 'bin/tiana.mjs'), 0o755);
chmodSync(path.join(staging, 'bin/git-remote-tiana.mjs'), 0o755);
for (const platform of platforms) {
  const directory = path.join(staging, 'platforms', platform);
  mkdirSync(directory, { recursive: true });
  for (const filename of files) copyFileSync(path.join(assets, platform, filename), path.join(directory, filename));
  for (const filename of ['tiana', 'git-remote-tiana']) chmodSync(path.join(directory, filename), 0o755);
}
const packed = JSON.parse(execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', output], {
  cwd: staging, encoding: 'utf8',
}));
const name = `tiana-cli-${pkg.version}.tgz`;
renameSync(path.join(output, packed[0].filename), path.join(output, name));
writeFileSync(path.join(output, `${name}.sha256`), `${sha(path.join(output, name))}  ${name}\n`, { flag: 'wx' });
console.log(`Created ${path.join(output, name)} (${packed[0].size} bytes). No upload or publication performed.`);
