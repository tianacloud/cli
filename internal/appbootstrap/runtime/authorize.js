const base = new URL('../', import.meta.url);
const button = document.querySelector('#tiana-sign-in');
const status = document.querySelector('#tiana-status');
const link = document.querySelector('#tiana-auth-link');
const openApp = () => location.replace(base.href + location.hash);
async function request(path, method = 'GET') {
  const requestId = 'req-' + crypto.randomUUID();
  try {
    const response = await fetch(new URL('_tiana/' + path, base), {method, cache:'no-store', headers:{'X-Tiana-Bootstrap':'1', 'X-Request-ID':requestId}});
    const data = await response.json();
    return {response, data, requestId};
  } catch (cause) {
    throw Object.assign(new Error('预览授权请求失败，请重试。', {cause}), {requestId});
  }
}
function showError(error, fallbackId) {
  const requestId = error.requestId || fallbackId;
  status.textContent = error.message + (requestId ? ' Request ID: ' + requestId : '');
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
  button.disabled = true;
  let requestId;
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
    status.textContent = '请在 Console 完成授权。';
    await poll();
  } catch (error) {popup?.close();showError(error, requestId);}
});
request('session').then(async ({response}) => {
  if (response.status === 200) openApp();
  else if (response.status === 202) {button.disabled = true;await poll();}
}).catch(error => showError(error));
