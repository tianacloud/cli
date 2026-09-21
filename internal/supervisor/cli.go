package supervisor

import (
	"fmt"
	"os"
	"strings"
)

type ParseResult struct {
	Connect *ConnectOptions
	Help    bool
}

// ParseCLI enforces the canonical ownership boundary. After -- it validates
// only the registered adapter's route authority; the vector otherwise belongs
// to the native client, even when an element resembles a Tiana option.
func ParseCLI(args []string) (ParseResult, error) {
	if len(args) == 0 {
		return ParseResult{}, fmt.Errorf("%w", ErrMissingSeparator)
	}
	if args[0] == "--help" || args[0] == "-h" {
		return ParseResult{Help: true}, nil
	}
	if args[0] != "connect" {
		return ParseResult{}, fmt.Errorf("only the canonical connect command is available")
	}
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		return ParseResult{Help: true}, nil
	}

	separator := -1
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return ParseResult{}, ErrMissingSeparator
	}
	if separator == len(args)-1 {
		return ParseResult{}, ErrEmptyNativeClient
	}

	options := ConnectOptions{
		Gateway:     GatewayOptions{CAFile: os.Getenv("TIANA_CA_FILE")},
		Credential:  DefaultCredentialSource(),
		Security:    DefaultSecurityPolicy(),
		Interactive: true,
		NativeArgv:  append([]string(nil), args[separator+1:]...),
	}
	var adapterSet, profileSet, securitySet, allowUnisolatedSet bool
	for i := 1; i < separator; i++ {
		arg := args[i]
		switch {
		case arg == "--token" || strings.HasPrefix(arg, "--token="):
			return ParseResult{}, ErrRawTokenForbidden
		case arg == "--token-stdin":
			if options.TokenSourceSet {
				return ParseResult{}, fmt.Errorf("credential sources conflict")
			}
			options.Credential = CredentialSource{Kind: CredentialFromStdin}
			options.TokenSourceSet = true
		case arg == "--token-file":
			value, next, ok := optionValue(args, i, separator)
			if !ok || options.TokenSourceSet {
				if options.TokenSourceSet {
					return ParseResult{}, fmt.Errorf("credential sources conflict")
				}
				return ParseResult{}, fmt.Errorf("token-file requires a path")
			}
			i = next
			options.Credential = CredentialSource{Kind: CredentialFromFile, Value: value}
			options.TokenSourceSet = true
		case strings.HasPrefix(arg, "--token-file="):
			if options.TokenSourceSet || strings.TrimPrefix(arg, "--token-file=") == "" {
				return ParseResult{}, fmt.Errorf("credential source is invalid")
			}
			options.Credential = CredentialSource{Kind: CredentialFromFile, Value: strings.TrimPrefix(arg, "--token-file=")}
			options.TokenSourceSet = true
		case arg == "--token-env":
			value, next, ok := optionValue(args, i, separator)
			if !ok || options.TokenSourceSet || !validEnvName(value) {
				return ParseResult{}, fmt.Errorf("token-env requires a valid environment variable name")
			}
			i = next
			options.Credential = CredentialSource{Kind: CredentialFromEnvironment, Value: value}
			options.TokenSourceSet = true
		case strings.HasPrefix(arg, "--token-env="):
			value := strings.TrimPrefix(arg, "--token-env=")
			if options.TokenSourceSet || !validEnvName(value) {
				return ParseResult{}, fmt.Errorf("token-env requires a valid environment variable name")
			}
			options.Credential = CredentialSource{Kind: CredentialFromEnvironment, Value: value}
			options.TokenSourceSet = true
		case arg == "--adapter":
			value, next, ok := optionValue(args, i, separator)
			if !ok || value == "" {
				return ParseResult{}, ErrUnsupportedAdapter
			}
			if adapterSet {
				return ParseResult{}, fmt.Errorf("adapter selector is repeated")
			}
			i = next
			options.AdapterID = value
			adapterSet = true
		case strings.HasPrefix(arg, "--adapter="):
			value := strings.TrimPrefix(arg, "--adapter=")
			if value == "" || adapterSet {
				return ParseResult{}, ErrUnsupportedAdapter
			}
			options.AdapterID = value
			adapterSet = true
		case arg == "--profile":
			value, next, ok := optionValue(args, i, separator)
			if !ok || profileSet || !validSQLDProfile(value) {
				return ParseResult{}, ErrUnsupportedProfile
			}
			i = next
			options.Profile = value
			profileSet = true
		case strings.HasPrefix(arg, "--profile="):
			value := strings.TrimPrefix(arg, "--profile=")
			if profileSet || !validSQLDProfile(value) {
				return ParseResult{}, ErrUnsupportedProfile
			}
			options.Profile = value
			profileSet = true
		case arg == "--ca-file":
			value, next, ok := optionValue(args, i, separator)
			if !ok {
				return ParseResult{}, fmt.Errorf("ca-file requires a PEM certificate path")
			}
			options.Gateway.CAFile = value
			i = next
		case strings.HasPrefix(arg, "--ca-file="):
			options.Gateway.CAFile = strings.TrimPrefix(arg, "--ca-file=")
			if options.Gateway.CAFile == "" {
				return ParseResult{}, fmt.Errorf("ca-file requires a PEM certificate path")
			}
		case arg == "--insecure" && debugTLSAvailable:
			if options.Gateway.InsecureTLS {
				return ParseResult{}, fmt.Errorf("insecure is repeated")
			}
			options.Gateway.InsecureTLS = true
		case arg == "--minimum-security":
			value, next, ok := optionValue(args, i, separator)
			if !ok {
				return ParseResult{}, fmt.Errorf("minimum-security requires a value")
			}
			level, parseErr := ParseSecurityLevel(value)
			if parseErr != nil {
				return ParseResult{}, parseErr
			}
			if securitySet {
				return ParseResult{}, fmt.Errorf("minimum-security selector is repeated")
			}
			i = next
			options.Security.Minimum = level
			options.Security.MinimumExplicit = true
			securitySet = true
		case strings.HasPrefix(arg, "--minimum-security="):
			if securitySet {
				return ParseResult{}, fmt.Errorf("minimum-security selector is repeated")
			}
			level, parseErr := ParseSecurityLevel(strings.TrimPrefix(arg, "--minimum-security="))
			if parseErr != nil {
				return ParseResult{}, parseErr
			}
			options.Security.Minimum = level
			options.Security.MinimumExplicit = true
			securitySet = true
		case arg == "--allow-unisolated-loopback":
			if allowUnisolatedSet {
				return ParseResult{}, fmt.Errorf("allow-unisolated-loopback is repeated")
			}
			options.Security.AllowUnisolated = true
			options.Security.AllowUnisolatedSet = true
			allowUnisolatedSet = true
		case arg == "--non-interactive":
			options.Interactive = false
		case arg == "--help" || arg == "-h":
			return ParseResult{Help: true}, nil
		default:
			return ParseResult{}, fmt.Errorf("unknown Tiana option")
		}
	}
	adapter, err := NewAdapterRegistry().Select(options.NativeArgv[0], options.AdapterID)
	if err != nil {
		return ParseResult{}, err
	}
	if _, err := adapter.ResolveEndpoint(options.NativeArgv); err != nil {
		return ParseResult{}, err
	}
	return ParseResult{Connect: &options}, nil
}

