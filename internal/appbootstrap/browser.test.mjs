import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const {chromium}=await import(pathToFileURL(process.argv[2]).href);
const origin=process.argv[3];
const browser=await chromium.launch({headless:true});
try {
 const context=await browser.newContext();
 await context.route('https://console.example/authorize', route=>route.fulfill({contentType:'text/html',body:'<h1>Explicit Console test fixture</h1>'}));
 const page=await context.newPage();
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const denied=await context.request.get(origin+'/web/billing/_tiana/files/assets/app.js');assert.equal(denied.status(),401);
 await page.goto(origin+'/web/billing/#/transactions');
 await page.locator('#tiana-sign-in').waitFor({state:'visible'});
 const popupPromise=context.waitForEvent('page');
 await page.locator('#tiana-sign-in').click();
 const popup=await popupPromise;
 await popup.waitForURL('https://console.example/authorize');
 assert.equal(await page.locator('#app').isVisible(),false);
 await context.request.get(origin+'/_fixture/approve');
 await page.locator('#app').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.querySelector('#app').dataset.instance==='ins_billing');
 assert.equal(await page.locator('#app').textContent(),'Browser ledger');
 assert.equal(await page.locator('#app').getAttribute('data-route'),'#/transactions');
 assert.equal(await page.locator('#tiana-bar,#tiana-login,#tiana-loading').count(),0);
 await page.reload();
 await page.waitForFunction(()=>document.querySelector('#app').textContent==='Browser ledger');
 assert.equal(await page.locator('#tiana-loading').count(),0);
 await page.route('**/assets/app.css',route=>route.abort());
 await page.reload();
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal(await page.locator('#tiana-loading').count(),0);
 await page.unroute('**/assets/app.css');
 await context.request.get(origin+'/_fixture/fail-next-mount');
 await page.reload();
 await page.getByText('应用加载失败，请刷新后重试。',{exact:true}).waitFor({state:'visible'});
 await page.getByRole('button',{name:'重新加载',exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal(await page.locator('#app').getAttribute('data-failed-mount'),null);
 assert.equal(await page.locator('#tiana-loading').count(),0);
 assert.equal(await page.evaluate(()=>localStorage.length+sessionStorage.length),0);
 assert.equal(await page.evaluate(()=>document.cookie.includes('tiana_preview')),false);
 await context.grantPermissions(['clipboard-read','clipboard-write'],{origin});
 for(const mode of ['http','json','network']) {
  let requestID;
  await context.route('**/_tiana/connection', async route=>{
   requestID=route.request().headers()['x-request-id'];
   if(mode==='network')return route.abort();
   return route.fulfill({status:mode==='http'?503:200,contentType:'application/json',body:mode==='json'?'{':'{}'});
  });
  await page.reload();
  await page.getByRole('button',{name:'复制 Request ID',exact:true}).waitFor({timeout:5000});
  assert.ok(requestID,'request ID must precede fetch');
  await page.getByRole('button',{name:'复制 Request ID',exact:true}).click();
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),requestID);
  assert.ok(await page.getByText('Request ID: '+requestID,{exact:true}).isVisible());
  await context.unroute('**/_tiana/connection');
 }
 await page.reload();
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 await page.evaluate(()=>fetch(new URL('_tiana/logout',location.href),{method:'POST',headers:{'X-Tiana-Bootstrap':'1'}}));
 await page.reload();
 await page.locator('#tiana-sign-in').waitFor({state:'visible'});
 assert.equal((await context.request.get(origin+'/web/billing/_tiana/files/assets/app.js')).status(),401);
 for (const mode of ['http','json','network']) {
  let requestID;
  await context.route('**/_tiana/login',route=>{
   requestID=route.request().headers()['x-request-id'];
   if(mode==='network')return route.abort();
   return route.fulfill({status:mode==='http'?503:200,contentType:'application/json',body:mode==='json'?'{':'{}'});
  });
  await page.reload();
  await page.locator('#tiana-sign-in').click();
  await page.getByRole('button',{name:'复制 Request ID',exact:true}).waitFor({timeout:5000});
  assert.ok(requestID);
  await page.getByRole('button',{name:'复制 Request ID',exact:true}).click();
  assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),requestID);
  await context.unroute('**/_tiana/login');
 }
 let sessionID;
 await context.route('**/_tiana/session',route=>{sessionID=route.request().headers()['x-request-id'];return route.abort();});
 await page.reload();
 await page.getByRole('button',{name:'复制 Request ID',exact:true}).waitFor({timeout:5000});
 assert.ok(sessionID);
 await page.getByRole('button',{name:'复制 Request ID',exact:true}).click();
 assert.equal(await page.evaluate(()=>navigator.clipboard.readText()),sessionID);
 await context.unroute('**/_tiana/session');
 assert.deepEqual(errors,[]);
 console.log('Browser fixture passed: login, gated assets, nested module, runtime connection, hash route, refresh recovery with a clean document, HttpOnly cookie, logout.');
} finally {await browser.close();}
