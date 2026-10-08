#!/usr/bin/env python3
"""Actual preview process failure lifecycle; HTTP client does not claim browser execution."""
import argparse,hashlib,http.client,http.server,json,os,signal,socket,ssl,subprocess,tempfile,threading,time,re
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('binary',type=Path);p.add_argument('--output',type=Path,required=True);p.add_argument('--saved-only',action='store_true');p.add_argument('--extra-only',action='store_true');a=p.parse_args();binary=a.binary.resolve();state={};rows=[]
class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self,*_):pass
    def do_GET(self):self.respond()
    def do_POST(self):self.respond()
    def respond(self):
        self.rfile.read(int(self.headers.get('Content-Length',0)))
        stage='whoami' if self.path.endswith('/whoami') else 'instance' if '/instances/' in self.path else 'poll' if self.path.endswith('/poll') else 'create'
        rid=self.headers.get('X-Request-ID');assert rid
        if not state['fault'].startswith('saved-'):assert rid=='req-preview-login'
        state['requests'].append({'stage':stage,'request_id':rid})
        fault=state['fault'];status=200
        body={'transaction_id':'fixture','client_secret':'fixture-client','user_code':'TEST','verification_uri_complete':state['origin']+'/approve','expires_in':600,'poll_interval':1} if stage=='create' else {'status':'denied'}
        if stage=='whoami':body={'user':{'user_id':'owner','tenant_id':'tenant'},'sync_status':'complete'}
        if stage=='instance':body={'id':'ins_fixture','engine':'sqlite','endpoint_id':'ep-7k3rbz6104qg3qk2z6de5z2vmh','connection':{'hostname':'ep-7k3rbz6104qg3qk2z6de5z2vmh.db.example.test'}}
        if fault=='wrong-origin':body['verification_uri_complete']='https://other.example.test/approve'
        if fault=='http503':status,body=503,{'error':{'code':'FIXTURE','message':'fixture'}}
        if fault=='timeout':time.sleep(13);return
        if fault=='disconnect':self.connection.shutdown(socket.SHUT_RDWR);self.connection.close();return
        if fault in ['pending','shutdown-pending'] and stage=='poll':body={'status':'pending','retry_after':1}
        raw=b'{' if fault=='invalid' else json.dumps(body).encode()
        self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers()
        try:self.wfile.write(raw)
        except (BrokenPipeError,ConnectionResetError):pass

