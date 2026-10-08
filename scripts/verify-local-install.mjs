#!/usr/bin/env node
import {runNPM} from './npm-command.mjs';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { platformPackage } from '../packaging/npm/bin/platforms.mjs';
const directory=path.resolve(process.argv[2]);
const release=JSON.parse(readFileSync(path.join(directory,'npm-release.json'),'utf8'));
const native=release.packages.find(entry=>entry.name===platformPackage(process.platform,process.arch));
const main=release.packages.find(entry=>entry.name==='@tianacloud/cli');
if(!native||!main)throw new Error('Release is missing the current platform or main package');
const prefix=mkdtempSync(path.join(tmpdir(),'tiana artifact install-'));
try{
 runNPM(['install','--offline','--ignore-scripts','--include=optional','--no-audit','--no-fund','--prefix',prefix,'--cache',path.join(prefix,'cache'),path.join(directory,native.filename),path.join(directory,main.filename)],{stdio:'inherit'});
 const launcher=path.join(prefix,'node_modules/@tianacloud/cli/bin/tiana.mjs');
 for(const args of [['version'],['web','list-template','--help'],['web','init-template','--help']])execFileSync(process.execPath,[launcher,...args],{stdio:'inherit'});
}finally{rmSync(prefix,{recursive:true,force:true})}
