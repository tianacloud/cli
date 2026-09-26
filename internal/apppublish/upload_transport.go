package apppublish

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"
)

// newUploadHTTPClient keeps signed object URLs out of process-configured HTTP
// proxies. On platforms that support interface-scoped dialing, DialContext also
// avoids virtual tunnel routes that cannot reach the object store.
func newUploadHTTPClient(roots *x509.CertPool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = directUploadDialContext
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport, Timeout: 2 * time.Minute}
}
