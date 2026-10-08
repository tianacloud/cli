import {runNPM, npmInvocation} from '../scripts/npm-command.mjs';
import assert from 'node:assert/strict';
import { execFileSync, execFile } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { cp } from 'node:fs/promises';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { targets, binaryName, platformPackage, platformDirectory } from './npm/bin/platforms.mjs';
const run=promisify(execFile);
const repo=fileURLToPath(new URL('..',import.meta.url));
test('npm selects the matching native package and runs with scripts enabled or ignored',{timeout:90000},async t=>{
 const root=mkdtempSync(path.join(tmpdir(),'tiana npm install 中文-'));t.after(()=>rmSync(root,{recursive:true,force:true}));
 const version='9.8.7-test.1';const hostTarget=platformDirectory(process.platform,process.arch);const expected=platformPackage(process.platform,process.arch);
 const native=path.join(root,'fixture');execFileSync('go',['build','-o',native,'./packaging/testdata/native.go'],{cwd:repo,env:{...process.env,GOWORK:'off'}});
 const packages=new Map();
 for(const target of targets){
  const [os,arch]=target.split('-');const npmOS=os==='windows'?'win32':os;const npmArch=arch==='amd64'?'x64':arch;const name=platformPackage(npmOS,npmArch);const directory=path.join(root,target);mkdirSync(path.join(directory,'bin'),{recursive:true});
  await cp(native,path.join(directory,'bin',binaryName(target)));writeFileSync(path.join(directory,'package.json'),JSON.stringify({name,version,os:[npmOS],cpu:[npmArch]}));
  const entry=JSON.parse(runNPM(['pack','--ignore-scripts','--json','--pack-destination',root],{cwd:directory,encoding:'utf8'}))[0];packages.set(name,{entry,bytes:readFileSync(path.join(root,entry.filename))});
 }
 const downloads=[];let base;
 const server=createServer((req,res)=>{
  const pathname=decodeURIComponent(new URL(req.url,'http://fixture.test').pathname.slice(1));
  if(pathname.startsWith('tarballs/')){const name=pathname.slice(9);const data=packages.get(name);if(!data){res.writeHead(404).end();return}downloads.push(name);res.end(data.bytes);return}
  const data=packages.get(pathname);if(!data){res.writeHead(404).end(JSON.stringify({error:'not found'}));return}
  const [os,arch]=pathname.split('/cli-')[1].split('-');res.setHeader('content-type','application/json');res.end(JSON.stringify({name:pathname,'dist-tags':{latest:version},versions:{[version]:{name:pathname,version,os:[os],cpu:[arch],dist:{tarball:base+'/tarballs/'+encodeURIComponent(pathname),integrity:data.entry.integrity}}}}));
 });await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));base=`http://127.0.0.1:${server.address().port}`;t.after(()=>new Promise(resolve=>server.close(resolve)));
 const main=path.join(root,'main');await cp(path.join(repo,'packaging/npm'),main,{recursive:true});const pkg=JSON.parse(readFileSync(path.join(main,'package.json')));pkg.version=version;pkg.optionalDependencies=Object.fromEntries([...packages.keys()].map(name=>[name,version]));writeFileSync(path.join(main,'package.json'),JSON.stringify(pkg));
 const packed=JSON.parse(runNPM(['pack','--ignore-scripts','--json','--pack-destination',root],{cwd:main,encoding:'utf8'}))[0];
 for(const ignore of [false,true]){await t.test(ignore?'ignore scripts':'normal installation',async()=>{
  const prefix=path.join(root,ignore?'ignored':'normal');mkdirSync(prefix);downloads.length=0;
  const invocation=npmInvocation();
  await run(invocation.command,[...invocation.prefix,'install','--prefix',prefix,'--cache',path.join(prefix,'cache'),'--include=optional',`--ignore-scripts=${ignore}`,'--no-audit','--no-fund','--registry',base,path.join(root,packed.filename)],{timeout:45000});
  assert.deepEqual(downloads,[expected]);
  const launcher=path.join(prefix,'node_modules/@tianacloud/cli/bin/tiana.mjs');const {stdout}=await run(process.execPath,[launcher]);assert.match(stdout,/packaging fixture/);
  assert.equal(readFileSync(path.join(prefix,'node_modules',expected,'bin',binaryName(hostTarget))).length,readFileSync(native).length);
 })}
});
