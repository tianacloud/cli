import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const sha = bytes => createHash('sha256').update(bytes).digest('hex');
const repository = '/repos/tianacloud/cli-releases';

export function usesTrustedPublishing(environment = process.env) {
  return Boolean(environment.ACTIONS_ID_TOKEN_REQUEST_URL && environment.ACTIONS_ID_TOKEN_REQUEST_TOKEN);
}

export function npmEnvironment(environment = process.env) {
  if (!environment.NPM_TOKEN) return environment;
  return { ...environment, NPM_CONFIG_USERCONFIG: fileURLToPath(new URL('./npm-token.npmrc', import.meta.url)) };
}

export async function uploadRelease({ directory, version, token, apiBase = 'https://gitee.com/api/v5' }) {
  async function api(method, endpoint, body, missing = false) {
    const headers = { Authorization: `Bearer ${token}`, Accept: 'application/json' };
    if (body && !(body instanceof FormData)) { headers['Content-Type'] = 'application/json'; body = JSON.stringify(body); }
    const response = await fetch(apiBase + endpoint, { method, headers, body });
    if (missing && response.status === 404) return undefined;
    if (!response.ok) throw new Error(`Gitee ${method} ${endpoint}: HTTP ${response.status}`);
    return response.json();
  }
  async function verify(url, checksum) {
    const response = await fetch(url);
    if (!response.ok) throw new Error(`Anonymous release download failed: HTTP ${response.status}`);
    if (sha(Buffer.from(await response.arrayBuffer())) !== checksum) throw new Error('Published attachment checksum differs from the local release; use a new version for changed artifacts.');
  }
  const manifest = JSON.parse(readFileSync(path.join(directory, 'package/release.json'), 'utf8'));
  if (manifest.version !== version) throw new Error('Release directory version mismatch');
  const repo = await api('GET', repository);
  if (repo.public !== true) throw new Error('Gitee tianacloud/cli-releases must be publicly accessible.');
  let release = await api('GET', `${repository}/releases/tags/v${version}`, undefined, true);
  if (!release) release = await api('POST', `${repository}/releases`, {
    tag_name: `v${version}`, name: `Tiana CLI ${version}`, target_commitish: repo.default_branch,
    body: `Install with npm install -g @tianacloud/cli@${version}.\nSeven native platform archives and SHA-256 checksums.`, prerelease: version.includes('-'),
  });
  const endpoint = `${repository}/releases/${release.id}/attach_files`;
  const attachments = await api('GET', endpoint + '?per_page=100');
  const assets = [...Object.values(manifest.assets)];
  const sums = readFileSync(path.join(directory, 'SHA256SUMS'));
  assets.push({ filename: 'SHA256SUMS', sha256: sha(sums), url: new URL('SHA256SUMS', assets[0].url).href });
  const npmFilename = `tiana-cli-${version}.tgz`;
  for (const filename of [npmFilename, `${npmFilename}.sha256`]) {
    assets.push({ filename, sha256: sha(readFileSync(path.join(directory, filename))), url: new URL(filename, assets[0].url).href });
  }
  // Check conflicts before uploading anything else on a rerun.
  for (const asset of assets) {
    if (attachments.some(a => a.name === asset.filename)) await verify(asset.url, asset.sha256);
  }
  for (const asset of assets) {
    console.log(`Upload and verify ${asset.filename}`);
    const bytes = readFileSync(path.join(directory, asset.filename));
    if (sha(bytes) !== asset.sha256) throw new Error(`Local attachment checksum mismatch: ${asset.filename}`);
    if (!attachments.some(a => a.name === asset.filename)) {
      const form = new FormData();
      form.append('file', new Blob([bytes]), asset.filename);
      await api('POST', endpoint, form);
    }
    await verify(asset.url, asset.sha256);
  }
}

export function publishNpm(directory, version, tag) {
  const env = npmEnvironment();
  const tarball = path.join(directory, `tiana-cli-${version}.tgz`);
  const integrity = 'sha512-' + createHash('sha512').update(readFileSync(tarball)).digest('base64');
  function remoteIntegrity() {
    try {
      return JSON.parse(execFileSync('npm', ['view', `@tianacloud/cli@${version}`, 'dist.integrity', '--json', '--registry=https://registry.npmjs.org/'], { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }));
    } catch (error) {
      let data; try { data = JSON.parse(error.stdout?.toString() || '{}'); } catch {}
      if (data?.error?.code === 'E404') return undefined;
      throw new Error('Cannot determine whether this npm version is already published. Check npm registry access and retry.');
    }
  }
  const remote = remoteIntegrity();
  if (remote && remote !== integrity) throw new Error('This npm version already contains different artifacts. Choose a new version.');
  if (!remote) {
    try {
      execFileSync('npm', ['publish', tarball, '--access=public', `--tag=${tag}`, '--registry=https://registry.npmjs.org/'], { env, stdio: 'inherit' });
    } catch {
      if (remoteIntegrity() !== integrity) throw new Error('npm publication did not complete. Release files remain available; rerun the same version after resolving npm authentication or network access.');
    }
  }
  if (remoteIntegrity() !== integrity) throw new Error('npm publication verification failed');
  const tags = JSON.parse(execFileSync('npm', ['view', '@tianacloud/cli', 'dist-tags', '--json', '--registry=https://registry.npmjs.org/'], { env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }));
  if (tags[tag] !== version) {
    if (usesTrustedPublishing(env)) throw new Error(`npm version is published, but ${tag} does not point to ${version}. OIDC cannot change existing dist-tags; update the tag using npm login.`);
    execFileSync('npm', ['dist-tag', 'add', `@tianacloud/cli@${version}`, tag, '--registry=https://registry.npmjs.org/'], { env, stdio: 'inherit' });
  }
}
