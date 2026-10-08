#!/usr/bin/env node
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { targets } from '../packaging/npm/bin/platforms.mjs';
import { publishNpm } from './publish-cli.mjs';
async function main(){
 const {values,positionals}=parseArgs({allowPositionals:true,options:{'dry-run':{type:'boolean',default:false},help:{type:'boolean',default:false}}});
 if(values.help){console.log('Usage: ./scripts/release.mjs [VERSION] [--dry-run]\nBuild seven native targets, pack eight npm packages, then publish. Dry run keeps local artifacts only.');return}
 const repo=fileURLToPath(new URL('..',import.meta.url));
 const version=positionals[0]??JSON.parse(readFileSync(path.join(repo,'packaging/npm/package.json'),'utf8')).version;
 if(positionals.length>1 || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(version))throw new Error('Provide a semantic version.');
 const parent=path.join(repo,'dist/releases');mkdirSync(parent,{recursive:true});const directory=mkdtempSync(path.join(parent,`${version}-`));console.log(`Release output: ${directory}`);
 for(const target of targets)execFileSync(process.execPath,[path.join(repo,'scripts/build-platform.mjs'),target,path.join(directory,'assets'),version],{cwd:repo,stdio:'inherit'});
 const packed=path.join(directory,'packed');execFileSync(process.execPath,[path.join(repo,'scripts/package-cli.mjs'),path.join(directory,'assets'),packed,version],{cwd:repo,stdio:'inherit'});
 if(values['dry-run']){console.log(`Dry run complete: ${packed}`);return}
 publishNpm(packed,version,version.split('+')[0].includes('-')?'next':'latest');
 console.log(`Published eight CLI packages at ${version}.`);
}
main().catch(error=>{console.error(`Release failed: ${error.message}`);process.exitCode=1});
