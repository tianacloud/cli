#!/usr/bin/env node
import {runNPM} from './npm-command.mjs';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { targets, binaryName, platformPackage } from '../packaging/npm/bin/platforms.mjs';

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
const release = { version: pkg.version, packages: [] };
function pack(directory, target) {
  const result = JSON.parse(runNPM( ['pack', '--ignore-scripts', '--json', '--pack-destination', output], { cwd: directory, encoding: 'utf8' }))[0];
  release.packages.push({ name: result.name, version: result.version, filename: result.filename, integrity: result.integrity, size: result.size, ...(target ? { target } : {}) });
}
for (const target of targets) {
  const [os, arch] = target.split('-');
  const npmOS = os === 'windows' ? 'win32' : os;
  const npmArch = arch === 'amd64' ? 'x64' : arch;
  const name = platformPackage(npmOS, npmArch);
  const directory = path.join(output, target);
  mkdirSync(path.join(directory, 'bin'), { recursive: true });
  const binary = binaryName(target);
  copyFileSync(path.join(assets, target, binary), path.join(directory, 'bin', binary));
  chmodSync(path.join(directory, 'bin', binary), 0o755);
  copyFileSync(path.join(assets, target, 'manifest.json'), path.join(directory, 'manifest.json'));
  copyFileSync(path.join(template, 'README.md'), path.join(directory, 'README.md'));
  if (existsSync(path.join(template, 'LICENSE'))) copyFileSync(path.join(template, 'LICENSE'), path.join(directory, 'LICENSE'));
  const metadata = { name, version: pkg.version, description: `Tiana CLI native executable for ${npmOS}/${npmArch}`, repository: pkg.repository, os: [npmOS], cpu: [npmArch], files: ['bin', 'manifest.json', 'README.md', 'LICENSE'], publishConfig: pkg.publishConfig };
  writeFileSync(path.join(directory, 'package.json'), JSON.stringify(metadata, null, 2)+'\n');
  pack(directory, target);
}
const staging = path.join(output, 'package');
mkdirSync(staging);
cpSync(path.join(template, 'bin'), path.join(staging, 'bin'), { recursive: true });
copyFileSync(path.join(template, 'README.md'), path.join(staging, 'README.md'));
pkg.optionalDependencies = Object.fromEntries(targets.map(target => {
  const [os,arch] = target.split('-');
  return [platformPackage(os==='windows'?'win32':os,arch==='amd64'?'x64':arch),pkg.version];
}));
writeFileSync(path.join(staging, 'package.json'), JSON.stringify(pkg, null, 2)+'\n');
for (const file of ['tiana.mjs', 'git-remote-tiana.mjs']) chmodSync(path.join(staging, 'bin', file), 0o755);
pack(staging);
writeFileSync(path.join(output, 'npm-release.json'), JSON.stringify(release, null, 2)+'\n');
for (const entry of release.packages) console.log(`${entry.name}@${entry.version}: ${entry.filename} (${entry.size} bytes)`);
