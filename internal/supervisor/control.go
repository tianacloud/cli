package supervisor

// Semantic helper message names are shared by the process implementation and
// fake-helper tests. Their payload encoding is the implementation freeze from
// the companion helper and is deliberately kept private to this package.
const (
	ControlVersion       uint8 = 1
	MaxControlFrame            = 64 * 1024
	MinControlFrame            = 2
	MaxControlPayload          = MaxControlFrame - 2
	MaxControlString           = 4096
	MaxControlCredential       = 512
)

// Capability bits are decoded as a u64 bitset. The low bit is the registered
// SQLD adapter capability; unknown bits remain observable in RawBits but do
// not silently enable a new adapter or profile.
const (
	CapabilitySQLDAdapter uint64 = 1 << 0
	// Keep this fixed bitmap position in lockstep with
	// sdk-rust/src/control.rs::CAPABILITY_SQLD_EXPLICIT_PROFILE.
	CapabilitySQLDExplicitProfile uint64 = 1 << 3
)

func validSQLDProfile(value string) bool {
	return value == ProfileHranaHTTP || value == ProfileHranaWebSocket
}

func profileScheme(profile string) (string, bool) {
	switch profile {
	case "", ProfileHranaHTTP:
		return "http", true
	case ProfileHranaWebSocket:
		return "ws", true
	default:
		return "", false
	}
}

type controlKind uint8

const (
	kindHello controlKind = iota + 1
	kindHelloAck
	kindConfig
	kindCredential
	kindBound
	kindReady
	kindChildStarted
	kindServing
	kindDrain
	kindStop
	kindStopped
	kindError
	kindSessionError
)

func (k controlKind) String() string {
	switch k {
	case kindHello:
		return "HELLO"
	case kindHelloAck:
		return "HELLO_ACK"
	case kindConfig:
		return "CONFIG"
	case kindCredential:
		return "CREDENTIAL"
	case kindBound:
		return "BOUND"
	case kindReady:
		return "READY"
	case kindChildStarted:
		return "CHILD_STARTED"
	case kindServing:
		return "SERVING"
	case kindDrain:
		return "DRAIN"
	case kindStop:
		return "STOP"
	case kindStopped:
		return "STOPPED"
	case kindError:
		return "ERROR"
	case kindSessionError:
		return "SESSION_ERROR"
	default:
		return "UNKNOWN"
	}
}

type stableErrorCode string

const (
	errorProtocol      stableErrorCode = "protocol"
	errorConfiguration stableErrorCode = "configuration"
	errorCredential    stableErrorCode = "credential"
	errorListener      stableErrorCode = "listener"
	errorSecurity      stableErrorCode = "security"
	errorTransport     stableErrorCode = "transport"
	errorInternal      stableErrorCode = "internal"
)
