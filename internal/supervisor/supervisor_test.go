package supervisor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func testEndpoint(t *testing.T) Endpoint {
	t.Helper()
	endpoint, err := ParseEndpoint("ep-0" + strings.Repeat("a", 25) + ".db.example.test")
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func testEndpointURL(t *testing.T) string {
	t.Helper()
	return "https://" + testEndpoint(t).Hostname()
}

func testBound() BoundInfo {
	return BoundInfo{Endpoint: LocalEndpoint{
		Network:       "tcp",
		Address:       "127.0.0.1:1234",
		URL:           "http://127.0.0.1:1234",
		SecurityLevel: SecurityLoopbackUnisolated,
		Capability:    1,
	}}
}

type fakeLauncher struct {
	helper   *fakeHelper
	launched bool
}

func (l *fakeLauncher) Launch(context.Context, CredentialSource) (HelperClient, error) {
	l.launched = true
	return l.helper, nil
}

type fakeHelper struct {
	token      *SecretToken
	deliverErr error
	stopErr    error
	drained    bool
	identity   ChildIdentity
	explicit   bool
	bound      BoundInfo
	config     HelperConfig
	configured bool
}

func (h *fakeHelper) Handshake(context.Context, []uint16, string) (HelperCapabilities, error) {
	bits := uint64(CapabilitySQLDAdapter)
	if h.explicit {
		bits |= CapabilitySQLDExplicitProfile
	}
	return HelperCapabilities{SelectedVersion: HelperContractVersion, RawBits: bits, SQLDAdapter: true, SQLDExplicitProfile: h.explicit}, nil
}
func (h *fakeHelper) Configure(_ context.Context, config HelperConfig) error {
	h.config = config
	h.configured = true
	return nil
}
func (h *fakeHelper) DeliverCredential(_ context.Context, token *SecretToken) error {
	h.token = token
	return h.deliverErr
}
func (h *fakeHelper) WaitBound(context.Context) (BoundInfo, error) {
	if h.bound.Endpoint.URL != "" {
		return h.bound, nil
	}
	return testBound(), nil
}
func (h *fakeHelper) WaitReady(context.Context) (ReadyInfo, error) { return ReadyInfo{}, nil }
func (h *fakeHelper) ChildStarted(_ context.Context, identity ChildIdentity) error {
	h.identity = identity
	return nil
}
func (h *fakeHelper) Drain(context.Context) error {
	h.drained = true
	return nil
}
func (h *fakeHelper) WaitStopped(context.Context) error { return h.stopErr }
func (h *fakeHelper) Close() error                      { return nil }

func writeNativeFixture(t *testing.T, exitCode int) string {
	t.Helper()
	directory := t.TempDir()
	program := filepath.Join(directory, "turso")
	script := []byte("#!/bin/sh\nexit " + string(rune('0'+exitCode)) + "\n")
	if err := os.WriteFile(program, script, 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

func TestSupervisorDestroysTokenAtCredentialBoundary(t *testing.T) {
	program := writeNativeFixture(t, 7)
	const sourceName = "TIANA_TEST_TOKEN"
	token := "tia_" + strings.Repeat("A", 43)
	if err := os.Setenv(sourceName, token); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv(sourceName)

	helper := &fakeHelper{}
	supervisor := NewSupervisor(&fakeLauncher{helper: helper})
	supervisor.IO = IO{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	options := ConnectOptions{
		NativeArgv: []string{program, "db", "shell", testEndpointURL(t)},
		AdapterID:  SQLDAdapterID,
		Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: sourceName},
		Security:   SecurityPolicy{AllowUnisolated: true},
	}
	status, err := supervisor.Run(context.Background(), options)
	if err != nil || status != 7 {
		t.Fatalf("Run() status=%d err=%v", status, err)
	}
	if helper.token == nil || len(helper.token.BytesForHandoff()) != 0 {
		t.Fatal("credential token survived the handoff boundary")
	}
	if helper.config.Endpoint != testEndpoint(t).Hostname() {
		t.Fatalf("helper endpoint=%q", helper.config.Endpoint)
	}
	if runtime.GOOS == "linux" && helper.identity.StartTime == 0 {
		t.Fatal("child identity did not contain a proc starttime")
	}
	if !helper.drained {
		t.Fatal("normal child exit did not request bounded helper drain")
	}
}

func TestSupervisorSurfacesSessionFailureWithoutReplacingNativeExitStatus(t *testing.T) {
	t.Setenv("TIANA_OPTIONAL_TOKEN", "tia_"+strings.Repeat("A", 43))
	program := writeNativeFixture(t, 7)
	helper := &fakeHelper{stopErr: SessionFailureError{
		Phase:        SessionFailureBeforeConnect,
		Status:       407,
		Code:         "AUTH_REQUIRED",
		RetryAfterMS: 0,
	}}
	supervisor := NewSupervisor(&fakeLauncher{helper: helper})
	supervisor.IO = IO{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	status, err := supervisor.Run(context.Background(), ConnectOptions{
		NativeArgv: []string{program, "db", "shell", testEndpointURL(t)},
		AdapterID:  SQLDAdapterID,
		Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: "TIANA_OPTIONAL_TOKEN"},
		Security:   SecurityPolicy{AllowUnisolated: true},
	})
	if status != 7 {
		t.Fatalf("Run() status=%d, want native status 7", status)
	}
	var failure SessionFailureError
	if !errors.As(err, &failure) {
		t.Fatalf("Run() err=%v, want SessionFailureError", err)
	}
	if got := failure.Error(); got != "Gateway requires a valid connection Token (status=407 code=AUTH_REQUIRED)" {
		t.Fatalf("failure=%q", got)
	}
}

func TestSupervisorDestroysTokenWhenHelperRejects(t *testing.T) {
	const sourceName = "TIANA_REJECT_TOKEN"
	token := "tia_" + strings.Repeat("A", 43)
	if err := os.Setenv(sourceName, token); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv(sourceName)
	helper := &fakeHelper{deliverErr: errors.New("reject")}
	supervisor := NewSupervisor(&fakeLauncher{helper: helper})
	supervisor.IO = IO{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
	options := ConnectOptions{
		NativeArgv: []string{"turso", "db", "shell", testEndpointURL(t)},
		AdapterID:  SQLDAdapterID,
		Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: sourceName},
		Security:   SecurityPolicy{AllowUnisolated: true},
	}
	if _, err := supervisor.Run(context.Background(), options); err == nil {
		t.Fatal("helper rejection was ignored")
	}
	if helper.token == nil || len(helper.token.BytesForHandoff()) != 0 {
		t.Fatal("credential token survived helper rejection")
	}
}

func TestEndpointAndCLIAuthority(t *testing.T) {
	valid := "ep-0" + strings.Repeat("a", 25) + ".db.example.test"
	for _, value := range []string{
		"ep-0" + strings.Repeat("a", 25),
		strings.ToUpper(valid),
		"https://" + valid,
		"127.0.0.1",
		valid + "/path",
	} {
		if _, err := ParseEndpoint(value); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("ParseEndpoint(%q) err=%v", value, err)
		}
	}
	for _, value := range []string{
		valid,
		"http://" + valid,
		"https://" + valid + "/",
		"https://" + valid + ":65536",
		"https://user@" + valid,
		"https://" + valid + "?route=other",
	} {
		if _, err := ParseEndpointURL(value); !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("ParseEndpointURL(%q) err=%v", value, err)
		}
	}
	for _, value := range []string{"https://" + valid, "https://" + valid + ":443", "https://" + valid + ":30080"} {
		if endpoint, err := ParseEndpointURL(value); err != nil || endpoint.Hostname() != valid {
			t.Errorf("ParseEndpointURL(%q) endpoint=%q err=%v", value, endpoint.Hostname(), err)
		}
	}
	for _, args := range [][]string{
		{"connect", "--adapter=sqld", "--adapter=sqld", "--", "turso", "db", "shell", "https://" + valid},
		{"connect", "--minimum-security=same_user", "--minimum-security=same_user", "--", "turso", "db", "shell", "https://" + valid},
		{"connect", "--token-env", "1BAD", "--", "turso", "db", "shell", "https://" + valid},
		{"connect", valid, "--", "turso", "db", "shell", "https://" + valid},
	} {
		if _, err := ParseCLI(args); err == nil {
			t.Errorf("ParseCLI(%v) unexpectedly accepted", args)
		}
	}
}

func TestParseCLIUsesNativeTursoEndpointAndDefaultTokenEnvironment(t *testing.T) {
	endpoint := testEndpoint(t)
	result, err := ParseCLI([]string{
		"connect", "--", "turso", "db", "shell",
		"https://" + endpoint.Hostname(), "SELECT 1",
	})
	if err != nil {
		t.Fatalf("ParseCLI: %v", err)
	}
	if result.Connect == nil {
		t.Fatal("missing connect options")
	}
	if got := result.Connect.NativeArgv; !reflect.DeepEqual(got, []string{
		"turso", "db", "shell", "https://" + endpoint.Hostname(), "SELECT 1",
	}) {
		t.Fatalf("native argv=%q", got)
	}
	if result.Connect.Credential.Kind != CredentialFromEnvironment || result.Connect.Credential.Value != "TIANA_TOKEN" {
		t.Fatalf("credential=%+v", result.Connect.Credential)
	}
}

func TestParseCLISupportsAllCredentialSources(t *testing.T) {
	endpointURL := testEndpointURL(t)
	for _, test := range []struct {
		name string
		args []string
		want CredentialSource
	}{
		{
			name: "file",
			args: []string{"--token-file", "/run/secrets/tiana-token"},
			want: CredentialSource{Kind: CredentialFromFile, Value: "/run/secrets/tiana-token"},
		},
		{
			name: "environment",
			args: []string{"--token-env", "MY_TIANATOKEN"},
			want: CredentialSource{Kind: CredentialFromEnvironment, Value: "MY_TIANATOKEN"},
		},
		{
			name: "stdin",
			args: []string{"--token-stdin"},
			want: CredentialSource{Kind: CredentialFromStdin},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"connect"}, test.args...)
			args = append(args, "--", "turso", "db", "shell", endpointURL)
			result, err := ParseCLI(args)
			if err != nil {
				t.Fatalf("ParseCLI: %v", err)
			}
			if result.Connect == nil || result.Connect.Credential != test.want {
				t.Fatalf("credential=%+v want=%+v", result.Connect.Credential, test.want)
			}
		})
	}

	if _, err := ParseCLI([]string{
		"connect", "--token", "tia_argv_secret", "--",
		"turso", "db", "shell", endpointURL,
	}); !errors.Is(err, ErrRawTokenForbidden) {
		t.Fatalf("raw token err=%v", err)
	}
}

