import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {mkdtempSync,mkdirSync,writeFileSync,rmSync} from 'node:fs';
import path from 'node:path';
import {tmpdir} from 'node:os';
import test from 'node:test';

test('Windows npm executes its JavaScript entrypoint with literal arguments', async t=>{
 const {npmInvocation}=await import('../scripts/npm-command.mjs');
 const root=mkdtempSync(path.join(tmpdir(),'npm native 中文-'));t.after(()=>rmSync(root,{recursive:true,force:true}));
 const script=path.join(root,'node_modules/npm/bin/npm-cli.js');mkdirSync(path.dirname(script),{recursive:true});
 writeFileSync(script,'console.log(JSON.stringify(process.argv.slice(2)))');
 const old=process.env.npm_execpath;process.env.npm_execpath=script;t.after(()=>{if(old===undefined)delete process.env.npm_execpath;else process.env.npm_execpath=old;});
 const invocation=npmInvocation('win32');
 assert.equal(invocation.command,process.execPath);
 const args=['install','C:\\path with space\\中文 & literal.zip','%VARIABLE%','$(literal)'];
 assert.deepEqual(JSON.parse(execFileSync(invocation.command,[...invocation.prefix,...args],{encoding:'utf8'})),args);
});
