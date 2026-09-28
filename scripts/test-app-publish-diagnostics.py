#!/usr/bin/env python3
"""Actual web create/upload processes with isolated verified management/object TLS."""
import argparse, hashlib, http.server, json, os, re, signal, socket, ssl, subprocess, tempfile, threading, time
from pathlib import Path
from urllib.parse import urlsplit

p = argparse.ArgumentParser()
p.add_argument('binary', type=Path)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--object-timeout-only', action='store_true')
p.add_argument('--extra-only', action='store_true')
p.add_argument('--extra-upload-only', action='store_true')
a = p.parse_args()
binary = a.binary.resolve()
state, rows = {}, []

class Peer(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_): pass
    def do_GET(self): self.respond()
    def do_POST(self): self.respond()
    def do_PUT(self): self.respond()
    def respond(self):
        raw = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        path = urlsplit(self.path).path
        rid = self.headers.get('X-Request-ID')
        if path.startswith('/objects/'):
            stage = 'object'
            assert not self.headers.get('Authorization') and not self.headers.get('Cookie')
            assert raw == state['content']
            body = {}
        else:
            assert rid and self.headers.get('Authorization') == 'Bearer app-fixture-access'
            data = json.loads(raw or b'null')
            if '/versions/' not in path:
                stage, body = 'create', {'app_id': 'AAAAAAAAAAAA', 'name': data['name'], 'description':data['description'], 'owner_id':'fixture-user','tenant_id':'fixture-tenant'}
                assert self.command=='POST' and data['request_id']
                state['business_request_id']=data['request_id']
                assert data['name'] == state['name']
            elif path.endswith('/uploads'):
                stage = 'plan'
                f = data['files'][0]
                body = {'files': [{'path': f['path'], 'method': 'PUT', 'url': state['origin']+'/objects/entry',
                    'headers': {'Content-MD5':f['md5'], 'Content-Type':f['content_type'], 'X-Oss-Forbid-Overwrite':'true'},
                    'expires_at':'2099-01-01T00:00:00Z', 'uploaded': state.get('stored',False)}]}
            elif path.endswith('/bootstrap'):
                stage, body = 'bootstrap', dict(state['manifest'],version_id=state['version']['version_id'],application_url='https://apps.example.test/apps/fixture',version_url='https://apps.example.test/apps/fixture?version='+state['version']['version_id'])
            elif path.endswith('/complete'):
                stage, body = 'complete', dict(state['version'], state='published')
            else:
                stage = 'prepare'
                state['manifest'] = data.get('application',{})
                state['version'] = {'tenant_id':'fixture-tenant','app_id':'fixture','version_id':path.split('/')[-1],
                    'fingerprint':hashlib.sha256(raw.strip()).hexdigest(),'state':'published' if state['fault']=='published' else 'uploading'}
                body = dict(state['version'])
        state['requests'].append({'stage':stage,'method':self.command,'request_id':rid,'path':path})
        fault = state['fault'] if stage == state['stage'] else ''
        status = 200
        if fault=='object-timeout':
            if sum(r['stage']=='object' for r in state['requests'])==1:
                state['release'].wait(125);return
            status=503
        elif fault.startswith('http'):
            status, body = int(fault[4:]), {'error': {'code':'FIXTURE_REJECTED','message':'fixture'}}
        elif fault == 'timeout':
            state['release'].wait(32); return
        elif fault == 'cancel':
            state['reached'].set(); state['release'].wait(15); return
        elif fault == 'disconnect':
            self.connection.shutdown(socket.SHUT_RDWR); self.connection.close(); return
        elif fault == 'identity': body['version_id' if stage=='bootstrap' else 'tenant_id'] = 'another'
        elif fault == 'cardinality': body['files'] = []
        elif fault == 'expired' and sum(r['stage']=='plan' for r in state['requests'])==1:
            body['files'][0]['expires_at']='2000-01-01T00:00:00Z'
        elif fault == 'changed': state['entry'].write_bytes(b'changed')
        elif fault == 'uncertain': state['stored']=True; status=503
        elif fault == 'redirect': status=307
        raw = b'{' if fault == 'invalid' else json.dumps(body).encode()
        self.send_response(status)
        if fault == 'redirect': self.send_header('Location',state['origin']+'/forbidden-redirect')
        self.send_header('Content-Length',str(len(raw))); self.send_header('Content-Type','application/json'); self.end_headers()
        try: self.wfile.write(raw)
        except (BrokenPipeError,ConnectionResetError): pass

