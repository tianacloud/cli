import { spawn } from 'node:child_process';
import { constants } from 'node:os';
import { createRequire } from 'node:module';
import path from 'node:path';
import { platformDirectory, platformPackage, binaryName } from './platforms.mjs';
export { platformDirectory } from './platforms.mjs';

export function run(args = process.argv.slice(2)) {
  const platform = platformDirectory(process.platform, process.arch);
  if (!platform) {
    console.error(`Tiana CLI does not support ${process.platform}/${process.arch}`);
    process.exitCode = 1;
    return;
  }
  const name = platformPackage(process.platform, process.arch);
  let executable;
  try {
    const manifest = createRequire(import.meta.url).resolve(`${name}/package.json`);
    executable = path.join(path.dirname(manifest), 'bin', binaryName(platform));
  } catch {
    console.error(`Missing ${name}. Reinstall with npm install -g @tianacloud/cli@latest --include=optional`);
    process.exitCode = 1;
    return;
  }
  const child = spawn(executable, args, { stdio: 'inherit' });
  // Windows delivers Ctrl+C to both processes attached to this console.
  const onInterrupt = () => { if (process.platform !== 'win32') child.kill('SIGINT'); };
  const onTerminate = () => child.kill('SIGTERM');
  process.on('SIGINT', onInterrupt);
  process.on('SIGTERM', onTerminate);
  child.on('error', () => {
    console.error(`Cannot start ${name}. Reinstall with npm install -g @tianacloud/cli@latest --include=optional`);
    process.exitCode = 1;
  });
  child.on('close', (code, signal) => {
    process.off('SIGINT', onInterrupt);
    process.off('SIGTERM', onTerminate);
    process.exitCode = signal ? 128 + constants.signals[signal] : (code !== null && code >= 0 ? code : 1);
  });
}