func TestSupervisorRejectsNativeEndpointBeforeHelperLaunch(t *testing.T) {
	launcher := &fakeLauncher{helper: &fakeHelper{}}
	supervisor := NewSupervisor(launcher)
	_, err := supervisor.Run(context.Background(), ConnectOptions{
		NativeArgv: []string{"turso", "db", "shell", "https://attacker.example"},
		Credential: DefaultCredentialSource(),
		Security:   DefaultSecurityPolicy(),
	})
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("Run err=%v", err)
	}
	if launcher.launched {
		t.Fatal("invalid native Endpoint launched the helper")
	}
}

func TestAllowlistedEnvironmentPreservesOrderAndSecrets(t *testing.T) {
	environment := []string{
		"PATH=/bin",
		"KEEP=not-allowlisted",
		"LANG=C",
		"PATH=/usr/bin",
		"CUSTOM_TOKEN=canary",
		"TIANA_TOKEN=secret",
		"TURSO_DATABASE_URL=https://remote",
	}
	got := allowlistedChildEnvironment(environment, "CUSTOM_TOKEN")
	want := []string{"PATH=/bin", "LANG=C"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("allowlist=%q want=%q", got, want)
	}
	if strings.Contains(strings.Join(got, "\x00"), "canary") {
		t.Fatal("credential canary leaked into child environment")
	}
}

