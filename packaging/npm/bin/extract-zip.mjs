import { unzipSync } from 'fflate';
import { writeFileSync } from 'node:fs';
import path from 'node:path';

export function extractWindowsBinary(archive, directory) {
  const files = unzipSync(archive, { filter: entry => entry.name === 'tiana.exe' });
  const binary = files['tiana.exe'];
  if (!binary) throw new Error('Binary archive is missing tiana.exe.');
  writeFileSync(path.join(directory, 'tiana.exe'), binary);
}
