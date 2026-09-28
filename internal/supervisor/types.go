package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	SQLDHeaderLimit        = 16 * 1024
	SQLDFieldLimit         = 64
	SQLDPre200Limit        = 64 * 1024
	SQLDClassifierDeadline = time.Second
	GatewayRootCertLimit   = 16 * 1024
	GatewayRootCertCount   = 8

	ProfileHranaHTTP      = "hrana-http"
	ProfileHranaWebSocket = "hrana-websocket"

	SelectionModeBoundedHTTPHeaderClassifier = "bounded-http-header-classifier"
	SelectionModeExplicitProfile             = "explicit-profile"
)

// State is the lifecycle state of one helper invocation. The ordering is part
// of the Go supervisor/helper semantic contract; the helper remains the owner
// of the listener and data plane.
type State uint8

const (
	StateStarting State = iota
	StateHandshaking
	StateBound
	StateReady
	StateServing
	StateDraining
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "STARTING"
	case StateHandshaking:
		return "HANDSHAKING"
	case StateBound:
		return "BOUND"
	case StateReady:
		return "READY"
	case StateServing:
		return "SERVING"
	case StateDraining:
		return "DRAINING"
	case StateStopped:
		return "STOPPED"
	default:
		return "UNKNOWN"
	}
}

// SecurityLevel is deliberately ordered from weakest to strongest.
type SecurityLevel uint8

const (
	SecurityLoopbackUnisolated SecurityLevel = iota
	SecuritySameUser
	SecurityStrictProcess
)

func (s SecurityLevel) String() string {
	switch s {
	case SecurityLoopbackUnisolated:
		return "loopback_unisolated"
	case SecuritySameUser:
		return "same_user"
	case SecurityStrictProcess:
		return "strict_process"
	default:
		return "unknown"
	}
}

// LocalEndpoint is only a locator/capability report. The supervisor never
// relays database bytes; the helper process owns the listener and sessions.
type LocalEndpoint struct {
	Network       string
	Address       string
	URL           string
	SecurityLevel SecurityLevel
	Capability    uint64
}

// SecurityPolicy is checked after READY and before the child is spawned.
type SecurityPolicy struct {
	Minimum            SecurityLevel
	MinimumExplicit    bool
	AllowUnisolated    bool
	AllowUnisolatedSet bool
}

func DefaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{Minimum: SecurityLoopbackUnisolated}
}

// CredentialSource identifies where a token is read from. It never contains
// the token itself, which keeps source configuration safe to include in
// diagnostics.
type CredentialSource struct {
	Kind  CredentialSourceKind
	Value string
}

type CredentialSourceKind uint8

const (
	CredentialFromEnvironment CredentialSourceKind = iota
	CredentialFromFile
	CredentialFromStdin
)

func DefaultCredentialSource() CredentialSource {
	return CredentialSource{Kind: CredentialFromEnvironment, Value: "TIANA_TOKEN"}
}

// ConnectOptions is the validated, canonical command model. NativeArgv is
// kept as a vector and is never converted to a shell command.
type ConnectOptions struct {
	NativeArgv     []string
	AdapterID      string
	Profile        string
	Credential     CredentialSource
	Gateway        GatewayOptions
	Security       SecurityPolicy
	Interactive    bool
	TokenSourceSet bool
}

// GatewayOptions keeps the physical transport target separate from the
// logical Endpoint. Address affects only TCP dialing; Endpoint remains the
// TLS SNI and HTTP/2 :authority. Public CA verification is the default.
type GatewayOptions struct {
	CACertificates [][]byte
	CAFile         string
	Address        string
	InsecureTLS    bool
}

// HelperCapabilities describes capabilities advertised after the semantic
// HELLO/HELLO_ACK exchange.
type HelperCapabilities struct {
	SelectedVersion     uint16
	RawBits             uint64
	SQLDAdapter         bool
	SQLDExplicitProfile bool
	SecurityLevels      []SecurityLevel
}

type HelperConfig struct {
	Endpoint        string
	AdapterID       string
	AllowedProfile  []string
	SelectionMode   string
	ExplicitProfile string
	Deadline        time.Duration
	HeaderLimit     int
	FieldLimit      int
	Pre200Limit     int
	RequireToken    bool
	GatewayAddress  string
	InsecureTLS     bool
	UseWebPKIRoots  bool
	RootCertDER     [][]byte
}

// BoundInfo is returned while accepting is still disabled.
type BoundInfo struct {
	Endpoint LocalEndpoint
}

// ReadyInfo is returned only after transport and credential machinery are
// ready. It still does not authorize accept until ChildStarted succeeds.
type ReadyInfo struct {
	Endpoint LocalEndpoint
}

// SessionFailurePhase is the only context the helper may attach to a
// per-session failure. No token, SQL, remote body, or arbitrary error text is
// permitted on the private control channel.
type SessionFailurePhase uint8

const (
	SessionFailureLocalRequest SessionFailurePhase = iota + 1
	SessionFailureBeforeConnect
	SessionFailureAfterConnect
)

// SessionFailureError is bounded, non-sensitive metadata produced by the
// trusted helper. RetryAfter is advisory only; the CLI never automatically
// replays a database operation.
type SessionFailureError struct {
	Phase        SessionFailurePhase
	Status       uint16
	Code         string
	RetryAfterMS uint32
}

