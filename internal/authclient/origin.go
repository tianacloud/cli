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

const fallbackOrigin = "https://console.service.internal.tiana.com"

// ResolveOrigin uses the inherited management environment or one saved account origin, then the deployment default.
func ResolveOrigin(_ context.Context) (string, error) {
	if origin := DefaultOrigin(); origin != "" {
		return origin, nil
	}
	missing := errors.New("set TIANA_API_ORIGIN before starting the agent or shell")
	if _, set := os.LookupEnv("TIANA_API_ORIGIN"); set {
		return "", missing
	}
	path := strings.TrimSpace(os.Getenv("TIANA_CREDENTIALS_FILE"))
	if path == "" {
		var err error
		path, err = DefaultCredentialPath()
		if err != nil {
			return "", errors.New("cannot locate account credentials; set TIANA_API_ORIGIN before starting the agent or shell")
		}
	}
	contents, err := localfile.Read(path, 8<<20, true)
	if errors.Is(err, os.ErrNotExist) {
		return fallbackOrigin, nil
	}
	if err != nil {
		return "", errors.New("cannot read account credentials: require an owned regular mode-0600 file, no symlinks, at most 8 MiB")
	}
	defer clear(contents)
	var file struct {
		Credentials map[string]struct{} `json:"credentials"`
	}
	if err := json.Unmarshal(contents, &file); err != nil {
		return "", errors.New("credential store is invalid; set TIANA_API_ORIGIN before starting the agent or shell")
	}
	if len(file.Credentials) == 0 {
		return fallbackOrigin, nil
	}
	if len(file.Credentials) != 1 {
		return "", errors.New("multiple saved management origins; select one with TIANA_API_ORIGIN before starting the agent or shell")
	}
	for origin := range file.Credentials {
		if _, err := clientconfig.ParseManagementOrigin(origin); err != nil {
			return "", errors.New("saved management origin is invalid; set TIANA_API_ORIGIN before starting the agent or shell")
		}
		return origin, nil
	}
	return "", missing
}
