#!/usr/bin/env node
import { publishNpm } from './publish-cli.mjs';

const [directory, version, tag = 'latest', ...extra] = process.argv.slice(2);
if (!directory || !version || extra.length) {
  console.error('Usage: node scripts/publish-npm.mjs DIRECTORY VERSION [TAG]');
  process.exitCode = 2;
} else {
  try {
    publishNpm(directory, version, tag);
    console.log(`Published @tianadb/cli@${version} to ${tag}.`);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
