package appbootstrap

import (
	"context"
	"encoding/json"
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
	dir := buildFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.css"), []byte(`@import url('https://fonts.googleapis.com/css2?family=DM+Sans'); body {color: #123}`), 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "assets", "chunk.js"), []byte(`export const label='Browser ledger';`), 0600)
	os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(`import {label} from './chunk.js'; export async function mount(root,context){ const c=await context.connection(); const gate=await fetch('/_fixture/mount',{cache:'no-store'}); if(!gate.ok){root.dataset.failedMount='1';throw new Error('transient mount failure')} if(root.dataset.failedMount)throw new Error('dirty document was reused'); root.textContent=label; root.dataset.instance=c.instance_id; root.dataset.route=location.hash; }`), 0600)
	build, err := LoadBuild(dir)
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
	handler, err := NewServer(Config{Origin: origin, BasePath: "/web/billing", Build: build, StartLogin: func(context.Context) (LoginFlow, error) {
		return LoginFlow{VerificationURL: "https://console.example/authorize", ExpiresAt: time.Now().Add(time.Minute), Complete: func(ctx context.Context) (Identity, error) {
			select {
			case <-approved:
			case <-ctx.Done():
				return Identity{}, ctx.Err()
			}
			return Identity{ID: "fixture-owner", Label: "Fixture", Connection: func(context.Context) (Connection, error) {
				return Connection{InstanceID: "ins_billing", Origin: "https://ep-00000000000000000000000000.db.example.test", Token: "fixture-access-token", SQLAPI: "hrana-v3", ExpiresAt: time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)}, nil
			}}, nil
		}}, nil
	}})
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_fixture/launch":
			link, err := handler.AuthorizeLocalAccount(Identity{ID: "saved-owner", Connection: func(context.Context) (Connection, error) {
				return Connection{InstanceID: "ins_billing", Origin: "https://ep-00000000000000000000000000.db.example.test", Token: "fixture-saved-access", SQLAPI: "hrana-v3", ExpiresAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)}, nil
			}})
			if err != nil {
				t.Error(err)
			}
			json.NewEncoder(w).Encode(map[string]string{"url": link})
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
		handler.ServeHTTP(w, r)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "browser.test.mjs", module, origin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser verification failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
