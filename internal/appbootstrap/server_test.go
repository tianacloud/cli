package appbootstrap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func previewFixture(t *testing.T, start StartLogin) *Server {
	t.Helper()
	build, err := LoadBuild(buildFixture(t), fixtureDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Config{Origin: "http://127.0.0.1:4174", BasePath: "/web/billing", Build: build, StartLogin: start})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); build.Close() })
	return s
}
func previewRequest(s *Server, method, p string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:4174"+p, nil)
	if method == "POST" {
		r.Header.Set("Origin", "http://127.0.0.1:4174")
		r.Header.Set("X-Tiana-Bootstrap", "1")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestBootstrapIsFixedAndApplicationFilesRequireSession(t *testing.T) {
	s := previewFixture(t, nil)
	w := previewRequest(s, "GET", "/web/billing/", nil)
	if w.Code != 303 || w.Header().Get("Location") != "/web/billing/_tiana/authorize" {
		t.Fatalf("no platform Bootstrap: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "账单") || strings.Contains(w.Body.String(), "ins_billing") {
		t.Fatal("HTML must not be generated from app metadata")
	}
	for _, p := range []string{"_tiana/app", "_tiana/files/assets/app.js"} {
		if got := previewRequest(s, "GET", "/web/billing/"+p, nil).Code; got != 401 {
			t.Fatalf("unauthorized %s: %d", p, got)
		}
	}
	if got := previewRequest(s, "GET", "/web/other", nil).Code; got != 404 {
		t.Fatalf("another app route = %d", got)
	}
}
func TestConsoleLoginGrantsOnlyBoundPreviewSession(t *testing.T) {
	approved := make(chan struct{})
	s := previewFixture(t, func(ctx context.Context) (LoginFlow, error) {
		return LoginFlow{VerificationURL: "https://console.example/auth/approve", ExpiresAt: time.Now().Add(time.Minute), Complete: func(ctx context.Context) (Identity, error) {
			select {
			case <-approved:
			case <-ctx.Done():
				return Identity{}, ctx.Err()
			}
			return Identity{ID: "owner", Label: "Owner", Connection: func(context.Context) (Connection, error) {
				return Connection{InstanceID: "ins_billing", Origin: "tiana-http://example", Token: "instance-only"}, nil
			}}, nil
		}}, nil
	})
	login := previewRequest(s, "POST", "/web/billing/_tiana/login", nil)
	if login.Code != 202 {
		t.Fatalf("login = %d: %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("missing bound HttpOnly local cookie")
	}
	cookie := cookies[0]
	if strings.Contains(cookie.Value, "owner") || strings.Contains(login.Body.String(), "instance-only") {
		t.Fatal("login leaked credentials")
	}
	pending := previewRequest(s, "GET", "/web/billing/_tiana/session", cookie)
	if pending.Code != 202 {
		t.Fatalf("pending = %d", pending.Code)
	}
	close(approved)
	deadline := time.Now().Add(time.Second)
	for previewRequest(s, "GET", "/web/billing/_tiana/session", cookie).Code == 202 {
		if time.Now().After(deadline) {
			t.Fatal("authorization did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	doc := previewRequest(s, "GET", "/web/billing/", cookie)
	if doc.Code != 200 || strings.Contains(doc.Body.String(), "tiana-bar") || strings.Contains(doc.Body.String(), "bootstrap.css") || strings.Contains(doc.Body.String(), "tiana-login") || !strings.Contains(doc.Body.String(), `"local_preview":true`) {
		t.Fatal("authenticated document is not a clean application template")
	}
	if !strings.Contains(doc.Body.String(), `id="tiana-bootstrap-script"`) || !strings.Contains(doc.Body.String(), `src="/web/billing/_tiana/bootstrap.js"`) {
		t.Fatalf("bootstrap script does not target this app: %s", doc.Body.String())
	}
	manifest := previewRequest(s, "GET", "/web/billing/_tiana/app", cookie)
	if manifest.Code != 200 || !strings.Contains(manifest.Body.String(), "assets/app.js") {
		t.Fatal("authorized manifest not available")
	}
	asset := previewRequest(s, "GET", "/web/billing/_tiana/files/assets/app.js", cookie)
	if asset.Code != 200 || !strings.Contains(asset.Body.String(), "export function mount") {
		t.Fatal("authorized application not served")
	}
	connection := previewRequest(s, "POST", "/web/billing/_tiana/connection", cookie)
	if connection.Code != 200 || !strings.Contains(connection.Body.String(), "instance-only") || connection.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("scoped runtime connection not available")
	}
	if previewRequest(s, "POST", "/web/billing/_tiana/logout", cookie).Code != 204 {
		t.Fatal("logout failed")
	}
	if previewRequest(s, "GET", "/web/billing/_tiana/files/assets/app.js", cookie).Code != 401 {
		t.Fatal("logout did not revoke local access")
	}
}
func TestPreviewRejectsCrossOriginAndHostRebinding(t *testing.T) {
	starts := 0
	s := previewFixture(t, func(context.Context) (LoginFlow, error) { starts++; return LoginFlow{}, errors.New("unexpected") })
	for _, tc := range []struct{ host, origin, site, proof string }{{"evil.test:4174", "http://127.0.0.1:4174", "", "1"}, {"127.0.0.1:4174", "https://evil.test", "cross-site", "1"}, {"127.0.0.1:4174", "", "", ""}} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/web/billing/_tiana/login", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		r.Header.Set("X-Tiana-Bootstrap", tc.proof)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Errorf("unsafe request accepted: %d", w.Code)
		}
	}
	if starts != 0 {
		t.Fatal("unsafe request reached auth backend")
	}
}
func TestPreviewRejectsNonLoopbackOrigin(t *testing.T) {
	b, err := LoadBuild(buildFixture(t), fixtureDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, origin := range []string{"http://0.0.0.0:4174", "http://evil.test:4174", "http://127.0.0.1:4174/path"} {
		s, err := NewServer(Config{Origin: origin, BasePath: "/web/billing", Build: b})
		if err == nil {
			s.Close()
			t.Errorf("unsafe origin accepted: %s", origin)
		}
	}
}

func TestConsoleMayNavigateToPublicBootstrapButNotReadAssets(t *testing.T) {
	s := previewFixture(t, nil)
	r := httptest.NewRequest("GET", "http://127.0.0.1:4174/web/billing/", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 303 {
		t.Fatalf("Console link cannot open Bootstrap: %d", w.Code)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:4174/web/billing/_tiana/files/assets/app.js", nil)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site navigation exposed app assets")
	}
}

func TestLocalBootstrapEnvironmentIsExplicit(t *testing.T) {
	dir := buildFixture(t)
	build, err := LoadBuild(dir, fixtureDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	defer build.Close()
	s, err := NewServer(Config{Origin: "http://127.0.0.1:4174", BasePath: "/web/billing", Build: build})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := httptest.NewRequest("GET", "http://127.0.0.1:4174/web/billing/_tiana/environment", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"local_preview":true`) {
		t.Fatalf("missing local runtime environment: %d %s", w.Code, w.Body.String())
	}
}

func TestLocalAccountLaunchCapability(t *testing.T) {
	s := previewFixture(t, nil)
	identity := Identity{ID: "owner", Connection: func(context.Context) (Connection, error) {
		return Connection{InstanceID: "ins_billing", Token: "account-access"}, nil
	}}
	link, err := s.AuthorizeLocalAccount(identity)
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Split(link, "#tiana_launch=")[1]
	request := func(secret, origin, host string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:4174/web/billing/_tiana/local-login", nil)
		r.Host = host
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Tiana-Bootstrap", "1")
		r.Header.Set("X-Tiana-Launch", secret)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, args := range [][3]string{{"", "http://127.0.0.1:4174", "127.0.0.1:4174"}, {proof, "https://evil.example.test", "127.0.0.1:4174"}, {proof, "http://127.0.0.1:4174", "evil.example.test"}} {
		if request(args[0], args[1], args[2]).Code != 403 {
			t.Fatal("untrusted launch accepted")
		}
	}
	w := request(proof, "http://127.0.0.1:4174", "127.0.0.1:4174")
	if w.Code != 200 || len(w.Result().Cookies()) != 1 || strings.Contains(w.Body.String(), "account-access") {
		t.Fatal("launch failed or leaked credential")
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	if previewRequest(s, "GET", "/web/billing/", cookie).Code != 200 {
		t.Fatal("not authorized")
	}
	if request(proof, "http://127.0.0.1:4174", "127.0.0.1:4174").Code != 403 {
		t.Fatal("launch replayed")
	}
	previewRequest(s, "POST", "/web/billing/_tiana/logout", cookie)
	if previewRequest(s, "GET", "/web/billing/_tiana/app", cookie).Code != 401 {
		t.Fatal("logout ineffective")
	}
	_, err = s.AuthorizeLocalAccount(identity)
	if err != nil {
		t.Fatal(err)
	}
	s.launchExpires = time.Now().Add(-time.Second)
	if request(s.launchProof, "http://127.0.0.1:4174", "127.0.0.1:4174").Code != 403 {
		t.Fatal("expired capability accepted")
	}
}

func TestPreviewAuthorizationPreservesVersionQuery(t *testing.T) {
	s := previewFixture(t, nil)
	response := previewRequest(s, "GET", "/web/billing/?version=v1", nil)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/web/billing/_tiana/authorize?version=v1" {
		t.Fatalf("authorization redirect: status=%d location=%s", response.Code, response.Header().Get("Location"))
	}
}

func TestApplicationDocumentPreservesPlaceholderTextInName(t *testing.T) {
	s := previewFixture(t, nil)
	s.config.Build.Manifest.Name = "__TIANA_BOOTSTRAP_SCRIPT__"
	w := httptest.NewRecorder()
	s.applicationDocument(w, httptest.NewRequest("GET", "/web/billing/", nil))
	if !strings.Contains(w.Body.String(), `"name":"__TIANA_BOOTSTRAP_SCRIPT__"`) || !strings.Contains(w.Body.String(), `src="/web/billing/_tiana/bootstrap.js"`) {
		t.Fatalf("document=%s", w.Body.String())
	}
}

func TestPreviewCanonicalEntryPreservesVersion(t *testing.T) {
	s := previewFixture(t, nil)
	for _, entry := range []string{"/", "/web/billing"} {
		w := previewRequest(s, "GET", entry+"?version=v1", nil)
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/web/billing/?version=v1" {
			t.Fatalf("entry %s: status=%d location=%s", entry, w.Code, w.Header().Get("Location"))
		}
	}
}
