package appbootstrap

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This verifies browser behavior against an explicit auth/DB fixture. It does
// not count as deployed Console, Gateway, SQLite or fresh-agent acceptance.
func TestBootstrapBrowser(t *testing.T) {
	module := os.Getenv("TIANA_PLAYWRIGHT_MODULE")
	if module == "" {
		t.Skip("set TIANA_PLAYWRIGHT_MODULE to the installed Playwright module")
	}
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
			functionSocketFixture(t, w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer gateway.Close()
	dir := buildFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.css"), []byte(`@import url('https://fonts.googleapis.com/css2?family=DM+Sans'); body {color: #123}`), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "assets", "chunk.js"), []byte(`export const label='Browser ledger';`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "sw.js"), []byte(`self.addEventListener('install',()=>self.skipWaiting());self.addEventListener('activate',e=>e.waitUntil(self.clients.claim()));self.addEventListener('fetch',e=>{const u=new URL(e.request.url);if(u.pathname==='/web/billing/_sw_probe/ping'){e.respondWith(new Response('sw-intercepted-ok',{status:200,headers:{'Content-Type':'text/plain; charset=utf-8','X-Preview-SW':'active'}}));return;}if(u.pathname==='/web/billing/_sw_probe/badge.svg'){e.respondWith(new Response('<svg xmlns="http://www.w3.org/2000/svg" width="140" height="28"><rect width="140" height="28" rx="6" fill="#0f766e"/><text x="12" y="19" fill="#ffffff" font-family="sans-serif" font-size="13">SW Intercepted</text></svg>',{status:200,headers:{'Content-Type':'image/svg+xml','X-Preview-SW':'active'}}));}});`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "old-probe-sw.js"), []byte(`self.addEventListener('install',()=>self.skipWaiting());self.addEventListener('activate',e=>e.waitUntil(self.clients.claim()));self.addEventListener('fetch',e=>{const req=e.request;const u=new URL(req.url);if(u.pathname==='/_fixture/sw-spy')return;const launchHeader=req.headers.get('x-tiana-launch')||'';if(u.pathname==='/web/billing/_sw_spy_baseline'){e.respondWith((async()=>{await fetch('/_fixture/sw-spy',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:req.url,method:req.method,mode:req.mode,launchHeader,kind:'baseline'})});return new Response('old-probe-active',{status:200,headers:{'Content-Type':'text/plain; charset=utf-8'}});})());return;}if(u.pathname.endsWith('/_tiana/connection')){e.respondWith((async()=>{const resp=await fetch(req);const body=await resp.clone().text().catch(()=>'');await fetch('/_fixture/sw-spy',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:req.url,method:req.method,mode:req.mode,launchHeader,body})}).catch(()=>{});return resp;})());return;}e.waitUntil(fetch('/_fixture/sw-spy',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({url:req.url,method:req.method,mode:req.mode,launchHeader})}).catch(()=>{}));});`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(`import {label} from './chunk.js'; window.__previewSwScriptURL=new URL('./sw.js',import.meta.url).href; window.__previewOldProbeSwURL=new URL('./old-probe-sw.js',import.meta.url).href; export async function mount(root,context){ const c=await context.connection(); const gate=await fetch('/_fixture/mount',{cache:'no-store'}); if(!gate.ok){root.dataset.failedMount='1';throw new Error('transient mount failure')} if(root.dataset.failedMount)throw new Error('dirty document was reused'); root.textContent=label; root.dataset.instance=c.instance_id; root.dataset.user=context.user.id; root.dataset.tenant=context.user.tenantId; root.dataset.route=location.hash; }`), 0600)
	build, err := LoadBuild(dir, fixtureDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	defer build.Close()
	approved := make(chan struct{})
	var once sync.Once
	var fixtureMu sync.Mutex
	failNextMount := false
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	newHandler := func() *Server {
		h, err := NewServer(Config{Origin: origin, BasePath: "/web/billing", Build: build, StartLogin: func(context.Context) (LoginFlow, error) {
			return LoginFlow{VerificationURL: "https://console.example/authorize", ExpiresAt: time.Now().Add(time.Minute), Complete: func(ctx context.Context) (Identity, error) {
				select {
				case <-approved:
				case <-ctx.Done():
					return Identity{}, ctx.Err()
				}
				return Identity{FileRequest: func(_ context.Context, method, path string, body, result any) error {
					if method == "GET" && path == "/api/v1/web-projects/billing" {
						return json.Unmarshal([]byte(`{"owner_id":"fixture-owner"}`), result)
					}
					if method == "GET" && path == "/api/v1/web-projects/billing/functions" {
						return json.Unmarshal([]byte(`{"function_instance_ids":["fn-orders"]}`), result)
					}
					if method != "POST" || path != "/api/v1/functions/fn-orders/connection" {
						t.Errorf("unexpected Function provider request: %s %s", method, path)
					}
					if value, ok := body.(map[string]string); !ok || value["web_id"] != "billing" {
						t.Error("missing originating Web")
					}
					data, _ := json.Marshal(map[string]any{"instance_id": "fn-orders", "origin": gateway.URL, "token": "fixture-function-fn-orders", "expires_at": time.Now().Add(time.Minute).Unix()})
					return json.Unmarshal(data, result)
				}, ID: "fixture-owner", TenantID: "fixture-tenant", Label: "Fixture", Connection: func(context.Context) (Connection, error) {
					return Connection{InstanceID: "ins_billing", Origin: "https://ep-00000000000000000000000000.db.example.test", Token: "fixture-access-token", SQLAPI: "hrana-v3", ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)}, nil
				}}, nil
			}}, nil
		}})
		if err != nil {
			listener.Close()
			t.Fatal(err)
		}
		return h
	}
	var handlerMu sync.RWMutex
	currentHandler := newHandler()
	defer func() {
		handlerMu.RLock()
		h := currentHandler
		handlerMu.RUnlock()
		h.Close()
	}()
	var spyMu sync.Mutex
	var spyEvents []json.RawMessage
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_fixture/launch":
			token := r.URL.Query().Get("token")
			if token == "" {
				token = "fixture-saved-access"
			}
			handlerMu.RLock()
			h := currentHandler
			handlerMu.RUnlock()
			link, err := h.AuthorizeLocalAccount(Identity{ID: "saved-owner", TenantID: "saved-tenant", Connection: func(context.Context) (Connection, error) {
				return Connection{InstanceID: "ins_billing", Origin: "https://ep-00000000000000000000000000.db.example.test", Token: token, SQLAPI: "hrana-v3", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, nil
			}})
			if err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(map[string]string{"url": link})
			return
		case "/_fixture/restart":
			fresh := newHandler()
			handlerMu.Lock()
			old := currentHandler
			currentHandler = fresh
			handlerMu.Unlock()
			old.Close()
			w.WriteHeader(204)
			return
		case "/_fixture/sw-spy":
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if len(raw) > 0 {
				spyMu.Lock()
				spyEvents = append(spyEvents, append(json.RawMessage(nil), raw...))
				spyMu.Unlock()
			}
			w.WriteHeader(204)
			return
		case "/_fixture/sw-spy-events":
			spyMu.Lock()
			copied := append([]json.RawMessage(nil), spyEvents...)
			spyMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(copied)
			return
		case "/_fixture/sw-spy-reset":
			spyMu.Lock()
			spyEvents = nil
			spyMu.Unlock()
			w.WriteHeader(204)
			return
		case "/_fixture/approve":
			once.Do(func() { close(approved) })
			w.WriteHeader(204)
			return
		case "/_fixture/fail-next-mount":
			fixtureMu.Lock()
			failNextMount = true
			fixtureMu.Unlock()
			w.WriteHeader(204)
			return
		case "/_fixture/mount":
			fixtureMu.Lock()
			fail := failNextMount
			failNextMount = false
			fixtureMu.Unlock()
			if fail {
				http.Error(w, "fixture failure", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(204)
			return
		}
		handlerMu.RLock()
		h := currentHandler
		handlerMu.RUnlock()
		h.ServeHTTP(w, r)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "browser.test.mjs", module, origin, gateway.URL, functionFixtureSPKI(gateway))
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser verification failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}

// functionSocketFixture performs a real RFC 6455 handshake and echoes frames.
func functionSocketFixture(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.URL.Query().Get("access_token") != "fixture-function-fn-orders" || r.Header.Get("Sec-WebSocket-Protocol") != "business.v1" || r.Header.Get("Authorization") != "" {
		t.Error("incorrect native WebSocket credential or business protocol")
	}
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()
	sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: business.v1\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
	rw.Flush()
	for {
		header := make([]byte, 2)
		if _, err := io.ReadFull(rw, header); err != nil {
			return
		}
		size := int(header[1] & 127)
		if size > 125 {
			t.Error("unexpected fixture frame size")
			return
		}
		mask := make([]byte, 4)
		if _, err := io.ReadFull(rw, mask); err != nil {
			return
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(rw, body); err != nil {
			return
		}
		for i := range body {
			body[i] ^= mask[i%4]
		}
		rw.Write([]byte{header[0], byte(size)})
		rw.Write(body)
		rw.Flush()
		if header[0]&15 == 8 {
			return
		}
	}
}

func functionFixtureSPKI(server *httptest.Server) string {
	sum := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}
