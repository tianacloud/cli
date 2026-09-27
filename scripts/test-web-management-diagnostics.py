#!/usr/bin/env python3
"""Actual Web list/delete command modes and request diagnostics over isolated TLS."""
import argparse,hashlib,http.server,json,os,re,signal,socket,ssl,subprocess,tempfile,threading,time
from pathlib import Path
from urllib.parse import urlsplit
p=argparse.ArgumentParser();p.add_argument('binary',type=Path);p.add_argument('--output',type=Path,required=True);p.add_argument('--timer-only',action='store_true');a=p.parse_args();binary=a.binary.resolve();state={};rows=[]
ID='web-AAAAAAAAAAAAAAAAAAAAAAAA'
class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self,*_):pass
    def do_GET(self):self.respond()
    def do_DELETE(self):self.respond()
    def respond(self):
        self.rfile.read(int(self.headers.get('Content-Length',0)));path=urlsplit(self.path).path;rid=self.headers.get('X-Request-ID');assert rid
        assert self.headers.get('Authorization')=='Bearer fixture-access'
        web={'id':ID,'name':'Fixture','owner_id':'owner','tenant_id':'tenant'};code=200
        if path=='/api/v1/web-projects':
            stage='page2' if '?' in self.path else 'page1';item=dict(web,id=ID+('b' if stage=='page2' else 'a'));body={'items':[item],'next_cursor':item['id'] if stage=='page1' else ''}
        elif self.command=='DELETE':stage='delete';body={'id':ID,'state':'deleting','requested_at':1}
        elif path.endswith('/deletion'):stage='observe';body={'id':ID,'state':'deleted','requested_at':1,'deleted_at':2}
        else:stage='resolve';body=web
        state['requests'].append({'stage':stage,'request_id':rid,'method':self.command})
        fault=state['fault'] if stage==state['stage'] else ''
        if fault.startswith('http'):
            code=int(fault[4:]);body={'error':{'code':'WEB_UPLOAD_IN_PROGRESS' if code==409 else 'FIXTURE','message':'fixture'}}
        elif fault=='timer-cancel':state['reached'].set()
        elif fault=='semantic':body={}
        elif fault=='cancel':state['reached'].set();state['release'].wait(10);return
        elif fault=='disconnect':self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
        raw=b'{' if fault=='invalid' else json.dumps(body).encode();self.send_response(code);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers()
        try:self.wfile.write(raw)
        except (BrokenPipeError,ConnectionResetError):pass