func TestSQLDAdapterRejectsUnprovenLocators(t *testing.T) {
	endpoint := testBound().Endpoint
	adapter := SQLDAdapter{}
	remote := testEndpointURL(t)
	for _, argv := range [][]string{
		{"turso", "db", "shell", remote, "--url", "https://remote"},
		{"turso", "db", "shell", remote, "--host=remote"},
		{"turso", "db", "shell", remote, "--port", "443"},
		{"turso", "db", "shell", remote, "--socket", "/tmp/socket"},
		{"turso", "db", "shell", remote, "--proxy-url", "https://proxy"},
		{"turso", "db", "shell", remote, "--proxy", "http://proxy"},
		{"turso", "db", "shell", remote, "--instance", "direct"},
		{"turso", "db", "shell", remote, "--location", "ord"},
		{"turso", "db", "shell", "https://remote"},
		{"turso", "db", "shell", remote, "SELECT 1", "https://remote"},
		{"turso", "db", "shell", remote, "SELECT 1", "/tmp/second-locator"},
		{"turso", "db", "shell", remote, "__tiana_real_native_child", "/tmp/record", "/tmp/release"},
	} {
		if _, err := adapter.Prepare(endpoint, "", argv, nil); !errors.Is(err, ErrUnsupportedClient) && !errors.Is(err, ErrRouteConflict) && !errors.Is(err, ErrInvalidEndpoint) {
			t.Errorf("Prepare(%v) err=%v", argv, err)
		}
	}
	if _, _, _, err := localTCPParts(LocalEndpoint{Network: "tcp", Address: "127.0.0.1:1234", URL: "http://localhost:1234"}); err == nil {
		t.Fatal("hostname alias was accepted as authoritative local locator")
	}
}

