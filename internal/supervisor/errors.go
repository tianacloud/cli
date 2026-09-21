package supervisor

import "errors"

var (
	ErrInvalidEndpoint       = errors.New("invalid Tiana Endpoint")
	ErrInvalidToken          = errors.New("invalid Tiana InstanceToken")
	ErrRawTokenForbidden     = errors.New("raw token options are forbidden; choose a credential source")
	ErrMissingSeparator      = errors.New("canonical connect requires -- before the native client")
	ErrEmptyNativeClient     = errors.New("native client argv is empty")
	ErrUnsupportedClient     = errors.New("native client is not registered for the SQLD/libSQL MVP")
	ErrUnsupportedAdapter    = errors.New("requested adapter is not registered")
	ErrUnsupportedProfile    = errors.New("requested SQLD profile is not registered")
	ErrRouteConflict         = errors.New("native connection locator is ambiguous or conflicting")
	ErrSecurityPolicy        = errors.New("local endpoint security level does not satisfy policy")
	ErrHelperProtocol        = errors.New("trusted helper protocol failure")
	ErrHelperNotTrusted      = errors.New("trusted helper could not be resolved")
	ErrCredentialPermissions = errors.New("credential file must be a current-user-owned regular file with mode 0600")
	ErrGatewayConfiguration  = errors.New("invalid Gateway integration configuration")
)
