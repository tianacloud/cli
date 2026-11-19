import {mkdirSync} from 'node:fs';
import {join} from 'node:path';
import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
const {chromium}=await import(pathToFileURL(process.argv[2]).href);
const origin=process.argv[3];
const evidenceDir=process.env.TIANA_UI_EVIDENCE_DIR||'';

function makeMinimalTTF() {
 const u16=(b,o,v)=>b.writeUInt16BE(v>>>0,o);
 const i16=(b,o,v)=>b.writeInt16BE(v,o);
 const u32=(b,o,v)=>b.writeUInt32BE(v>>>0,o);
 const head=Buffer.alloc(54);
 u32(head,0,0x00010000);u32(head,4,0x00010000);u32(head,12,0x5F0F3CF5);
 u16(head,16,0x000B);u16(head,18,1000);i16(head,36,0);i16(head,38,0);i16(head,40,500);i16(head,42,700);u16(head,46,8);i16(head,48,2);
 const hhea=Buffer.alloc(36);
 u32(hhea,0,0x00010000);i16(hhea,4,800);i16(hhea,6,-200);u16(hhea,10,500);u16(hhea,34,2);
 const maxp=Buffer.alloc(32);
 u32(maxp,0,0x00010000);u16(maxp,4,2);u16(maxp,6,4);u16(maxp,8,1);u16(maxp,14,1);
 const hmtx=Buffer.alloc(8);
 u16(hmtx,0,500);u16(hmtx,4,500);
 const g1=Buffer.from([0,1,0,0,0,0,1,244,2,188,0,2,0,0,0x33,0x33,0x33,100,200,0,100,0,200,0]);
 const loca=Buffer.alloc(6);
 u16(loca,0,0);u16(loca,2,0);u16(loca,4,g1.length/2);
 const cmapSub=Buffer.alloc(32);
 u16(cmapSub,0,4);u16(cmapSub,2,32);u16(cmapSub,6,4);u16(cmapSub,8,4);u16(cmapSub,10,1);u16(cmapSub,12,0);
 u16(cmapSub,14,0x0041);u16(cmapSub,16,0xFFFF);u16(cmapSub,20,0x0041);u16(cmapSub,22,0xFFFF);i16(cmapSub,24,-64);i16(cmapSub,26,1);
 const cmap=Buffer.alloc(12+cmapSub.length);
 u16(cmap,2,1);u16(cmap,4,3);u16(cmap,6,1);u32(cmap,8,12);cmapSub.copy(cmap,12);
 const name=Buffer.alloc(6);u16(name,4,6);
 const os2=Buffer.alloc(78);
 u16(os2,0,0);i16(os2,2,500);u16(os2,4,400);u16(os2,6,5);u16(os2,62,0x0040);u16(os2,64,0x0041);u16(os2,66,0x0041);i16(os2,68,800);i16(os2,70,-200);u16(os2,74,800);u16(os2,76,200);
 const post=Buffer.alloc(32);u32(post,0,0x00030000);
 const tables=[['OS/2',os2],['cmap',cmap],['glyf',g1],['head',head],['hhea',hhea],['hmtx',hmtx],['loca',loca],['maxp',maxp],['name',name],['post',post]];
 const num=tables.length;
 const out=[Buffer.alloc(12+num*16)];
 u32(out[0],0,0x00010000);u16(out[0],4,num);u16(out[0],6,128);u16(out[0],8,3);u16(out[0],10,(num-8)*16);
 let offset=12+num*16;
 tables.forEach(([tag,buf],idx)=>{
  const padded=Buffer.concat([buf,Buffer.alloc((4-(buf.length%4))%4)]);
  let sum=0;for(let i=0;i<padded.length;i+=4)sum=(sum+padded.readUInt32BE(i))>>>0;
  out[0].write(tag,12+idx*16,4,'ascii');
  u32(out[0],12+idx*16+4,sum);u32(out[0],12+idx*16+8,offset);u32(out[0],12+idx*16+12,buf.length);
  out.push(padded);offset+=padded.length;
 });
 return [...Buffer.concat(out)];
}

