import {execFileSync} from 'node:child_process';
import {existsSync} from 'node:fs';
import path from 'node:path';

// npm.cmd is a batch file. Execute npm's JavaScript entrypoint on Windows.
export function npmInvocation(platform = process.platform) {
  if (platform !== 'win32') return {command: 'npm', prefix: []};
  let script = process.env.npm_execpath;
  if (!script || !existsSync(script)) {
    const shims = execFileSync('where.exe', ['npm.cmd'], {encoding: 'utf8'}).trim().split(/\r?\n/);
    script = shims.map(shim => path.join(path.dirname(shim), 'node_modules', 'npm', 'bin', 'npm-cli.js')).find(existsSync);
  }
  if (!script) throw new Error('Cannot locate npm-cli.js; install Node.js with npm before continuing.');
  return {command: process.execPath, prefix: [script]};
}
export function runNPM(args, options) {
  const invocation = npmInvocation();
  return execFileSync(invocation.command, [...invocation.prefix, ...args], options);
}
