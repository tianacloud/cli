const base = new URL('../', import.meta.url);
const button = document.querySelector('#tiana-sign-in');
const status = document.querySelector('#tiana-status');
const link = document.querySelector('#tiana-auth-link');
const buttonLabel = document.querySelector('#tiana-button-label');
function showState(message, busy = false, error = false) {
  button.disabled = busy;
  button.setAttribute('aria-busy', String(busy));
  buttonLabel.textContent = busy ? '正在连接…' : '通过 Console 登录';
  status.textContent = message;
  status.dataset.state = error ? 'error' : 'waiting';
}
const openApp = () => location.replace(base.href.replace(/\/$/, '') + location.search + location.hash);
async function request(path, method = 'GET', extraHeaders = {}) {
  const requestId = 'req-' + crypto.randomUUID();
  try {
    const response = await fetch(new URL('_tiana/' + path, base), {method, cache:'no-store', headers:{'X-Tiana-Bootstrap':'1', 'X-Request-ID':requestId, ...extraHeaders}});
    const data = await response.json();
    return {response, data, requestId};
  } catch (cause) {
    throw Object.assign(new Error('预览授权请求失败，请重试。', {cause}), {requestId});
  }
}
function showError(error, fallbackId) {
  const requestId = error.requestId || fallbackId;
  showState(error.message + (requestId ? ' Request ID: ' + requestId : ''), false, true);
  document.querySelector('#tiana-request-copy')?.remove();
  if (requestId) {
    const copy = document.createElement('button');
    copy.id = 'tiana-request-copy'; copy.textContent = '复制 Request ID';
    copy.addEventListener('click', () => { void navigator.clipboard.writeText(requestId).then(() => { copy.textContent = '已复制'; }); });
    status.after(copy);
  }
  button.disabled = false;
}
async function poll() {
  const deadline = Date.now() + 15 * 60 * 1000;
  let lastRequestId;
  while (Date.now() < deadline) {
    const {response, requestId} = await request('session');
    lastRequestId = requestId;
    if (response.status === 200) {openApp(); return;}
    if (response.status !== 202) throw Object.assign(new Error('授权未完成，请重新登录。'), {requestId});
    await new Promise(resolve => setTimeout(resolve, 1500));
  }
  throw Object.assign(new Error('授权等待超时，请重新登录。'), {requestId: lastRequestId});
}
button.addEventListener('click', async () => {
  const popup = window.open('about:blank', '_blank');
  if (popup) popup.opener = null;
  let requestId;
  showState('正在打开 Console…', true);
  link.hidden = true;
  try {
    const result = await request('login', 'POST');
    requestId = result.requestId;
    const {response, data} = result;
    if (!response.ok) throw new Error('无法发起授权，请重试。');
    if (data.state === 'ready') {popup?.close();openApp();return;}
    const uri = new URL(data.verification_url);
    if (uri.protocol !== 'https:') throw new Error('授权地址无效。');
    link.href = uri.href; link.hidden = false;
    if (popup) popup.location.replace(uri.href);
    showState('请在 Console 完成授权。', true);
    await poll();
  } catch (error) {popup?.close();showError(error, requestId);}
});
function resumeAuthorization() {
  const launch = new URLSearchParams(location.hash.slice(1)).get('tiana_launch');
  if (launch) {
    history.replaceState(null, '', location.pathname + location.search);
    showState('正在使用已登录的账号打开预览…', true);
    request('local-login', 'POST', {'X-Tiana-Launch':launch})
      .then(({response, requestId}) => {if (!response.ok) throw Object.assign(new Error('本地授权链接已失效，请重新启动预览或使用 Console 登录。'), {requestId}); openApp();})
      .catch(error => showError(error));
  } else request('session').then(async ({response}) => {
    if (response.status === 200) openApp();
    else if (response.status === 202) {showState('请在 Console 完成授权。', true);await poll();}
  }).catch(error => showError(error));
}
window.addEventListener('hashchange', resumeAuthorization);
resumeAuthorization();
