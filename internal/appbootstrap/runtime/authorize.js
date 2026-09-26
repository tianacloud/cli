const base = new URL('../', import.meta.url);
const button = document.querySelector('#tiana-sign-in');
const status = document.querySelector('#tiana-status');
const link = document.querySelector('#tiana-auth-link');
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
  button.disabled = true;
  try {
    const response = await fetch(new URL('_tiana/login', base), {method:'POST', headers:{'X-Tiana-Bootstrap':'1'}, cache:'no-store'});
    if (!response.ok) throw new Error('无法发起授权，请重试。');
    const data = await response.json();
    if (data.state === 'ready') {popup?.close();openApp();return;}
    const uri = new URL(data.verification_url);
    if (uri.protocol !== 'https:') throw new Error('授权地址无效。');
    link.href = uri.href; link.hidden = false;
    if (popup) popup.location.replace(uri.href);
    status.textContent = '请在 Console 完成授权。';
    await poll();
  } catch (error) {popup?.close();status.textContent = error.message;button.disabled = false;}
});
fetch(new URL('_tiana/session', base), {cache:'no-store'}).then(async response => {
  if (response.status === 200) openApp();
  else if (response.status === 202) {button.disabled = true;await poll();}
}).catch(() => {status.textContent='预览授权暂不可用，请重试。';button.disabled=false;});
