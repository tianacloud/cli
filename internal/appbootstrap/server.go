package appbootstrap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed runtime/index.html runtime/bootstrap.js runtime/authorize.html runtime/authorize.js runtime/authorize.css
var runtimeFiles embed.FS

type Connection struct {
	InstanceID string `json:"instance_id"`
	Origin     string `json:"origin"`
	Token      string `json:"tianaToken"`
	SQLAPI     string `json:"sql_api"`
	ExpiresAt  string `json:"expires_at"`
}
type Identity struct {
	ID, Label  string
	Connection func(context.Context) (Connection, error)
}
type LoginFlow struct {
	VerificationURL string
	ExpiresAt       time.Time
	Complete        func(context.Context) (Identity, error)
}
type StartLogin func(context.Context) (LoginFlow, error)
type Config struct {
	Origin, BasePath string
	Build            *Build
	StartLogin       StartLogin
}
type session struct {
	identity Identity
	prompt   string
	state    string
	expires  time.Time
	cancel   context.CancelFunc
}
type Server struct {
	config         Config
	host, cookie   string
	mu             sync.Mutex
	sessions       map[string]*session
	closed         bool
	launchProof    string
	launchExpires  time.Time
	launchIdentity Identity
}

func NewServer(c Config) (*Server, error) {
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("preview origin must be http://127.0.0.1:PORT")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("preview requires an explicit valid port")
	}
	if c.Build == nil || c.BasePath != "/web/"+c.Build.Manifest.AppID+"/" {
		return nil, errors.New("preview path must identify the configured app")
	}
	return &Server{config: c, host: u.Host, cookie: "tiana_preview_" + u.Port(), sessions: map[string]*session{}}, nil
}