def check(command, fmt, diag, stage, fault, root, origin, ca):
    content = b'<h1>Fixture</h1>\n'
    build = root/'build'; build.mkdir(); entry = build/'index.html'; entry.write_bytes(content)
    csr = stage=='bootstrap' or fault=='csr'
    if csr:
        entry.rename(build/'app.js'); entry=build/'app.js'
        (build/'tiana.app.json').write_text(json.dumps({'schema_version':1,'app_id':'fixture','name':'Fixture','rendering':'csr','routing':'hash','entry':'app.js','database_instance_id':'ins_fixture'}))
    state.clear();state.update(origin=origin,content=content,entry=entry,requests=[],name='Fixture App',stage=stage,fault=fault,reached=threading.Event(),release=threading.Event())
    creds = root/'credential.json'
    creds.write_text(json.dumps({'credentials':{origin:{'access_token':'app-fixture-access','refresh_token':'app-fixture-refresh','expires_at':'2099-01-01T00:00:00Z','user':{'user_id':'fixture-user','tenant_id':'fixture-tenant'}}}}));creds.chmod(0o600)
    env={k:v for k,v in os.environ.items() if not k.startswith('TIANA_') and 'proxy' not in k.lower()}
    env.update(TIANA_API_ORIGIN=origin,TIANA_CA_FILE=str(ca),TIANA_CREDENTIALS_FILE=str(creds),TIANA_PENDING_COMMAND_FILE=str(root/'pending.json'))
    if diag: env['TIANA_DIAGNOSTICS']='1'
    argv=['web',command,state['name'] if command=='create' else 'fixture']
    if command=='upload': argv+=['--dir',str(build),'--entry','index.html','--upload-ca-file',str(ca)]
    if csr:
        offset=argv.index('--entry');del argv[offset:offset+2]
        argv+=['--version','fixed-csr-v1']
    if fmt=='json': argv+=['--json']
    if fault=='input': argv[2]='' if command=='create' else 'bad/app'
    if fault=='missing-dir': argv[argv.index('--dir')+1]=str(root/'absent')
    if fault=='missing-entry': argv[argv.index('--entry')+1]='missing.html'
    reader=writer=None
    if fault=='pipe': reader,writer=os.pipe();os.close(reader)
    started=time.monotonic()
    child=subprocess.Popen([str(binary),*argv],env=env,stdin=subprocess.DEVNULL,stdout=writer if writer is not None else subprocess.PIPE,stderr=subprocess.PIPE)
    if writer is not None: os.close(writer)
    try:
        if fault=='cancel':
            assert state['reached'].wait(10),f'not reached {stage}'
            child.send_signal(signal.SIGINT)
        out,err=child.communicate(timeout=150 if fault=='object-timeout' else 35)
    finally:
        state['release'].set()
        if child.poll() is None: child.kill();child.wait()
    out,err=(out or b'').decode(),err.decode()
    expected=0
    if fault in ['http503','invalid','disconnect'] and stage not in ['object','bootstrap']: expected=4
    elif fault=='cancel':expected=130
    elif fault=='input':expected=2
    elif fault not in ['', 'published','expired','uncertain','csr','default-name']:expected=1
    if stage in ['complete','create'] and fault=='identity':expected=4
    failures=[]
    if child.returncode!=expected: failures.append(f'exit {child.returncode} expected {expected}')
    elapsed=time.monotonic()-started
    if fault=='object-timeout' and not 119<=elapsed<140:failures.append(f'object deadline elapsed {elapsed}')
    requests=list(state['requests']);mgr=[r for r in requests if r['stage']!='object']; ids={r['request_id'] for r in mgr}
    result=None
    if fmt=='json' and fault!='pipe':
        try:result=json.loads(out)
        except Exception:failures.append('invalid JSON output')
        if result and expected!=0 and mgr and result.get('error',{}).get('request_id')!=mgr[-1]['request_id']:failures.append('JSON missing last management ID')
        if result and expected==0 and result.get('status')!='succeeded':failures.append('missing success')
    printed=set(re.findall(r'req-[A-Za-z0-9_-]+',err))
    if printed-ids:failures.append('invented stderr ID')
    if (diag or expected!=0) and ids-printed:failures.append('missing stderr ID')
    if expected==0 and not diag and printed:failures.append('default success ID noise')
    stages=[r['stage'] for r in requests]
    if command=='create' and len(requests)>1:failures.append('create replay')
    if fault in ['http503','redirect','object-timeout'] and stage=='object' and stages.count('object')!=3:failures.append('object retry bound')
    if fault=='uncertain' and (stages.count('object')!=1 or stages.count('plan')!=2 or stages.count('complete')!=1):failures.append('uncertain object not resumed')
    if fault=='expired' and (stages.count('plan')!=2 or stages.count('object')!=1):failures.append('expired plan not renewed')
    if fault=='published' and stages!=['prepare']:failures.append('published resumed with writes')
    if fault in ['input','missing-dir','missing-entry'] and requests:failures.append('local rejection used network')
    if fault=='changed' and 'object' in stages:failures.append('changed file uploaded')
    if any('forbidden-redirect' in r['path'] for r in requests):failures.append('redirect followed')
    if 'app-fixture-access' in out+err or 'app-fixture-refresh' in out+err:failures.append('credential printed')
    rows.append({'command':command,'format':fmt,'diagnostics':diag,'stage':stage,'fault':fault or 'none','exit':child.returncode,'elapsed_seconds':elapsed,'requests':requests,'failures':failures,'stdout_sha256':hashlib.sha256(out.encode()).hexdigest(),'stderr_sha256':hashlib.sha256(err.encode()).hexdigest()})
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
        for command in ['create','upload']:
            if (a.extra_upload_only or a.object_timeout_only) and command=='create':continue
            cases=[('', ''),('', 'input'),('', 'pipe')]
            if command=='create':cases += [('create',f) for f in ['identity','http403','http409','http503','invalid','disconnect','cancel']]
            else:
                cases += [('',f) for f in ['missing-dir','missing-entry','published']]
                cases += [(s,f) for s in ['prepare','plan','complete'] for f in ['http503','invalid','cancel','identity' if s!='plan' else 'cardinality']]
                cases += [('object',f) for f in ['http503','redirect','uncertain','cancel']]+[('plan','expired'),('plan','changed')]
            if a.extra_only or a.extra_upload_only:
                cases=[('create','timeout')] if command=='create' else [('', 'csr')]+[('bootstrap',f) for f in ['http503','invalid','identity','cancel']]+[('plan','timeout')]
            if a.object_timeout_only:cases=[('object','object-timeout')]
            for fmt in ['text','json']:
                for diag in [False,True]:
                    for stage,fault in cases:
                        if a.object_timeout_only and (fmt!='json' or diag):continue
                        if fault=='timeout' and (fmt!='json' or diag):continue
                        with tempfile.TemporaryDirectory(dir=root) as case:check(command,fmt,diag,stage,fault,Path(case),f'https://127.0.0.1:{server.server_port}',root/'ca.pem')
    finally:server.shutdown();server.server_close();thread.join()
result={'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'rows':rows,'passed':sum(not r['failures'] for r in rows),'failed':sum(bool(r['failures']) for r in rows),'fixture_cleaned':True}
a.output.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:result[k] for k in ['passed','failed','fixture_cleaned']}))
for r in rows:
    if r['failures']:print(json.dumps(r))
raise SystemExit(bool(result['failed']))