const browser=await chromium.launch({headless:true,args:['--ignore-certificate-errors-spki-list='+process.argv[5]]});
try {
 const context=await browser.newContext({ignoreHTTPSErrors:true});
 await context.route('https://fonts.googleapis.com/**',r=>r.fulfill({contentType:'text/css',body:''}));
 await context.route('https://console.example/authorize', route=>route.fulfill({contentType:'text/html',body:'<h1>Explicit Console test fixture</h1>'}));
 const page=await context.newPage();
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const denied=await context.request.get(origin+'/web/billing/_tiana/files/assets/app.js');assert.equal(denied.status(),401);
 const authorizeResp=await page.goto(origin+'/web/billing?view=fixture#/transactions');
 const authorizeCSP=authorizeResp.headers()['content-security-policy']||'';
 assert.ok(!authorizeCSP.includes("script-src 'self' https: blob:"),'unauthenticated authorize page must not allow script-src blob:');
 assert.ok(!authorizeCSP.includes("style-src 'self' blob:"),'unauthenticated authorize page must not allow style-src blob:');
 assert.ok(!authorizeCSP.includes("font-src 'self' data: blob:"),'unauthenticated authorize page must not allow font-src blob:');
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
 const wasmAnswer=await page.evaluate(async()=>{
  const bytes=new Uint8Array([0,97,115,109,1,0,0,0,1,5,1,96,0,1,127,3,2,1,0,7,10,1,6,97,110,115,119,101,114,0,0,10,6,1,4,0,65,42,11]);
  const {instance}=await WebAssembly.instantiate(bytes);
  return instance.exports.answer();
 });
 assert.equal(wasmAnswer,42);

 await context.route('https://maps.example.test/sdk.js',r=>r.fulfill({contentType:'text/javascript',body:'window.mapSDKLoaded=true;'}));
 await context.route('https://maps.example.test/icon.svg',r=>r.fulfill({contentType:'image/svg+xml',body:'<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>'}));
 const mapResources=await page.evaluate(async()=>{
  await new Promise((resolve,reject)=>{
   const script=document.createElement('script');
   script.src='https://maps.example.test/sdk.js';script.onload=resolve;script.onerror=()=>reject(new Error('HTTPS map SDK blocked'));
   document.head.append(script);
  });
  await new Promise((resolve,reject)=>{
   const image=new Image();image.onload=resolve;image.onerror=()=>reject(new Error('HTTPS map image blocked'));
   image.src='https://maps.example.test/icon.svg';
  });
  const url=URL.createObjectURL(new Blob(['onmessage=event=>postMessage(event.data+1)'],{type:'text/javascript'}));
  const worker=new Worker(url);
  try {
   const answer=await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error('Map worker timeout')),5000);
    worker.onmessage=event=>{clearTimeout(timer);resolve(event.data)};
    worker.onerror=()=>{clearTimeout(timer);reject(new Error('Map worker blocked'))};
    worker.postMessage(41);
   });
   return {sdk:window.mapSDKLoaded,answer};
  }finally{worker.terminate();URL.revokeObjectURL(url)}
 });
 assert.deepEqual(mapResources,{sdk:true,answer:42});

 const ttfBytes=makeMinimalTTF();
 const blobProbe=await page.evaluate(async(fontData)=>{
  const violations=[];
  const onViolation=e=>violations.push(e.violatedDirective+':'+e.blockedURI);
  document.addEventListener('securitypolicyviolation',onViolation);
  try {
   const classicUrl=URL.createObjectURL(new Blob(['window.__previewBlobClassic=77;'],{type:'text/javascript'}));
   await new Promise((resolve,reject)=>{
    const s=document.createElement('script');
    s.src=classicUrl;s.onload=()=>{s.remove();URL.revokeObjectURL(classicUrl);resolve()};
    s.onerror=()=>reject(new Error('preview classic blob script blocked'));
    document.head.append(s);
   });
   const depUrl=URL.createObjectURL(new Blob(['export const n=100;'],{type:'text/javascript'}));
   const esmUrl=URL.createObjectURL(new Blob(['import {n} from "'+depUrl+'"; export const v=n+23;'],{type:'text/javascript'}));
   const mod=await import(esmUrl);
   URL.revokeObjectURL(depUrl);URL.revokeObjectURL(esmUrl);

   const fontUrl=URL.createObjectURL(new Blob([new Uint8Array(fontData)],{type:'font/ttf'}));
   const ff=new FontFace('PreviewBlobFont','url("'+fontUrl+'")');
   await ff.load();
   document.fonts.add(ff);
   const fontLoaded=ff.status==='loaded'&&document.fonts.check('16px PreviewBlobFont');

   const cssUrl=URL.createObjectURL(new Blob(['#preview-blob-badge{color:rgb(34, 85, 136);font-family:"PreviewBlobFont",sans-serif;padding:8px;margin-top:8px;border:1px solid #258}'],{type:'text/css'}));
   const link=document.createElement('link');
   await new Promise((resolve,reject)=>{
    link.rel='stylesheet';link.href=cssUrl;link.onload=resolve;
    link.onerror=()=>reject(new Error('preview blob stylesheet blocked'));
    document.head.append(link);
   });
   const badge=document.createElement('div');
   badge.id='preview-blob-badge';
   badge.textContent='CLI Preview CSP Blob OK: esm='+mod.v+', font='+fontLoaded;
   document.body.append(badge);
   const color=getComputedStyle(badge).color;
   const violationsBeforeEval=[...violations];

   let evalBlocked=false;
   try{eval('window.__previewEvalRan=1')}catch(err){evalBlocked=err instanceof EvalError}

   return {classic:window.__previewBlobClassic,esm:mod.v,fontLoaded,color,evalBlocked,violationsBeforeEval,cssUrl,fontUrl};
  } finally {
   document.removeEventListener('securitypolicyviolation',onViolation);
  }
 },ttfBytes);
 assert.equal(blobProbe.classic,77);
 assert.equal(blobProbe.esm,123);
 assert.equal(blobProbe.fontLoaded,true);
 assert.equal(blobProbe.color,'rgb(34, 85, 136)');
 assert.equal(blobProbe.evalBlocked,true);
 assert.deepEqual(blobProbe.violationsBeforeEval,[]);
 if(evidenceDir){
  mkdirSync(evidenceDir,{recursive:true});
  await page.screenshot({path:join(evidenceDir,'cli-preview-blob-csp.png'),fullPage:true});
 }
 await page.evaluate(({cssUrl,fontUrl})=>{
  document.querySelector('#preview-blob-badge')?.remove();
  document.querySelectorAll('link[href^="blob:"]').forEach(el=>el.remove());
  URL.revokeObjectURL(cssUrl);URL.revokeObjectURL(fontUrl);
  delete window.__previewBlobClassic;
 },{cssUrl:blobProbe.cssUrl,fontUrl:blobProbe.fontUrl});

 assert.equal(await page.locator('#app').getAttribute('data-route'),'#/transactions');
 assert.equal(new URL(page.url()).pathname,'/web/billing/');
 assert.equal(new URL(page.url()).search,'?view=fixture');
 assert.equal(await page.locator('#tiana-bar,#tiana-login,#tiana-loading').count(),0);
 await page.reload();
 await page.waitForFunction(()=>document.querySelector('#app').textContent==='Browser ledger');
 assert.equal(await page.locator('#tiana-loading').count(),0);
 assert.equal(await page.locator('#app').getAttribute('data-user'),'fixture-owner');
 assert.equal(await page.locator('#app').getAttribute('data-tenant'),'fixture-tenant');
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
 assert.equal(await page.evaluate(()=>typeof tiana.functions.websocket),'function');
 await page.evaluate(async()=>{
  window.savedFunctions=tiana.functions;
  window.liveSocket=await savedFunctions.websocket('fn-orders','/socket?room=one',{protocols:'business.v1'});
  if(!(liveSocket instanceof WebSocket)||liveSocket.readyState!==WebSocket.OPEN||liveSocket.protocol!=='business.v1')throw new Error('native socket contract');
  const echoed=new Promise(resolve=>liveSocket.addEventListener('message',event=>resolve(event.data),{once:true}));
  liveSocket.send('preview');if(await echoed!=='preview')throw new Error('native message failed');
  if(!(await savedFunctions.fetch('fn-orders','/while-open')).ok)throw new Error('HTTP failed');
  window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));
  window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));
 });
 await page.waitForFunction(()=>liveSocket.readyState===WebSocket.CLOSED);
 await page.evaluate(async()=>{
  window.restoredSocket=await savedFunctions.websocket('fn-orders','/socket',{protocols:['business.v1']});
  window.dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));
 });
 await page.waitForFunction(()=>restoredSocket.readyState===WebSocket.CLOSED);
 // Exercise provider renewal, concurrent callers and abort isolation in a real browser.
 let connections=0;
 await page.route('**/_tiana/connection',async route=>{
   connections++;
   const response=await route.fetch();
   const data=await response.json();
   data.tianaToken='fixture-access-'+connections;
   data.expires_at=new Date(Date.now()+(connections===1?1000:3600000)).toISOString();
   if(connections>1)await new Promise(resolve=>setTimeout(resolve,50));
   await route.fulfill({response,json:data});
 });
 await page.reload();
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 const renewal=await page.evaluate(async()=>{
   const controller=new AbortController();
   const cancelled=window.tiana.auth.getAccessToken({signal:controller.signal}).catch(error=>error.name);
   const one=window.tiana.auth.getAccessToken();
   const two=window.tiana.auth.getAccessToken();
   controller.abort();
   return Promise.all([cancelled,one,two]);
 });
 assert.deepEqual(renewal,['AbortError','fixture-access-2','fixture-access-2']);
 assert.equal(connections,2);
 await page.unroute('**/_tiana/connection');
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
 await page.evaluate(()=>fetch(new URL('/web/billing/_tiana/logout',location.origin),{method:'POST',headers:{'X-Tiana-Bootstrap':'1'}}));
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
 const launch=await (await context.request.get(origin+'/_fixture/launch')).json();
 await page.goto(launch.url);
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal(await page.evaluate(()=>window.tiana.auth.getAccessToken()),'fixture-saved-access');
 assert.equal(await page.locator('#app').getAttribute('data-user'),'saved-owner');
 assert.equal(await page.locator('#app').getAttribute('data-tenant'),'saved-tenant');
 assert.equal(new URL(page.url()).hash,'');
 assert.equal(await page.locator('#tiana-sign-in').count(),0);
 const stranger=await browser.newContext();
 const replay=await stranger.newPage();await replay.goto(launch.url);
 await replay.getByText('本地授权链接已失效，请重新启动预览或使用 Console 登录。',{exact:false}).waitFor();
 assert.equal((await stranger.request.get(origin+'/web/billing/_tiana/app')).status(),401);
 await stranger.close();
 assert.deepEqual(errors,[]);
 console.log('Browser fixture passed: login, gated assets, nested module, runtime connection, hash route, refresh recovery with a clean document, HttpOnly cookie, logout.');
} finally {await browser.close();}