func optionValue(args []string, current, separator int) (string, int, bool) {
	if current+1 >= separator {
		return "", current, false
	}
	value := args[current+1]
	if value == "--" || strings.HasPrefix(value, "-") {
		return "", current, false
	}
	return value, current + 1, true
}

func validEnvName(value string) bool {
	if value == "" || !(value[0] == '_' || value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z') {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !(value[i] == '_' || value[i] >= 'A' && value[i] <= 'Z' || value[i] >= 'a' && value[i] <= 'z' || value[i] >= '0' && value[i] <= '9') {
			return false
		}
	}
	return true
}

func ParseSecurityLevel(value string) (SecurityLevel, error) {
	switch value {
	case "loopback_unisolated":
		return SecurityLoopbackUnisolated, nil
	case "same_user":
		return SecuritySameUser, nil
	case "strict_process":
		return SecurityStrictProcess, nil
	default:
		return SecurityLoopbackUnisolated, fmt.Errorf("invalid minimum security policy")
	}
}

func Usage() string {
	debugHelp := ""
	if debugTLSAvailable {
		debugHelp = "Internal debugging only: --insecure disables Gateway certificate verification.\n"
	}
	return "Usage: tiana connect [options] -- turso db shell https://<tiana-hostname> [sql] [native flags...]\n\n" +
		"Tiana reads the canonical Endpoint from Turso's native replica URL and replaces only that locator with the local tunnel URL.\n" +
		"SQLD/libSQL defaults to the bounded local HTTP classifier; --profile hrana-http|hrana-websocket selects one reviewed profile.\n" +
		"Credential sources: --token-env NAME, --token-file PATH, or --token-stdin.\n" +
		"Defaults to TIANA_TOKEN; when unset, prompts with hidden input in an interactive terminal. --non-interactive disables prompting.\n" +
		"Gateway TLS verifies the Endpoint hostname. Use --ca-file PATH or TIANA_CA_FILE for a deployment CA.\n" + debugHelp +
		"Raw --token values are forbidden. Non-locator native arguments after -- are passed unchanged.\n"
}
