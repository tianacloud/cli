#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { binaryName, platformDirectory } from './platforms.mjs';

const root = fileURLToPath(new URL('..', import.meta.url));
async function install() {
  const target = platformDirectory(process.platform, process.arch);
  if (!target) throw new Error(`Tiana CLI does not support ${process.platform}/${process.arch}`);
  const pkg = JSON.parse(readFileSync(path.join(root, 'package.json'), 'utf8'));
  const release = JSON.parse(readFileSync(path.join(root, 'release.json'), 'utf8'));
  if (release.version !== pkg.version) throw new Error('The binary manifest does not match the npm package version.');
  const asset = release.assets[target];
  const temporary = mkdtempSync(path.join(root, '.install-'));
  try {
    console.log(`Downloading Tiana CLI ${pkg.version} (${target}) from Gitee...`);
    const response = await fetch(asset.url);
    if (!response.ok) throw new Error(`Binary download failed: HTTP ${response.status}`);
    const contents = Buffer.from(await response.arrayBuffer());
    if (createHash('sha256').update(contents).digest('hex') !== asset.sha256) throw new Error('Binary archive checksum mismatch.');
    const archive = path.join(temporary, target.startsWith('windows-') ? 'cli.zip' : 'cli.tar.gz');
    writeFileSync(archive, contents);
    const extracted = path.join(temporary, 'unpacked');
    mkdirSync(extracted);
    if (process.platform === 'win32') {
      execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', "$ErrorActionPreference='Stop'; Add-Type -AssemblyName System.IO.Compression.FileSystem; [System.IO.Compression.ZipFile]::ExtractToDirectory($env:TIANA_ARCHIVE,$env:TIANA_DEST)"], {
        env: { ...process.env, TIANA_ARCHIVE: archive, TIANA_DEST: extracted }, stdio: 'pipe',
      });
    } else {
      execFileSync('tar', ['-xzf', archive, '-C', extracted], { stdio: 'pipe' });
    }
    const name = binaryName(target);
    chmodSync(path.join(extracted, name), 0o755);
    mkdirSync(path.join(root, 'native'), { recursive: true });
    renameSync(path.join(extracted, name), path.join(root, 'native', name));
    console.log(`Tiana CLI ${pkg.version} installed.`);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}
install().catch(error => { console.error(`Tiana CLI installation failed: ${error.message}`); process.exitCode = 1; });