// AuthorizeLocalAccount returns a one-use, five-minute launcher capability.
// The fragment is consumed before loading any application code; it is not an
// account credential and is never sent in a request URL or Referer.
func (s *Server) AuthorizeLocalAccount(identity Identity) (string, error) {
	if identity.ID == "" || identity.Connection == nil {
		return "", errors.New("invalid preview identity")
	}
	proof := make([]byte, 32)
	if _, err := rand.Read(proof); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("preview stopped")
	}
	s.launchProof = base64.RawURLEncoding.EncodeToString(proof)
	s.launchIdentity = identity
	s.launchExpires = time.Now().Add(5 * time.Minute)
	return s.config.Origin + s.config.BasePath + "_tiana/authorize#tiana_launch=" + s.launchProof, nil
}
func (s *Server) localLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	proof := r.Header.Get("X-Tiana-Launch")
	if s.closed || s.launchProof == "" || !time.Now().Before(s.launchExpires) || subtle.ConstantTimeCompare([]byte(proof), []byte(s.launchProof)) != 1 {
		s.failure(w, 403, "LAUNCH_AUTHORIZATION_REQUIRED")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.failure(w, 500, "LOGIN_UNAVAILABLE")
		return
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	s.sessions[id] = &session{identity: s.launchIdentity, state: "ready", expires: time.Now().Add(time.Hour), cancel: func() {}}
	s.launchProof = ""
	s.launchIdentity = Identity{}
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id, Path: s.config.BasePath, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
	s.json(w, 200, map[string]string{"state": "ready"})
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.launchProof = ""
	s.launchIdentity = Identity{}
	for k, v := range s.sessions {
		v.cancel()
		delete(s.sessions, k)
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' https:; img-src 'self' data: blob:; font-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	publicNavigation := (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Sec-Fetch-Mode") == "navigate" && (r.URL.Path == s.config.BasePath || r.URL.Path == strings.TrimSuffix(s.config.BasePath, "/") || r.URL.Path == "/")
	if r.Host != s.host || (r.Header.Get("Sec-Fetch-Site") == "cross-site" && !publicNavigation) {
		s.failure(w, 403, "ORIGIN_REJECTED")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.config.Origin {
		s.failure(w, 403, "ORIGIN_REJECTED")
		return
	}
	if r.Method == http.MethodPost && (r.Header.Get("Origin") != s.config.Origin || r.Header.Get("X-Tiana-Bootstrap") != "1") {
		s.failure(w, 403, "ORIGIN_REJECTED")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		s.failure(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	if r.URL.Path == strings.TrimSuffix(s.config.BasePath, "/") || r.URL.Path == "/" {
		http.Redirect(w, r, s.config.BasePath, http.StatusTemporaryRedirect)
		return
	}
	if !strings.HasPrefix(r.URL.Path, s.config.BasePath) {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, s.config.BasePath)
	if p == "" && (r.Method == "GET" || r.Method == "HEAD") {
		if _, state := s.identity(r); state != "ready" {
			http.Redirect(w, r, s.config.BasePath+"_tiana/authorize", http.StatusSeeOther)
			return
		}
		s.applicationDocument(w, r)
		return
	}
	if (p == "_tiana/bootstrap.js" || p == "_tiana/authorize.js" || p == "_tiana/authorize.css") && (r.Method == "GET" || r.Method == "HEAD") {
		s.platformFile(w, r, path.Base(p))
		return
	}
	if p == "_tiana/authorize" && (r.Method == "GET" || r.Method == "HEAD") {

		s.platformFile(w, r, "authorize.html")
		return
	}
	if p == "_tiana/environment" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"local_preview": true})
		return
	}
	if r.Method == http.MethodPost {
		// None of these actions accept input. A browser cannot select another resource.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
		if err != nil || len(body) != 0 {
			s.failure(w, 400, "BODY_NOT_ALLOWED")
			return
		}
		switch p {
		case "_tiana/local-login":
			s.localLogin(w, r)
			return
		case "_tiana/login":
			s.login(w, r)
			return
		case "_tiana/logout":
			s.logout(w, r)
			return
		}
	}
	identity, state := s.identity(r)
	if p == "_tiana/session" && r.Method == http.MethodGet {
		switch state {
		case "pending":
			s.json(w, 202, map[string]string{"state": "pending"})
		case "ready":
			s.json(w, 200, map[string]any{"state": "ready", "user": map[string]string{"id": identity.ID, "label": identity.Label}, "app_id": s.config.Build.Manifest.AppID})
		default:
			s.failure(w, 401, "LOGIN_REQUIRED")
		}
		return
	}
	if state != "ready" {
		s.failure(w, 401, "LOGIN_REQUIRED")
		return
	}
	switch {
	case p == "_tiana/app" && r.Method == http.MethodGet:
		s.json(w, 200, s.config.Build.Manifest)
	case p == "_tiana/connection" && r.Method == http.MethodPost:
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		connection, err := identity.Connection(ctx)
		if err != nil || connection.InstanceID != s.config.Build.Manifest.DatabaseInstanceID || connection.Token == "" {
			s.failure(w, 403, "DATABASE_ACCESS_UNAVAILABLE")
			return
		}
		s.json(w, 200, connection)
	case strings.HasPrefix(p, "_tiana/files/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.applicationFile(w, r, strings.TrimPrefix(p, "_tiana/files/"))
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) platformFile(w http.ResponseWriter, r *http.Request, name string) {
	bytes, err := runtimeFiles.ReadFile("runtime/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	w.Header().Set("Content-Length", strconv.Itoa(len(bytes)))
	if r.Method != http.MethodHead {
		w.Write(bytes)
	}
}
func (s *Server) applicationFile(w http.ResponseWriter, r *http.Request, name string) {
	if !safePath(name) || !s.config.Build.files[name] {
		http.NotFound(w, r)
		return
	}
	f, err := s.config.Build.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > MaxFileSize {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, name, stat.ModTime(), f)
}
func (s *Server) identity(r *http.Request) (Identity, string) {
	cookie, err := r.Cookie(s.cookie)
	if err != nil {
		return Identity{}, "none"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[cookie.Value]
	if !ok {
		return Identity{}, "none"
	}
	if !time.Now().Before(v.expires) {
		v.cancel()
		delete(s.sessions, cookie.Value)
		return Identity{}, "expired"
	}
	return v.identity, v.state
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.config.StartLogin == nil {
		s.failure(w, 503, "LOGIN_UNAVAILABLE")
		return
	}
	// Serialize creation to bound both pending transactions and retained sessions.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		s.failure(w, 503, "PREVIEW_STOPPED")
		return
	}
	for k, v := range s.sessions {
		if !time.Now().Before(v.expires) {
			v.cancel()
			delete(s.sessions, k)
		}
	}
	if cookie, err := r.Cookie(s.cookie); err == nil {
		if v, ok := s.sessions[cookie.Value]; ok {
			if v.state == "ready" {
				s.json(w, 200, map[string]string{"state": "ready"})
				return
			}
			if v.state == "pending" {
				s.json(w, 202, map[string]string{"state": "pending", "verification_url": v.prompt})
				return
			}
			v.cancel()
			delete(s.sessions, cookie.Value)
		}
	}
	if len(s.sessions) >= 8 {
		s.failure(w, 429, "TOO_MANY_SESSIONS")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	flow, err := s.config.StartLogin(ctx)
	if err != nil || flow.Complete == nil || !flow.ExpiresAt.After(time.Now()) || flow.ExpiresAt.After(time.Now().Add(30*time.Minute)) {
		s.failure(w, 502, "LOGIN_UNAVAILABLE")
		return
	}
	proof := make([]byte, 32)
	if _, err = rand.Read(proof); err != nil {
		s.failure(w, 500, "LOGIN_UNAVAILABLE")
		return
	}
	id := base64.RawURLEncoding.EncodeToString(proof)
	flowCtx, stop := context.WithDeadline(context.Background(), flow.ExpiresAt)
	v := &session{state: "pending", prompt: flow.VerificationURL, expires: flow.ExpiresAt, cancel: stop}
	s.sessions[id] = v
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: id, Path: s.config.BasePath, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 3600})
	go func() {
		defer stop()
		identity, err := flow.Complete(flowCtx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sessions[id] != v || s.closed {
			return
		}
		if err != nil || identity.ID == "" || identity.Connection == nil {
			v.state = "failed"
			v.prompt = ""
			return
		}
		v.state = "ready"
		v.prompt = ""
		v.identity = identity
		v.expires = time.Now().Add(time.Hour)
	}()
	s.json(w, 202, map[string]string{"state": "pending", "verification_url": flow.VerificationURL})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cookie, err := r.Cookie(s.cookie); err == nil {
		if v, ok := s.sessions[cookie.Value]; ok {
			v.cancel()
			delete(s.sessions, cookie.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Path: s.config.BasePath, MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(204)
}
func (s *Server) json(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func (s *Server) failure(w http.ResponseWriter, status int, code string) {
	s.json(w, status, map[string]string{"error": code})
}

func (s *Server) applicationDocument(w http.ResponseWriter, r *http.Request) {
	descriptor := struct {
		Manifest
		LocalPreview bool `json:"local_preview"`
	}{s.config.Build.Manifest, true}
	data, err := json.Marshal(descriptor)
	if err != nil {
		s.failure(w, 500, "APP_UNAVAILABLE")
		return
	}
	template, err := runtimeFiles.ReadFile("runtime/index.html")
	if err != nil {
		s.failure(w, 500, "APP_UNAVAILABLE")
		return
	}
	document := bytes.Replace(template, []byte("__TIANA_BOOTSTRAP_DATA__"), data, 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(document)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(document)
	}
}
