package gitremote

import (
	"net/http"
	"testing"
)

func TestClosedConnectEnvelope(t *testing.T) {
	valid := func() *http.Response {
		return &http.Response{StatusCode: 200, ProtoMajor: 2, Header: http.Header{"Tiana-Tunnel-Version": []string{"1"}, "Tiana-Request-Id": []string{"req-test"}, "Tiana-Auth-Mode": []string{"DISABLED"}}}
	}
	if !validSuccess(valid(), "req-test") {
		t.Fatal("valid envelope rejected")
	}
	for _, mutate := range []func(*http.Response){
		func(r *http.Response) { r.ProtoMajor = 1 },
		func(r *http.Response) { r.StatusCode = 204 },
		func(r *http.Response) { r.Header.Set("Unexpected", "value") },
		func(r *http.Response) { r.Header.Add("Tiana-Request-Id", "req-test") },
		func(r *http.Response) { r.Header.Set("Tiana-Request-Id", "req-other") },
		func(r *http.Response) { r.Header.Del("Tiana-Tunnel-Version") },
		func(r *http.Response) { r.Header.Set("Tiana-Auth-Mode", "unknown") },
	} {
		r := valid()
		mutate(r)
		if validSuccess(r, "req-test") {
			t.Fatal("invalid success envelope accepted")
		}
	}
	r := valid()
	r.Header.Set("Tiana-Auth-Mode", "TOKEN_REQUIRED")
	if !validSuccess(r, "req-test") {
		t.Fatal("authenticated envelope rejected")
	}
}