func TestParseCLIExplicitProfileIsReviewedAndOwnedByTiana(t *testing.T) {
	valid := "ep-0" + strings.Repeat("a", 25) + ".db.example.test"
	for _, profile := range []string{ProfileHranaHTTP, ProfileHranaWebSocket} {
		result, err := ParseCLI([]string{"connect", "--profile", profile, "--", "turso", "db", "shell", "https://" + valid, "SELECT 1"})
		if err != nil {
			t.Fatalf("ParseCLI(%q): %v", profile, err)
		}
		if result.Connect == nil || result.Connect.Profile != profile {
			t.Fatalf("ParseCLI(%q) profile=%+v", profile, result.Connect)
		}
	}
	for _, args := range [][]string{
		{"connect", "--profile", "libsql", "--", "turso", "db", "shell", "https://" + valid},
		{"connect", "--profile=hrana-http", "--profile", "hrana-websocket", "--", "turso", "db", "shell", "https://" + valid},
	} {
		if _, err := ParseCLI(args); !errors.Is(err, ErrUnsupportedProfile) {
			t.Errorf("ParseCLI(%v) err=%v, want ErrUnsupportedProfile", args, err)
		}
	}
}

func TestSQLDAdapterReplacesEndpointAndPreservesSQL(t *testing.T) {
	adapter := SQLDAdapter{}
	remote := testEndpointURL(t)
	for _, test := range []struct {
		profile string
		scheme  string
	}{
		{profile: ProfileHranaHTTP, scheme: "http"},
		{profile: ProfileHranaWebSocket, scheme: "ws"},
	} {
		endpoint := testBound().Endpoint
		endpoint.URL = test.scheme + "://127.0.0.1:1234"
		prepared, err := adapter.Prepare(endpoint, test.profile, []string{"turso", "db", "shell", remote, "SELECT 1", "--verbose"}, nil)
		if err != nil {
			t.Fatalf("Prepare(%s): %v", test.profile, err)
		}
		want := []string{"turso", "db", "shell", test.scheme + "://127.0.0.1:1234", "SELECT 1", "--verbose"}
		if !reflect.DeepEqual(prepared.Argv, want) {
			t.Fatalf("Prepare(%s) argv=%q want=%q", test.profile, prepared.Argv, want)
		}
	}
}

func TestSQLDAdapterPreservesNativeSQLWithoutKeywordGuessing(t *testing.T) {
	adapter := SQLDAdapter{}
	for _, sql := range []string{"VACUUM", ".tables", "REINDEX", "-- SQL comment"} {
		prepared, err := adapter.Prepare(
			testBound().Endpoint,
			"",
			[]string{"turso", "db", "shell", testEndpointURL(t), sql},
			nil,
		)
		if err != nil {
			t.Fatalf("Prepare(%q): %v", sql, err)
		}
		if got := prepared.Argv[4]; got != sql {
			t.Fatalf("SQL=%q want=%q", got, sql)
		}
	}
}

func TestExplicitProfileRequiresHelperCapabilityBeforeConfiguration(t *testing.T) {
	token := "tia_" + strings.Repeat("A", 43)
	helper := &fakeHelper{}
	supervisor := NewSupervisor(&fakeLauncher{helper: helper})
	supervisor.IO = IO{Stdin: strings.NewReader(token + "\n"), Stdout: io.Discard, Stderr: io.Discard}
	_, err := supervisor.Run(context.Background(), ConnectOptions{
		NativeArgv: []string{"turso", "db", "shell", testEndpointURL(t)},
		AdapterID:  SQLDAdapterID,
		Profile:    ProfileHranaHTTP,
		Credential: CredentialSource{Kind: CredentialFromStdin},
		Security:   SecurityPolicy{AllowUnisolated: true},
	})
	if !errors.Is(err, ErrHelperProtocol) {
		t.Fatalf("Run() err=%v, want ErrHelperProtocol", err)
	}
	if helper.configured {
		t.Fatal("missing explicit-profile capability reached CONFIG")
	}
	if helper.token != nil {
		t.Fatal("missing explicit-profile capability reached CREDENTIAL")
	}
	if helper.bound.Endpoint.URL != "" || helper.identity.PID != 0 || helper.drained {
		t.Fatal("missing explicit-profile capability reached BOUND or child lifecycle")
	}
}