func (e SessionFailureError) Error() string {
	var message string
	switch e.Phase {
	case SessionFailureLocalRequest:
		message = fmt.Sprintf("local database request was rejected: code=%s", e.Code)
	case SessionFailureBeforeConnect:
		message = beforeConnectFailureMessage(e.Status, e.Code)
	case SessionFailureAfterConnect:
		message = fmt.Sprintf("database tunnel failed after CONNECT 200: code=%s; request outcome may be unknown; automatic retry is disabled", e.Code)
	default:
		return "invalid trusted helper session failure"
	}
	if e.RetryAfterMS != 0 {
		message += fmt.Sprintf(" retry_after_ms=%d (automatic retry is disabled)", e.RetryAfterMS)
	}
	return message
}

func beforeConnectFailureMessage(status uint16, code string) string {
	message := "Gateway connection failed before HTTP 200"
	switch code {
	case "GATEWAY_UNREACHABLE":
		message = "could not reach Gateway; check Endpoint DNS, VPN/firewall access, and the configured Gateway port"
	case "GATEWAY_TCP_TIMEOUT":
		message = "timed out opening a TCP connection to Gateway; check Endpoint DNS, VPN/firewall access, and the configured Gateway port"
	case "GATEWAY_TLS_TIMEOUT":
		message = "Gateway TLS handshake timed out; check the network path and TLS interception"
	case "GATEWAY_PROTOCOL_TIMEOUT":
		message = "Gateway HTTP/2 handshake timed out; check that the network path permits ALPN h2"
	case "GATEWAY_CONNECT_TIMEOUT":
		message = "Gateway did not answer CONNECT before the timeout; check Gateway, Control, and Runtime health"
	case "GATEWAY_TIMEOUT":
		message = "Gateway connection setup timed out; check Endpoint DNS, VPN/firewall access, and the configured Gateway port, then check Gateway, Control, and Runtime health"
	case "GATEWAY_TLS":
		message = "Gateway TLS handshake failed; check the Endpoint hostname and trusted CA"
	case "GATEWAY_PROTOCOL":
		message = "Gateway returned an invalid HTTP/2 response"
	case "CLIENT_CONFIGURATION":
		message = "the local Gateway connection configuration is invalid"
	case "AUTH_REQUIRED":
		message = "Gateway requires a valid connection Token"
	case "ACCESS_DENIED":
		message = "the connection Token is not authorized for this database"
	case "AUTHORIZATION_EXPIRED":
		message = "the connection Token has expired"
	case "CALLER_DEADLINE":
		message = "Gateway exhausted the connection setup deadline"
	case "ENDPOINT_MISMATCH":
		message = "the Endpoint hostname does not match the CONNECT target"
	case "CONNECTION_LIMIT":
		message = "Gateway connection limit reached; retry later"
	case "POLICY_UNAVAILABLE":
		message = "Gateway could not load connection policy; retry later"
	case "INSTANCE_UNAVAILABLE":
		message = "the database instance is temporarily unavailable; retry later"
	case "ACTIVATION_TIMEOUT":
		message = "the database instance did not become ready before the Gateway deadline; retry once"
	}
	if status != 0 {
		return fmt.Sprintf("%s (status=%d code=%s)", message, status, code)
	}
	return fmt.Sprintf("%s (code=%s)", message, code)
}

func validSessionFailureCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, value := range []byte(code) {
		if value != '_' && (value < 'A' || value > 'Z') {
			return false
		}
	}
	return !strings.ContainsAny(code, "\r\n\t ")
}

// ChildIdentity is the trusted handoff produced from the supervisor's spawn
// result. StartTime is the Linux /proc/<pid>/stat starttime tick value, or
// Windows process creation FILETIME. macOS uses zero with kqueue exit watching.
type ChildIdentity struct {
	PID       int
	StartTime uint64
}

// HelperClient is the semantic boundary between supervisor and helper process.
// Implementations must not expose a Tunnel or database bytes to the supervisor.
type HelperClient interface {
	Handshake(context.Context, []uint16, string) (HelperCapabilities, error)
	Configure(context.Context, HelperConfig) error
	DeliverCredential(context.Context, *SecretToken) error
	WaitBound(context.Context) (BoundInfo, error)
	WaitReady(context.Context) (ReadyInfo, error)
	ChildStarted(context.Context, ChildIdentity) error
	Drain(context.Context) error
	WaitStopped(context.Context) error
	Close() error
}

// HelperLauncher resolves and launches exactly one trusted companion helper.
type HelperLauncher interface {
	Launch(context.Context, CredentialSource) (HelperClient, error)
}

// PreparedCommand is the only data the supervisor gives to os/exec. Env is
// already filtered and contains the adapter's local locator, but never the
// outer InstanceToken or private control handle.
type PreparedCommand struct {
	Program string
	Argv    []string
	Env     []string
}

// Adapter is a reviewed native-client adapter. Match is intentionally based
// on registered data; callers must not infer support from arbitrary basenames.
type Adapter interface {
	ID() string
	Matches(program string, explicit bool) bool
	ResolveEndpoint([]string) (Endpoint, error)
	Prepare(LocalEndpoint, string, []string, []string) (PreparedCommand, error)
}

// IO wires the native child directly to the caller's stdio. It is kept
// injectable for process-contract tests.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func DefaultIO() IO {
	return IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
}
