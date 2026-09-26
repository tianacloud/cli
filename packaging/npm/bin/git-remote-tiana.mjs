#!/usr/bin/env node
import { run } from './launcher.mjs';

// Resolve the native tiana from this package, never another executable on PATH.
run(['git', 'remote-helper', ...process.argv.slice(2)]);
