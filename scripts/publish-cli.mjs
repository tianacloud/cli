import {runNPM} from './npm-command.mjs';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { targets, platformPackage } from '../packaging/npm/bin/platforms.mjs';

export function usesTrustedPublishing(environment = process.env) {
 return Boolean(environment.ACTIONS_ID_TOKEN_REQUEST_URL && environment.ACTIONS_ID_TOKEN_REQUEST_TOKEN);
}
export function npmEnvironment(environment = process.env) {
 if (!environment.NPM_TOKEN) return environment;
 return {...environment,NPM_CONFIG_USERCONFIG:fileURLToPath(new URL('./npm-token.npmrc',import.meta.url))};
}
export function readNpmRelease(directory,version){
 const release=JSON.parse(readFileSync(path.join(directory,'npm-release.json'),'utf8'));
 const names=[...targets.map(target=>{const [os,arch]=target.split('-');return platformPackage(os==='windows'?'win32':os,arch==='amd64'?'x64':arch)}),'@tianacloud/cli'];
 if(release.version!==version || release.packages.length!==names.length) throw new Error('npm release version or package count does not match');
 return names.map(name=>{
  const entries=release.packages.filter(item=>item.name===name);
  if(entries.length!==1 || entries[0].version!==version) throw new Error(`Missing or mismatched npm package ${name}@${version}`);
  const entry=entries[0];const integrity='sha512-'+createHash('sha512').update(readFileSync(path.join(directory,entry.filename))).digest('base64');
  if(integrity!==entry.integrity)throw new Error(`Local npm artifact integrity differs: ${entry.name}`);
  return entry;
 });
}
export function publishNpm(directory,version,tag,{pause=ms=>Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,ms)}={}){
 const entries=readNpmRelease(directory,version),env=npmEnvironment();
 function remoteIntegrity(entry){
  try{return JSON.parse(runNPM(['view',`${entry.name}@${version}`,'dist.integrity','--json','--registry=https://registry.npmjs.org/'],{env,encoding:'utf8',stdio:['ignore','pipe','pipe']}))}
  catch(error){let data;try{data=JSON.parse(error.stdout?.toString()||'{}')}catch{}
   if(data?.error?.code==='E404')return undefined;
   throw new Error(`Cannot check ${entry.name}@${version}; verify npm registry access and retry the same artifacts.`);
  }
 }
 // Read back existing exact versions before uploading the prepared set.
 const existing=new Map(entries.map(entry=>[entry.name,remoteIntegrity(entry)]));
 for(const entry of entries){if(existing.get(entry.name) && existing.get(entry.name)!==entry.integrity)throw new Error(`${entry.name}@${version} contains different artifacts. Choose a new version.`)}
 for(const entry of entries){
  if(!existing.get(entry.name)){
   console.log(`Publish ${entry.name}@${version}`);
   try{runNPM(['publish',path.join(directory,entry.filename),'--access=public',`--tag=${tag}`,'--registry=https://registry.npmjs.org/'],{env,stdio:'inherit'})}
   catch{if(remoteIntegrity(entry)!==entry.integrity)throw new Error(`${entry.name} publication did not complete. Preserve this output directory and retry after resolving npm access.`)}
  }
  let verified=remoteIntegrity(entry);
  for(let attempt=0;verified===undefined && attempt<60;attempt++){pause(5000);verified=remoteIntegrity(entry)}
  if(verified!==entry.integrity)throw new Error(`${entry.name} npm publication verification failed; retry the original artifacts after registry processing`);
  const tags=JSON.parse(runNPM(['view',entry.name,'dist-tags','--json','--registry=https://registry.npmjs.org/'],{env,encoding:'utf8',stdio:['ignore','pipe','pipe']}));
  if(tags[tag]!==version)throw new Error(`${entry.name}@${version} is published, but ${tag} points elsewhere. Inspect the channel before changing its tag.`);
 }
 return entries;
}
