package authclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/tianacloud/cli/internal/clientconfig"
	"github.com/tianacloud/cli/internal/localfile"
)

// ResolveOrigin uses invocation settings, environment configuration or one saved account origin.
func ResolveOrigin(ctx context.Context) (string, error) {
	if origin := clientconfig.ManagementOrigin(ctx); origin != "" {
		return origin, nil
	}
	if origin := DefaultOrigin(); origin != "" {
		return origin, nil
	}
	missing := errors.New("provide --config with managementOrigin or set TIANA_MGR_ORIGIN")
	for _, key := range []string{"TIANA_MGR_ORIGIN", "TIANA_AUTH_ORIGIN"} {
		if _, set := os.LookupEnv(key); set {
			return "", missing
		}
	}
	path := strings.TrimSpace(os.Getenv("TIANA_CREDENTIALS_FILE"))
	if path == "" {
		var err error
		path, err = DefaultCredentialPath()
		if err != nil {
			return "", errors.New("cannot locate account credentials; provide --config or set TIANA_MGR_ORIGIN")
		}
	}
	contents, err := localfile.Read(path, 8<<20, true)
	if errors.Is(err, os.ErrNotExist) {
		return "", missing
	}
	if err != nil {
		return "", errors.New("cannot read account credentials: require an owned regular mode-0600 file, no symlinks, at most 8 MiB")
	}
	defer clear(contents)
	var file struct {
		Credentials map[string]struct{} `json:"credentials"`
	}
	if err := json.Unmarshal(contents, &file); err != nil {
		return "", errors.New("credential store is invalid; provide --config or set TIANA_MGR_ORIGIN")
	}
	if len(file.Credentials) == 0 {
		return "", missing
	}
	if len(file.Credentials) != 1 {
		return "", errors.New("multiple saved management origins; select one with --config or TIANA_MGR_ORIGIN")
	}
	for origin := range file.Credentials {
		if _, err := clientconfig.ParseManagementOrigin(origin); err != nil {
			return "", errors.New("saved management origin is invalid; provide --config or set TIANA_MGR_ORIGIN")
		}
		return origin, nil
	}
	return "", missing
}
