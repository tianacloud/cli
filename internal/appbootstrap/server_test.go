package appbootstrap

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	if w.Code != 303 || w.Header().Get("Location") != "/_tiana/authorize" || w.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("no platform Bootstrap: status=%d location=%q clear=%q", w.Code, w.Header().Get("Location"), w.Header().Get("Clear-Site-Data"))
	}
	if strings.Contains(w.Body.String(), "账单") || strings.Contains(w.Body.String(), "ins_billing") {
		t.Fatal("HTML must not be generated from app metadata")
	}
	for _, p := range []string{"_tiana/app", "_tiana/files/assets/app.js"} {
		res := previewRequest(s, "GET", "/web/billing/"+p, nil)
		if res.Code != 401 || res.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("unauthorized %s: status=%d clear=%q", p, res.Code, res.Header().Get("Clear-Site-Data"))
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
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/_tiana/authorize?version=v1" || response.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("authorization redirect: status=%d location=%s clear=%q", response.Code, response.Header().Get("Location"), response.Header().Get("Clear-Site-Data"))
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
	// Root "/" and base path without trailing slash always 307 to BasePath+"/" without inspecting
	// Path=/web/billing cookies or emitting Clear-Site-Data (browsers do not send Path=/web/billing
	// cookies to "/").
	for _, entry := range []string{"/", "/web/billing"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := previewRequest(s, method, entry+"?version=v1", nil)
			if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/web/billing/?version=v1" || w.Header().Get("Clear-Site-Data") != "" {
				t.Fatalf("canonical %s %s: status=%d location=%s clear=%q", method, entry, w.Code, w.Header().Get("Location"), w.Header().Get("Clear-Site-Data"))
			}
			wPlain := previewRequest(s, method, entry, nil)
			if wPlain.Code != http.StatusTemporaryRedirect || wPlain.Header().Get("Location") != "/web/billing/" || wPlain.Header().Get("Clear-Site-Data") != "" {
				t.Fatalf("canonical %s %s (no query): status=%d location=%s clear=%q", method, entry, wPlain.Code, wPlain.Header().Get("Location"), wPlain.Header().Get("Clear-Site-Data"))
			}
		}
	}
	// Following the 307 to /web/billing/ without a session cookie emits 303 without Clear-Site-Data
	// (a cross-site top-level navigation withholds SameSite=Strict cookies; storage eviction happens on
	// same-origin X-Tiana-Bootstrap: 1 auth endpoints instead).
	unauthApp := previewRequest(s, http.MethodGet, "/web/billing/?version=v1", nil)
	if unauthApp.Code != http.StatusSeeOther || unauthApp.Header().Get("Location") != "/_tiana/authorize?version=v1" || unauthApp.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("unauthenticated /web/billing/: status=%d location=%s clear=%q", unauthApp.Code, unauthApp.Header().Get("Location"), unauthApp.Header().Get("Clear-Site-Data"))
	}
	link, err := s.AuthorizeLocalAccount(Identity{ID: "owner", TenantID: "tenant", Connection: func(context.Context) (Connection, error) {
		return Connection{InstanceID: "ins_billing", Token: "account-access"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Split(link, "#tiana_launch=")[1]
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4174/web/billing/_tiana/local-login", nil)
	loginReq.Header.Set("Origin", "http://127.0.0.1:4174")
	loginReq.Header.Set("X-Tiana-Bootstrap", "1")
	loginReq.Header.Set("X-Tiana-Launch", proof)
	loginRec := httptest.NewRecorder()
	s.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("local-login status=%d", loginRec.Code)
	}
	cookie := loginRec.Result().Cookies()[0]
	// Following the 307 to /web/billing/ WITH the Path=/web/billing cookie succeeds with 200 and no Clear-Site-Data.
	authApp := previewRequest(s, http.MethodGet, "/web/billing/?version=v1", cookie)
	if authApp.Code != http.StatusOK || authApp.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("authenticated /web/billing/: status=%d clear=%q", authApp.Code, authApp.Header().Get("Clear-Site-Data"))
	}
}

func TestPreviewApplicationCSPBoundaries(t *testing.T) {
	s := previewFixture(t, nil)
	if strings.Contains(previewApplicationCSP, "'unsafe-eval'") || strings.Contains(previewDefaultCSP, "'unsafe-eval'") {
		t.Fatal("unsafe-eval must remain forbidden")
	}
	for _, required := range []string{
		"script-src 'self' https: blob: 'wasm-unsafe-eval'",
		"style-src 'self' blob: 'unsafe-inline'",
		"font-src 'self' data: blob:",
		"worker-src 'self' blob:",
	} {
		if !strings.Contains(previewApplicationCSP, required) {
			t.Fatalf("previewApplicationCSP missing %q: %s", required, previewApplicationCSP)
		}
	}
	for _, forbidden := range []string{
		"script-src 'self' https: blob:",
		"style-src 'self' blob:",
		"font-src 'self' data: blob:",
	} {
		if strings.Contains(previewDefaultCSP, forbidden) {
			t.Fatalf("previewDefaultCSP leaked blob directive %q: %s", forbidden, previewDefaultCSP)
		}
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{
			"/",
			"/web/billing",
			"/web/billing/",
			"/_tiana/authorize",
			"/_tiana/authorize.js",
			"/_tiana/authorize.css",
			"/web/billing/_tiana/authorize",
			"/web/billing/_tiana/authorize.js",
			"/web/billing/_tiana/authorize.css",
			"/web/billing/_tiana/app",
			"/web/billing/_tiana/files/assets/app.js",
			"/web/other",
		} {
			w := previewRequest(s, method, path, nil)
			if got := w.Header().Get("Content-Security-Policy"); got != previewDefaultCSP {
				t.Fatalf("unauthenticated %s %s CSP = %q, want default", method, path, got)
			}
		}
	}

	link, err := s.AuthorizeLocalAccount(Identity{ID: "owner", TenantID: "tenant", Connection: func(context.Context) (Connection, error) {
		return Connection{InstanceID: "ins_billing", Token: "account-access"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Split(link, "#tiana_launch=")[1]
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4174/web/billing/_tiana/local-login", nil)
	loginReq.Header.Set("Origin", "http://127.0.0.1:4174")
	loginReq.Header.Set("X-Tiana-Bootstrap", "1")
	loginReq.Header.Set("X-Tiana-Launch", proof)
	loginRec := httptest.NewRecorder()
	s.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("local-login status=%d", loginRec.Code)
	}
	cookie := loginRec.Result().Cookies()[0]

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, appPath := range []string{
			"/web/billing/",
			"/web/billing/_tiana/bootstrap.js",
			"/web/billing/_tiana/files/assets/app.js",
		} {
			w := previewRequest(s, method, appPath, cookie)
			if w.Code != http.StatusOK {
				t.Fatalf("authenticated %s %s status=%d", method, appPath, w.Code)
			}
			if got := w.Header().Get("Content-Security-Policy"); got != previewApplicationCSP {
				t.Fatalf("authenticated %s %s CSP = %q, want previewApplicationCSP", method, appPath, got)
			}
		}
		for _, authPath := range []string{
			"/_tiana/authorize",
			"/_tiana/authorize.js",
			"/_tiana/authorize.css",
			"/web/billing/_tiana/authorize",
			"/web/billing/_tiana/authorize.js",
			"/web/billing/_tiana/authorize.css",
		} {
			// Browsers only send the Path=/web/billing cookie under /web/billing.
			var reqCookie *http.Cookie
			if strings.HasPrefix(authPath, "/web/billing") {
				reqCookie = cookie
			}
			w := previewRequest(s, method, authPath, reqCookie)
			if w.Code != http.StatusOK {
				t.Fatalf("authorize asset %s %s status=%d", method, authPath, w.Code)
			}
			if got := w.Header().Get("Content-Security-Policy"); got != previewDefaultCSP {
				t.Fatalf("authorize route %s %s CSP = %q, want previewDefaultCSP", method, authPath, got)
			}
			if got := w.Header().Get("Clear-Site-Data"); got != "" {
				t.Fatalf("authorize route %s %s unexpectedly emitted Clear-Site-Data=%q", method, authPath, got)
			}
		}
	}
	appManifest := previewRequest(s, http.MethodGet, "/web/billing/_tiana/app", cookie)
	if appManifest.Code != http.StatusOK || appManifest.Header().Get("Content-Security-Policy") != previewApplicationCSP {
		t.Fatalf("authenticated _tiana/app status=%d csp=%q", appManifest.Code, appManifest.Header().Get("Content-Security-Policy"))
	}
}

func TestIsJavaScriptMediaType(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"text/javascript", true},
		{"text/javascript; charset=utf-8", true},
		{"application/javascript", true},
		{"APPLICATION/JAVASCRIPT; charset=UTF-8", true},
		{"text/css; charset=utf-8", false},
		{"application/json", false},
		{"application/octet-stream", false},
		{"text/plain", false},
		{"", false},
		{"invalid;;;", false},
	} {
		if got := isJavaScriptMediaType(tc.raw); got != tc.want {
			t.Errorf("isJavaScriptMediaType(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestPreviewServiceWorkerAllowedScopeAndBoundaries(t *testing.T) {
	dir := buildFixture(t)
	swBody := "self.addEventListener('install', () => self.skipWaiting());\n"
	if err := os.WriteFile(filepath.Join(dir, "assets", "sw.js"), []byte(swBody), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "sw.mjs"), []byte(swBody), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "data.json"), []byte(`{"ok":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "raw"), []byte("opaque"), 0600); err != nil {
		t.Fatal(err)
	}
	build, err := LoadBuild(dir, fixtureDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	pendingDone := make(chan struct{})
	defer close(pendingDone)
	s, err := NewServer(Config{
		Origin:   "http://127.0.0.1:4174",
		BasePath: "/web/billing",
		Build:    build,
		StartLogin: func(context.Context) (LoginFlow, error) {
			return LoginFlow{
				VerificationURL: "https://console.example/auth/approve",
				ExpiresAt:       time.Now().Add(time.Minute),
				Complete: func(ctx context.Context) (Identity, error) {
					select {
					case <-pendingDone:
					case <-ctx.Done():
					}
					return Identity{}, errors.New("cancelled")
				},
			}, nil
		},
	})
	if err != nil {
		build.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); build.Close() })

	send := func(method, reqPath string, cookie *http.Cookie, headers map[string]string, preSetAllowed string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:4174"+reqPath, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		if preSetAllowed != "" {
			w.Header().Set("Service-Worker-Allowed", preSetAllowed)
		}
		s.ServeHTTP(w, r)
		return w
	}

	// Unauthenticated and pending sessions must reject and never emit Service-Worker-Allowed.
	pendingLogin := previewRequest(s, http.MethodPost, "/web/billing/_tiana/login", nil)
	if pendingLogin.Code != http.StatusAccepted {
		t.Fatalf("pending login status=%d", pendingLogin.Code)
	}
	pendingCookie := pendingLogin.Result().Cookies()[0]
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
	}{
		{"anonymous", nil},
		{"pending", pendingCookie},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := send(method, "/web/billing/_tiana/files/assets/sw.js", tc.cookie, map[string]string{"Service-Worker": "script"}, "/")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s status=%d, want 401", tc.name, method, w.Code)
			}
			if got := w.Header().Get("Service-Worker-Allowed"); got != "" {
				t.Fatalf("%s %s leaked Service-Worker-Allowed=%q", tc.name, method, got)
			}
			if got := w.Header().Get("Content-Security-Policy"); got != previewDefaultCSP {
				t.Fatalf("%s %s CSP=%q, want previewDefaultCSP", tc.name, method, got)
			}
		}
	}

	// Authorize a ready preview session.
	link, err := s.AuthorizeLocalAccount(Identity{ID: "owner", TenantID: "tenant", Connection: func(context.Context) (Connection, error) {
		return Connection{InstanceID: "ins_billing", Token: "account-access"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	proof := strings.Split(link, "#tiana_launch=")[1]
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4174/web/billing/_tiana/local-login", nil)
	loginReq.Header.Set("Origin", "http://127.0.0.1:4174")
	loginReq.Header.Set("X-Tiana-Bootstrap", "1")
	loginReq.Header.Set("X-Tiana-Launch", proof)
	loginRec := httptest.NewRecorder()
	s.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("local-login status=%d", loginRec.Code)
	}
	cookie := loginRec.Result().Cookies()[0]

	// Authenticated GET/HEAD JS/MJS requests with Service-Worker: script (case/whitespace tolerant) receive scoped headers.
	for _, assetPath := range []string{
		"/web/billing/_tiana/files/assets/sw.js",
		"/web/billing/_tiana/files/assets/sw.mjs",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			for _, swHeader := range []string{"script", "  ScRiPt  "} {
				w := send(method, assetPath, cookie, map[string]string{"Service-Worker": swHeader}, "/")
				if w.Code != http.StatusOK {
					t.Fatalf("%s %s (%q) status=%d, want 200", method, assetPath, swHeader, w.Code)
				}
				if got := w.Header().Get("Service-Worker-Allowed"); got != "/web/billing/" {
					t.Fatalf("%s %s Service-Worker-Allowed=%q, want /web/billing/", method, assetPath, got)
				}
				if got := w.Header().Get("Content-Security-Policy"); got != previewApplicationCSP {
					t.Fatalf("%s %s CSP=%q, want previewApplicationCSP", method, assetPath, got)
				}
				if got := w.Header().Get("Cache-Control"); got != "no-store" {
					t.Fatalf("%s %s Cache-Control=%q, want no-store", method, assetPath, got)
				}
				mediaType, _, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
				if err != nil || (mediaType != "text/javascript" && mediaType != "application/javascript") {
					t.Fatalf("%s %s Content-Type=%q not JS", method, assetPath, w.Header().Get("Content-Type"))
				}
				if got := w.Header().Get("Content-Length"); got != strconv.Itoa(len(swBody)) {
					t.Fatalf("%s %s Content-Length=%q, want %d", method, assetPath, got, len(swBody))
				}
				if method == http.MethodGet && w.Body.String() != swBody {
					t.Fatalf("GET %s body=%q, want %q", assetPath, w.Body.String(), swBody)
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Fatalf("HEAD %s returned non-empty body", assetPath)
				}
			}
		}
	}

	// Conditional and Range headers on a Service-Worker: script fetch must preserve RFC 9110 semantics and never emit Service-Worker-Allowed on non-200 responses.
	futureHTTPTime := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	pastHTTPTime := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	for _, tc := range []struct {
		name           string
		headerName     string
		headerValue    string
		wantStatus     int
		wantBodyPrefix string
		wantLength     string
	}{
		{"if-match mismatch", "If-Match", `"mismatch"`, http.StatusPreconditionFailed, "", ""},
		{"expired if-unmodified-since", "If-Unmodified-Since", pastHTTPTime, http.StatusPreconditionFailed, "", ""},
		{"if-none-match star", "If-None-Match", "*", http.StatusNotModified, "", ""},
		{"future if-modified-since", "If-Modified-Since", futureHTTPTime, http.StatusNotModified, "", ""},
		{"range partial content", "Range", "bytes=0-3", http.StatusPartialContent, swBody[:4], "4"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := send(method, "/web/billing/_tiana/files/assets/sw.js", cookie, map[string]string{
				"Service-Worker": "script",
				tc.headerName:    tc.headerValue,
			}, "/")
			if w.Code != tc.wantStatus {
				t.Fatalf("%s %s status=%d, want %d", tc.name, method, w.Code, tc.wantStatus)
			}
			if got := w.Header().Get("Service-Worker-Allowed"); got != "" {
				t.Fatalf("%s %s leaked Service-Worker-Allowed=%q", tc.name, method, got)
			}
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("%s %s Cache-Control=%q, want no-store", tc.name, method, got)
			}
			if got := w.Header().Get("Content-Security-Policy"); got != previewApplicationCSP {
				t.Fatalf("%s %s CSP=%q, want previewApplicationCSP", tc.name, method, got)
			}
			if tc.wantLength != "" && w.Header().Get("Content-Length") != tc.wantLength {
				t.Fatalf("%s %s Content-Length=%q, want %q", tc.name, method, w.Header().Get("Content-Length"), tc.wantLength)
			}
			if method == http.MethodGet && tc.wantBodyPrefix != "" && w.Body.String() != tc.wantBodyPrefix {
				t.Fatalf("%s GET body=%q, want %q", tc.name, w.Body.String(), tc.wantBodyPrefix)
			}
			if method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatalf("%s HEAD returned non-empty body", tc.name)
			}
		}
	}

	// Requests without Service-Worker: script (including ordinary 200 and 304) must never return Service-Worker-Allowed.
	for _, swHeader := range []string{"", "worker", "sharedworker"} {
		headers := map[string]string{}
		if swHeader != "" {
			headers["Service-Worker"] = swHeader
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := send(method, "/web/billing/_tiana/files/assets/sw.js", cookie, headers, "/")
			if w.Code != http.StatusOK {
				t.Fatalf("non-SW %s (%q) status=%d, want 200", method, swHeader, w.Code)
			}
			if got := w.Header().Get("Service-Worker-Allowed"); got != "" {
				t.Fatalf("non-SW %s (%q) leaked Service-Worker-Allowed=%q", method, swHeader, got)
			}
		}
	}
	w304 := send(http.MethodGet, "/web/billing/_tiana/files/assets/sw.js", cookie, map[string]string{"If-Modified-Since": futureHTTPTime}, "/")
	if w304.Code != http.StatusNotModified {
		t.Fatalf("ordinary conditional request status=%d, want 304", w304.Code)
	}
	if got := w304.Header().Get("Service-Worker-Allowed"); got != "" {
		t.Fatalf("ordinary 304 leaked Service-Worker-Allowed=%q", got)
	}

	// Non-JS files must never receive Service-Worker-Allowed even with Service-Worker: script.
	for _, nonJS := range []string{
		"/web/billing/_tiana/files/assets/app.css",
		"/web/billing/_tiana/files/assets/data.json",
		"/web/billing/_tiana/files/assets/raw",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := send(method, nonJS, cookie, map[string]string{"Service-Worker": "script"}, "/")
			if w.Code != http.StatusOK {
				t.Fatalf("non-JS %s %s status=%d, want 200", method, nonJS, w.Code)
			}
			if got := w.Header().Get("Service-Worker-Allowed"); got != "" {
				t.Fatalf("non-JS %s %s leaked Service-Worker-Allowed=%q", method, nonJS, got)
			}
		}
	}

	// Missing files, traversal/hidden paths, and platform/foreign routes must never receive Service-Worker-Allowed.
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/web/billing/_tiana/files/assets/missing.js", http.StatusNotFound},
		{"/web/billing/_tiana/files/../assets/sw.js", http.StatusNotFound},
		{"/web/billing/_tiana/files/assets/../../secret.js", http.StatusNotFound},
		{"/web/billing/_tiana/files/.hidden.js", http.StatusNotFound},
		{"/web/other/_tiana/files/assets/sw.js", http.StatusNotFound},
		{"/web/billing/_tiana/bootstrap.js", http.StatusOK},
		{"/web/billing/_tiana/sdk/auth.js", http.StatusOK},
		{"/web/billing/_tiana/authorize.js", http.StatusOK},
		{"/_tiana/authorize.js", http.StatusOK},
	} {
		var reqCookie *http.Cookie
		if strings.HasPrefix(tc.path, "/web/billing/") {
			reqCookie = cookie
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := send(method, tc.path, reqCookie, map[string]string{"Service-Worker": "script"}, "/")
			if w.Code != tc.want {
				t.Fatalf("boundary %s %s status=%d, want %d", method, tc.path, w.Code, tc.want)
			}
			if got := w.Header().Get("Service-Worker-Allowed"); got != "" {
				t.Fatalf("boundary %s %s leaked Service-Worker-Allowed=%q", method, tc.path, got)
			}
		}
	}
}

func TestPreviewAuthorizationIsolationAndClearSiteData(t *testing.T) {
	pendingDone := make(chan struct{})
	defer close(pendingDone)
	startLoginCalls := 0
	s := previewFixture(t, func(context.Context) (LoginFlow, error) {
		startLoginCalls++
		return LoginFlow{
			VerificationURL: "https://console.example/auth/approve",
			ExpiresAt:       time.Now().Add(time.Minute),
			Complete: func(ctx context.Context) (Identity, error) {
				select {
				case <-pendingDone:
				case <-ctx.Done():
				}
				return Identity{}, errors.New("cancelled")
			},
		}, nil
	})
	identity := Identity{ID: "owner", TenantID: "tenant", Connection: func(context.Context) (Connection, error) {
		return Connection{InstanceID: "ins_billing", Token: "account-access"}, nil
	}}
	link, err := s.AuthorizeLocalAccount(identity)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "http://127.0.0.1:4174/_tiana/authorize#tiana_launch=") {
		t.Fatalf("AuthorizeLocalAccount URL = %q, want outside /web/billing/ scope", link)
	}

	// Cross-site top-level navigation to /_tiana/authorize must be allowed (e.g. clicking link from terminal/external page)
	// and must NEVER emit Clear-Site-Data because browsers do not send Path=/web/billing cookies to /_tiana/authorize.
	navReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4174/_tiana/authorize", nil)
	navReq.Header.Set("Sec-Fetch-Site", "cross-site")
	navReq.Header.Set("Sec-Fetch-Mode", "navigate")
	navRec := httptest.NewRecorder()
	s.ServeHTTP(navRec, navReq)
	if navRec.Code != http.StatusOK || navRec.Header().Get("Clear-Site-Data") != "" || navRec.Header().Get("Content-Security-Policy") != previewDefaultCSP {
		t.Fatalf("cross-site /_tiana/authorize status=%d clear=%q csp=%q", navRec.Code, navRec.Header().Get("Clear-Site-Data"), navRec.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(navRec.Body.String(), `<meta name="tiana-app-base" content="/web/billing/">`) {
		t.Fatalf("/_tiana/authorize missing tiana-app-base meta: %s", navRec.Body.String())
	}

	// Out-of-scope /_tiana/authorize* routes and root "/" never see Path=/web/billing cookies and must NEVER emit Clear-Site-Data.
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rootRec := previewRequest(s, method, "/", nil)
		if rootRec.Code != http.StatusTemporaryRedirect || rootRec.Header().Get("Location") != "/web/billing/" || rootRec.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("%s / status=%d location=%q clear=%q", method, rootRec.Code, rootRec.Header().Get("Location"), rootRec.Header().Get("Clear-Site-Data"))
		}
		for _, p := range []string{"/_tiana/authorize", "/_tiana/authorize.js", "/_tiana/authorize.css"} {
			w := previewRequest(s, method, p, nil)
			if w.Code != http.StatusOK || w.Header().Get("Clear-Site-Data") != "" {
				t.Fatalf("%s %s status=%d clear=%q", method, p, w.Code, w.Header().Get("Clear-Site-Data"))
			}
		}
	}

	// Top-level navigation and page routes (including unauthenticated /web/billing/ 303 and
	// /web/billing/_tiana/authorize 200) must NEVER emit Clear-Site-Data because cross-site
	// top-level clicks withhold SameSite=Strict cookies even for authenticated sessions.
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		unauthRoot := previewRequest(s, method, "/web/billing/", nil)
		if unauthRoot.Code != http.StatusSeeOther || unauthRoot.Header().Get("Location") != "/_tiana/authorize" || unauthRoot.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("unauthenticated %s /web/billing/ status=%d location=%q clear=%q", method, unauthRoot.Code, unauthRoot.Header().Get("Location"), unauthRoot.Header().Get("Clear-Site-Data"))
		}
		crossSiteApp := httptest.NewRequest(method, "http://127.0.0.1:4174/web/billing/", nil)
		crossSiteApp.Header.Set("Sec-Fetch-Site", "cross-site")
		crossSiteApp.Header.Set("Sec-Fetch-Mode", "navigate")
		crossSiteRec := httptest.NewRecorder()
		s.ServeHTTP(crossSiteRec, crossSiteApp)
		if crossSiteRec.Code != http.StatusSeeOther || crossSiteRec.Header().Get("Location") != "/_tiana/authorize" || crossSiteRec.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("cross-site %s /web/billing/ status=%d location=%q clear=%q", method, crossSiteRec.Code, crossSiteRec.Header().Get("Location"), crossSiteRec.Header().Get("Clear-Site-Data"))
		}
		unauthLegacyAuth := previewRequest(s, method, "/web/billing/_tiana/authorize", nil)
		if unauthLegacyAuth.Code != http.StatusOK || unauthLegacyAuth.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("unauthenticated %s /web/billing/_tiana/authorize status=%d clear=%q", method, unauthLegacyAuth.Code, unauthLegacyAuth.Header().Get("Clear-Site-Data"))
		}
		if method == http.MethodGet && !strings.Contains(unauthLegacyAuth.Body.String(), `<meta name="tiana-app-base" content="/web/billing/">`) {
			t.Fatalf("unauthenticated GET /web/billing/_tiana/authorize missing tiana-app-base meta")
		}
	}

	// Unauthenticated 401 LOGIN_REQUIRED emits Clear-Site-Data: "storage" ONLY when the explicit
	// same-origin X-Tiana-Bootstrap: 1 header is present.
	for _, p := range []string{"/web/billing/_tiana/session", "/web/billing/_tiana/app", "/web/billing/_tiana/file-storage/list"} {
		plain := previewRequest(s, http.MethodGet, p, nil)
		if plain.Code != http.StatusUnauthorized || plain.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("unauthenticated non-bootstrap %s status=%d clear=%q", p, plain.Code, plain.Header().Get("Clear-Site-Data"))
		}
		bootReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4174"+p, nil)
		bootReq.Header.Set("X-Tiana-Bootstrap", "1")
		bootRec := httptest.NewRecorder()
		s.ServeHTTP(bootRec, bootReq)
		if bootRec.Code != http.StatusUnauthorized || bootRec.Header().Get("Clear-Site-Data") != `"storage"` {
			t.Fatalf("unauthenticated bootstrap %s status=%d clear=%q", p, bootRec.Code, bootRec.Header().Get("Clear-Site-Data"))
		}
	}

	// New POST login (202) emits Clear-Site-Data: "storage" and sets Path=/web/billing HttpOnly SameSite=Strict cookie.
	pendingLogin := previewRequest(s, http.MethodPost, "/web/billing/_tiana/login", nil)
	if pendingLogin.Code != http.StatusAccepted || pendingLogin.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("POST login status=%d clear=%q", pendingLogin.Code, pendingLogin.Header().Get("Clear-Site-Data"))
	}
	pendingCookies := pendingLogin.Result().Cookies()
	if len(pendingCookies) != 1 || pendingCookies[0].Path != "/web/billing" || !pendingCookies[0].HttpOnly || pendingCookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("POST login cookie attributes changed: %+v", pendingCookies)
	}
	// Repeating POST login with the same pending session cookie reuses it (202) without Clear-Site-Data or Set-Cookie.
	reusedPendingLogin := previewRequest(s, http.MethodPost, "/web/billing/_tiana/login", pendingCookies[0])
	if reusedPendingLogin.Code != http.StatusAccepted || reusedPendingLogin.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("reused pending POST login status=%d clear=%q", reusedPendingLogin.Code, reusedPendingLogin.Header().Get("Clear-Site-Data"))
	}
	if len(reusedPendingLogin.Result().Cookies()) != 0 || reusedPendingLogin.Header().Get("Set-Cookie") != "" {
		t.Fatalf("reused pending POST login unexpectedly re-issued cookie: %+v / %q", reusedPendingLogin.Result().Cookies(), reusedPendingLogin.Header().Get("Set-Cookie"))
	}
	if startLoginCalls != 1 {
		t.Fatalf("expected pending POST login reuse to keep StartLogin calls at 1, got %d", startLoginCalls)
	}

	// POST local-login (200) emits Clear-Site-Data: "storage" and keeps Path=/web/billing HttpOnly SameSite=Strict cookie.
	proof := strings.Split(link, "#tiana_launch=")[1]
	loginReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4174/web/billing/_tiana/local-login", nil)
	loginReq.Header.Set("Origin", "http://127.0.0.1:4174")
	loginReq.Header.Set("X-Tiana-Bootstrap", "1")
	loginReq.Header.Set("X-Tiana-Launch", proof)
	loginRec := httptest.NewRecorder()
	s.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK || loginRec.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("POST local-login status=%d clear=%q", loginRec.Code, loginRec.Header().Get("Clear-Site-Data"))
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Path != "/web/billing" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("POST local-login cookie attributes changed: %+v", cookies)
	}
	cookie := cookies[0]

	// POST login when session is already ready returns 200 without Clear-Site-Data.
	alreadyReadyLogin := previewRequest(s, http.MethodPost, "/web/billing/_tiana/login", cookie)
	if alreadyReadyLogin.Code != http.StatusOK || alreadyReadyLogin.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("POST login (ready) status=%d clear=%q", alreadyReadyLogin.Code, alreadyReadyLogin.Header().Get("Clear-Site-Data"))
	}

	// Authenticated app routes (with Path=/web/billing cookie, with or without X-Tiana-Bootstrap)
	// must NOT emit Clear-Site-Data.
	for _, p := range []string{"/web/billing/", "/web/billing/_tiana/session", "/web/billing/_tiana/authorize", "/web/billing/_tiana/files/assets/app.js"} {
		w := previewRequest(s, http.MethodGet, p, cookie)
		if w.Code != http.StatusOK || w.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("authenticated %s status=%d clear=%q", p, w.Code, w.Header().Get("Clear-Site-Data"))
		}
	}
	authSessionBoot := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4174/web/billing/_tiana/session", nil)
	authSessionBoot.Header.Set("X-Tiana-Bootstrap", "1")
	authSessionBoot.AddCookie(cookie)
	authSessionRec := httptest.NewRecorder()
	s.ServeHTTP(authSessionRec, authSessionBoot)
	if authSessionRec.Code != http.StatusOK || authSessionRec.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("authenticated bootstrap session status=%d clear=%q", authSessionRec.Code, authSessionRec.Header().Get("Clear-Site-Data"))
	}

	// POST logout (204) emits Clear-Site-Data: "storage" and deletes Path=/web/billing cookie.
	logoutRec := previewRequest(s, http.MethodPost, "/web/billing/_tiana/logout", cookie)
	if logoutRec.Code != http.StatusNoContent || logoutRec.Header().Get("Clear-Site-Data") != `"storage"` {
		t.Fatalf("POST logout status=%d clear=%q", logoutRec.Code, logoutRec.Header().Get("Clear-Site-Data"))
	}
	logoutCookies := logoutRec.Result().Cookies()
	if len(logoutCookies) != 1 || logoutCookies[0].Path != "/web/billing" || !logoutCookies[0].HttpOnly || logoutCookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("POST logout cookie attributes changed: %+v", logoutCookies)
	}

	// Post-logout 401 LOGIN_REQUIRED emits Clear-Site-Data: "storage" only with X-Tiana-Bootstrap: 1.
	for _, p := range []string{"/web/billing/_tiana/session", "/web/billing/_tiana/app", "/web/billing/_tiana/file-storage/list"} {
		plain := previewRequest(s, http.MethodGet, p, cookie)
		if plain.Code != http.StatusUnauthorized || plain.Header().Get("Clear-Site-Data") != "" {
			t.Fatalf("post-logout non-bootstrap %s status=%d clear=%q", p, plain.Code, plain.Header().Get("Clear-Site-Data"))
		}
		bootReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:4174"+p, nil)
		bootReq.Header.Set("X-Tiana-Bootstrap", "1")
		bootReq.AddCookie(cookie)
		bootRec := httptest.NewRecorder()
		s.ServeHTTP(bootRec, bootReq)
		if bootRec.Code != http.StatusUnauthorized || bootRec.Header().Get("Clear-Site-Data") != `"storage"` {
			t.Fatalf("post-logout bootstrap %s status=%d clear=%q", p, bootRec.Code, bootRec.Header().Get("Clear-Site-Data"))
		}
	}
}

func TestSingleJavaScriptContentTypeValidation(t *testing.T) {
	single := http.Header{"Content-Type": []string{"text/javascript; charset=utf-8"}}
	if !hasSingleJavaScriptContentType(single) {
		t.Fatal("single JS Content-Type rejected")
	}
	multi := http.Header{"Content-Type": []string{"text/javascript", "text/plain"}}
	if hasSingleJavaScriptContentType(multi) {
		t.Fatal("multi-valued Content-Type must be rejected")
	}
	empty := http.Header{}
	if hasSingleJavaScriptContentType(empty) {
		t.Fatal("missing Content-Type must be rejected")
	}
	rec := httptest.NewRecorder()
	rec.Header().Add("Content-Type", "text/javascript")
	rec.Header().Add("Content-Type", "text/javascript")
	rec.Header().Set("Service-Worker-Allowed", "/")
	sw := &serviceWorkerScriptWriter{ResponseWriter: rec, scope: "/web/billing/"}
	sw.WriteHeader(http.StatusOK)
	if got := rec.Header().Get("Service-Worker-Allowed"); got != "" {
		t.Fatalf("multi-valued Content-Type emitted Service-Worker-Allowed=%q", got)
	}
}