func TestSupervisorExplicitProfileUsesCanonicalAllowedSet(t *testing.T) {
	token := "tia_" + strings.Repeat("A", 43)
	for _, test := range []struct {
		profile string
		scheme  string
	}{
		{profile: ProfileHranaHTTP, scheme: "http"},
		{profile: ProfileHranaWebSocket, scheme: "ws"},
	} {
		program := writeNativeFixture(t, 0)
		helper := &fakeHelper{explicit: true}
		bound := testBound()
		bound.Endpoint.URL = test.scheme + "://127.0.0.1:1234"
		helper.bound = bound
		supervisor := NewSupervisor(&fakeLauncher{helper: helper})
		supervisor.IO = IO{Stdin: strings.NewReader(token + "\n"), Stdout: io.Discard, Stderr: io.Discard}
		status, err := supervisor.Run(context.Background(), ConnectOptions{
			NativeArgv: []string{program, "db", "shell", testEndpointURL(t)},
			AdapterID:  SQLDAdapterID,
			Profile:    test.profile,
			Credential: CredentialSource{Kind: CredentialFromStdin},
			Security:   SecurityPolicy{AllowUnisolated: true},
		})
		if err != nil || status != 0 {
			t.Fatalf("Run(%s) status=%d err=%v", test.profile, status, err)
		}
		if !helper.configured {
			t.Fatalf("Run(%s) did not configure helper", test.profile)
		}
		if !reflect.DeepEqual(helper.config.AllowedProfile, []string{ProfileHranaHTTP, ProfileHranaWebSocket}) {
			t.Fatalf("Run(%s) allowed profiles=%q", test.profile, helper.config.AllowedProfile)
		}
		if helper.config.SelectionMode != SelectionModeExplicitProfile || helper.config.ExplicitProfile != test.profile {
			t.Fatalf("Run(%s) selection=%q explicit=%q", test.profile, helper.config.SelectionMode, helper.config.ExplicitProfile)
		}
	}
}

type shortWriter struct{ max int }

func (w shortWriter) Write(value []byte) (int, error) {
	if len(value) > w.max {
		return w.max, nil
	}
	return len(value), nil
}