def check(fault,diag,root,origin,ca):
    state.clear();state.update(fault=fault,requests=[],origin=origin)
    build=root/'build';build.mkdir();(build/'app.js').write_text('export function mount(root){root.textContent="Fixture"}')
    manifest={'schema_version':1,'web_id':'fixture','name':'Fixture','rendering':'csr','routing':'hash','entry':'app.js','database_instance_id':'ins_fixture'}
    (build/'tiana.app.json').write_text(json.dumps(manifest))
    env={k:v for k,v in os.environ.items() if not k.startswith('TIANA_') and 'proxy' not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin,TIANA_CA_FILE=str(ca),XDG_CONFIG_HOME=str(root/'config'))
    if diag:env['TIANA_DIAGNOSTICS']='1'
    if fault.startswith('saved-'):
        config=root/'config';store=config/'tiana';store.mkdir(parents=True)
        credential=store/'credentials.json';credential.write_text(json.dumps({'credentials':{origin:{'access_token':'fixture-account','refresh_token':'fixture-refresh','expires_at':'2099-01-01T00:00:00Z','user':{'user_id':'owner','tenant_id':'tenant'}}}}));credential.chmod(0o600)
        env['XDG_CONFIG_HOME']=str(config)
    reserved=socket.socket();reserved.bind(('127.0.0.1',0));port=reserved.getsockname()[1]
    if fault!='bind':reserved.close()
    else:reserved.listen()
    argv=['web','serve','--dir',str(build),'--port',str(port)]
    if fault=='json':argv+=['--json']
    if fault=='input':argv[-1]='0'
    if fault=='manifest':manifest['routing']='history';(build/'tiana.app.json').write_text(json.dumps(manifest))
    writer=None
    if fault in ['pipe','saved-pipe']:reader,writer=os.pipe();os.close(reader)
    child=subprocess.Popen([str(binary),*argv],env=env,stdin=subprocess.DEVNULL,stdout=writer if writer is not None else subprocess.PIPE,stderr=subprocess.PIPE)
    if writer is not None:os.close(writer)
    responses=[];failures=[];pipe_alive=None
    def request(method,path,cookie=None):
        c=http.client.HTTPConnection('127.0.0.1',port,timeout=15)
        headers={'Origin':f'http://127.0.0.1:{port}','X-Tiana-Bootstrap':'1','X-Request-ID':'req-preview-login'}
        if cookie:headers['Cookie']=cookie
        c.request(method,'/web/fixture'+('/'+path if path else '/'),headers=headers);r=c.getresponse();body=r.read();info={'path':path,'status':r.status,'request_id':r.getheader('X-Request-ID')};responses.append(info);cookies=r.getheader('Set-Cookie');c.close();return r.status,body,cookies
    try:
        if fault in ['json','input','manifest','bind','pipe','saved-pipe']:
            out,err=child.communicate(timeout=10)
            expected=2 if fault in ['json','input'] else 1
            if child.returncode!=expected:failures.append('local failure exit')
            if fault in ['pipe','saved-pipe']:
                pipe_alive=False
                if b'broken pipe' not in err:failures.append('pipe diagnostic missing')
                probe=socket.socket()
                try:probe.bind(('127.0.0.1',port))
                except OSError:failures.append('preview listener remained bound')
                finally:probe.close()
        else:
            deadline=time.monotonic()+10
            while True:
                try:
                    with socket.create_connection(('127.0.0.1',port),timeout=.1):break
                except OSError:
                    if time.monotonic()>deadline:raise RuntimeError('preview never listened')
                    time.sleep(.02)
            assert request('GET','')[0]==303
            assert request('GET','_tiana/files/app.js')[0]==401
            status,body,cookie=request('POST','_tiana/login')
            if fault in ['http503','invalid','disconnect','wrong-origin','timeout']:assert status==502
            else:
                assert status==202 and 'HttpOnly' in cookie and 'SameSite=Strict' in cookie
                cookie=cookie.split(';')[0]
                if fault=='denied':
                    for _ in range(100):
                        status,_,_=request('GET','_tiana/session',cookie)
                        if status==401:break
                        time.sleep(.03)
                    assert status==401
                if fault=='pending':
                    assert request('GET','_tiana/session',cookie)[0]==202
                    assert request('POST','_tiana/logout',cookie)[0]==204
                    assert request('GET','_tiana/session',cookie)[0]==401
                assert request('GET','_tiana/files/app.js',cookie)[0]==401
            child.send_signal(signal.SIGINT);out,err=child.communicate(timeout=10)
            if child.returncode!=0:failures.append('SIGINT preview exit differs from existing contract')
        if fault=='saved-pipe':
            ids={r['request_id'] for r in state['requests']}
            if len(state['requests'])<3 or ids-{value.decode() for value in re.findall(rb'req-[A-Za-z0-9_-]+',err)}:failures.append('saved account original IDs missing')
        if (root/'untouched.json').exists():failures.append('CLI account credential mutated')
        if any(r['request_id']!='req-preview-login' for r in responses):failures.append('browser response ID lost')
    finally:
        reserved.close()
        if child.poll() is None:child.kill();child.wait()
    rows.append({'fault':fault,'diagnostics':diag,'exit':child.returncode,'requests':list(state['requests']),'responses':responses,'pipe_process_alive':pipe_alive,'failures':failures,'stdout_sha256':hashlib.sha256(out or b'').hexdigest(),'stderr_sha256':hashlib.sha256(err).hexdigest()})
    a.output.write_text(json.dumps({'in_progress':True,'rows':rows},indent=2)+'\n')

with tempfile.TemporaryDirectory(prefix='tiana-preview-') as temp:
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
        for diag in [False,True]:
            for fault in (['saved-pipe'] if a.saved_only else ['timeout','shutdown-pending'] if a.extra_only else ['http503','invalid','disconnect','wrong-origin','denied','pending','json','input','manifest','bind','pipe']):
                if diag and fault=='timeout':continue
                with tempfile.TemporaryDirectory(dir=root) as case:check(fault,diag,Path(case),f'https://127.0.0.1:{server.server_port}',root/'ca.pem')
    finally:server.shutdown();server.server_close();thread.join()
result={'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'rows':rows,'passed':sum(not r['failures'] for r in rows),'failed':sum(bool(r['failures']) for r in rows),'fixture_cleaned':True}
a.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:result[k] for k in ['passed','failed','fixture_cleaned']}))
for r in rows:
    if r['failures']:print(json.dumps(r))
raise SystemExit(bool(result['failed']))
