import {createTianaFunctions} from './sdk/functions.js';
import {createTianaFiles} from './sdk/files.js';
const base = new URL('../', import.meta.url);
const dataElement = document.querySelector('#tiana-bootstrap-data');
const manifest = JSON.parse(dataElement.textContent);
dataElement.remove();
const localPreview = manifest.local_preview;
const app = document.querySelector('#app');
const loaderScript = document.querySelector('#tiana-bootstrap-script');
const loadingHost = document.createElement('div');
loadingHost.id = 'tiana-loading';
const loading = loadingHost.attachShadow({mode: 'open'});
loading.innerHTML = `<style>
:host{all:initial;position:fixed;inset:0;z-index:2147483647;display:grid;place-items:center;pointer-events:none}
section{font:14px system-ui,sans-serif;color:#333;background:white;padding:18px 24px;border:1px solid #ddd;border-radius:8px}
button{font:inherit;cursor:pointer;margin-left:16px;pointer-events:auto}
</style><section role="status" aria-live="polite"><span>正在加载…</span></section>`;
document.body.append(loadingHost);

async function request(path, method = 'GET', payload, signal) {
  const url = new URL('_tiana/' + path, base);
  const requestId = 'req-' + crypto.randomUUID();
  const headers = {'X-Tiana-Bootstrap': '1', 'X-Request-ID': requestId};
  if (!localPreview) {
    const proof = document.cookie.split('; ').find(item => item.startsWith('tiana_mgr_csrf='));
    if (proof) headers['X-CSRF-Token'] = decodeURIComponent(proof.split('=').slice(1).join('='));
  }
  if (payload) {
    headers['Content-Type'] = 'application/json';

  }
  try {
    const response = await fetch(url, {method, credentials:'same-origin', cache:'no-store', headers, signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(12000)]) : AbortSignal.timeout(12000), body: payload ? JSON.stringify(payload) : undefined});
    return {response, requestId, data: response.status === 204 ? null : await response.json()};
  } catch (cause) {
    throw Object.assign(new Error('应用请求失败，请重试。', {cause}), {requestId});
  }
}
function credentialExpiryMilliseconds(value) {
  if (localPreview && typeof value === 'string') return Date.parse(value);
  return Number.isSafeInteger(value) && value > 0 && value <= 253402300799 ? value * 1000 : NaN;
}
async function loadApplication() {
  document.title = manifest.name;
  let assetBase = new URL('_tiana/files/', base);
  if (!localPreview) {
    assetBase = new URL(manifest.asset_base);
    if (assetBase.protocol !== 'https:' || assetBase.username || assetBase.password || assetBase.search || assetBase.hash || !assetBase.pathname.endsWith('/')) throw new Error('应用资源地址无效。');
  }
  const asset = path => new URL(path.split('/').map(encodeURIComponent).join('/'), assetBase).href;
  let credential;
  let credentialExpiresAt = 0;
  let pendingConnection;
  const auth = Object.freeze({async getAccessToken({signal} = {}) {
    if (signal?.aborted) throw signal.reason;
    const pending = context.connection().then(value => value.tianaToken);
    if (!signal) return pending;
    return new Promise((resolve, reject) => {
      const abort = () => { signal.removeEventListener('abort', abort); reject(signal.reason); };
      signal.addEventListener('abort', abort, {once: true});
      pending.then(value => { signal.removeEventListener('abort', abort); resolve(value); },
        error => { signal.removeEventListener('abort', abort); reject(error); });
      if (signal.aborted) abort();
    });
  }});
  let fileWrites = Promise.resolve();
  const fileRequest = (url, init) => {
    const send = () => {
      const headers = new Headers(init.headers);
      headers.set('X-Tiana-Bootstrap', '1');
      headers.set('X-Request-ID', 'req-' + crypto.randomUUID());
      if (!localPreview) {
        const proof = document.cookie.split('; ').find(item => item.startsWith('tiana_mgr_csrf='));
        if (proof) headers.set('X-CSRF-Token', decodeURIComponent(proof.split('=').slice(1).join('=')));
      }
      return fetch(url, {...init, headers});
    };
    if (init.method !== 'POST' && init.method !== 'DELETE') return send();
    const result = fileWrites.then(send);
    fileWrites = result.then(() => undefined, () => undefined);
    return result;
  };
  const files = createTianaFiles({
    apiBaseUrl: new URL('_tiana/file-storage', base).href,
    request: fileRequest,
    ...(localPreview ? {contentBaseUrl: new URL('_tiana/file-storage/content', base).href} : {}),
  });
  await files.ready();
  window.addEventListener('pagehide', () => files.dispose(), {once: true});
  const functionOptions = {connection: async (instanceId, {signal}) => {
    const {response, data, requestId} = await request('function-connection', 'POST', {instance_id: instanceId}, signal);
    if (!response.ok) throw Object.assign(new Error('后端服务授权未完成，请检查绑定或重新登录。'), {requestId});
    return data;
  }};
  let functionClient = createTianaFunctions(functionOptions);
  const functions = Object.freeze({fetch: (instanceId, path, init) => functionClient.fetch(instanceId, path, init)});
  window.addEventListener('pagehide', () => functionClient.dispose());
  window.addEventListener('pageshow', event => { if (event.persisted) functionClient = createTianaFunctions(functionOptions); });
  const context = Object.freeze({appId: manifest.web_id, localPreview, auth, files, functions, async connection() {
    if (credential && credentialExpiresAt > Date.now() + 30000) return credential;
    if (!pendingConnection) {
      pendingConnection = (async () => {
        const payload = localPreview ? undefined : {expected_revision: manifest.project_revision};
        const deadline = Date.now() + 10000;
        let result;
        do {
          result = await request('connection', 'POST', payload);
          if (result.response.status !== 503 || result.data?.error?.code !== 'WEB_ACCOUNT_AUTH_PENDING' || Date.now() >= deadline) break;
          await new Promise(resolve => setTimeout(resolve, 500));
        } while (true);
        const {response, data, requestId} = result;
        if (!response.ok) throw Object.assign(new Error('数据库授权未完成，请重新登录或刷新页面后重试。'), {requestId});
        let origin;
        try { origin = new URL(data.origin); }
        catch (cause) { throw Object.assign(new Error('数据库连接信息无效。', {cause}), {requestId}); }
        const expiresAt = credentialExpiryMilliseconds(data.expires_at);
        if (data.instance_id !== manifest.database_instance_id || !data.tianaToken || data.sql_api !== 'hrana-v3' || !Number.isFinite(expiresAt) || expiresAt <= Date.now() || origin.protocol !== 'https:' || origin.username || origin.password || origin.search || origin.hash || origin.pathname !== '/') throw Object.assign(new Error('数据库连接信息无效。'), {requestId});
        credential = Object.freeze(data);
        credentialExpiresAt = expiresAt;
        pendingConnection = undefined;
        return credential;
      })();
      // Both routes recover server-owned authorization. Lost lookup responses
      // do not repeat a refresh rotation or create a different hosted grant.
      pendingConnection = pendingConnection.catch(error => {
        pendingConnection = undefined;
        throw error;
      });
    }
    return pendingConnection;
  }});

  // Plain module entries can start themselves and request a connection on demand.
  Object.defineProperty(window, 'tiana', {value: context, configurable: true});
  const module = await import(asset(manifest.entry));
  // An entry may export mount; an ordinary module runs during import.
  if (typeof module.mount === 'function') await module.mount(app, context);
  loadingHost.remove();
  loaderScript?.remove();
}
loadApplication().catch(error => {
  loading.querySelector('span').textContent = '应用加载失败，请刷新后重试。';
  if (error.requestId) {
    const identity = document.createElement('p');
    identity.textContent = 'Request ID: ' + error.requestId;
    const copy = document.createElement('button');
    copy.textContent = '复制 Request ID';
    copy.addEventListener('click', () => { void navigator.clipboard.writeText(error.requestId).then(() => { copy.textContent = '已复制'; }); });
    loading.querySelector('section').append(identity, copy);
  }
  const retry = document.createElement('button');
  retry.textContent = '重新加载';
  retry.addEventListener('click', () => location.reload(), {once: true});
  loading.querySelector('section').append(retry);
});