func TestWriteAllHandlesShortWrites(t *testing.T) {
	if err := writeAll(shortWriter{max: 2}, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if err := writeAll(shortWriter{max: 0}, []byte("x")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v", err)
	}
}

func TestProcessHelperCancellationJoinsIO(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	helper := &ProcessHelper{read: read, reader: bufio.NewReader(read), state: StateHandshaking}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = helper.WaitBound(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBound err=%v", err)
	}
	if !helper.closed {
		t.Fatal("canceled read did not abort the helper I/O")
	}
}

func TestCredentialWriteCancellationJoinsWriter(t *testing.T) {
	helper := &ProcessHelper{write: &blockingWriteCloser{release: make(chan struct{})}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	payload := bytes.Repeat([]byte("credential-canary"), 3500)
	err := helper.writeFrameContext(ctx, kindCredential, payload)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writeFrameContext err=%v", err)
	}
	if !helper.closed {
		t.Fatal("canceled write did not abort the helper I/O")
	}
}

type blockingWriteCloser struct {
	release chan struct{}
	once    sync.Once
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.once.Do(func() { close(w.release) })
	return nil
}

func TestTrustedManifestIsStrict(t *testing.T) {
	valid := helperManifest{ContractVersion: 1, HelperRelativePath: trustedHelperRelativePath, SHA256: strings.Repeat("a", sha256.Size*2), Platform: runtime.GOOS, Arch: runtime.GOARCH}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManifest(encoded); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	if _, err := decodeManifest(append(append([]byte(nil), encoded...), []byte(" {}")...)); !errors.Is(err, ErrHelperNotTrusted) {
		t.Fatalf("trailing manifest accepted: %v", err)
	}
	if _, err := decodeManifest(append(append([]byte(nil), encoded[:len(encoded)-1]...), []byte(",\"extra\":1}")...)); !errors.Is(err, ErrHelperNotTrusted) {
		t.Fatalf("unknown manifest field accepted: %v", err)
	}
}

func TestEncodeChildStartedUsesCanonicalStartTimeBytes(t *testing.T) {
	encoded := encodeChildStarted(ChildIdentity{PID: 12, StartTime: 99})
	if !bytes.Equal(encoded, []byte{0, 0, 0, 12, 0, 0, 0, 8, 0, 0, 0, 0, 0, 0, 0, 99}) {
		t.Fatalf("unexpected child identity bytes: %x", encoded)
	}
	_ = hex.EncodeToString(encoded)
	_ = url.URL{}
}

func TestConfigWireUsesFrozenClassifierLimits(t *testing.T) {
	config := HelperConfig{
		Endpoint:       "ep-0aaaaaaaaaaaaaaaaaaaaaaaaa.db.example.test",
		AdapterID:      SQLDAdapterID,
		AllowedProfile: []string{"hrana-http", "hrana-websocket"},
		SelectionMode:  "bounded-http-header-classifier",
		Deadline:       SQLDClassifierDeadline,
		HeaderLimit:    SQLDHeaderLimit,
		FieldLimit:     SQLDFieldLimit,
		Pre200Limit:    SQLDPre200Limit,
		UseWebPKIRoots: true,
	}
	payload := encodeConfig(config)
	decoded, err := decodeConfig(payload)
	if err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	if !reflect.DeepEqual(decoded, config) {
		t.Fatalf("decoded config=%+v want=%+v", decoded, config)
	}
	if !bytes.Equal(encodeConfig(decoded), payload) {
		t.Fatal("config encode/decode did not preserve bytes")
	}
	if _, err := decodeConfig(append(append([]byte(nil), payload...), 0)); !errors.Is(err, ErrHelperProtocol) {
		t.Fatalf("trailing CONFIG byte accepted: %v", err)
	}

	integration := config
	integration.GatewayAddress = "192.0.2.10:30080"
	integration.UseWebPKIRoots = false
	integration.RootCertDER = [][]byte{{0x30, 0x00}}
	integrationPayload := encodeConfig(integration)
	integrationDecoded, err := decodeConfig(integrationPayload)
	if err != nil {
		t.Fatalf("decode integration CONFIG: %v", err)
	}
	if !reflect.DeepEqual(integrationDecoded, integration) {
		t.Fatalf("decoded integration config=%+v want=%+v", integrationDecoded, integration)
	}

	noTrust := config
	noTrust.UseWebPKIRoots = false
	if err := validateHelperConfig(noTrust); !errors.Is(err, ErrHelperProtocol) {
		t.Fatalf("CONFIG without trust roots err=%v", err)
	}
	noTrust.InsecureTLS = true
	decoded, err = decodeConfig(encodeConfig(noTrust))
	if err != nil || !reflect.DeepEqual(decoded, noTrust) {
		t.Fatalf("insecure CONFIG round trip: config=%+v err=%v", decoded, err)
	}
	noTrust.UseWebPKIRoots = true
	if err := validateHelperConfig(noTrust); err == nil {
		t.Fatal("insecure CONFIG accepted trust roots")
	}
}

func TestSupervisorLocalCloseRequiresSuccessfulNativeExit(t *testing.T) {
	for _, tc := range []struct {
		name, code               string
		nativeStatus, wantStatus int
		wantError                bool
	}{
		{"normal close", "LOCAL_CLIENT_CLOSED", 0, 0, false},
		{"native failure", "LOCAL_CLIENT_CLOSED", 7, 7, true},
		{"upstream failure", "TUNNEL_INTERRUPTED", 0, 1, true},
		{"both fail", "TUNNEL_INTERRUPTED", 7, 7, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TIANA_OPTIONAL_TOKEN", "tia_"+strings.Repeat("A", 43))
			program := writeNativeFixture(t, tc.nativeStatus)
			helper := &fakeHelper{stopErr: SessionFailureError{Phase: SessionFailureAfterConnect, Code: tc.code}}
			supervisor := NewSupervisor(&fakeLauncher{helper: helper})
			supervisor.IO = IO{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard}
			status, err := supervisor.Run(context.Background(), ConnectOptions{
				NativeArgv: []string{program, "db", "shell", testEndpointURL(t)},
				AdapterID:  SQLDAdapterID,
				Credential: CredentialSource{Kind: CredentialFromEnvironment, Value: "TIANA_OPTIONAL_TOKEN"},
				Security:   SecurityPolicy{AllowUnisolated: true},
			})
			if status != tc.wantStatus || (err != nil) != tc.wantError {
				t.Fatalf("Run() status=%d err=%v", status, err)
			}
		})
	}
}
