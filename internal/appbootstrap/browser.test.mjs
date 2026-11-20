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
 const unauthSessionPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/_tiana/session');
 const authorizeResp=await page.goto(origin+'/web/billing?view=fixture#/transactions');
 const unauthSessionResp=await unauthSessionPromise;
 assert.equal(new URL(page.url()).pathname,'/_tiana/authorize');
 const authorizeCSP=authorizeResp.headers()['content-security-policy']||'';
 assert.ok(!authorizeCSP.includes("script-src 'self' https: blob:"),'unauthenticated authorize page must not allow script-src blob:');
 assert.ok(!authorizeCSP.includes("style-src 'self' blob:"),'unauthenticated authorize page must not allow style-src blob:');
 assert.ok(!authorizeCSP.includes("font-src 'self' data: blob:"),'unauthenticated authorize page must not allow font-src blob:');
 assert.equal((await authorizeResp.allHeaders())['clear-site-data'],undefined,'/_tiana/authorize itself must not emit Clear-Site-Data');
 const authRedirectReq=authorizeResp.request().redirectedFrom();
 assert.ok(authRedirectReq,'expected 303 redirect from /web/billing/ to /_tiana/authorize');
 assert.equal(new URL(authRedirectReq.url()).pathname,'/web/billing/');
 const authRedirectResp=await authRedirectReq.response();
 assert.equal(authRedirectResp.status(),303);
 assert.equal((await authRedirectResp.allHeaders())['clear-site-data'],undefined,'unauthenticated /web/billing/ 303 must not emit Clear-Site-Data');
 assert.equal(unauthSessionResp.status(),401);
 assert.equal((await unauthSessionResp.request().allHeaders())['x-tiana-bootstrap'],'1');
 assert.equal((await unauthSessionResp.allHeaders())['clear-site-data'],'"storage"','same-origin X-Tiana-Bootstrap session 401 must emit Clear-Site-Data: "storage"');
 const canonRedirectReq=authRedirectReq.redirectedFrom();
 assert.ok(canonRedirectReq,'expected 307 redirect from /web/billing to /web/billing/');
 assert.equal(new URL(canonRedirectReq.url()).pathname,'/web/billing');
 const canonRedirectResp=await canonRedirectReq.response();
 assert.equal(canonRedirectResp.status(),307);
 assert.equal((await canonRedirectResp.allHeaders())['clear-site-data'],undefined,'canonical 307 redirect must not emit Clear-Site-Data');
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

 // Verify same-origin Service Worker registration within /web/billing/ and rejection of root scope /.
 const swProbe=await page.evaluate(async()=>{
  const swURL=window.__previewSwScriptURL;
  if(!swURL||!swURL.endsWith('/web/billing/_tiana/files/assets/sw.js'))throw new Error('unexpected sw URL: '+swURL);
  let rootScopeError=null;
  try {
   await navigator.serviceWorker.register(swURL,{scope:'/',updateViaCache:'none'});
  } catch (err) {
   rootScopeError={name:err.name,message:err.message};
  }
  const reg=await navigator.serviceWorker.register(swURL,{scope:'/web/billing/',updateViaCache:'none'});
  await navigator.serviceWorker.ready;
  if(!navigator.serviceWorker.controller){
   await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error('SW controller claim timeout')),5000);
    navigator.serviceWorker.addEventListener('controllerchange',()=>{clearTimeout(timer);resolve();},{once:true});
    if(navigator.serviceWorker.controller){clearTimeout(timer);resolve();}
   });
  }
  const pingResp=await fetch('/web/billing/_sw_probe/ping',{cache:'no-store'});
  const pingText=await pingResp.text();
  const pingHeader=pingResp.headers.get('x-preview-sw');
  const card=document.createElement('section');
  card.id='preview-sw-card';
  card.style.cssText='margin-top:16px;padding:12px 16px;border:1px solid #0f766e;border-radius:8px;font:14px/1.5 system-ui,sans-serif;background:#f0fdfa;color:#134e4a;max-width:640px';
  const title=document.createElement('h2');
  title.style.cssText='margin:0 0 8px;font-size:15px';
  title.textContent='CLI Preview Service Worker Active (R1)';
  const details=document.createElement('pre');
  details.style.cssText='margin:0 0 8px;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace';
  details.textContent=[
   'scope: '+new URL(reg.scope).pathname,
   'controller: '+new URL(navigator.serviceWorker.controller.scriptURL).pathname,
   'subresource fetch: '+pingResp.status+' '+pingText+' (X-Preview-SW='+pingHeader+')',
   'root scope (/) rejected: '+(rootScopeError?rootScopeError.name+': '+rootScopeError.message:'NONE')
  ].join('\n');
  const img=new Image();
  img.id='preview-sw-img';
  img.alt='SW intercepted SVG badge';
  await new Promise((resolve,reject)=>{
   img.onload=resolve;
   img.onerror=()=>reject(new Error('SW intercepted <img> subresource failed'));
   img.src='/web/billing/_sw_probe/badge.svg';
  });
  card.append(title,details,img);
  document.body.append(card);
  return {
   scopePath:new URL(reg.scope).pathname,
   controllerPath:new URL(navigator.serviceWorker.controller.scriptURL).pathname,
   pingStatus:pingResp.status,
   pingText,
   pingHeader,
   imgWidth:img.naturalWidth,
   imgHeight:img.naturalHeight,
   rootScopeError
  };
 });
 assert.equal(swProbe.scopePath,'/web/billing/');
 assert.equal(swProbe.controllerPath,'/web/billing/_tiana/files/assets/sw.js');
 assert.equal(swProbe.pingStatus,200);
 assert.equal(swProbe.pingText,'sw-intercepted-ok');
 assert.equal(swProbe.pingHeader,'active');
 assert.equal(swProbe.imgWidth,140);
 assert.equal(swProbe.imgHeight,28);
 assert.ok(swProbe.rootScopeError,'root scope / registration must be rejected');
 assert.equal(swProbe.rootScopeError.name,'SecurityError');
 if(evidenceDir){
  mkdirSync(evidenceDir,{recursive:true});
  await page.screenshot({path:join(evidenceDir,'r1-cli-sw-active.png'),fullPage:true});
 }
 await page.evaluate(async()=>{
  document.querySelector('#preview-sw-card')?.remove();
  const regs=await navigator.serviceWorker.getRegistrations();
  await Promise.all(regs.map(r=>r.unregister()));
 });

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

 // R2 security regression: cross-logout and cross-restart/re-login SW isolation & eviction WITHOUT manual registration.unregister().
 await context.request.post(origin+'/_fixture/sw-spy-reset');
 const registerOldProbeSW=async()=>page.evaluate(async()=>{
  const swURL=window.__previewOldProbeSwURL;
  if(!swURL||!swURL.endsWith('/web/billing/_tiana/files/assets/old-probe-sw.js'))throw new Error('unexpected old probe sw URL: '+swURL);
  const reg=await navigator.serviceWorker.register(swURL,{scope:'/web/billing/',updateViaCache:'none'});
  await navigator.serviceWorker.ready;
  if(!navigator.serviceWorker.controller||new URL(navigator.serviceWorker.controller.scriptURL).pathname!=='/web/billing/_tiana/files/assets/old-probe-sw.js'){
   await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error('old probe SW controller claim timeout')),5000);
    const check=()=>{if(navigator.serviceWorker.controller&&new URL(navigator.serviceWorker.controller.scriptURL).pathname==='/web/billing/_tiana/files/assets/old-probe-sw.js'){clearTimeout(timer);navigator.serviceWorker.removeEventListener('controllerchange',check);resolve();}};
    navigator.serviceWorker.addEventListener('controllerchange',check);
    check();
   });
  }
  const baseline=await fetch('/web/billing/_sw_spy_baseline',{cache:'no-store'});
  return {scope:new URL(reg.scope).pathname,baselineStatus:baseline.status,baselineText:await baseline.text(),regCount:(await navigator.serviceWorker.getRegistrations()).length};
 });

 // Case A: Server-side logout evicts old probe SW via Clear-Site-Data: "storage" (no manual unregister).
 // Note: server-side logout eviction applies when the logout request reaches the server; cross-restart /
 // re-authorization eviction in Case B works unconditionally via out-of-scope /_tiana/authorize.
 const logoutBaseline=await registerOldProbeSW();
 assert.equal(logoutBaseline.scope,'/web/billing/');
 assert.equal(logoutBaseline.baselineStatus,200);
 assert.equal(logoutBaseline.baselineText,'old-probe-active');
 assert.equal(logoutBaseline.regCount,1);
 const baselineEventsA=await (await context.request.get(origin+'/_fixture/sw-spy-events')).json();
 assert.ok(Array.isArray(baselineEventsA)&&baselineEventsA.some(ev=>ev.kind==='baseline'),'old probe SW must be verified active before logout');
 const logoutRespPromise=page.waitForResponse(r=>r.url().endsWith('/web/billing/_tiana/logout'));
 const logoutCheck=await page.evaluate(async()=>{
  const resp=await fetch('/web/billing/_tiana/logout',{method:'POST',headers:{'X-Tiana-Bootstrap':'1'}});
  const regs=await navigator.serviceWorker.getRegistrations();
  return {status:resp.status,regCount:regs.length};
 });
 const logoutResp=await logoutRespPromise;
 assert.equal(logoutCheck.status,204);
 assert.equal((await logoutResp.allHeaders())['clear-site-data'],'"storage"');
 assert.equal(logoutCheck.regCount,0,'logout Clear-Site-Data must evict old probe SW without manual unregister');

 // Case B: Simulated server restart (sessions wiped without logout or unregister), followed by AuthorizeLocalAccount #tiana_launch=<secret2> re-login.
 const launch1=await (await context.request.get(origin+'/_fixture/launch?token=fixture-session1-access')).json();
 assert.equal(new URL(launch1.url).pathname,'/_tiana/authorize');
 await page.goto(launch1.url);
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal((await page.evaluate(()=>window.tiana.connection())).tianaToken,'fixture-session1-access');
 await context.request.post(origin+'/_fixture/sw-spy-reset');
 const restartBaseline=await registerOldProbeSW();
 assert.equal(restartBaseline.scope,'/web/billing/');
 assert.equal(restartBaseline.baselineStatus,200);
 assert.equal(restartBaseline.baselineText,'old-probe-active');
 assert.equal(restartBaseline.regCount,1);
 const baselineEventsB=await (await context.request.get(origin+'/_fixture/sw-spy-events')).json();
 assert.ok(Array.isArray(baselineEventsB)&&baselineEventsB.some(ev=>ev.kind==='baseline'),'old probe SW must be verified active before simulated restart');

 // Restart server (clears all server-side sessions) while old probe SW remains registered and controlling /web/billing/.
 assert.equal((await context.request.post(origin+'/_fixture/restart')).status(),204);
 assert.equal(await page.evaluate(async()=>(await navigator.serviceWorker.getRegistrations()).length),1,'old probe SW must still be registered prior to re-login');
 const launch2=await (await context.request.get(origin+'/_fixture/launch?token=fixture-session2-access')).json();
 const launch2URL=new URL(launch2.url);
 assert.equal(launch2URL.pathname,'/_tiana/authorize','AuthorizeLocalAccount must point outside /web/<id>/ SW scope');
 const secret2=new URLSearchParams(launch2URL.hash.slice(1)).get('tiana_launch');
 assert.ok(secret2&&secret2.length>10,'missing launch secret2');

 // Reset spy log right before opening the new launch link; NEVER call registration.unregister().
 await context.request.post(origin+'/_fixture/sw-spy-reset');
 await page.goto(launch2.url);
 await page.waitForFunction(()=>document.querySelector('#app')?.textContent==='Browser ledger');
 const session2=await page.evaluate(async()=>{
  const conn=await window.tiana.connection();
  const regs=await navigator.serviceWorker.getRegistrations();
  return {
   pathname:location.pathname,
   hash:location.hash,
   token:conn.tianaToken,
   regCount:regs.length,
   controller:navigator.serviceWorker.controller?navigator.serviceWorker.controller.scriptURL:null
  };
 });
 assert.equal(session2.pathname,'/web/billing/');
 assert.equal(session2.hash,'');
 assert.equal(session2.token,'fixture-session2-access');
 assert.equal(session2.regCount,0,'old probe SW must be completely evicted after re-login without manual unregister');
 assert.equal(session2.controller,null,'build 2 document must not be controlled by old probe SW');
 const spyAfterRestart=await (await context.request.get(origin+'/_fixture/sw-spy-events')).json();
 const events=Array.isArray(spyAfterRestart)?spyAfterRestart:[];
 const capturedLaunch=events.filter(ev=>(ev.url||'').includes('tiana_launch')||(ev.url||'').includes(secret2)||(ev.launchHeader||'').includes(secret2));
 const capturedLocalLogin=events.filter(ev=>(ev.url||'').includes('local-login'));
 const capturedConnection=events.filter(ev=>(ev.url||'').includes('_tiana/connection')||(ev.body||'').includes('fixture-session2-access'));
 assert.deepEqual(capturedLaunch,[],'old probe SW must never capture #tiana_launch=<secret2>');
 assert.deepEqual(capturedLocalLogin,[],'old probe SW must never capture local-login');
 assert.deepEqual(capturedConnection,[],'old probe SW must never capture session 2 connection or tianaToken');
 assert.deepEqual(events,[],'old probe SW must capture zero events across restart and re-login');

 if(evidenceDir){
  await page.evaluate(({launch2Path,regCount,eventCount,token})=>{
   const card=document.createElement('section');
   card.id='preview-sw-r2-card';
   card.style.cssText='margin-top:16px;padding:12px 16px;border:1px solid #1d4ed8;border-radius:8px;font:14px/1.5 system-ui,sans-serif;background:#eff6ff;color:#1e3a8a;max-width:680px';
   const title=document.createElement('h2');
   title.style.cssText='margin:0 0 8px;font-size:15px';
   title.textContent='CLI Preview SW Isolation & Eviction Verified (R2)';
   const details=document.createElement('pre');
   details.style.cssText='margin:0;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace';
   details.textContent=[
    'authorize entry path (outside /web/billing/ scope): '+launch2Path,
    'manual registration.unregister(): NOT CALLED',
    'session 2 tianaToken verified: '+(token==='fixture-session2-access'),
    'old probe SW captured events (launch/local-login/connection): '+eventCount,
    'remaining SW registrations in session 2: '+regCount
   ].join('\n');
   card.append(title,details);
   document.body.append(card);
  },{launch2Path:launch2URL.pathname,regCount:session2.regCount,eventCount:events.length,token:session2.token});
  mkdirSync(evidenceDir,{recursive:true});
  await page.screenshot({path:join(evidenceDir,'r2-cli-sw-isolation-eviction.png'),fullPage:true});
  await page.evaluate(()=>document.querySelector('#preview-sw-r2-card')?.remove());
 }

 // R3 regression: authenticated session visiting "/" and "/_tiana/authorize" must preserve localStorage and active /web/billing/ SW.
 const r3Setup=await page.evaluate(async()=>{
  localStorage.setItem('keep','1');
  const swURL=window.__previewSwScriptURL;
  if(!swURL||!swURL.endsWith('/web/billing/_tiana/files/assets/sw.js'))throw new Error('unexpected sw URL: '+swURL);
  const reg=await navigator.serviceWorker.register(swURL,{scope:'/web/billing/',updateViaCache:'none'});
  await navigator.serviceWorker.ready;
  if(!navigator.serviceWorker.controller||new URL(navigator.serviceWorker.controller.scriptURL).pathname!=='/web/billing/_tiana/files/assets/sw.js'){
   await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(new Error('R3 SW controller claim timeout')),5000);
    const check=()=>{if(navigator.serviceWorker.controller&&new URL(navigator.serviceWorker.controller.scriptURL).pathname==='/web/billing/_tiana/files/assets/sw.js'){clearTimeout(timer);navigator.serviceWorker.removeEventListener('controllerchange',check);resolve();}};
    navigator.serviceWorker.addEventListener('controllerchange',check);
    check();
   });
  }
  const regs=await navigator.serviceWorker.getRegistrations();
  return {keep:localStorage.getItem('keep'),scope:new URL(reg.scope).pathname,regCount:regs.length};
 });
 assert.equal(r3Setup.keep,'1');
 assert.equal(r3Setup.scope,'/web/billing/');
 assert.equal(r3Setup.regCount,1);

 // 1) Navigate to same-origin root "/" while authenticated: 307 -> /web/billing/ (200), preserving localStorage & SW.
 const rootNavResp=await page.goto(origin+'/');
 await page.waitForFunction(()=>location.pathname==='/web/billing/'&&document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal(rootNavResp.status(),200,'GET / -> /web/billing/ must settle with 200');
 assert.equal((await rootNavResp.allHeaders())['clear-site-data'],undefined,'authenticated /web/billing/ must not emit Clear-Site-Data');
 const rootRedirectReq=rootNavResp.request().redirectedFrom();
 assert.ok(rootRedirectReq,'GET / must 307-redirect to /web/billing/');
 assert.equal(new URL(rootRedirectReq.url()).pathname,'/');
 const rootRedirectResp=await rootRedirectReq.response();
 assert.equal(rootRedirectResp.status(),307);
 assert.equal((await rootRedirectResp.allHeaders())['clear-site-data'],undefined,'GET / 307 must not emit Clear-Site-Data');
 const afterRootNav=await page.evaluate(async()=>{
  const regs=await navigator.serviceWorker.getRegistrations();
  const ping=await fetch('/web/billing/_sw_probe/ping',{cache:'no-store'});
  return {
   keep:localStorage.getItem('keep'),
   regCount:regs.length,
   controller:navigator.serviceWorker.controller?new URL(navigator.serviceWorker.controller.scriptURL).pathname:null,
   pingStatus:ping.status,
   pingText:await ping.text()
  };
 });
 assert.equal(afterRootNav.keep,'1','GET / must preserve localStorage for authenticated session');
 assert.equal(afterRootNav.regCount,1,'GET / must preserve active /web/billing/ SW registration');
 assert.equal(afterRootNav.controller,'/web/billing/_tiana/files/assets/sw.js');
 assert.equal(afterRootNav.pingStatus,200);
 assert.equal(afterRootNav.pingText,'sw-intercepted-ok');

 // 2) Navigate directly to "/_tiana/authorize" while authenticated: 200 (no Clear-Site-Data) -> session 200 -> openApp() -> /web/billing/ (200), preserving localStorage & SW.
 const authDocPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/_tiana/authorize'&&r.request().isNavigationRequest());
 const session200Promise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/_tiana/session');
 const appDocPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/'&&r.request().isNavigationRequest());
 await page.goto(origin+'/_tiana/authorize',{waitUntil:'commit'});
 const [authDocResp,session200Resp,appDocResp]=await Promise.all([authDocPromise,session200Promise,appDocPromise]);
 await page.waitForFunction(()=>location.pathname==='/web/billing/'&&document.querySelector('#app')?.textContent==='Browser ledger');
 assert.equal(authDocResp.status(),200);
 assert.equal((await authDocResp.allHeaders())['clear-site-data'],undefined,'GET /_tiana/authorize must not emit Clear-Site-Data');
 assert.equal(session200Resp.status(),200);
 assert.equal((await session200Resp.allHeaders())['clear-site-data'],undefined,'GET /web/billing/_tiana/session 200 must not emit Clear-Site-Data');
 assert.equal(appDocResp.status(),200);
 assert.equal((await appDocResp.allHeaders())['clear-site-data'],undefined,'GET /web/billing/ 200 must not emit Clear-Site-Data');
 const afterAuthNav=await page.evaluate(async()=>{
  const regs=await navigator.serviceWorker.getRegistrations();
  const ping=await fetch('/web/billing/_sw_probe/ping',{cache:'no-store'});
  return {
   keep:localStorage.getItem('keep'),
   regCount:regs.length,
   controller:navigator.serviceWorker.controller?new URL(navigator.serviceWorker.controller.scriptURL).pathname:null,
   pingStatus:ping.status,
   pingText:await ping.text()
  };
 });
 assert.equal(afterAuthNav.keep,'1','GET /_tiana/authorize while authenticated must preserve localStorage');
 assert.equal(afterAuthNav.regCount,1,'GET /_tiana/authorize while authenticated must preserve active /web/billing/ SW registration');
 assert.equal(afterAuthNav.controller,'/web/billing/_tiana/files/assets/sw.js');
 assert.equal(afterAuthNav.pingStatus,200);
 assert.equal(afterAuthNav.pingText,'sw-intercepted-ok');

 if(evidenceDir){
  await page.evaluate(({afterRoot,afterAuth})=>{
   const card=document.createElement('section');
   card.id='preview-sw-r3-card';
   card.style.cssText='margin-top:16px;padding:12px 16px;border:1px solid #047857;border-radius:8px;font:14px/1.5 system-ui,sans-serif;background:#ecfdf5;color:#065f46;max-width:680px';
   const title=document.createElement('h2');
   title.style.cssText='margin:0 0 8px;font-size:15px';
   title.textContent='CLI Preview Authenticated Storage & SW Preserved (R3)';
   const details=document.createElement('pre');
   details.style.cssText='margin:0;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace';
   details.textContent=[
    'after GET / (307 -> /web/billing/ 200): keep='+afterRoot.keep+', regs='+afterRoot.regCount+', controller='+afterRoot.controller+', ping='+afterRoot.pingText,
    'after GET /_tiana/authorize (200 -> session 200 -> /web/billing/ 200): keep='+afterAuth.keep+', regs='+afterAuth.regCount+', controller='+afterAuth.controller+', ping='+afterAuth.pingText
   ].join('\n');
   card.append(title,details);
   document.body.append(card);
  },{afterRoot:afterRootNav,afterAuth:afterAuthNav});
  mkdirSync(evidenceDir,{recursive:true});
  await page.screenshot({path:join(evidenceDir,'r3-cli-sw-storage-preserved.png'),fullPage:true});
  await page.evaluate(()=>document.querySelector('#preview-sw-r3-card')?.remove());
 }

 // R4 regression: real cross-site top-level link clicks to "/" and "/web/billing/" withhold SameSite=Strict cookie,
 // bounce through 303 -> /_tiana/authorize -> same-origin session 200 -> /web/billing/ 200, and MUST preserve localStorage & active SW.
 await context.route('https://external.example/link',route=>route.fulfill({
  contentType:'text/html; charset=utf-8',
  body:'<!doctype html><meta charset="utf-8"><title>External Link Fixture</title><h1>External Page</h1><p><a id="cross-site-root" href="'+origin+'/">Open Preview Root</a></p><p><a id="cross-site-app" href="'+origin+'/web/billing/">Open Preview App</a></p>'
 }));
 const runCrossSiteClick=async(selector,expectRootRedirect)=>{
  await page.goto('https://external.example/link');
  const root307Promise=expectRootRedirect?page.waitForResponse(r=>new URL(r.url()).pathname==='/'&&r.status()===307):null;
  const crossSite303Promise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/'&&r.status()===303);
  const crossSiteAuthPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/_tiana/authorize'&&r.status()===200);
  const sameOriginSessionPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/_tiana/session'&&r.status()===200);
  const recoveredAppPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/'&&r.status()===200);
  await page.locator(selector).click();
  const [root307Resp,crossSite303Resp,crossSiteAuthResp,sameOriginSessionResp,recoveredAppResp]=await Promise.all([
   root307Promise,
   crossSite303Promise,
   crossSiteAuthPromise,
   sameOriginSessionPromise,
   recoveredAppPromise
  ]);
  await page.waitForFunction(()=>location.pathname==='/web/billing/'&&document.querySelector('#app')?.textContent==='Browser ledger');
  if(expectRootRedirect){
   assert.equal(root307Resp.headers()['location'],'/web/billing/','cross-site click to / must 307 to /web/billing/');
   assert.equal((await root307Resp.allHeaders())['clear-site-data'],undefined,'cross-site GET / 307 must not emit Clear-Site-Data');
  }
  const bounceReqHeaders=await crossSite303Resp.request().allHeaders();
  assert.equal(crossSite303Resp.headers()['location'],'/_tiana/authorize','cross-site /web/billing/ without SameSite=Strict cookie must 303 to /_tiana/authorize');
  assert.equal(bounceReqHeaders['cookie'],undefined,'SameSite=Strict session cookie must be withheld on cross-site top-level navigation');
  assert.equal((await crossSite303Resp.allHeaders())['clear-site-data'],undefined,'cross-site GET /web/billing/ 303 must not emit Clear-Site-Data');
  assert.equal((await crossSiteAuthResp.allHeaders())['clear-site-data'],undefined,'GET /_tiana/authorize must not emit Clear-Site-Data');
  const sessionReqHeaders=await sameOriginSessionResp.request().allHeaders();
  assert.equal(sessionReqHeaders['x-tiana-bootstrap'],'1','authorize.js session probe must send X-Tiana-Bootstrap: 1');
  assert.equal((await sameOriginSessionResp.allHeaders())['clear-site-data'],undefined,'authenticated session 200 must not emit Clear-Site-Data');
  assert.equal((await recoveredAppResp.allHeaders())['clear-site-data'],undefined,'recovered /web/billing/ 200 must not emit Clear-Site-Data');
  const state=await page.evaluate(async()=>{
   const regs=await navigator.serviceWorker.getRegistrations();
   const ping=await fetch('/web/billing/_sw_probe/ping',{cache:'no-store'});
   return {
    keep:localStorage.getItem('keep'),
    regCount:regs.length,
    controller:navigator.serviceWorker.controller?new URL(navigator.serviceWorker.controller.scriptURL).pathname:null,
    pingStatus:ping.status,
    pingText:await ping.text()
   };
  });
  assert.equal(state.keep,'1','cross-site click '+selector+' must preserve localStorage');
  assert.equal(state.regCount,1,'cross-site click '+selector+' must preserve active /web/billing/ SW registration');
  assert.equal(state.controller,'/web/billing/_tiana/files/assets/sw.js');
  assert.equal(state.pingStatus,200);
  assert.equal(state.pingText,'sw-intercepted-ok');
  return state;
 };

 const afterCrossSiteRoot=await runCrossSiteClick('#cross-site-root',true);
 const afterCrossSiteApp=await runCrossSiteClick('#cross-site-app',false);

 if(evidenceDir){
  await page.evaluate(({rootState,appState})=>{
   const card=document.createElement('section');
   card.id='preview-sw-r4-card';
   card.style.cssText='margin-top:16px;padding:12px 16px;border:1px solid #6d28d9;border-radius:8px;font:14px/1.5 system-ui,sans-serif;background:#f5f3ff;color:#4c1d95;max-width:720px';
   const title=document.createElement('h2');
   title.style.cssText='margin:0 0 8px;font-size:15px';
   title.textContent='CLI Preview Cross-Site SameSite=Strict Navigation Preserved (R4)';
   const details=document.createElement('pre');
   details.style.cssText='margin:0;white-space:pre-wrap;font:12px/1.5 ui-monospace,monospace';
   details.textContent=[
    'from https://external.example/link -> <a href="/"> (cross-site 307->303->/_tiana/authorize->session 200->/web/billing/ 200):',
    '  keep='+rootState.keep+', regs='+rootState.regCount+', controller='+rootState.controller+', ping='+rootState.pingText,
    'from https://external.example/link -> <a href="/web/billing/"> (cross-site 303->/_tiana/authorize->session 200->/web/billing/ 200):',
    '  keep='+appState.keep+', regs='+appState.regCount+', controller='+appState.controller+', ping='+appState.pingText
   ].join('\n');
   card.append(title,details);
   document.body.append(card);
  },{rootState:afterCrossSiteRoot,appState:afterCrossSiteApp});
  mkdirSync(evidenceDir,{recursive:true});
  await page.screenshot({path:join(evidenceDir,'r4-cli-cross-site-nav-preserved.png'),fullPage:true});
  await page.evaluate(()=>document.querySelector('#preview-sw-r4-card')?.remove());
 }

 // Verify genuine restart/unauthenticated bounce still evicts stale storage & SW synchronously on same-origin X-Tiana-Bootstrap: 1 session 401.
 assert.equal((await context.request.post(origin+'/_fixture/restart')).status(),204);
 await page.goto('https://external.example/link');
 const restart303Promise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/'&&r.status()===303);
 const restartAuthPromise=page.waitForResponse(r=>new URL(r.url()).pathname==='/_tiana/authorize'&&r.status()===200);
 const restart401Promise=page.waitForResponse(r=>new URL(r.url()).pathname==='/web/billing/_tiana/session'&&r.status()===401);
 await page.locator('#cross-site-app').click();
 const [restart303Resp,restartAuthResp,restart401Resp]=await Promise.all([restart303Promise,restartAuthPromise,restart401Promise]);
 await page.locator('#tiana-sign-in').waitFor({state:'visible'});
 assert.equal((await restart303Resp.allHeaders())['clear-site-data'],undefined,'post-restart 303 must not emit Clear-Site-Data');
 assert.equal((await restartAuthResp.allHeaders())['clear-site-data'],undefined,'post-restart /_tiana/authorize must not emit Clear-Site-Data');
 assert.equal((await restart401Resp.request().allHeaders())['x-tiana-bootstrap'],'1');
 assert.equal((await restart401Resp.allHeaders())['clear-site-data'],'"storage"','post-restart same-origin session 401 must emit Clear-Site-Data: "storage"');
 const afterRestartEviction=await page.evaluate(async()=>({
  keep:localStorage.getItem('keep'),
  regCount:(await navigator.serviceWorker.getRegistrations()).length
 }));
 assert.equal(afterRestartEviction.keep,null,'post-restart same-origin session 401 must clear localStorage');
 assert.equal(afterRestartEviction.regCount,0,'post-restart same-origin session 401 must evict stale SW without manual unregister');

 assert.deepEqual(errors,[]);
 console.log('Browser fixture passed: login, gated assets, nested module, runtime connection, hash route, refresh recovery with a clean document, HttpOnly cookie, logout, SW scope, cross-restart/re-login SW eviction, authenticated root/authorize storage preservation & cross-site SameSite=Strict link navigation.');
} finally {await browser.close();}
