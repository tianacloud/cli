#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, cpSync, mkdirSync, readFileSync, renameSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { targets, binaryName, archiveName } from '../packaging/npm/bin/platforms.mjs';

const [assetsArgument, outputArgument, versionArgument, ...extra] = process.argv.slice(2);
if (!assetsArgument || !outputArgument || extra.length) {
  console.error('Usage: node scripts/package-cli.mjs PLATFORM-ASSETS OUTPUT-DIRECTORY [VERSION]');
  process.exit(2);
}
const assets = path.resolve(assetsArgument);
const output = path.resolve(outputArgument);
const template = fileURLToPath(new URL('../packaging/npm', import.meta.url));
const pkg = JSON.parse(readFileSync(path.join(template, 'package.json'), 'utf8'));
if (versionArgument) pkg.version = versionArgument;
const sha = filename => createHash('sha256').update(readFileSync(filename)).digest('hex');
function architectureMatches(bytes, target) {
  const [os, arch] = target.split('-');
  if (bytes.length < 64) return false;
  if (os === 'linux') return bytes.subarray(0, 4).equals(Buffer.from([127, 69, 76, 70])) && bytes[4] === 2 && bytes[5] === 1 && bytes.readUInt16LE(18) === { amd64: 62, arm64: 183, riscv64: 243 }[arch];
  if (os === 'darwin') return bytes.readUInt32LE(0) === 0xfeedfacf && bytes.readUInt32LE(4) === { amd64: 0x01000007, arm64: 0x0100000c }[arch];
  const offset = bytes.readUInt32LE(60);
  return bytes.toString('ascii', 0, 2) === 'MZ' && offset + 6 <= bytes.length && bytes.readUInt32LE(offset) === 0x4550 && bytes.readUInt16LE(offset + 4) === { amd64: 0x8664, arm64: 0xaa64 }[arch];
}
for (const target of targets) {
  const directory = path.join(assets, target);
  const manifest = JSON.parse(readFileSync(path.join(directory, 'manifest.json'), 'utf8'));
  if (manifest.version !== pkg.version || `${manifest.platform}-${manifest.arch}` !== target || manifest.insecure_tls !== false) {
    throw new Error(`${target}: version, platform or TLS policy does not match this release`);
  }
  const name = binaryName(target);
  if (sha(path.join(directory, name)) !== manifest.files?.[name]) throw new Error(`${target}/${name}: checksum does not match its manifest`);
  if (!architectureMatches(readFileSync(path.join(directory, name)), target)) throw new Error(`${target}/${name}: wrong executable architecture`);
}
mkdirSync(output);
const staging = path.join(output, 'package');
cpSync(template, staging, { recursive: true });
const release = { version: pkg.version, assets: {} };
const date = new Date('1980-01-01T00:00:00Z');
for (const target of targets) {
  const directory = path.join(output, target);
  mkdirSync(directory);
  const name = binaryName(target);
  for (const file of [name, 'manifest.json']) {
    copyFileSync(path.join(assets, target, file), path.join(directory, file));
    utimesSync(path.join(directory, file), date, date);
  }
  chmodSync(path.join(directory, name), 0o755);
  const archive = archiveName(pkg.version, target);
  if (target.startsWith('windows-')) {
    execFileSync('zip', ['-X', '-q', path.join(output, archive), name, 'manifest.json'], { cwd: directory });
  } else {
    const tar = execFileSync('tar', ['-cf', '-', name, 'manifest.json'], { cwd: directory, maxBuffer: 256 * 1024 * 1024 });
    writeFileSync(path.join(output, archive), gzipSync(tar));
  }
  release.assets[target] = { filename: archive, sha256: sha(path.join(output, archive)), url: `https://gitee.com/tianacloud/cli-releases/releases/download/v${pkg.version}/${archive}` };
  rmSync(directory, { recursive: true });
}
writeFileSync(path.join(output, 'SHA256SUMS'), Object.values(release.assets).map(a => `${a.sha256}  ${a.filename}\n`).join(''));
writeFileSync(path.join(staging, 'release.json'), JSON.stringify(release, null, 2) + '\n');
writeFileSync(path.join(staging, 'package.json'), JSON.stringify(pkg, null, 2) + '\n');
for (const file of ['tiana.mjs', 'git-remote-tiana.mjs']) chmodSync(path.join(staging, 'bin', file), 0o755);
const packed = JSON.parse(execFileSync('npm', ['pack', '--ignore-scripts', '--json', '--pack-destination', output], { cwd: staging, encoding: 'utf8' }));
const name = `tiana-cli-${pkg.version}.tgz`;
renameSync(path.join(output, packed[0].filename), path.join(output, name));
writeFileSync(path.join(output, `${name}.sha256`), `${sha(path.join(output, name))}  ${name}\n`, { flag: 'wx' });
console.log(`Created ${path.join(output, name)} (${packed[0].size} bytes). No upload performed.`);