def check(command,fmt,diag,stage,fault,root,origin,ca):
    state.clear();state.update(requests=[],stage=stage,fault=fault,reached=threading.Event(),release=threading.Event())
    creds=root/'credentials.json';creds.write_text(json.dumps({'credentials':{origin:{'access_token':'fixture-access','refresh_token':'fixture-refresh','expires_at':'2099-01-01T00:00:00Z','user':{'user_id':'owner','tenant_id':'tenant'}}}}));creds.chmod(0o600)
    env={k:v for k,v in os.environ.items() if not k.startswith('TIANA_') and 'proxy' not in k.lower()};env.update(TIANA_API_ORIGIN=origin,TIANA_CA_FILE=str(ca),TIANA_CREDENTIALS_FILE=str(creds),TIANA_PENDING_COMMAND_FILE=str(root/'pending.json'))
    if diag:env['TIANA_DIAGNOSTICS']='1'
    argv=['web',command]+([ID,'-f','--wait'] if command=='delete' else [])+(['--json'] if fmt=='json' else [])
    if fault=='input':argv.append('unexpected')
    writer=None
    if fault=='pipe':reader,writer=os.pipe();os.close(reader)
    child=subprocess.Popen([str(binary),*argv],env=env,stdin=subprocess.DEVNULL,stdout=writer if writer is not None else subprocess.PIPE,stderr=subprocess.PIPE)
    if writer is not None:os.close(writer)
    try:
        if fault in ['cancel','timer-cancel']:
            assert state['reached'].wait(10)
            if fault=='timer-cancel':time.sleep(.1)
            child.send_signal(signal.SIGINT)
        out,err=child.communicate(timeout=20)
    finally:
        state['release'].set()
        if child.poll() is None:child.kill();child.wait()
    out,err=(out or b'').decode(),err.decode();requests=list(state['requests']);failures=[]
    want=0 if not fault else 2 if fault=='input' else 130 if fault in ['cancel','timer-cancel'] else 4 if stage=='delete' and fault in ['http503','invalid','disconnect','semantic'] else 1
    if child.returncode!=want:failures.append(f'exit {child.returncode} expected {want}')
    ids={r['request_id'] for r in requests};printed=set(re.findall(r'req-[A-Za-z0-9_-]+',err))
    if printed-ids or (diag or want!=0) and ids-printed:failures.append('stderr requestID mismatch')
    if not diag and want==0 and printed:failures.append('default success ID noise')
    if fmt=='json' and fault not in ['pipe','input']:
        result=json.loads(out)
        if want and requests and result.get('error',{}).get('request_id')!=requests[-1]['request_id']:failures.append('JSON original requestID missing')
    if fault=='input' and requests:failures.append('local rejection used network')
    if sum(r['stage']=='delete' for r in requests)>1:failures.append('blind deletion replay')
    if not fault and len(requests)!=(3 if command=='delete' else 2):failures.append('unexpected normal route count')
    if fault=='http409' and (root/'pending.json').exists():failures.append('definitive conflict retained blocking intent')
    rows.append({'command':command,'format':fmt,'diagnostics':diag,'stage':stage,'fault':fault or 'none','exit':child.returncode,'requests':requests,'failures':failures,'pending_retained':(root/'pending.json').exists(),'stdout_sha256':hashlib.sha256(out.encode()).hexdigest(),'stderr_sha256':hashlib.sha256(err.encode()).hexdigest()})
    a.output.write_text(json.dumps({'in_progress':True,'rows':rows},indent=2)+'\n')

with tempfile.TemporaryDirectory(prefix='tiana-app-publish-') as temp:
    root=Path(temp)
    def openssl(argv): subprocess.run(['openssl',*argv],cwd=root,check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    openssl(['req','-x509','-newkey','rsa:2048','-nodes','-keyout','ca.key','-out','ca.pem','-days','1','-subj','/CN=App Peer','-addext','basicConstraints=critical,CA:TRUE'])
    openssl(['req','-new','-newkey','rsa:2048','-nodes','-keyout','leaf.key','-out','leaf.csr','-subj','/CN=localhost'])
    (root/'leaf.ext').write_text('basicConstraints=critical,CA:FALSE\nsubjectAltName=IP:127.0.0.1\nextendedKeyUsage=serverAuth\n')
    openssl(['x509','-req','-in','leaf.csr','-CA','ca.pem','-CAkey','ca.key','-CAcreateserial','-out','leaf.pem','-days','1','-extfile','leaf.ext'])
    server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Peer);server.daemon_threads=True
    tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(root/'leaf.pem',root/'leaf.key');server.socket=tls.wrap_socket(server.socket,server_side=True)
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    try:
        for command in ['list','delete']:
            if a.timer_only and command!='delete':continue
            stages=['page1','page2'] if command=='list' else ['resolve','delete','observe']
            cases=[('', ''),('', 'pipe'),('', 'input')]+[(stage,fault) for stage in stages for fault in ['http503','invalid','semantic','disconnect','cancel']]
            if command=='delete':cases += [('delete','http409')]
            if a.timer_only:cases=[('delete','timer-cancel')]
            for fmt in ['text','json']:
                for diag in [False,True]:
                    for stage,fault in cases:
                        with tempfile.TemporaryDirectory(dir=root) as case:check(command,fmt,diag,stage,fault,Path(case),f'https://127.0.0.1:{server.server_port}',root/'ca.pem')
    finally:server.shutdown();server.server_close();thread.join()
result={'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'rows':rows,'passed':sum(not r['failures'] for r in rows),'failed':sum(bool(r['failures']) for r in rows),'fixture_cleaned':True}
a.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:result[k] for k in ['passed','failed','fixture_cleaned']}))
for r in rows:
    if r['failures']:print(json.dumps(r))
raise SystemExit(bool(result['failed']))
