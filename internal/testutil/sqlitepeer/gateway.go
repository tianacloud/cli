package sqlitepeer

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	tiana "github.com/tianacloud/sdk-go"
	tianasqlite "github.com/tianacloud/sdk-go-sqlite"
)

const Token = "tia_0AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

const Endpoint = "ep-01j5c9m7q2v8x4k6n3r0t1w2yz.db.example.test"

func Gateway(t *testing.T, inner func(io.Reader, io.Writer), refuse bool) (tianasqlite.Config, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{Endpoint}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	count := &atomic.Int32{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.Method != "CONNECT" || r.Host != Endpoint+":443" || r.Header.Get("tiana-database-protocol") != "hrana-http" {
			t.Error("invalid native CONNECT")
		}
		if r.Header.Get("Proxy-Authorization") != "Bearer "+Token {
			t.Error("InstanceToken missing from outer CONNECT")
		}
		if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 || r.ProtoMajor != 2 {
			t.Error("TLS/H2 contract not met")
		}
		w.Header()["Date"] = nil
		w.Header()["Content-Type"] = nil
		if refuse {
			w.Header().Set("tiana-error-code", "AUTH_REQUIRED")
			w.WriteHeader(401)
			return
		}
		w.Header().Set("tiana-tunnel-version", "1")
		w.Header().Set("tiana-request-id", r.Header.Get("tiana-request-id"))
		w.Header().Set("tiana-auth-mode", "TOKEN_REQUIRED")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		inner(r.Body, flushingWriter{w})
		// Keep the CONNECT response open until the client closes. Returning with
		// unread request DATA lets net/http reset the stream and discard its tail.
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	t.Cleanup(server.Close)
	token, err := tiana.NewToken(Token)
	if err != nil {
		t.Fatal(err)
	}
	return tianasqlite.Config{Gateway: tiana.Config{Endpoint: Endpoint, Token: token, RootCAs: roots, DialAddress: server.Listener.Addr().String(), ConnectTimeout: time.Second, ResponseTimeout: time.Second}}, count
}

type flushingWriter struct{ http.ResponseWriter }

func (w flushingWriter) Write(p []byte) (int, error) {
	n, e := w.ResponseWriter.Write(p)
	w.ResponseWriter.(http.Flusher).Flush()
	return n, e
}
