const base = new URL('../', import.meta.url);
const dataElement = document.querySelector('#tiana-bootstrap-data');
const manifest = JSON.parse(dataElement.textContent);
dataElement.remove();
const localPreview = manifest.local_preview;
const resolvedVersion = manifest.version_id;
const app = document.querySelector('#app');
const loaderScript = document.querySelector('script[src="./_tiana/bootstrap.js"]');
const loadingHost = document.createElement('div');
loadingHost.id = 'tiana-loading';
const loading = loadingHost.attachShadow({mode: 'open'});
loading.innerHTML = `<style>
:host{all:initial;position:fixed;inset:0;z-index:2147483647;display:grid;place-items:center;pointer-events:none}
section{font:14px system-ui,sans-serif;color:#333;background:white;padding:18px 24px;border:1px solid #ddd;border-radius:8px}
button{font:inherit;cursor:pointer;margin-left:16px;pointer-events:auto}
</style><section role="status" aria-live="polite"><span>正在加载…</span></section>`;
document.body.append(loadingHost);

async function request(path, method = 'GET', payload) {
  const url = new URL('_tiana/' + path, base);
  if (!localPreview) url.searchParams.set('version', resolvedVersion);
  const headers = {'X-Tiana-Bootstrap': '1'};
  if (!localPreview) {
    const proof = document.cookie.split('; ').find(item => item.startsWith('tiana_mgr_csrf='));
    if (proof) headers['X-CSRF-Token'] = decodeURIComponent(proof.split('=').slice(1).join('='));
  }
  if (payload) {
    headers['Content-Type'] = 'application/json';
    headers['Idempotency-Key'] = payload.request_id;
  }
  const response = await fetch(url, {method, credentials:'same-origin', cache:'no-store', headers, body: payload ? JSON.stringify(payload) : undefined});
  return {response, data: response.status === 204 ? null : await response.json()};
}
function credentialExpiryMilliseconds(value) {
  if (localPreview && typeof value === 'string') return Date.parse(value);
  return Number.isSafeInteger(value) && value > 0 && value <= 253402300799 ? value * 1000 : NaN;
}
async function loadApplication() {
  document.title = manifest.name;
  let assetBase = new URL('_tiana/files/', base);
  if (!localPreview) {
    if (!/^[A-Za-z0-9_-]{1,80}$/.test(resolvedVersion ?? '')) throw new Error('应用版本无效。');
    assetBase = new URL(manifest.asset_base);
    if (assetBase.protocol !== 'https:' || assetBase.username || assetBase.password || assetBase.search || assetBase.hash || !assetBase.pathname.endsWith('/')) throw new Error('应用资源地址无效。');
  }
  const asset = path => new URL(path.split('/').map(encodeURIComponent).join('/'), assetBase).href;
  let credential;
  let credentialExpiresAt = 0;
  let pendingConnection;
  const context = Object.freeze({appId: manifest.app_id, localPreview, async connection() {
    if (credential && credentialExpiresAt > Date.now() + 30000) return credential;
    if (!pendingConnection) {
      pendingConnection = (async () => {
        const payload = localPreview ? undefined : {request_id: crypto.randomUUID(), expires_at: Math.floor(Date.now() / 1000) + 10 * 60};
        const {response, data} = await request('connection', 'POST', payload);
        if (!response.ok) throw new Error('数据库授权未完成，请重新登录或刷新页面后重试。');
        const origin = new URL(data.origin);
        const expiresAt = credentialExpiryMilliseconds(data.expires_at);
        if (data.instance_id !== manifest.database_instance_id || !data.tianaToken || data.sql_api !== 'hrana-v3' || !Number.isFinite(expiresAt) || expiresAt <= Date.now() || origin.protocol !== 'https:' || origin.username || origin.password || origin.search || origin.hash || origin.pathname !== '/') throw new Error('数据库连接信息无效。');
        credential = Object.freeze(data);
        credentialExpiresAt = expiresAt;
        pendingConnection = undefined;
        return credential;
      })();
    }
    // Retain a failed request until this document is reloaded. A lost creation
    // response must not cause automatic, repeated credential issuance.
    return pendingConnection;
  }});

  // Plain module entries can start themselves and request a connection on demand.
  Object.defineProperty(window, 'tiana', {value: context, configurable: true});
  await Promise.all((manifest.styles ?? []).map(path => new Promise(resolve => {
    const link = document.createElement('link');
    link.rel = 'stylesheet'; link.href = asset(path);
    link.onload = () => {link.onload = null; link.onerror = null; resolve();};
    link.onerror = () => {link.onload = null; link.onerror = null; resolve();};
    document.head.append(link);
  })));
  const module = await import(asset(manifest.entry));
  // Compatibility with existing releases; ordinary entries need no export.
  if (typeof module.mount === 'function') await module.mount(app, context);
  loadingHost.remove();
  loaderScript?.remove();
}
loadApplication().catch(() => {
  loading.querySelector('span').textContent = '应用加载失败，请刷新后重试。';
  const retry = document.createElement('button');
  retry.textContent = '重新加载';
  retry.addEventListener('click', () => location.reload(), {once: true});
  loading.querySelector('section').append(retry);
});
