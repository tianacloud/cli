import { spawn } from 'node:child_process';
import { constants } from 'node:os';
import { fileURLToPath } from 'node:url';

export function platformDirectory(platform, arch) {
  return {
    'darwin:arm64': 'darwin-arm64',
    'linux:x64': 'linux-amd64',
  }[`${platform}:${arch}`];
}

export function run(args = process.argv.slice(2)) {
  const platform = platformDirectory(process.platform, process.arch);
  if (!platform) {
    console.error(`Tiana CLI does not support ${process.platform}/${process.arch}`);
    process.exitCode = 1;
    return;
  }
  const executable = fileURLToPath(new URL(`../platforms/${platform}/tiana`, import.meta.url));
  const child = spawn(executable, args, { stdio: 'inherit' });
  const onInterrupt = () => child.kill('SIGINT');
  const onTerminate = () => child.kill('SIGTERM');
  process.on('SIGINT', onInterrupt);
  process.on('SIGTERM', onTerminate);
  child.on('error', () => {
    console.error('Cannot start the packaged Tiana CLI. Reinstall the complete package for this platform.');
    process.exitCode = 1;
  });
  child.on('close', (code, signal) => {
    process.off('SIGINT', onInterrupt);
    process.off('SIGTERM', onTerminate);
    process.exitCode = signal ? 128 + constants.signals[signal] : (code !== null && code >= 0 ? code : 1);
  });
}
