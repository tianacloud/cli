import {runNPM} from '../scripts/npm-command.mjs';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

const checksum = b => createHash('sha256').update(b).digest('hex');
test('npm token configuration is selected without storing the token', async () => {
  const { npmEnvironment } = await import('../scripts/publish-cli.mjs');
  const login = { PATH: process.env.PATH, NPM_CONFIG_USERCONFIG: '/existing/npmrc' };
  assert.equal(npmEnvironment(login), login);
  const env = npmEnvironment({ ...login, NPM_TOKEN: 'synthetic-publish-token' });
  assert.equal(readFileSync(env.NPM_CONFIG_USERCONFIG, 'utf8'), '//registry.npmjs.org/:_authToken=${NPM_TOKEN}\n');
  assert.equal(env.NPM_TOKEN, 'synthetic-publish-token');
  assert.equal(login.NPM_CONFIG_USERCONFIG, '/existing/npmrc');
  if (process.platform !== 'win32') {
    const selected = runNPM( ['config', 'get', 'userconfig'], { env, encoding: 'utf8' }).trim();
    assert.equal(selected, env.NPM_CONFIG_USERCONFIG);
  }
});


test('publishes seven platform packages before the main package and resumes by integrity',{skip:process.platform==='win32'},async t=>{
 const {publishNpm}=await import('../scripts/publish-cli.mjs');
 const {targets,platformPackage}=await import('./npm/bin/platforms.mjs');
 const directory=mkdtempSync(path.join(tmpdir(),'tiana npm publish-'));t.after(()=>rmSync(directory,{recursive:true,force:true}));
 const version='1.2.3';const names=[...targets.map(target=>{const [os,arch]=target.split('-');return platformPackage(os==='windows'?'win32':os,arch==='amd64'?'x64':arch)}),'@tianacloud/cli'];
 const entries=names.map((name,i)=>{const filename=`fixture-${i}.tgz`;const bytes=Buffer.from(name);writeFileSync(path.join(directory,filename),bytes);return {name,version,filename,integrity:'sha512-'+createHash('sha512').update(bytes).digest('base64')}});
 writeFileSync(path.join(directory,'npm-release.json'),JSON.stringify({version,packages:entries}));
 const stateFile=path.join(directory,'state.json');
 writeFileSync(path.join(directory,'npm'),`#!${process.execPath}
const fs=require('fs'),path=require('path');const p=process.env.TIANA_NPM_TEST_STATE;const s=JSON.parse(fs.readFileSync(p));const args=process.argv.slice(2);
if(args[0]==='view'){
 if(args[2]==='dist-tags') console.log(JSON.stringify(s.tags[args[1]]||{}));
 else {const name=args[1].slice(0,args[1].lastIndexOf('@'));if(s.processing?.[name]>0){s.processing[name]--;fs.writeFileSync(p,JSON.stringify(s));console.log(JSON.stringify({error:{code:'E404'}}));process.exit(1)}if(s.remote[name])console.log(JSON.stringify(s.remote[name]));else{console.log(JSON.stringify({error:{code:'E404'}}));process.exitCode=1}}
}else if(args[0]==='publish'){
 const entry=s.entries.find(e=>e.filename===path.basename(args[1]));s.published.push(entry.name);
 if(s.delay===entry.name)s.processing={[entry.name]:3};
 if(s.fail!==entry.name){s.remote[entry.name]=entry.integrity;s.tags[entry.name]={latest:'1.2.3'}}
 fs.writeFileSync(p,JSON.stringify(s));if(s.uncertain===entry.name||s.fail===entry.name)process.exitCode=1;
}
`,{mode:0o755});
 const originalPath=process.env.PATH,originalState=process.env.TIANA_NPM_TEST_STATE;process.env.PATH=directory+path.delimiter+originalPath;process.env.TIANA_NPM_TEST_STATE=stateFile;
 t.after(()=>{process.env.PATH=originalPath;if(originalState===undefined)delete process.env.TIANA_NPM_TEST_STATE;else process.env.TIANA_NPM_TEST_STATE=originalState});
 function initial(){return {entries,remote:{},tags:{},published:[]}}
 await t.test('platform-first publication and identical resume',()=>{
  writeFileSync(stateFile,JSON.stringify(initial()));publishNpm(directory,version,'latest');publishNpm(directory,version,'latest');assert.deepEqual(JSON.parse(readFileSync(stateFile)).published,names);
 });
 await t.test('accepted package waits for registry processing before the next package',()=>{
  const waits=[];writeFileSync(stateFile,JSON.stringify({...initial(),delay:names[0]}));publishNpm(directory,version,'latest',{pause:ms=>waits.push(ms)});assert.equal(waits.length,3);assert.deepEqual(JSON.parse(readFileSync(stateFile)).published,names);
 });
 await t.test('uncertain successful platform publication is read back',()=>{
  writeFileSync(stateFile,JSON.stringify({...initial(),uncertain:names[2]}));publishNpm(directory,version,'latest');assert.deepEqual(JSON.parse(readFileSync(stateFile)).published,names);
 });
 await t.test('failed platform blocks main and remaining files remain resumable',()=>{
  writeFileSync(stateFile,JSON.stringify({...initial(),fail:names[2]}));assert.throws(()=>publishNpm(directory,version,'latest'),/did not complete/);let state=JSON.parse(readFileSync(stateFile));assert.deepEqual(state.published,names.slice(0,3));delete state.fail;writeFileSync(stateFile,JSON.stringify(state));publishNpm(directory,version,'latest');assert.deepEqual(JSON.parse(readFileSync(stateFile)).published,[...names.slice(0,3),...names.slice(2)]);
 });
 await t.test('conflicting existing artifact is reported before writes',()=>{
  writeFileSync(stateFile,JSON.stringify({...initial(),remote:{[names[4]]:'different'}}));assert.throws(()=>publishNpm(directory,version,'latest'),/different artifacts/);assert.deepEqual(JSON.parse(readFileSync(stateFile)).published,[]);
 });
});
