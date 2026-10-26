package apppublish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tianacloud/cli/internal/authclient"
	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/netconfig"
	"github.com/tianacloud/sdk-go/fetch"
)

// Opt-in acceptance only: the caller supplies an exact task-owned Web project
// and a private receipt directory. Ordinary tests never access real accounts.
func TestLiveProxyPublicationRecovery(t *testing.T) {
	filename := os.Getenv("TIANA_TEST_LIVE_PROXY_CONFIG")
	if filename == "" {
		t.Skip("requires explicit task-owned live acceptance configuration")
	}
	var config struct{ WebID, Name, Dir, ReceiptDir string }
	raw, err := os.ReadFile(filename)
	if err != nil || json.Unmarshal(raw, &config) != nil || !strings.HasPrefix(config.Name, "proxy-acceptance-") || config.ReceiptDir == "" {
		t.Fatal("invalid live acceptance configuration")
	}
	if os.Getenv("ALL_PROXY") == "" || os.Getenv("TIANA_TEST_PROXY_SANDBOX") != "loopback-only" {
		t.Fatal("live acceptance requires the isolated proxy sandbox")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	ctx = netconfig.WithEnvironment(ctx, os.Environ())
	defer netconfig.Close(ctx)
	trust, err := clientconfig.Load("")
	if err != nil {
		t.Fatal(err)
	}
	ctx = clientconfig.WithTrust(ctx, trust)
	origin, err := authclient.ResolveOrigin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := netconfig.NewTransport(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := authclient.NewWithConfig(authclient.Config{Origin: origin, RootCAs: trust.Roots, HTTPClient: &http.Client{Transport: transport, Timeout: 25 * time.Second}, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	runner, failure := (Runner{Client: client}).Bind(ctx)
	if failure != nil {
		t.Fatal(failure)
	}
	project, failure := runner.Resolve(ctx, config.WebID)
	if failure != nil || project.Name != config.Name {
		t.Fatal("live target does not match the task-owned project")
	}
	if err := os.MkdirAll(config.ReceiptDir, 0700); err != nil {
		t.Fatal(err)
	}
	writePrivate := func(name string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil || os.WriteFile(filepath.Join(config.ReceiptDir, name), data, 0600) != nil {
			t.Fatal("cannot save private acceptance evidence")
		}
	}
	var loss *discardLiveReceipt
	writer := runner
	writer.PersistPublication = func(_ context.Context, receipt PublicationReceipt) error {
		data, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(config.ReceiptDir, "intent.json"), data, 0600)
	}
	writer.Fetch = func(c fetch.Config) (FetchTransport, error) {
		inner, err := fetch.NewClient(c)
		loss = &discardLiveReceipt{inner: inner}
		return loss, err
	}
	result := writer.Run(ctx, Options{Command: "publish", ID: config.WebID, Dir: config.Dir})
	writePrivate("unknown.json", result)
	if result.Status != "unknown" || loss == nil || !loss.discarded || loss.puts != 1 {
		t.Fatal("live upload did not exercise one lost receipt", result.Error)
	}
	identity, ok := result.Data.(map[string]string)
	if !ok || identity["publish_id"] == "" || identity["publish_id"] != loss.publishID || identity["sha256"] != loss.sha {
		t.Fatal("lost response changed the accepted publication identity")
	}
	archive := identity["archive_path"]
	info, err := os.Stat(archive)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unknown publication did not retain its private archive")
	}
	confirmed := false
	t.Cleanup(func() {
		if confirmed {
			os.Remove(archive)
		}
	})
	receipt := runner.Run(ctx, Options{Command: "status", ID: config.WebID, PublishID: identity["publish_id"]})
	writePrivate("receipt.json", receipt)
	if receipt.Error != nil {
		t.Fatal("cannot recover the real publication receipt", receipt.Error)
	}
	recovered, ok := receipt.Data.(map[string]any)
	if !ok || recovered["publish_id"] != identity["publish_id"] || recovered["sha256"] != identity["sha256"] {
		t.Fatal("receipt recovery returned a different identity or archive")
	}
	for {
		current := runner.Run(ctx, Options{Command: "status", ID: config.WebID})
		writePrivate("current.json", current)
		state, _ := current.Data.(map[string]any)
		if current.Error == nil && state["state"] == "running" && state["remote_sha256"] == identity["sha256"] && state["serving_sha256"] == identity["sha256"] {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real publication did not reach matching running checksums")
		case <-time.After(time.Second):
		}
	}
	instance, err := client.GetInstance(ctx, project.InstanceID)
	if err != nil || instance.Connection == nil {
		t.Fatal("published Web instance unavailable")
	}
	credential, err := client.ConnectionCredential(ctx, runner.PrincipalID, runner.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(credential)
	policy, err := netconfig.Policy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(instance.Connection.URL)
	if err != nil {
		t.Fatal("invalid live Endpoint URL")
	}
	resources, err := fetch.NewResourceClient(fetch.Config{Endpoint: instance.Connection.Hostname, Port: u.Port(), Token: string(credential), RootCAs: trust.Roots, Network: policy})
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	want, err := os.ReadFile(filepath.Join(config.Dir, "probe.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		response, err := resources.Do(ctx, fetch.Request{Method: method, PathQuery: "/probe.bin"})
		if err != nil {
			t.Fatal("real native Web resource request failed", err)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, int64(len(want))+1))
		response.Body.Close()
		if err != nil || response.Status != 200 || (method == "GET" && !bytes.Equal(body, want)) || (method == "HEAD" && len(body) != 0) {
			t.Fatal("native resource did not preserve the published bytes")
		}
	}
	if loss.puts != 1 {
		t.Fatal("receipt recovery replayed the live publication")
	}
	confirmed = true
	sum := sha256.Sum256(want)
	writePrivate("passed.json", map[string]any{"put_count": loss.puts, "resource_sha256": hex.EncodeToString(sum[:]), "receipt_recovered": true, "serving_confirmed": true})
	t.Log("One real PUT, simulated receipt loss at the fetch adapter, original receipt recovery, serving checksums and native GET/HEAD passed")
}

// Real TLS/HTTP and OSS completion finish before this adapter discards the
// receipt. This models loss at the caller boundary without MITM, replay or a
// claim that a physical network fault was injected into the deployed service.
type discardLiveReceipt struct {
	inner          *fetch.Client
	puts           int
	discarded      bool
	publishID, sha string
}

func (d *discardLiveReceipt) Close() { d.inner.Close() }
func (d *discardLiveReceipt) Do(ctx context.Context, request fetch.Request) (*fetch.Response, error) {
	if request.Method == "PUT" {
		d.puts++
	}
	response, err := d.inner.Do(ctx, request)
	if err != nil || request.Method != "PUT" || response.Status != 200 {
		return response, err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	response.Body.Close()
	var receipt struct {
		Uploaded  bool   `json:"uploaded"`
		PublishID string `json:"publish_id"`
		SHA       string `json:"sha256"`
	}
	if err == nil && json.Unmarshal(raw, &receipt) == nil && receipt.Uploaded && receipt.PublishID != "" {
		d.discarded, d.publishID, d.sha = true, receipt.PublishID, receipt.SHA
		return nil, fetch.ErrTransport
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, err
}
