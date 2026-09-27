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
const openApp = () => location.replace(base.href + location.hash);
async function poll() {
  const deadline = Date.now() + 15 * 60 * 1000;
  while (Date.now() < deadline) {
    const response = await fetch(new URL('_tiana/session', base), {cache: 'no-store'});
    if (response.status === 200) {openApp(); return;}
    if (response.status !== 202) throw new Error('授权未完成，请重新登录。');
    await new Promise(resolve => setTimeout(resolve, 1500));
  }
  throw new Error('授权等待超时，请重新登录。');
}
button.addEventListener('click', async () => {
  const popup = window.open('about:blank', '_blank');
  if (popup) popup.opener = null;
  showState('正在打开 Console…', true);
  link.hidden = true;
  try {
    const response = await fetch(new URL('_tiana/login', base), {method:'POST', headers:{'X-Tiana-Bootstrap':'1'}, cache:'no-store'});
    if (!response.ok) throw new Error('无法发起授权，请重试。');
    const data = await response.json();
    if (data.state === 'ready') {popup?.close();openApp();return;}
    const uri = new URL(data.verification_url);
    if (uri.protocol !== 'https:') throw new Error('授权地址无效。');
    link.href = uri.href; link.hidden = false;
    if (popup) popup.location.replace(uri.href);
    showState('请在 Console 完成授权。', true);
    await poll();
  } catch (error) {popup?.close();showState(error.message, false, true);}
});
function resumeAuthorization() {
  const launch = new URLSearchParams(location.hash.slice(1)).get('tiana_launch');
  if (launch) {
    history.replaceState(null, '', location.pathname + location.search);
    showState('正在使用已登录的账号打开预览…', true);
    fetch(new URL('_tiana/local-login', base), {method:'POST', headers:{'X-Tiana-Bootstrap':'1','X-Tiana-Launch':launch}, cache:'no-store'})
      .then(response => {if (!response.ok) throw new Error(); openApp();})
      .catch(() => showState('本地授权链接已失效，请重新启动预览或使用 Console 登录。', false, true));
  } else fetch(new URL('_tiana/session', base), {cache:'no-store'}).then(async response => {
    if (response.status === 200) openApp();
    else if (response.status === 202) {showState('请在 Console 完成授权。', true);await poll();}
  }).catch(() => showState('预览授权暂不可用，请重试。', false, true));
}
window.addEventListener('hashchange', resumeAuthorization);
resumeAuthorization();
